package writer

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/prooftamil/api/internal/cascade"
)

// Generator streams generated Tamil from a model.
type Generator interface {
	Generate(ctx context.Context, system, user string, temperature float64, emit func(string) error) error
}

// Service runs the three writer modes (§16).
type Service struct {
	gen      Generator
	prompts  map[Mode]string
	quota    Quota      // nil = unlimited (dev)
	pro      ProChecker // nil = everyone is Pro (dev)
	cascade  *cascade.Orchestrator
	maxChars int
	log      *slog.Logger
}

func NewService(
	gen Generator,
	prompts map[Mode]string,
	quota Quota,
	pro ProChecker,
	orch *cascade.Orchestrator,
	log *slog.Logger,
) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		gen: gen, prompts: prompts, quota: quota, pro: pro, cascade: orch,
		// Bounds the input, which bounds the cost. A user pasting a novel into
		// "rewrite" would otherwise be a single very expensive call.
		maxChars: 8000,
		log:      log,
	}
}

// Event is one message on the writer's SSE stream.
type Event struct {
	Type string `json:"type"` // chunk | suggestions | done | error

	// Text is a fragment of generated Tamil, streamed as the model produces it.
	Text string `json:"text,omitempty"`

	// Suggestions are proofreading fixes for the FINISHED text (§16.2). They arrive
	// after the last chunk: the cascade needs whole sentences, and a half-written one
	// would be flagged for errors it does not have yet.
	Suggestions []cascade.Suggestion `json:"suggestions,omitempty"`

	Error string `json:"error,omitempty"`
}

// Generate runs one writer request, streaming the result.
func (s *Service) Generate(ctx context.Context, userID string, req Request, emit func(Event) error) error {
	if err := req.Validate(); err != nil {
		return err
	}
	if n := len([]rune(req.Text)) + len([]rune(req.Context)); n > s.maxChars {
		return fmt.Errorf("text is %d characters; the limit is %d", n, s.maxChars)
	}

	// --- the gates, BEFORE the model is called ---------------------------
	//
	// Order matters. Entitlement first (a free user must never consume quota), then
	// quota (a Pro user must never exceed their budget). Checking either one after
	// the call would mean we had already paid for the tokens.
	if s.pro != nil {
		ok, err := s.pro.IsUserPro(ctx, userID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotPro
		}
	}
	if s.quota != nil {
		if err := s.quota.Take(ctx, userID); err != nil {
			return err
		}
	}

	system, ok := s.prompts[req.Mode]
	if !ok {
		return fmt.Errorf("no prompt for mode %q", req.Mode)
	}
	system, user, temp := render(system, req)

	var built strings.Builder
	start := time.Now()

	err := s.gen.Generate(ctx, system, user, temp, func(chunk string) error {
		built.WriteString(chunk)
		return emit(Event{Type: "chunk", Text: chunk})
	})
	if err != nil {
		return err
	}

	out := strings.TrimSpace(built.String())
	s.log.InfoContext(ctx, "generated",
		"mode", req.Mode, "chars", len([]rune(out)), "took_ms", time.Since(start).Milliseconds())

	// --- §16.2: run our own output through our own proofreader ------------
	//
	// This is the moat. A generic LLM writes plausible Tamil with sandhi and
	// agreement errors in it. Shipping that from a PROOFREADING product would be the
	// product calling itself a liar.
	//
	// A failure here is not fatal: the writer still gets their text, just unchecked.
	// Withholding generated content they have already paid for, because the checker
	// hiccupped, would be the wrong trade.
	if s.cascade != nil && out != "" {
		res, err := s.cascade.Proofread(ctx, out)
		if err != nil {
			s.log.WarnContext(ctx, "could not proofread generated text", "err", err)
		} else if len(res.Suggestions) > 0 {
			s.log.InfoContext(ctx, "cascade found issues in generated text",
				"count", len(res.Suggestions))
			if err := emit(Event{Type: "suggestions", Suggestions: res.Suggestions}); err != nil {
				return err
			}
		}
	}

	return emit(Event{Type: "done"})
}

// render fills the prompt template and picks a temperature for the mode.
func render(system string, req Request) (string, string, float64) {
	switch req.Mode {
	case ModeRewrite:
		system = strings.ReplaceAll(system, "{axis}", req.Axis)
		system = strings.ReplaceAll(system, "{target}", req.Target)
		// Transformation, not invention — keep it tight.
		return system, req.Text, 0.4

	case ModeTemplate:
		t := Templates[req.TemplateID]
		var fields strings.Builder
		for _, f := range t.Fields {
			v := req.Fields[f]
			if strings.TrimSpace(v) == "" {
				v = "[___]" // the prompt is told to leave gaps, not invent
			}
			fmt.Fprintf(&fields, "- %s: %s\n", f, v)
		}
		system = strings.ReplaceAll(system, "{template_type}", t.Name)
		system = strings.ReplaceAll(system, "{fields}", fields.String())
		return system, fields.String(), 0.5

	case ModeContinue:
		// Genuine generation, so a higher temperature than the other two.
		return system, req.Context, 0.6
	}
	return system, "", 0.4
}

/* ------------------------------------------------------------------ quota */

// RedisQuota is a per-user daily generation cap (§16.5).
type RedisQuota struct {
	rdb   *redis.Client
	limit int
}

func NewRedisQuota(rdb *redis.Client, limit int) *RedisQuota {
	if limit <= 0 {
		limit = 50
	}
	return &RedisQuota{rdb: rdb, limit: limit}
}

func (q *RedisQuota) Take(ctx context.Context, userID string) error {
	if q.rdb == nil {
		return nil // dev: no Redis, no cap
	}

	// Key is per user PER DAY, and expires on its own. No sweeper job, no unbounded
	// key growth — the TTL is the reset.
	key := fmt.Sprintf("pt:writer:%s:%s", userID, time.Now().UTC().Format("2006-01-02"))

	n, err := q.rdb.Incr(ctx, key).Result()
	if err != nil {
		// FAIL OPEN. Redis being down must not lock a paying user out of the feature
		// they bought. The blast radius is bounded — a Redis outage is measured in
		// minutes, and the cost of over-serving for that long is far smaller than the
		// cost of a Pro subscriber finding the product broken.
		return nil
	}
	if n == 1 {
		// First call of the day: give the key a lifetime slightly over 24h so a user
		// near midnight is not reset mid-session.
		q.rdb.Expire(ctx, key, 25*time.Hour)
	}
	if int(n) > q.limit {
		return ErrQuotaExceeded
	}
	return nil
}

/* -------------------------------------------------------------------- pro */

// AlwaysPro is the DEV stand-in for entitlement.
//
// Auth and billing do not exist yet (Phase 6, blocked on the §1 accounts), so there is
// no subscription table to consult. This says so out loud rather than pretending to
// check. When billing lands, the real implementation reads `subscriptions` and this
// type is deleted — a compile error at every call site is exactly the reminder we
// want, which is why the interface is not defaulted to nil.
type AlwaysPro struct{ Log *slog.Logger }

func (a AlwaysPro) IsUserPro(context.Context, string) (bool, error) {
	return true, nil
}

var _ ProChecker = AlwaysPro{}

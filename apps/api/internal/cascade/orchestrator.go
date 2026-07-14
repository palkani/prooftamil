package cascade

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/prooftamil/api/internal/cache"
)

// Tier1 is the deterministic corrector. An interface so the orchestrator can be
// tested without a live ML service, and so Tier 1 can be swapped for a gRPC
// transport later without touching this file.
type Tier1 interface {
	Analyze(ctx context.Context, target, before, after string) (*analyzeResponse, error)
}

// Orchestrator sequences the cascade tiers.
type Orchestrator struct {
	tier1 Tier1
	cache *cache.Exact
	log   *slog.Logger

	// confidenceGate suppresses low-confidence suggestions before the user ever
	// sees them (CONFIDENCE_GATE, §3.1). Tier 1 reports ambiguous corrections at
	// low confidence precisely so this gate can drop them.
	confidenceGate float64
}

func NewOrchestrator(tier1 Tier1, c *cache.Exact, gate float64, log *slog.Logger) *Orchestrator {
	if log == nil {
		log = slog.Default()
	}
	return &Orchestrator{tier1: tier1, cache: c, confidenceGate: gate, log: log}
}

// Proofread runs the cascade over `text` and returns everything the fast tiers
// could resolve. Anything left over is signalled by Result.ModelPending, and the
// client keeps its SSE stream open for the model tiers (Phase 2).
func (o *Orchestrator) Proofread(ctx context.Context, text string) (*Result, error) {
	start := time.Now()

	segments := Segment(text)
	if len(segments) == 0 {
		return &Result{Suggestions: []Suggestion{}, LatencyMS: 0}, nil
	}

	// --- Tier 2: exact cache, per sentence -------------------------------
	//
	// Cached PER SENTENCE, not per document. Documents are nearly unique, so a
	// document-level key would almost never hit; sentences repeat constantly
	// across users and across edits of the same draft. Per-sentence keys also
	// mean editing one sentence still hits cache for every other sentence — the
	// common case while someone is typing.
	var (
		out          []Suggestion
		allCached    = true
		modelPending bool
	)

	for i, seg := range segments {
		key := NormalizeForCache(seg.Text)

		var cached []Suggestion
		if err := o.cache.Get(ctx, key, &cached); err == nil {
			out = append(out, offsetBy(cached, seg.Start)...)
			continue
		} else if !errors.Is(err, cache.ErrMiss) {
			// A cache failure must degrade to a miss, never fail the request.
			o.log.WarnContext(ctx, "cache get failed; treating as miss", "err", err)
		}

		allCached = false

		// --- Tier 1: deterministic rules ---------------------------------
		before, after := Context(segments, i)
		res, err := o.tier1.Analyze(ctx, seg.Text, before, after)
		if err != nil {
			// Tier 1 being down must not break proofreading — the model tiers can
			// still answer. Log it and fall through; readiness will pull the
			// instance if the ML service is really gone.
			o.log.WarnContext(ctx, "tier1 failed; falling through to model tiers", "err", err)
			modelPending = true
			continue
		}

		gated := o.gate(res.Suggestions)
		out = append(out, offsetBy(gated, seg.Start)...)

		// Cache what Tier 1 produced for this sentence.
		if err := o.cache.Set(ctx, key, gated); err != nil {
			o.log.WarnContext(ctx, "cache set failed", "err", err)
		}

		// Tier 1 can prove a word is misspelled but never that a sentence is
		// clean, so it always reports unresolved and a model tier still owes an
		// answer.
		if !res.Resolved {
			modelPending = true
		}
	}

	if out == nil {
		out = []Suggestion{}
	}

	return &Result{
		Suggestions:  out,
		CacheHit:     allCached,
		ModelPending: modelPending,
		LatencyMS:    time.Since(start).Milliseconds(),
	}, nil
}

// Event is one message on the SSE stream (§7.2).
type Event struct {
	// Seq lets the client drop stale responses. A fast typist outruns the
	// cascade, so results for an edit the user has already replaced WILL arrive
	// late; without a sequence number the editor would flicker corrections for
	// text that no longer exists.
	Seq int `json:"seq"`

	Type         string       `json:"type"` // suggestions | done | error
	Suggestions  []Suggestion `json:"suggestions,omitempty"`
	ModelPending bool         `json:"model_pending,omitempty"`
	Error        string       `json:"error,omitempty"`
}

// Stream runs the cascade and emits results sentence-by-sentence as they land,
// rather than making the writer wait for the whole document.
//
// emit is called from this goroutine; returning an error from it (a disconnected
// client) aborts the run.
func (o *Orchestrator) Stream(ctx context.Context, text string, emit func(Event) error) error {
	segments := Segment(text)
	seq := 0

	modelPending := false

	for i, seg := range segments {
		// Abandon the work as soon as the client goes away — no point paying for
		// tiers whose output nobody will read.
		if err := ctx.Err(); err != nil {
			return err
		}

		key := NormalizeForCache(seg.Text)

		var suggestions []Suggestion
		var cached []Suggestion
		if err := o.cache.Get(ctx, key, &cached); err == nil {
			suggestions = offsetBy(cached, seg.Start)
		} else {
			before, after := Context(segments, i)
			res, err := o.tier1.Analyze(ctx, seg.Text, before, after)
			if err != nil {
				o.log.WarnContext(ctx, "tier1 failed during stream", "err", err)
				modelPending = true
				continue
			}
			gated := o.gate(res.Suggestions)
			if err := o.cache.Set(ctx, key, gated); err != nil {
				o.log.WarnContext(ctx, "cache set failed", "err", err)
			}
			suggestions = offsetBy(gated, seg.Start)
			if !res.Resolved {
				modelPending = true
			}
		}

		if len(suggestions) > 0 {
			seq++
			if err := emit(Event{Seq: seq, Type: "suggestions", Suggestions: suggestions}); err != nil {
				return err
			}
		}
	}

	seq++
	// ModelPending tells the client whether to keep the stream open for the model
	// tiers (Phase 2). Today it is essentially always true.
	return emit(Event{Seq: seq, Type: "done", ModelPending: modelPending})
}

// gate drops suggestions below the confidence threshold. Tier 1 deliberately
// emits ambiguous corrections at low confidence so they die here rather than
// being shown to a writer as if they were certain.
func (o *Orchestrator) gate(in []Suggestion) []Suggestion {
	out := make([]Suggestion, 0, len(in))
	for _, s := range in {
		if s.Confidence >= o.confidenceGate {
			out = append(out, s)
		}
	}
	return out
}

// offsetBy maps sentence-relative offsets onto the document.
//
// Both are RUNE offsets, so this is plain addition. It would be wrong if either
// side were bytes: Tamil is 3 bytes per character in UTF-8, so a byte offset
// added to a rune offset would land mid-character and corrupt the span.
func offsetBy(in []Suggestion, delta int) []Suggestion {
	out := make([]Suggestion, len(in))
	for i, s := range in {
		s.Start += delta
		s.End += delta
		out[i] = s
	}
	return out
}

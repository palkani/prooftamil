package corrector

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/prooftamil/api/internal/cascade"
)

// Router runs the model tiers (plan §8).
//
// FALLBACK IS ON ERROR, NOT ON A TIMER (§8.6).
//
// An earlier version raced the fallback after HEDGE_DELAY_MS. That was tuned for a
// latency regime that does not exist. Measured live:
//
//	Gemini (primary):   p50 0.9s,  tail 1.2s
//	Sarvam (fallback):  p50 7.8s,  tail 17.8s
//
// A hedge only pays when the second model might FINISH FIRST. Sarvam is ~8x slower,
// so it can never win the race — the hedge would fire on the tail of every slow
// request, double the spend, and still return Gemini's answer. Pure cost, zero
// latency benefit.
//
// So the fallback fires only when the primary actually FAILS: an error, or
// MODEL_TIMEOUT_MS elapsing. That is failover, which is about availability, not
// latency.
type Router struct {
	primary  Corrector // Gemini 2.5-flash, thinking OFF
	fallback Corrector // Sarvam — error-only, kept for provider independence
	verifier Verifier  // Gemini, thinking ON

	// timeout bounds the primary. Past this it is treated as failed and the fallback
	// takes over. Sized well above the primary's measured tail (1.2s) so a merely
	// slow-but-alive call is not thrown away.
	timeout time.Duration

	// tier1Only disables the model tiers entirely (§8.5). A feature flag, not a
	// config accident: if the fallback's quality ever proves WORSE than silence, the
	// safe degraded path is deterministic Tier 1 alone — it can never surface a
	// hallucinated fix.
	tier1Only bool

	verifyBelow float64

	log *slog.Logger
}

type RouterOption func(*Router)

func WithVerifier(v Verifier, below float64) RouterOption {
	return func(r *Router) { r.verifier = v; r.verifyBelow = below }
}

// WithTier1Only forces the degraded path: no model is ever called (§8.5).
func WithTier1Only(on bool) RouterOption {
	return func(r *Router) { r.tier1Only = on }
}

func NewRouter(primary, fallback Corrector, timeout time.Duration, log *slog.Logger, opts ...RouterOption) *Router {
	if log == nil {
		log = slog.Default()
	}
	if timeout <= 0 {
		timeout = 6 * time.Second
	}
	r := &Router{primary: primary, fallback: fallback, timeout: timeout, log: log}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Correct runs the primary, and falls back ONLY if it fails or times out.
func (r *Router) Correct(ctx context.Context, req Request) (*Response, error) {
	if r.tier1Only {
		// §8.5 — the degraded path. Tier 1's deterministic corrections still reach the
		// user; the model residue simply does not. Safe by construction.
		return nil, errors.New("model tier disabled (tier-1-only mode)")
	}
	if r.primary == nil && r.fallback == nil {
		return nil, errors.New("no model configured")
	}

	if r.primary != nil {
		resp, err := r.callWithTimeout(ctx, r.primary, req)
		if err == nil {
			return r.verify(ctx, req, resp)
		}
		r.log.WarnContext(ctx, "primary failed; falling back",
			"model", r.primary.Name(), "err", err, "timeout", r.timeout)

		if r.fallback == nil {
			return nil, err
		}
	}

	// The fallback exists for provider independence — a Google outage must not take
	// the product down — not for speed. Its output passes the SAME quote-anchor and
	// confidence gates, which is exactly what rejected its live English hallucination.
	resp, err := r.callWithTimeout(ctx, r.fallback, req)
	if err != nil {
		return nil, err
	}
	r.log.InfoContext(ctx, "served by fallback", "model", r.fallback.Name())
	return r.verify(ctx, req, resp)
}

// callWithTimeout bounds a single model call. Without this a hung provider would hold
// the user's editor open until the HTTP client's own (much longer) timeout fired.
func (r *Router) callWithTimeout(ctx context.Context, c Corrector, req Request) (*Response, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	resp, err := c.Correct(ctx, req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, errors.New(c.Name() + ": timed out after " + r.timeout.String())
		}
		return nil, err
	}
	return resp, nil
}

// CorrectSentence adapts Router to cascade.ModelTier, which is declared in terms of
// plain strings so the cascade package does not have to import this one.
func (r *Router) CorrectSentence(ctx context.Context, target, before, after string) ([]cascade.Suggestion, error) {
	resp, err := r.Correct(ctx, Request{
		Target:        target,
		ContextBefore: before,
		ContextAfter:  after,
	})
	if err != nil {
		return nil, err
	}
	return resp.Suggestions, nil
}

// verify gives the verifier a veto over low-confidence suggestions (§8.3). High-
// confidence ones pass straight through: verifying everything would double the cost
// of the expensive path and add a round trip to corrections we were already sure of.
func (r *Router) verify(ctx context.Context, req Request, resp *Response) (*Response, error) {
	if r.verifier == nil || len(resp.Suggestions) == 0 {
		return resp, nil
	}

	kept := make([]cascade.Suggestion, 0, len(resp.Suggestions))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, s := range resp.Suggestions {
		if s.Confidence >= r.verifyBelow {
			mu.Lock()
			kept = append(kept, s)
			mu.Unlock()
			continue
		}

		wg.Add(1)
		go func(s cascade.Suggestion) {
			defer wg.Done()

			v, err := r.verifier.Verify(ctx, req.Target, s)
			if err != nil {
				// The verifier is down. Keeping an unverified low-confidence suggestion
				// would show the writer exactly the guess we wanted a second opinion on,
				// so drop it. Silence is the safe failure.
				r.log.WarnContext(ctx, "verifier unavailable; dropping unverified suggestion",
					"err", err, "original", s.Original)
				return
			}
			if !v.Approve {
				r.log.InfoContext(ctx, "verifier rejected",
					"original", s.Original, "suggestion", s.Suggestion, "reason", v.Reason)
				return
			}

			if v.RevisedSuggestion != "" && v.RevisedSuggestion != s.Suggestion {
				s.Suggestion = v.RevisedSuggestion
			}
			// Approval is evidence, so the suggestion inherits the verifier's confidence —
			// that is what lets it clear the gate it would otherwise have failed.
			s.Confidence = v.Confidence
			s.SourceTier = cascade.TierVerifier

			mu.Lock()
			kept = append(kept, s)
			mu.Unlock()
		}(s)
	}
	wg.Wait()

	sortByPosition(kept)
	resp.Suggestions = kept
	return resp, nil
}

func sortByPosition(s []cascade.Suggestion) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j].Start < s[j-1].Start; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

package corrector

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/prooftamil/api/internal/cascade"
)

// Router runs the model tiers: a primary corrector, a hedged fallback, and a
// verifier for anything the primary was not sure about.
type Router struct {
	primary  Corrector // Sarvam
	fallback Corrector // Gemini, in its corrector role
	verifier Verifier  // Gemini, in its verifier role

	// hedgeDelay is how long we wait for the primary before ALSO firing the
	// fallback (HEDGE_DELAY_MS, §3.1). This is a latency device, not a failover
	// one: failover happens on error, hedging happens on slowness.
	hedgeDelay time.Duration

	// verifyBelow: suggestions the primary reports at or above this confidence are
	// shown as-is. Below it, the verifier gets a veto.
	//
	// Verifying EVERYTHING would double the cost of the expensive path and add a
	// second round trip to corrections we were already confident about. Verifying
	// NOTHING would let the primary's shakiest guesses reach the writer. This
	// threshold is where that trade is set.
	verifyBelow float64

	log *slog.Logger
}

type RouterOption func(*Router)

func WithVerifier(v Verifier, below float64) RouterOption {
	return func(r *Router) { r.verifier = v; r.verifyBelow = below }
}

func NewRouter(primary, fallback Corrector, hedgeDelay time.Duration, log *slog.Logger, opts ...RouterOption) *Router {
	if log == nil {
		log = slog.Default()
	}
	r := &Router{
		primary:    primary,
		fallback:   fallback,
		hedgeDelay: hedgeDelay,
		log:        log,
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

type attempt struct {
	resp *Response
	err  error
	who  string
}

// Correct runs the hedged primary/fallback race, then verifies the low-confidence
// survivors.
//
// The hedge:
//
//	t=0            fire the primary (Sarvam)
//	t=hedgeDelay   if it has not answered, ALSO fire the fallback (Gemini)
//	               — we do not cancel the primary; it may still win
//	first success  wins; the loser is cancelled
//
// This bounds tail latency without doubling cost on the common path: when the
// primary answers inside hedgeDelay (the normal case) the fallback is never
// called at all. It costs a second call only on the slow tail, which is exactly
// where a user is about to give up.
//
// If the primary FAILS (rather than being slow) the fallback fires immediately —
// no point waiting out the hedge delay for an answer that is never coming.
func (r *Router) Correct(ctx context.Context, req Request) (*Response, error) {
	if r.primary == nil && r.fallback == nil {
		return nil, errors.New("no model configured")
	}
	// With only one model available, the hedge is meaningless — just call it.
	if r.primary == nil {
		return r.withVerification(ctx, req, r.fallback)
	}
	if r.fallback == nil {
		return r.withVerification(ctx, req, r.primary)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // cancels whichever call lost the race

	results := make(chan attempt, 2)
	var wg sync.WaitGroup

	fire := func(c Corrector) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := c.Correct(ctx, req)
			results <- attempt{resp: resp, err: err, who: c.Name()}
		}()
	}

	fire(r.primary)

	hedge := time.NewTimer(r.hedgeDelay)
	defer hedge.Stop()

	var (
		firstErr error
		pending  = 1
		hedged   bool
	)

	for pending > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()

		case <-hedge.C:
			// The primary is slow. Race the fallback alongside it rather than
			// replacing it — the primary may still be about to answer.
			if !hedged {
				hedged = true
				pending++
				r.log.InfoContext(ctx, "hedging: primary slow, firing fallback",
					"after", r.hedgeDelay, "fallback", r.fallback.Name())
				fire(r.fallback)
			}

		case a := <-results:
			pending--
			if a.err == nil {
				r.log.InfoContext(ctx, "model tier resolved",
					"winner", a.who, "hedged", hedged,
					"suggestions", len(a.resp.Suggestions), "latency_ms", a.resp.LatencyMS)
				return r.verify(ctx, req, a.resp)
			}

			r.log.WarnContext(ctx, "corrector failed", "model", a.who, "err", a.err)
			if firstErr == nil {
				firstErr = a.err
			}

			// The primary died rather than dragged. Don't sit out the hedge delay
			// waiting for a corpse — fire the fallback now.
			if !hedged {
				hedged = true
				pending++
				fire(r.fallback)
			}
		}
	}

	return nil, firstErr
}

// CorrectSentence adapts Router to cascade.ModelTier, which is declared in terms
// of plain strings so the cascade package does not have to import this one.
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

func (r *Router) withVerification(ctx context.Context, req Request, c Corrector) (*Response, error) {
	resp, err := c.Correct(ctx, req)
	if err != nil {
		return nil, err
	}
	return r.verify(ctx, req, resp)
}

// verify gives the verifier a veto over the primary's low-confidence suggestions
// (§8.2). High-confidence ones pass straight through.
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
				// The verifier is unavailable. Keeping an unverified low-confidence
				// suggestion would show the writer exactly the guess we wanted a
				// second opinion on, so drop it. Staying silent is the safe failure.
				r.log.WarnContext(ctx, "verifier unavailable; dropping unverified suggestion",
					"err", err, "original", s.Original)
				return
			}
			if !v.Approve {
				r.log.InfoContext(ctx, "verifier rejected",
					"original", s.Original, "suggestion", s.Suggestion, "reason", v.Reason)
				return
			}

			// The verifier may propose something better than the primary did.
			if v.RevisedSuggestion != "" && v.RevisedSuggestion != s.Suggestion {
				s.Suggestion = v.RevisedSuggestion
			}
			// Approval is evidence, so the suggestion inherits the verifier's
			// confidence — that is what lets it clear the confidence gate it would
			// otherwise have failed.
			s.Confidence = v.Confidence
			s.SourceTier = cascade.TierVerifier

			mu.Lock()
			kept = append(kept, s)
			mu.Unlock()
		}(s)
	}
	wg.Wait()

	// Verification runs concurrently, so restore document order.
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

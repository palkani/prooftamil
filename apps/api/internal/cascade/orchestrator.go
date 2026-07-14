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

// ModelTier is the paid path: Sarvam primary, hedged Gemini fallback, Gemini
// verifier (Tiers 3–4). Declared here rather than importing the corrector package
// so the dependency runs one way (corrector -> cascade) and the orchestrator stays
// testable with a fake.
//
// Nil is a supported configuration: with no API keys the cascade runs Tier 1 + 2
// only. That is what lets the whole stack come up in dev before the §1 accounts
// exist.
type ModelTier interface {
	CorrectSentence(ctx context.Context, target, before, after string) ([]Suggestion, error)
}

// Orchestrator sequences the cascade tiers.
type Orchestrator struct {
	tier1  Tier1
	models ModelTier // nil when no API keys are configured (dev)
	cache  *cache.Exact
	log    *slog.Logger

	// confidenceGate suppresses low-confidence suggestions before the user ever
	// sees them (CONFIDENCE_GATE, §3.1). Tier 1 reports ambiguous corrections at
	// low confidence precisely so this gate can drop them.
	confidenceGate float64
}

func NewOrchestrator(tier1 Tier1, models ModelTier, c *cache.Exact, gate float64, log *slog.Logger) *Orchestrator {
	if log == nil {
		log = slog.Default()
	}
	return &Orchestrator{tier1: tier1, models: models, cache: c, confidenceGate: gate, log: log}
}

// merge combines Tier 1 and model suggestions, dropping model suggestions that
// overlap a span Tier 1 already covered.
//
// Both tiers see the same sentence, so both will often flag the same word. Tier 1
// wins those: it is deterministic, free, and already correct. Showing the writer
// two overlapping underlines on one word is a bug, and applying both would corrupt
// the text.
//
// It returns the merged set AND, separately, the model suggestions that survived.
// The streaming path needs that second list: it has already sent Tier 1 to the
// client, so it must emit only the genuinely new ones. Slicing the merged set
// would be wrong — merged is sorted by position, so the model's additions are
// interleaved with Tier 1's, not appended after them.
func merge(tier1, model []Suggestion) (merged, modelOnly []Suggestion) {
	modelOnly = make([]Suggestion, 0, len(model))

	for _, m := range model {
		overlaps := false
		for _, t := range tier1 {
			if m.Start < t.End && t.Start < m.End {
				overlaps = true
				break
			}
		}
		if !overlaps {
			modelOnly = append(modelOnly, m)
		}
	}

	merged = make([]Suggestion, 0, len(tier1)+len(modelOnly))
	merged = append(merged, tier1...)
	merged = append(merged, modelOnly...)
	sortByPosition(merged)

	return merged, modelOnly
}

func sortByPosition(s []Suggestion) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j].Start < s[j-1].Start; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
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

		tier1 := o.gate(res.Suggestions)

		// --- Tiers 3–4: the model path ------------------------------------
		//
		// Reached only on a cache miss that Tier 1 could not resolve, which is the
		// whole point of the cascade: this is the call that costs money.
		var modelSuggestions []Suggestion
		if o.models != nil && !res.Resolved {
			ms, err := o.models.CorrectSentence(ctx, seg.Text, before, after)
			if err != nil {
				// The models are down. Tier 1's findings are still real and still
				// worth showing — degrade to them rather than failing the request.
				o.log.WarnContext(ctx, "model tier failed; serving tier 1 only", "err", err)
				modelPending = true
			} else {
				modelSuggestions = o.gate(ms)
			}
		} else if o.models == nil {
			// No API keys configured (dev). Be explicit that a tier is missing
			// rather than implying the sentence was fully checked.
			modelPending = true
		}

		combined, _ := merge(tier1, modelSuggestions)
		out = append(out, offsetBy(combined, seg.Start)...)

		// Cache the COMBINED result. Caching Tier 1 alone would be worse than
		// useless: the next request for this sentence would hit cache, skip the
		// model tier, and silently return a partial answer forever.
		if err := o.cache.Set(ctx, key, combined); err != nil {
			o.log.WarnContext(ctx, "cache set failed", "err", err)
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

		// A cache hit is the whole answer — both tiers, already merged.
		var cached []Suggestion
		if err := o.cache.Get(ctx, key, &cached); err == nil {
			if len(cached) > 0 {
				seq++
				if err := emit(Event{Seq: seq, Type: "suggestions",
					Suggestions: offsetBy(cached, seg.Start)}); err != nil {
					return err
				}
			}
			continue
		}

		before, after := Context(segments, i)

		res, err := o.tier1.Analyze(ctx, seg.Text, before, after)
		if err != nil {
			o.log.WarnContext(ctx, "tier1 failed during stream", "err", err)
			modelPending = true
			continue
		}
		tier1 := o.gate(res.Suggestions)

		// Emit Tier 1 IMMEDIATELY, before touching a model. This is the point of
		// streaming: deterministic corrections land in single-digit milliseconds
		// while the model takes hundreds. The writer sees their spelling fixed
		// while the grammar check is still in flight.
		if len(tier1) > 0 {
			seq++
			if err := emit(Event{Seq: seq, Type: "suggestions",
				Suggestions: offsetBy(tier1, seg.Start)}); err != nil {
				return err
			}
		}

		if o.models == nil || res.Resolved {
			if o.models == nil {
				modelPending = true
			}
			if err := o.cache.Set(ctx, key, tier1); err != nil {
				o.log.WarnContext(ctx, "cache set failed", "err", err)
			}
			continue
		}

		ms, err := o.models.CorrectSentence(ctx, seg.Text, before, after)
		if err != nil {
			// Tier 1's results already went out and remain valid; only the model
			// residue is missing. Do NOT cache a partial result — the next request
			// would hit it and never call the model at all.
			o.log.WarnContext(ctx, "model tier failed during stream", "err", err)
			modelPending = true
			continue
		}

		combined, modelOnly := merge(tier1, o.gate(ms))
		if err := o.cache.Set(ctx, key, combined); err != nil {
			o.log.WarnContext(ctx, "cache set failed", "err", err)
		}

		// Emit only what Tier 1 did not already send, so the client never receives
		// the same underline twice.
		if len(modelOnly) > 0 {
			seq++
			if err := emit(Event{Seq: seq, Type: "suggestions",
				Suggestions: offsetBy(modelOnly, seg.Start)}); err != nil {
				return err
			}
		}
	}

	seq++
	// ModelPending tells the client a tier could not be consulted — models down,
	// or not configured. It is NOT set when the models ran successfully.
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

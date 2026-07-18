package corrector

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prooftamil/api/internal/cascade"
)

// --- response validation (the security boundary) ---------------------------

const target = "அந்த பையன் வந்தான்" // அந்த = runes 0..4

func TestValidateLocatesTheQuotedTextAndDerivesOffsets(t *testing.T) {
	// The model quotes; the SERVER computes offsets. It never sends start/end.
	raw := rawResponse{Suggestions: []rawSuggestion{
		{Quote: "அந்த", Suggestion: "அந்தப்", Type: "sandhi", Confidence: 0.95},
	}}

	got, err := validate(raw, target, cascade.TierPrimary)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d suggestions, want 1", len(got))
	}

	// Derived offsets must actually index the quoted word.
	runes := []rune(target)
	if s := string(runes[got[0].Start:got[0].End]); s != "அந்த" {
		t.Errorf("derived offsets [%d:%d] select %q, want அந்த", got[0].Start, got[0].End, s)
	}
	if got[0].SourceTier != cascade.TierPrimary {
		t.Errorf("source tier = %d, want %d", got[0].SourceTier, cascade.TierPrimary)
	}
}

// REGRESSION (found live). Prompt v1 asked the model for character offsets, and
// Gemini returned the offsets memorised from the prompt's own few-shot example
// (18/24) rather than counting the real sentence (20/26). Deriving offsets from the
// quoted text makes the whole class of bug impossible: wherever the word actually
// is, that is where the underline goes.
func TestOffsetsAreDerivedNotTrusted(t *testing.T) {
	sentence := "நாங்கள் நகரத்திற்கு போனேன்." // போனேன் truly sits at runes 20..26
	raw := rawResponse{Suggestions: []rawSuggestion{
		{Quote: "போனேன்", Suggestion: "போனோம்", Type: "agreement", Confidence: 0.96},
	}}

	got, _ := validate(raw, sentence, cascade.TierPrimary)
	if len(got) != 1 {
		t.Fatalf("got %d, want 1 — this correction was being silently dropped", len(got))
	}
	if got[0].Start != 20 || got[0].End != 26 {
		t.Errorf("offsets = [%d:%d], want [20:26]", got[0].Start, got[0].End)
	}
	if s := string([]rune(sentence)[got[0].Start:got[0].End]); s != "போனேன்" {
		t.Errorf("offsets select %q, want போனேன்", s)
	}
}

func TestValidateRejectsTextThatIsNotInTheSentence(t *testing.T) {
	// The model quoted something that does not appear in the target: it
	// hallucinated, paraphrased, or (observed live from Sarvam) answered in
	// English. It must not touch the writer's document.
	for _, orig := range []string{"NOT_IN_TEXT", "they vanijars", "பையன்கள்"} {
		raw := rawResponse{Suggestions: []rawSuggestion{
			{Quote: orig, Suggestion: "x", Type: "grammar", Confidence: 0.99},
		}}
		if got, _ := validate(raw, target, cascade.TierPrimary); len(got) != 0 {
			t.Errorf("quoting %q (absent from the target) must be rejected", orig)
		}
	}
}

func TestValidateRejectsUnknownTypeAndInsaneConfidence(t *testing.T) {
	for _, raw := range []rawSuggestion{
		{Quote: "அந்த", Suggestion: "அந்தப்", Type: "vibes", Confidence: 0.9},
		{Quote: "அந்த", Suggestion: "அந்தப்", Type: "sandhi", Confidence: 7.0},
		{Quote: "அந்த", Suggestion: "அந்தப்", Type: "sandhi", Confidence: -1},
	} {
		if got, _ := validate(rawResponse{Suggestions: []rawSuggestion{raw}}, target, cascade.TierPrimary); len(got) != 0 {
			t.Errorf("must reject %+v", raw)
		}
	}
}

func TestValidateRejectsANoOpCorrection(t *testing.T) {
	// "Correcting" a word to itself is noise in the editor.
	raw := rawResponse{Suggestions: []rawSuggestion{
		{Quote: "அந்த", Suggestion: "அந்த", Type: "sandhi", Confidence: 0.9},
	}}
	if got, _ := validate(raw, target, cascade.TierPrimary); len(got) != 0 {
		t.Error("a suggestion that changes nothing must be dropped")
	}
}

func TestValidateRejectsNoOpAcrossUnicodeNormalisation(t *testing.T) {
	// மோதல் → மோதல் as seen live: the quote is composed (ோ = U+0BCB) and the model
	// echoes it back decomposed (U+0BC7 + U+0BBE). Byte-different, visually identical,
	// and a raw == let it through. It must be dropped as a no-op.
	const composed = "மோதல்"
	decomposed := strings.ReplaceAll(composed, "\u0BCB", "\u0BC7\u0BBE")
	if composed == decomposed {
		t.Fatal("test setup: strings should differ byte-for-byte")
	}
	sentence := "இங்கே " + composed + " உள்ளது."
	raw := rawResponse{Suggestions: []rawSuggestion{
		{Quote: composed, Suggestion: decomposed, Type: "spelling", Confidence: 0.9},
	}}
	if got, _ := validate(raw, sentence, cascade.TierPrimary); len(got) != 0 {
		t.Errorf("a no-op that differs only in Unicode normalisation must be dropped, got %d", len(got))
	}
}

func TestValidateKeepsGoodRowsWhenOneRowIsBad(t *testing.T) {
	// One unlocatable row must not discard the model's correct work.
	raw := rawResponse{Suggestions: []rawSuggestion{
		{Quote: "அந்த", Suggestion: "அந்தப்", Type: "sandhi", Confidence: 0.95},
		{Quote: "junk", Suggestion: "junk2", Type: "spelling", Confidence: 0.9},
	}}

	got, _ := validate(raw, target, cascade.TierPrimary)
	if len(got) != 1 {
		t.Fatalf("got %d, want 1 (keep the good row, drop the bad)", len(got))
	}
	if got[0].Suggestion != "அந்தப்" {
		t.Errorf("kept the wrong row: %+v", got[0])
	}
}

// §8.4 — AN AMBIGUOUS QUOTE IS REFUSED, NOT GUESSED AT.
//
// The model wants to fix the SECOND அந்த but gives no context. Resolving to the first
// occurrence would rewrite a word in a sentence the writer never asked about — a
// confident, silent, WRONG edit. A miss costs one uncaught error; a wrong-instance
// edit costs trust in every suggestion. So we withhold.
func TestAmbiguousQuoteWithoutContextIsDropped(t *testing.T) {
	sentence := "அந்த பையன் வந்தான். அந்த பெண் வந்தாள்."
	raw := rawResponse{Suggestions: []rawSuggestion{
		{Quote: "அந்த", Suggestion: "அந்தப்", Type: "sandhi", Confidence: 0.95},
	}}

	got, _ := validate(raw, sentence, cascade.TierPrimary)
	if len(got) != 0 {
		t.Fatalf("an ambiguous quote must be DROPPED, not resolved to the first match; got %+v", got)
	}
}

// With quote_context the occurrence is pinned, so the correction is safe to apply.
func TestQuoteContextDisambiguatesARepeatedQuote(t *testing.T) {
	sentence := "அந்த பையன் வந்தான். அந்த பெண் வந்தாள்."
	raw := rawResponse{Suggestions: []rawSuggestion{
		{Quote: "அந்த", QuoteContext: "அந்த பெண்", Suggestion: "அந்தப்", Type: "sandhi", Confidence: 0.95},
	}}

	got, _ := validate(raw, sentence, cascade.TierPrimary)
	if len(got) != 1 {
		t.Fatalf("quote_context should have resolved this; got %d", len(got))
	}

	// It must land on the SECOND அந்த, not the first.
	runes := []rune(sentence)
	if string(runes[got[0].Start:got[0].End]) != "அந்த" {
		t.Errorf("span does not select அந்த")
	}
	second := len([]rune("அந்த பையன் வந்தான். "))
	if got[0].Start != second {
		t.Errorf("resolved to offset %d, want %d (the SECOND occurrence)", got[0].Start, second)
	}
}

// Both occurrences legitimately need fixing: each carries its own context.
func TestBothOccurrencesCanBeCorrectedWithContext(t *testing.T) {
	sentence := "அந்த பையன் வந்தான். அந்த பெண் வந்தாள்."
	raw := rawResponse{Suggestions: []rawSuggestion{
		{Quote: "அந்த", QuoteContext: "அந்த பையன்", Suggestion: "அந்தப்", Type: "sandhi", Confidence: 0.95},
		{Quote: "அந்த", QuoteContext: "அந்த பெண்", Suggestion: "அந்தப்", Type: "sandhi", Confidence: 0.95},
	}}

	got, _ := validate(raw, sentence, cascade.TierPrimary)
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	if got[0].Start == got[1].Start {
		t.Error("both landed on the same occurrence")
	}
}

// --- JSON extraction -------------------------------------------------------

func TestExtractJSONSurvivesModelFormattingHabits(t *testing.T) {
	want := `{"suggestions":[]}`
	for _, in := range []string{
		`{"suggestions":[]}`,
		"```json\n{\"suggestions\":[]}\n```",
		"```\n{\"suggestions\":[]}\n```",
		"Here is the JSON:\n{\"suggestions\":[]}",
	} {
		got, err := extractJSON(in)
		if err != nil {
			t.Errorf("extractJSON(%q) errored: %v", in, err)
			continue
		}
		if strings.ReplaceAll(got, " ", "") != want {
			t.Errorf("extractJSON(%q) = %q", in, got)
		}
	}

	if _, err := extractJSON("I'm sorry, I can't help with that."); err == nil {
		t.Error("a reply with no JSON object must error")
	}
}

// --- the hedged router -----------------------------------------------------

type fakeCorrector struct {
	name  string
	delay time.Duration
	err   error
	calls atomic.Int32
}

func (f *fakeCorrector) Name() string { return f.name }

func (f *fakeCorrector) Correct(ctx context.Context, _ Request) (*Response, error) {
	f.calls.Add(1)
	select {
	case <-time.After(f.delay):
	case <-ctx.Done():
		return nil, ctx.Err() // cancelled: it lost the race
	}
	if f.err != nil {
		return nil, f.err
	}
	return &Response{
		Model: f.name,
		Suggestions: []cascade.Suggestion{
			{Start: 0, End: 4, Original: "அந்த", Suggestion: "அந்தப்", Type: "sandhi", Confidence: 0.95},
		},
	}, nil
}

func req() Request { return Request{Target: target} }

func TestFallbackIsNotFiredWhenThePrimarySucceeds(t *testing.T) {
	// §8.6 — the fallback is FAILOVER, not a latency hedge. A slow-but-working primary
	// must never cost a second call: Sarvam is ~8x slower than Gemini, so racing it
	// could not win the race anyway — it would only double the spend.
	primary := &fakeCorrector{name: "gemini", delay: 300 * time.Millisecond}
	fallback := &fakeCorrector{name: "sarvam"}

	r := NewRouter(primary, fallback, 6*time.Second, nil)

	resp, err := r.Correct(context.Background(), req())
	if err != nil {
		t.Fatal(err)
	}
	if resp.Model != "gemini" {
		t.Errorf("winner = %s, want gemini", resp.Model)
	}
	if n := fallback.calls.Load(); n != 0 {
		t.Errorf("fallback called %d times; a working primary must never cost a second call", n)
	}
}

// FAULT INJECTION (§8.6, Phase 2 exit check): Gemini down, Sarvam serves.
func TestFallbackFiresOnPrimaryError(t *testing.T) {
	primary := &fakeCorrector{name: "gemini", err: errors.New("503 service unavailable")}
	fallback := &fakeCorrector{name: "sarvam", delay: 10 * time.Millisecond}

	r := NewRouter(primary, fallback, 6*time.Second, nil)

	resp, err := r.Correct(context.Background(), req())
	if err != nil {
		t.Fatalf("a primary outage must be survivable: %v", err)
	}
	if resp.Model != "sarvam" {
		t.Errorf("winner = %s, want sarvam", resp.Model)
	}
}

func TestFallbackFiresOnPrimaryTimeout(t *testing.T) {
	// MODEL_TIMEOUT_MS: a hung provider must not hold the writer's editor open until
	// the HTTP client's own (much longer) timeout fires.
	primary := &fakeCorrector{name: "gemini", delay: 10 * time.Second}
	fallback := &fakeCorrector{name: "sarvam", delay: 10 * time.Millisecond}

	r := NewRouter(primary, fallback, 200*time.Millisecond, nil)

	start := time.Now()
	resp, err := r.Correct(context.Background(), req())
	if err != nil {
		t.Fatalf("a timed-out primary must fall back: %v", err)
	}
	if resp.Model != "sarvam" {
		t.Errorf("winner = %s, want sarvam", resp.Model)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("took %v — the timeout did not bound the primary", elapsed)
	}
}

func TestBothModelsDownReturnsAnError(t *testing.T) {
	primary := &fakeCorrector{name: "gemini", err: errors.New("down")}
	fallback := &fakeCorrector{name: "sarvam", err: errors.New("also down")}

	r := NewRouter(primary, fallback, time.Second, nil)

	if _, err := r.Correct(context.Background(), req()); err == nil {
		t.Fatal("both models failing must surface an error, not a silent empty result")
	}
}

// §8.5 — the degraded path. If the fallback's quality ever proves worse than silence,
// TIER1_ONLY stops any model being called at all. The orchestrator then serves Tier 1's
// deterministic corrections alone, which can never be a hallucination.
func TestTier1OnlyCallsNoModel(t *testing.T) {
	primary := &fakeCorrector{name: "gemini"}
	fallback := &fakeCorrector{name: "sarvam"}

	r := NewRouter(primary, fallback, time.Second, nil, WithTier1Only(true))

	if _, err := r.Correct(context.Background(), req()); err == nil {
		t.Fatal("tier-1-only mode must not return a model result")
	}
	if primary.calls.Load() != 0 || fallback.calls.Load() != 0 {
		t.Error("tier-1-only mode must call NO model")
	}
}

// --- the verifier ----------------------------------------------------------

type fakeVerifier struct {
	approve bool
	err     error
	calls   atomic.Int32
	revised string
}

func (f *fakeVerifier) Name() string { return "verifier" }

func (f *fakeVerifier) Verify(_ context.Context, _ string, _ cascade.Suggestion) (*Verdict, error) {
	f.calls.Add(1)
	if f.err != nil {
		return nil, f.err
	}
	return &Verdict{
		Approve:           f.approve,
		Confidence:        0.93,
		Reason:            "test",
		RevisedSuggestion: f.revised,
	}, nil
}

// A corrector returning one high- and one low-confidence suggestion.
type mixedCorrector struct{}

func (mixedCorrector) Name() string { return "mixed" }

func (mixedCorrector) Correct(context.Context, Request) (*Response, error) {
	return &Response{Model: "mixed", Suggestions: []cascade.Suggestion{
		{Start: 0, End: 4, Original: "அந்த", Suggestion: "அந்தப்", Type: "sandhi", Confidence: 0.95},
		{Start: 5, End: 10, Original: "பையன்", Suggestion: "பையனை", Type: "grammar", Confidence: 0.50},
	}}, nil
}

func TestVerifierOnlyRunsOnLowConfidenceSuggestions(t *testing.T) {
	v := &fakeVerifier{approve: true}
	r := NewRouter(mixedCorrector{}, nil, time.Second, nil, WithVerifier(v, 0.85))

	resp, err := r.Correct(context.Background(), req())
	if err != nil {
		t.Fatal(err)
	}

	// Verifying the 0.95 suggestion too would double cost and latency for a
	// correction we were already sure about.
	if n := v.calls.Load(); n != 1 {
		t.Errorf("verifier called %d times, want 1 (only the 0.50 suggestion)", n)
	}
	if len(resp.Suggestions) != 2 {
		t.Errorf("got %d suggestions, want 2 (approved)", len(resp.Suggestions))
	}
}

func TestVerifierVetoDropsTheSuggestion(t *testing.T) {
	v := &fakeVerifier{approve: false}
	r := NewRouter(mixedCorrector{}, nil, time.Second, nil, WithVerifier(v, 0.85))

	resp, err := r.Correct(context.Background(), req())
	if err != nil {
		t.Fatal(err)
	}

	if len(resp.Suggestions) != 1 {
		t.Fatalf("got %d suggestions, want 1 (the rejected one must be gone)", len(resp.Suggestions))
	}
	if resp.Suggestions[0].Confidence != 0.95 {
		t.Error("the surviving suggestion should be the high-confidence one")
	}
}

func TestUnverifiableSuggestionIsDroppedNotShown(t *testing.T) {
	// The verifier is down. The low-confidence suggestion is exactly the guess we
	// wanted a second opinion on, so showing it unverified is the wrong failure
	// mode. Silence is the safe one.
	v := &fakeVerifier{err: errors.New("verifier unreachable")}
	r := NewRouter(mixedCorrector{}, nil, time.Second, nil, WithVerifier(v, 0.85))

	resp, err := r.Correct(context.Background(), req())
	if err != nil {
		t.Fatal(err)
	}

	if len(resp.Suggestions) != 1 {
		t.Fatalf("got %d, want 1: an unverifiable low-confidence suggestion must be dropped",
			len(resp.Suggestions))
	}
}

func TestApprovedSuggestionInheritsVerifierConfidenceAndRevision(t *testing.T) {
	v := &fakeVerifier{approve: true, revised: "பையனுக்கு"}
	r := NewRouter(mixedCorrector{}, nil, time.Second, nil, WithVerifier(v, 0.85))

	resp, err := r.Correct(context.Background(), req())
	if err != nil {
		t.Fatal(err)
	}

	var verified *cascade.Suggestion
	for i := range resp.Suggestions {
		if resp.Suggestions[i].SourceTier == cascade.TierVerifier {
			verified = &resp.Suggestions[i]
		}
	}
	if verified == nil {
		t.Fatal("the verified suggestion should be marked TierVerifier")
	}

	// It arrived at 0.50 — below the 0.85 gate. Verification is what earns it the
	// right to be shown.
	if verified.Confidence != 0.93 {
		t.Errorf("confidence = %v, want the verifier's 0.93", verified.Confidence)
	}
	if verified.Suggestion != "பையனுக்கு" {
		t.Errorf("suggestion = %q, want the verifier's revision", verified.Suggestion)
	}
}

func TestSuggestionsStayInDocumentOrderAfterConcurrentVerification(t *testing.T) {
	v := &fakeVerifier{approve: true}
	r := NewRouter(mixedCorrector{}, nil, time.Second, nil, WithVerifier(v, 0.85))

	resp, err := r.Correct(context.Background(), req())
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(resp.Suggestions); i++ {
		if resp.Suggestions[i].Start < resp.Suggestions[i-1].Start {
			t.Fatal("verification runs concurrently; results must be re-sorted by position")
		}
	}
}

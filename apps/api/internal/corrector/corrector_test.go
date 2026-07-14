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

func TestValidateAcceptsAWellFormedSuggestion(t *testing.T) {
	raw := rawResponse{Suggestions: []rawSuggestion{
		{Start: 0, End: 4, Original: "அந்த", Suggestion: "அந்தப்", Type: "sandhi", Confidence: 0.95},
	}}

	got, err := validate(raw, target, cascade.TierPrimary)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d suggestions, want 1", len(got))
	}
	if got[0].SourceTier != cascade.TierPrimary {
		t.Errorf("source tier = %d, want %d", got[0].SourceTier, cascade.TierPrimary)
	}
}

func TestValidateRejectsAHallucinatedSpan(t *testing.T) {
	// The model claims runes 0..4 contain "பையன்", but they contain "அந்த".
	// This is the check that matters most: we slice the USER'S DOCUMENT with these
	// offsets. A model that invents a location must never reach the editor.
	raw := rawResponse{Suggestions: []rawSuggestion{
		{Start: 0, End: 4, Original: "பையன்", Suggestion: "பையனை", Type: "grammar", Confidence: 0.9},
	}}

	got, _ := validate(raw, target, cascade.TierPrimary)
	if len(got) != 0 {
		t.Errorf("a suggestion whose `original` does not match the span must be dropped, got %+v", got)
	}
}

func TestValidateRejectsOutOfRangeOffsets(t *testing.T) {
	for _, tc := range []struct {
		name       string
		start, end int
	}{
		{"end past the sentence", 0, 9999},
		{"negative start", -1, 4},
		{"inverted", 4, 0},
		{"empty span", 2, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := rawResponse{Suggestions: []rawSuggestion{
				{Start: tc.start, End: tc.end, Original: "x", Suggestion: "y", Type: "spelling", Confidence: 0.9},
			}}
			if got, _ := validate(raw, target, cascade.TierPrimary); len(got) != 0 {
				t.Errorf("offsets (%d,%d) must be rejected", tc.start, tc.end)
			}
		})
	}
}

func TestValidateRejectsUnknownTypeAndInsaneConfidence(t *testing.T) {
	for _, raw := range []rawSuggestion{
		{Start: 0, End: 4, Original: "அந்த", Suggestion: "அந்தப்", Type: "vibes", Confidence: 0.9},
		{Start: 0, End: 4, Original: "அந்த", Suggestion: "அந்தப்", Type: "sandhi", Confidence: 7.0},
		{Start: 0, End: 4, Original: "அந்த", Suggestion: "அந்தப்", Type: "sandhi", Confidence: -1},
	} {
		if got, _ := validate(rawResponse{Suggestions: []rawSuggestion{raw}}, target, cascade.TierPrimary); len(got) != 0 {
			t.Errorf("must reject %+v", raw)
		}
	}
}

func TestValidateRejectsANoOpCorrection(t *testing.T) {
	// "Correcting" a word to itself is noise in the editor.
	raw := rawResponse{Suggestions: []rawSuggestion{
		{Start: 0, End: 4, Original: "அந்த", Suggestion: "அந்த", Type: "sandhi", Confidence: 0.9},
	}}
	if got, _ := validate(raw, target, cascade.TierPrimary); len(got) != 0 {
		t.Error("a suggestion that changes nothing must be dropped")
	}
}

func TestValidateKeepsGoodRowsWhenOneRowIsBad(t *testing.T) {
	// One malformed row must not discard the model's correct work.
	raw := rawResponse{Suggestions: []rawSuggestion{
		{Start: 0, End: 4, Original: "அந்த", Suggestion: "அந்தப்", Type: "sandhi", Confidence: 0.95},
		{Start: 0, End: 9999, Original: "junk", Suggestion: "junk2", Type: "spelling", Confidence: 0.9},
	}}

	got, _ := validate(raw, target, cascade.TierPrimary)
	if len(got) != 1 {
		t.Fatalf("got %d, want 1 (keep the good row, drop the bad)", len(got))
	}
	if got[0].Suggestion != "அந்தப்" {
		t.Errorf("kept the wrong row: %+v", got[0])
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

func TestFastPrimaryNeverFiresTheFallback(t *testing.T) {
	// The common case. Hedging must not double our model spend when the primary
	// is answering promptly.
	primary := &fakeCorrector{name: "sarvam", delay: 10 * time.Millisecond}
	fallback := &fakeCorrector{name: "gemini"}

	r := NewRouter(primary, fallback, 200*time.Millisecond, nil)

	resp, err := r.Correct(context.Background(), req())
	if err != nil {
		t.Fatal(err)
	}
	if resp.Model != "sarvam" {
		t.Errorf("winner = %s, want sarvam", resp.Model)
	}
	if n := fallback.calls.Load(); n != 0 {
		t.Errorf("fallback called %d times; a fast primary must not cost a second call", n)
	}
}

func TestSlowPrimaryTriggersTheHedge(t *testing.T) {
	// The primary drags; the fallback is fired alongside it and wins.
	primary := &fakeCorrector{name: "sarvam", delay: 2 * time.Second}
	fallback := &fakeCorrector{name: "gemini", delay: 10 * time.Millisecond}

	r := NewRouter(primary, fallback, 50*time.Millisecond, nil)

	start := time.Now()
	resp, err := r.Correct(context.Background(), req())
	if err != nil {
		t.Fatal(err)
	}

	if resp.Model != "gemini" {
		t.Errorf("winner = %s, want gemini (the primary was slow)", resp.Model)
	}
	if fallback.calls.Load() != 1 {
		t.Error("the hedge should have fired the fallback")
	}
	// It must not have waited out the slow primary.
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %v — the hedge did not bound tail latency", elapsed)
	}
}

// FAULT INJECTION (plan §9, Phase 2 exit check): Sarvam down, Gemini serves.
func TestPrimaryFailureFallsBackImmediately(t *testing.T) {
	primary := &fakeCorrector{name: "sarvam", err: errors.New("503 service unavailable")}
	fallback := &fakeCorrector{name: "gemini", delay: 10 * time.Millisecond}

	// A long hedge delay: if failover waited for it, this test would take 10s.
	r := NewRouter(primary, fallback, 10*time.Second, nil)

	start := time.Now()
	resp, err := r.Correct(context.Background(), req())
	if err != nil {
		t.Fatalf("a primary outage must be survivable: %v", err)
	}

	if resp.Model != "gemini" {
		t.Errorf("winner = %s, want gemini", resp.Model)
	}
	// A dead primary must not make us sit out the hedge delay waiting for a
	// response that is never coming.
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("failover took %v — it waited for the hedge timer instead of "+
			"reacting to the error", elapsed)
	}
}

func TestBothModelsDownReturnsAnError(t *testing.T) {
	primary := &fakeCorrector{name: "sarvam", err: errors.New("down")}
	fallback := &fakeCorrector{name: "gemini", err: errors.New("also down")}

	r := NewRouter(primary, fallback, 10*time.Millisecond, nil)

	if _, err := r.Correct(context.Background(), req()); err == nil {
		t.Fatal("both models failing must surface an error, not a silent empty result")
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

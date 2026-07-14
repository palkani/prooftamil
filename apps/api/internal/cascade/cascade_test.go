package cascade

import (
	"context"
	"errors"
	"testing"

	"github.com/prooftamil/api/internal/cache"
)

// --- segmentation ----------------------------------------------------------

func TestSegmentOffsetsAreRunesNotBytes(t *testing.T) {
	// Every Tamil character here is 3 bytes in UTF-8. If Segment returned byte
	// offsets, Start would be 3x too large and every correction span would slice
	// mid-character. This is the single most likely bug in the whole cascade.
	text := "நான் வந்தேன். அவன் போனான்."

	segs := Segment(text)
	if len(segs) != 2 {
		t.Fatalf("got %d segments, want 2", len(segs))
	}

	runes := []rune(text)
	for _, seg := range segs {
		if got := string(runes[seg.Start:seg.End]); got != seg.Text {
			t.Errorf("offsets do not index the text: runes[%d:%d] = %q, want %q",
				seg.Start, seg.End, got, seg.Text)
		}
	}

	if segs[0].Start != 0 {
		t.Errorf("first segment starts at %d, want 0", segs[0].Start)
	}
	// The second sentence starts at rune 13, NOT byte 37.
	if segs[1].Text != "அவன் போனான்." {
		t.Errorf("second segment = %q", segs[1].Text)
	}
}

func TestSegmentKeepsUnterminatedTrailingText(t *testing.T) {
	// Someone mid-sentence has no full stop yet. Dropping it would mean the
	// sentence being actively typed is the one sentence never proofread.
	segs := Segment("நான் வந்தேன். அவன்")
	if len(segs) != 2 {
		t.Fatalf("got %d segments, want 2", len(segs))
	}
	if segs[1].Text != "அவன்" {
		t.Errorf("trailing segment = %q, want அவன்", segs[1].Text)
	}
}

func TestSegmentSkipsWhitespaceOnlySpans(t *testing.T) {
	for _, in := range []string{"", "   ", "\n\n", ". . ."} {
		for _, seg := range Segment(in) {
			if seg.Text == "" {
				t.Errorf("Segment(%q) produced an empty segment", in)
			}
		}
	}
}

func TestContextGivesNeighbouringSentences(t *testing.T) {
	segs := Segment("ஒன்று. இரண்டு. மூன்று.")

	before, after := Context(segs, 1)
	if before != "ஒன்று." || after != "மூன்று." {
		t.Errorf("Context(1) = (%q, %q)", before, after)
	}

	// Edges must not panic and must return empty.
	if b, _ := Context(segs, 0); b != "" {
		t.Errorf("first segment should have no preceding context, got %q", b)
	}
	if _, a := Context(segs, len(segs)-1); a != "" {
		t.Errorf("last segment should have no following context, got %q", a)
	}
}

func TestNormalizeForCacheCollapsesWhitespace(t *testing.T) {
	// Otherwise "நான்  வந்தேன்" and "நான் வந்தேன்" occupy two cache slots and
	// each pays for its own model call.
	a := NormalizeForCache("நான்  வந்தேன்")
	b := NormalizeForCache(" நான் வந்தேன் ")
	if a != b {
		t.Errorf("whitespace variants produced different keys: %q vs %q", a, b)
	}
}

// --- orchestrator ----------------------------------------------------------

type fakeTier1 struct {
	suggestions []Suggestion
	err         error
	calls       int
}

func (f *fakeTier1) Analyze(_ context.Context, _, _, _ string) (*analyzeResponse, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return &analyzeResponse{Suggestions: f.suggestions, Resolved: false}, nil
}

// newOrch builds an orchestrator with no Redis. cache.Exact with a nil client
// returns ErrMiss on every Get and no-ops on Set, so the cascade runs uncached.
func newOrch(t1 Tier1, gate float64) *Orchestrator {
	return NewOrchestrator(t1, cache.NewExact(nil, 0, "test"), gate, nil)
}

func TestProofreadMapsSentenceOffsetsOntoTheDocument(t *testing.T) {
	// Tier 1 reports offsets relative to the SENTENCE. The orchestrator must
	// shift them onto the document, or corrections underline the wrong word.
	text := "நான் வந்தேன். அது ஒரு பரவை."
	segs := Segment(text)
	second := segs[1] // "அது ஒரு பரவை."

	// "பரவை" sits at rune 8 within the second sentence.
	local := Suggestion{Start: 8, End: 12, Original: "பரவை", Suggestion: "பறவை", Confidence: 0.9}

	t1 := &fakeTier1{}
	o := newOrch(t1, 0.85)

	// Only return the suggestion for the second sentence.
	t1.suggestions = nil
	res, err := o.Proofread(context.Background(), text)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Suggestions) != 0 {
		t.Fatalf("expected no suggestions, got %d", len(res.Suggestions))
	}

	// Now verify the offset arithmetic directly.
	shifted := offsetBy([]Suggestion{local}, second.Start)[0]
	runes := []rune(text)
	if got := string(runes[shifted.Start:shifted.End]); got != "பரவை" {
		t.Errorf("document offsets [%d:%d] select %q, want பரவை",
			shifted.Start, shifted.End, got)
	}
}

func TestProofreadAppliesTheConfidenceGate(t *testing.T) {
	t1 := &fakeTier1{suggestions: []Suggestion{
		{Start: 0, End: 3, Suggestion: "sure", Confidence: 0.90},
		{Start: 4, End: 7, Suggestion: "coinflip", Confidence: 0.55}, // ambiguous
	}}
	o := newOrch(t1, 0.85)

	res, err := o.Proofread(context.Background(), "ஒன்று இரண்டு")
	if err != nil {
		t.Fatal(err)
	}

	// The ambiguous one must never reach the writer.
	if len(res.Suggestions) != 1 {
		t.Fatalf("got %d suggestions, want 1 (the gate should drop the 0.55)", len(res.Suggestions))
	}
	if res.Suggestions[0].Suggestion != "sure" {
		t.Errorf("gate kept the wrong suggestion: %q", res.Suggestions[0].Suggestion)
	}
}

func TestProofreadSurvivesTier1Failure(t *testing.T) {
	// The ML service being down must degrade to "the model tiers will handle it",
	// not fail the user's request.
	t1 := &fakeTier1{err: errors.New("connection refused")}
	o := newOrch(t1, 0.85)

	res, err := o.Proofread(context.Background(), "நான் வந்தேன்.")
	if err != nil {
		t.Fatalf("a Tier 1 outage must not fail the request, got: %v", err)
	}
	if !res.ModelPending {
		t.Error("with Tier 1 down, the model tiers still owe an answer — ModelPending must be true")
	}
}

func TestProofreadAlwaysLeavesModelPending(t *testing.T) {
	// Tier 1 cannot prove a sentence is CLEAN, so even a sentence it finds nothing
	// wrong with must still fall through to the model tiers. Marking it resolved
	// would silently drop every grammar and real-word error.
	t1 := &fakeTier1{suggestions: nil}
	o := newOrch(t1, 0.85)

	res, err := o.Proofread(context.Background(), "நான் பள்ளிக்கு சென்றேன்.")
	if err != nil {
		t.Fatal(err)
	}
	if !res.ModelPending {
		t.Error("ModelPending must be true: Tier 1 cannot certify a sentence as clean")
	}
}

func TestProofreadCallsTier1OncePerSentence(t *testing.T) {
	t1 := &fakeTier1{}
	o := newOrch(t1, 0.85)

	if _, err := o.Proofread(context.Background(), "ஒன்று. இரண்டு. மூன்று."); err != nil {
		t.Fatal(err)
	}
	if t1.calls != 3 {
		t.Errorf("Tier 1 called %d times, want 3 (once per sentence)", t1.calls)
	}
}

func TestProofreadOnEmptyInput(t *testing.T) {
	o := newOrch(&fakeTier1{}, 0.85)

	res, err := o.Proofread(context.Background(), "   ")
	if err != nil {
		t.Fatal(err)
	}
	// Must be an empty slice, not nil — it serializes to [] rather than null, and
	// a null would make the frontend's .map() throw.
	if res.Suggestions == nil {
		t.Error("Suggestions must be [] not nil, so it serializes as [] not null")
	}
}

// --- cache -----------------------------------------------------------------

func TestCacheKeyChangesWithVersion(t *testing.T) {
	// Bumping the version must invalidate everything. Without this, shipping a
	// rules fix leaves every cached sentence serving the OLD correction until the
	// TTL expires — a week by default — and it looks like the fix never deployed.
	v1 := cache.NewExact(nil, 0, "1")
	v2 := cache.NewExact(nil, 0, "2")

	if v1.Key("நான் வந்தேன்") == v2.Key("நான் வந்தேன்") {
		t.Error("the same text must map to different keys under different engine versions")
	}
}

func TestCacheKeyIsStableAndContentAddressed(t *testing.T) {
	c := cache.NewExact(nil, 0, "1")

	if c.Key("நான் வந்தேன்") != c.Key("நான் வந்தேன்") {
		t.Error("the same text must map to the same key")
	}
	if c.Key("நான் வந்தேன்") == c.Key("அவன் போனான்") {
		t.Error("different text must map to different keys")
	}
}

func TestCacheGetWithoutRedisIsAMiss(t *testing.T) {
	// Dev runs with no Redis. That must be a clean miss, not a crash.
	c := cache.NewExact(nil, 0, "1")

	var out []Suggestion
	if err := c.Get(context.Background(), "x", &out); !errors.Is(err, cache.ErrMiss) {
		t.Errorf("Get without Redis = %v, want ErrMiss", err)
	}
	if err := c.Set(context.Background(), "x", out); err != nil {
		t.Errorf("Set without Redis = %v, want nil (no-op)", err)
	}
}

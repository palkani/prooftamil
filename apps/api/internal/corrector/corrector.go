// Package corrector implements cascade Tiers 3 and 4 — the model tiers
// (plan §8, §9 Phase 2).
//
// Only text that Tier 1 (deterministic rules) and Tier 2 (cache) could not
// resolve reaches here, so every call in this package costs money and adds
// hundreds of milliseconds. The cascade exists to make that rare.
package corrector

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/prooftamil/api/internal/cascade"
)

// Request is one sentence to correct, with the neighbouring sentences for
// context. Only Target is ever corrected (§8.1).
type Request struct {
	Target        string
	ContextBefore string
	ContextAfter  string
}

// Response is a validated model reply.
type Response struct {
	Suggestions []cascade.Suggestion

	// Provenance, logged on every ai_requests row (§8.3) so a bad correction can
	// always be traced back to the exact model and prompt that produced it.
	Model         string
	PromptVersion int
	TokensIn      int
	TokensOut     int
	LatencyMS     int64
}

// Corrector is one model that can propose corrections. Sarvam (primary) and
// Gemini (fallback/verifier) both implement it, which is what lets the router
// hedge between them and lets tests inject failures without a network.
type Corrector interface {
	Name() string
	Correct(ctx context.Context, req Request) (*Response, error)
}

// Verifier judges a proposed correction (§8.2). It only ever runs on
// low-confidence suggestions — verifying everything would double the cost of the
// expensive path.
type Verifier interface {
	Name() string
	Verify(ctx context.Context, sentence string, s cascade.Suggestion) (*Verdict, error)
}

type Verdict struct {
	Approve           bool    `json:"approve"`
	Confidence        float64 `json:"confidence"`
	Reason            string  `json:"reason"`
	RevisedSuggestion string  `json:"revised_suggestion,omitempty"`
}

// rawSuggestion is the model's wire shape, before validation. It is deliberately
// separate from cascade.Suggestion: nothing a model says is trusted until it has
// been through validate().
//
// Note there are NO offsets here. Prompt v1 asked the model for start/end and it
// failed against the live API in the worst way: Gemini echoed the offsets from the
// prompt's own few-shot example (18/24) rather than counting the real sentence
// (20/26). LLMs cannot count characters reliably, so we stopped asking. The model
// quotes the substring; validate() locates it and derives the offsets, which is
// exact by construction.
type rawSuggestion struct {
	Original    string  `json:"original"`
	Suggestion  string  `json:"suggestion"`
	Type        string  `json:"type"`
	Explanation string  `json:"explanation"`
	Confidence  float64 `json:"confidence"`
}

type rawResponse struct {
	Suggestions []rawSuggestion `json:"suggestions"`
}

var validTypes = map[string]bool{
	"spelling": true, "sandhi": true, "grammar": true,
	"agreement": true, "style": true,
}

// validate turns a model's claims into suggestions we are willing to show a
// writer, and drops everything else.
//
// This is a security and correctness boundary, not a formality. The model returns
// offsets into the user's text; we slice the document with them. A hallucinated
// span would either corrupt the correction or panic the server. So every
// suggestion must prove itself:
//
//   - offsets in range, in order, and counted in RUNES (the prompt asks for
//     codepoints; Tamil is 3 bytes per character, so a byte reading would be
//     silently wrong for every non-ASCII sentence)
//   - `original` must actually equal the text at those offsets. This is the check
//     that catches a model that invented a span, drifted by a character, or echoed
//     text from the CONTEXT sentences instead of the target.
//   - a known type, a sane confidence, and an actual change
//
// A model that returns ten suggestions of which two are malformed yields the
// eight good ones. Rejecting the whole response would throw away correct work
// because of one bad row.
func validate(raw rawResponse, target string, tier cascade.Tier) ([]cascade.Suggestion, error) {
	runes := []rune(target)
	out := make([]cascade.Suggestion, 0, len(raw.Suggestions))

	// Tracks which parts of the sentence are already spoken for, so two suggestions
	// cannot claim the same word and so repeated words map to successive
	// occurrences rather than all collapsing onto the first.
	taken := make([]bool, len(runes))

	for _, r := range raw.Suggestions {
		if !validTypes[r.Type] {
			continue
		}
		if r.Confidence < 0 || r.Confidence > 1 {
			continue
		}
		if r.Original == "" || !utf8.ValidString(r.Suggestion) {
			continue
		}

		// A "correction" that changes nothing is noise in the UI.
		if strings.TrimSpace(r.Suggestion) == "" || r.Suggestion == r.Original {
			continue
		}

		// THE ANCHOR. The model quoted a substring; find it. If it is not there
		// verbatim, the model invented it — it hallucinated, paraphrased, or (seen
		// live from Sarvam) answered in English. Either way it must not touch the
		// writer's document.
		start, end, ok := locate(runes, []rune(r.Original), taken)
		if !ok {
			continue
		}
		for i := start; i < end; i++ {
			taken[i] = true
		}

		out = append(out, cascade.Suggestion{
			Start:       start,
			End:         end,
			Original:    r.Original,
			Suggestion:  r.Suggestion,
			Type:        r.Type,
			Explanation: r.Explanation,
			Confidence:  r.Confidence,
			SourceTier:  tier,
		})
	}

	// The model returns suggestions in whatever order it pleases; the editor needs
	// them in document order.
	sortByPosition(out)
	return out, nil
}

// locate finds the first occurrence of `needle` in `hay` that does not overlap a
// span already claimed by an earlier suggestion.
//
// Offsets are computed HERE rather than taken from the model, because the model
// cannot count characters — prompt v1 proved it by echoing the offsets from its own
// few-shot example. Deriving them from a verbatim quote is exact by construction.
func locate(hay, needle []rune, taken []bool) (start, end int, ok bool) {
	if len(needle) == 0 || len(needle) > len(hay) {
		return 0, 0, false
	}

	for i := 0; i+len(needle) <= len(hay); i++ {
		match := true
		for j := range needle {
			if hay[i+j] != needle[j] || taken[i+j] {
				match = false
				break
			}
		}
		if match {
			return i, i + len(needle), true
		}
	}
	return 0, 0, false
}

// extractJSON pulls the JSON object out of a model reply.
//
// Models wrap JSON in ```json fences and prose despite being told not to, and a
// hard parse failure on the whole reply would throw away a perfectly good set of
// corrections over a formatting habit.
func extractJSON(s string) (string, error) {
	s = strings.TrimSpace(s)

	if fence := strings.Index(s, "```"); fence >= 0 {
		rest := s[fence+3:]
		rest = strings.TrimPrefix(rest, "json")
		if end := strings.Index(rest, "```"); end >= 0 {
			s = strings.TrimSpace(rest[:end])
		}
	}

	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		return "", fmt.Errorf("no JSON object in model reply")
	}
	return s[start : end+1], nil
}

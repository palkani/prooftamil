package cascade

import "strings"

// Sentence is one segment of the document, carrying its RUNE offsets into it.
//
// Offsets are runes throughout the cascade. The ML service returns codepoint
// offsets relative to the sentence; adding Start maps them to the document. If
// these were byte offsets, the addition would be meaningless for Tamil, which is
// 3 bytes per character in UTF-8.
type Sentence struct {
	Text  string
	Start int // rune offset into the document, inclusive
	End   int // rune offset, exclusive
}

// sentenceEnders terminate a Tamil sentence. Tamil uses the Latin full stop;
// the danda (।) shows up in text imported from other Indic scripts.
var sentenceEnders = map[rune]bool{
	'.': true, '?': true, '!': true, '\n': true, '।': true,
}

// Segment splits text into sentences.
//
// The plan's corrector prompt (§8.1) takes a TARGET sentence plus one sentence of
// context each side and corrects only the target, so the cascade has to know
// where sentences begin and end before it can call any model.
func Segment(text string) []Sentence {
	runes := []rune(text)
	var segs []Sentence

	start := 0
	for i, r := range runes {
		if !sentenceEnders[r] {
			continue
		}
		// Include the terminator in the segment.
		if seg, ok := makeSegment(runes, start, i+1); ok {
			segs = append(segs, seg)
		}
		start = i + 1
	}

	// Trailing text with no terminator — the common case while someone is still
	// typing, so it must not be dropped.
	if start < len(runes) {
		if seg, ok := makeSegment(runes, start, len(runes)); ok {
			segs = append(segs, seg)
		}
	}

	return segs
}

// makeSegment trims surrounding whitespace while keeping offsets pointing at the
// real characters in the document, and drops whitespace-only spans.
func makeSegment(runes []rune, start, end int) (Sentence, bool) {
	for start < end && isSpace(runes[start]) {
		start++
	}
	for end > start && isSpace(runes[end-1]) {
		end--
	}
	if start >= end {
		return Sentence{}, false
	}
	return Sentence{Text: string(runes[start:end]), Start: start, End: end}, true
}

func isSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}

// Context returns the sentences either side of segs[i], as the §8.1 prompt
// expects. They are for context only and are never corrected.
func Context(segs []Sentence, i int) (before, after string) {
	if i > 0 {
		before = segs[i-1].Text
	}
	if i+1 < len(segs) {
		after = segs[i+1].Text
	}
	return before, after
}

// NormalizeForCache produces the cache key input for a segment: collapsing
// whitespace so that "நான்  வந்தேன்" and "நான் வந்தேன்" are one cache entry
// rather than two.
func NormalizeForCache(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

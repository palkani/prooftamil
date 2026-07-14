// Package cascade implements the tiered proofreading pipeline (plan §9, Phase 1).
//
//	Tier 0  browser (WASM)      dictionary + debounce      — not here
//	Tier 1  apps/ml             deterministic rules        — always runs, ~1ms
//	Tier 2  Redis, then Qdrant  exact, then semantic cache
//	Tier 3  Sarvam              primary corrector          — Phase 2
//	Tier 4  Gemini              verifier                   — Phase 2
//
// Cost and latency both fall the earlier a correction is resolved. Only a
// Tier 1+2 miss should ever reach a paid model.
package cascade

// Tier identifies which stage produced a suggestion, so §11 can report how much
// work each tier actually resolves and whether the cascade is paying for itself.
type Tier int

const (
	TierClient   Tier = 0
	TierRules    Tier = 1
	TierCache    Tier = 2
	TierPrimary  Tier = 3 // Sarvam
	TierVerifier Tier = 4 // Gemini
)

// Suggestion is one proposed correction.
//
// Start/End are offsets into the ORIGINAL request text, in RUNES, not bytes.
// The ML service speaks codepoint offsets and Tamil is multi-byte in UTF-8, so
// byte offsets here would slice mid-character and corrupt every span. Callers
// converting to bytes must go through []rune.
type Suggestion struct {
	Start       int     `json:"start"`
	End         int     `json:"end"`
	Original    string  `json:"original"`
	Suggestion  string  `json:"suggestion"`
	Type        string  `json:"type"` // spelling|sandhi|grammar|agreement|style
	Explanation string  `json:"explanation,omitempty"`
	Confidence  float64 `json:"confidence"`
	SourceTier  Tier    `json:"source_tier"`
}

// Result is one pass of the cascade over a request.
type Result struct {
	Suggestions []Suggestion `json:"suggestions"`

	// CacheHit reports whether the whole result came from Tier 2 without any
	// model call. This is the number that decides if the cascade is working.
	CacheHit bool `json:"cache_hit"`

	// ModelPending is true when Tier 1 and Tier 2 could not fully resolve the
	// text, so a model tier still owes an answer. The client keeps its SSE
	// stream open when this is set.
	//
	// It is true on essentially every request today: Tier 1 can prove a word is
	// misspelled, but never that a sentence is CLEAN — that needs semantics.
	ModelPending bool `json:"model_pending"`

	LatencyMS int64 `json:"latency_ms"`
}

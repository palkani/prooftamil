/** Shared shapes, mirroring the Go API (apps/api/internal/cascade/types.go). */

export type SuggestionType =
  | "spelling"
  | "sandhi"
  | "grammar"
  | "agreement"
  | "style";

export interface Suggestion {
  /**
   * RUNE offsets into the document text, NOT bytes and NOT ProseMirror positions.
   *
   * The whole stack speaks runes: the ML service returns codepoint offsets, the Go
   * orchestrator maps them onto the document in runes, and JS string indices are
   * UTF-16 code units — which agree with runes for Tamil (all BMP), but NOT with
   * bytes. Tamil is 3 bytes per character in UTF-8, so anything byte-based here
   * would slice mid-character.
   *
   * These still have to be converted to ProseMirror positions before they can be
   * drawn — see suggestionPlugin.
   */
  start: number;
  end: number;
  original: string;
  suggestion: string;
  type: SuggestionType;
  explanation?: string;
  confidence: number;
  /** 1 = deterministic rules (free, instant). 3/4 = model (costs money). */
  source_tier: number;
}

export interface ProofreadResult {
  suggestions: Suggestion[];
  cache_hit: boolean;
  /** True when a tier could not be consulted — the answer is incomplete. */
  model_pending: boolean;
  latency_ms: number;
}

export interface StreamEvent {
  seq: number;
  type: "suggestions" | "done" | "error";
  suggestions?: Suggestion[];
  model_pending?: boolean;
  error?: string;
}

export interface IMESuggestion {
  word: string;
  score: number;
  /** "generated" = a best-effort spelling, not a dictionary word. Must be marked. */
  source: "lexicon" | "generated";
}

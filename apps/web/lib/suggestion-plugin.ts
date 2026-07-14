/**
 * Inline suggestion underlines, drawn as ProseMirror DECORATIONS.
 *
 * Decorations, not marks, and the distinction is the whole point: a mark is part of
 * the document and would be saved, exported, and undone with it. A suggestion is not
 * content — it is a transient annotation ABOUT the content. Storing it in the
 * document would mean every draft carried its proofreading state around, exports
 * contained invisible junk, and Ctrl-Z stepped through underlines.
 *
 * OFFSET TRANSLATION IS THE HARD PART.
 *
 * The API returns RUNE offsets into the plain text. ProseMirror positions are
 * something else entirely: they count node boundaries, so every paragraph adds 2.
 * Drawing at the raw offsets would misplace every underline after the first
 * paragraph, and the error compounds. So we build an explicit text-offset ->
 * PM-position map by walking the document once.
 */
import { Plugin, PluginKey } from "@tiptap/pm/state";
import { Decoration, DecorationSet } from "@tiptap/pm/view";
import type { EditorState } from "@tiptap/pm/state";
import type { Node as PMNode } from "@tiptap/pm/model";

import type { Suggestion } from "./types";

export const suggestionPluginKey = new PluginKey<SuggestionState>("suggestions");

interface SuggestionState {
  suggestions: Suggestion[];
  decorations: DecorationSet;
  /** The suggestion the user is currently looking at, if any. */
  active: number | null;
}

/** Replace the current suggestion set. */
export const setSuggestions = (suggestions: Suggestion[]) => ({
  type: "set" as const,
  suggestions,
});

/** Remove one suggestion (it was accepted or dismissed). */
export const removeSuggestion = (index: number) => ({
  type: "remove" as const,
  index,
});

/**
 * Extract the document's plain text AND the position map in one pass.
 *
 * `textOffsetToPos[i]` is the ProseMirror position of the i-th character of the
 * plain text. Built by walking text nodes in order; the gaps between them (node
 * boundaries) are exactly what makes a naive `pos = offset` wrong.
 *
 * The joiner must match what the client sent to the API as the document text, or the
 * offsets will not line up. Both use "\n" between blocks.
 */
export function buildPositionMap(doc: PMNode): {
  text: string;
  map: number[];
} {
  const chars: string[] = [];
  const map: number[] = [];
  let first = true;

  doc.descendants((node, pos) => {
    if (node.isText && node.text) {
      const runes = Array.from(node.text);
      for (let i = 0; i < runes.length; i++) {
        chars.push(runes[i]);
        // pos+1 skips the node's own opening token.
        map.push(pos + i);
      }
      return false;
    }

    if (node.isBlock && !first && node.type.name !== "doc") {
      // A block boundary is a "\n" in the plain text the API saw. It occupies no
      // real character position, so point it at the block's start; nothing draws
      // on it.
      chars.push("\n");
      map.push(pos);
    }
    if (node.isBlock && node.type.name !== "doc") first = false;

    return true;
  });

  return { text: chars.join(""), map };
}

/**
 * Map a rune span from the API onto ProseMirror positions.
 *
 * Returns null when the span no longer fits the document — which happens constantly
 * and is not an error: the user keeps typing while a model call is in flight, so
 * results arrive describing text that has already changed. Dropping those silently
 * is correct; drawing them would underline the wrong words.
 */
function spanToPositions(
  map: number[],
  start: number,
  end: number,
): { from: number; to: number } | null {
  if (start < 0 || end > map.length || start >= end) return null;
  return { from: map[start], to: map[end - 1] + 1 };
}

function build(state: EditorState, suggestions: Suggestion[], active: number | null): DecorationSet {
  const { text, map } = buildPositionMap(state.doc);
  const decos: Decoration[] = [];

  suggestions.forEach((s, i) => {
    const span = spanToPositions(map, s.start, s.end);
    if (!span) return;

    // The anchor check, client-side. The same discipline the Go validator applies to
    // model output: if the text at these offsets is not what the suggestion claims,
    // the document has moved on and this underline would land on the wrong word.
    const actual = Array.from(text).slice(s.start, s.end).join("");
    if (actual !== s.original) return;

    decos.push(
      Decoration.inline(span.from, span.to, {
        class: [
          "pt-suggestion",
          `pt-${s.type}`,
          s.source_tier === 1 ? "pt-tier1" : "pt-model",
          i === active ? "pt-active" : "",
        ]
          .filter(Boolean)
          .join(" "),
        "data-suggestion": String(i),
      }),
    );
  });

  return DecorationSet.create(state.doc, decos);
}

export function suggestionPlugin() {
  return new Plugin<SuggestionState>({
    key: suggestionPluginKey,

    state: {
      init: () => ({
        suggestions: [],
        decorations: DecorationSet.empty,
        active: null,
      }),

      apply(tr, value, _old, newState) {
        const meta = tr.getMeta(suggestionPluginKey);

        if (meta?.type === "set") {
          return {
            suggestions: meta.suggestions,
            decorations: build(newState, meta.suggestions, null),
            active: null,
          };
        }

        if (meta?.type === "remove") {
          const next = value.suggestions.filter((_, i) => i !== meta.index);
          return {
            suggestions: next,
            decorations: build(newState, next, null),
            active: null,
          };
        }

        if (meta?.type === "active") {
          return { ...value, active: meta.index, decorations: build(newState, value.suggestions, meta.index) };
        }

        // The document changed under us. Rebuilding against the NEW text
        // re-runs the anchor check, so suggestions whose words the user has just
        // edited disappear on their own — no stale underline can survive an edit.
        if (tr.docChanged) {
          return {
            ...value,
            decorations: build(newState, value.suggestions, value.active),
          };
        }

        return value;
      },
    },

    props: {
      decorations: (state) => suggestionPluginKey.getState(state)?.decorations,
    },
  });
}

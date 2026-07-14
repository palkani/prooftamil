/**
 * The client-side IME index (RFC-001 §4).
 *
 * THE PROBLEM IT SOLVES: without it, every keystroke is a round trip to the API. On
 * localhost that is invisible. From India it is not — an IME has to answer inside about
 * 50ms to feel like part of the keyboard rather than a website, and the RTT alone can eat
 * that before the server has done any work.
 *
 * So the head of the distribution lives here: 30,000 words, 0.30 MB gzipped, ~83% of real
 * Tamil usage. Anything it cannot answer falls through to /api/v1/suggest, which still
 * has all 349,633 words and the out-of-vocabulary generator.
 *
 * THREE RULES THIS FILE OBEYS:
 *
 *  1. LAZY. The index is fetched after first paint, never as part of the initial bundle.
 *     A user who never types romanized Tamil must not pay 0.3 MB for it, and the editor
 *     must be usable the instant it renders — the server path answers until the index
 *     lands.
 *
 *  2. NEVER BLOCK TYPING. Every failure path returns null, which means "ask the server".
 *     A missing artifact, a parse error, a slow network — none of them may break the IME,
 *     because the fallback is always there.
 *
 *  3. THE FOLD RULES COME FROM THE ARTIFACT. They are not reimplemented here. They live
 *     in packages/tamil-rules/translit/scheme.yaml, and a hand-copy in TypeScript would
 *     work right up until someone edits the YAML and forgets it — at which point client
 *     and server would silently disagree about what a word sounds like, for exactly the
 *     users on the fast path.
 */

import type { IMESuggestion } from "./types";

interface Artifact {
  v: number;
  words: number;
  fold: [string, string][];
  /** key -> [[word, score], ...] where score = round(log1p(count) * 100). */
  idx: Record<string, [string, number][]>;
}

let index: Artifact | null = null;
let sortedKeys: string[] | null = null;
let loading: Promise<void> | null = null;
let failed = false;

/** Collapse runs of the same character: vanakkam -> vanakam. */
const collapse = (s: string) => s.replace(/(.)\1+/g, "$1");

/**
 * Start downloading the index. Safe to call repeatedly; the fetch happens once.
 *
 * Call it from an idle callback after the editor has painted — not during render.
 */
export function preloadIMEIndex(): void {
  if (index || loading || failed) return;

  loading = fetch("/ime-index.v1.json")
    .then((r) => {
      if (!r.ok) throw new Error(`HTTP ${r.status}`);
      return r.json();
    })
    .then((data: Artifact) => {
      index = data;
      // Sort once, here, so every keystroke is a binary search rather than a scan.
      // 27k keys sorts in a couple of milliseconds, and only ever once.
      sortedKeys = Object.keys(data.idx).sort();
    })
    .catch(() => {
      // The server path still works. A missing index is a performance regression, never
      // a broken feature — so it must not be loud, and it must not retry in a loop.
      failed = true;
    })
    .finally(() => {
      loading = null;
    });
}

export const imeIndexReady = () => index !== null;

/** Apply the shipped fold rules, then collapse doubles — exactly as the server does. */
function fold(query: string): string {
  if (!index) return "";
  let q = query.toLowerCase().trim();
  for (const [from, to] of index.fold) {
    q = q.split(from).join(to);
  }
  return collapse(q);
}

/** First index whose key is >= target. */
function lowerBound(keys: string[], target: string): number {
  let lo = 0;
  let hi = keys.length;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (keys[mid] < target) lo = mid + 1;
    else hi = mid;
  }
  return lo;
}

/**
 * Look a query up locally.
 *
 * Returns null when the index is not loaded OR has nothing for this query — both mean
 * "ask the server", and the caller cannot tell them apart on purpose. A rare word the
 * client does not carry is not an error; it is the 17% we deliberately left on the
 * server.
 */
export function suggestLocally(query: string, limit = 8): IMESuggestion[] | null {
  if (!index || !sortedKeys || query.length < 2) return null;

  const key = fold(query);
  if (!key) return null;

  // THE CLIENT MUST RANK, exactly as the server does — pooling exact and prefix hits and
  // choosing by frequency, NOT simply putting exact matches first.
  //
  // Typing "tam" has to give தமிழ் (79,058 occurrences, a PREFIX hit) and not தம் (a rare
  // EXACT hit). An earlier version ordered exact-before-prefix and produced exactly that
  // wrong answer — the parity gate in the build script caught it. A fast path that
  // disagrees with the slow one is worse than no fast path.
  //
  // 300 == the server's _EXACT_BONUS of 3.0, on the same x100 log scale. A completed word
  // is preferred, but a far commoner one still wins.
  const EXACT_BONUS = 300;
  const pool = new Map<string, number>();

  // Exact key, plus the trailing-schwa probe: a Tamil word ending in a bare consonant
  // keys without a final vowel (ஊர் -> "ur") but is PRONOUNCED with one, so people type
  // "ooru". Same rule the server applies; without it those words are unreachable.
  const probes = [key];
  if (key.length > 2 && key.endsWith("u")) probes.push(key.slice(0, -1));

  for (const probe of probes) {
    for (const [word, score] of index.idx[probe] ?? []) {
      pool.set(word, Math.max(pool.get(word) ?? 0, score + EXACT_BONUS));
    }
  }

  // Prefix hits — the common case mid-word. Binary search, then walk while the prefix
  // holds. Bounded, because a one-letter prefix matches thousands of keys and scanning
  // them all on every keystroke is the one thing that would make typing feel slow.
  let i = lowerBound(sortedKeys, key);
  let scanned = 0;
  while (i < sortedKeys.length && sortedKeys[i].startsWith(key) && scanned < 2000) {
    for (const [word, score] of index.idx[sortedKeys[i]]) {
      if (!pool.has(word)) pool.set(word, score);
    }
    i++;
    scanned++;
  }

  // Nothing locally: a rare word or a name. The server has the other 320k words and the
  // out-of-vocabulary generator, so hand it over rather than showing an empty dropdown.
  if (pool.size === 0) return null;

  return [...pool.entries()]
    .sort((a, b) => b[1] - a[1])
    .slice(0, limit)
    .map(([word, score]) => ({ word, score: score / 100, source: "lexicon" as const }));
}

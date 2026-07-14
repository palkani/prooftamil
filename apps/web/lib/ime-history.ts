/**
 * Personal IME history — the strongest ranking signal there is, because people reuse
 * their own vocabulary far more than they use the language average.
 *
 * IT LIVES ON THE DEVICE, DELIBERATELY (RFC-001 §5, §7).
 *
 * This is a record of the words a person writes. Sending it to a server to
 * "personalise ranking" would turn the suggest endpoint — which fires on every
 * keystroke — into a per-user log of what someone is writing. Keeping it in
 * localStorage gets the full benefit of personalisation with none of that: the
 * server stays identity-free, user-independent and edge-cacheable.
 *
 * If someone later proposes a `user_id` on /api/v1/suggest for "better ranking",
 * this file is the answer: we already have better ranking, and it costs no privacy.
 */

const KEY = "pt.ime.history.v1";
const MAX_ENTRIES = 500;

type History = Record<string, { word: string; n: number; t: number }[]>;

function load(): History {
  if (typeof window === "undefined") return {};
  try {
    return JSON.parse(localStorage.getItem(KEY) ?? "{}");
  } catch {
    return {};
  }
}

function save(h: History) {
  try {
    // Bound the store. Without a cap this grows forever in a long-lived tab and
    // eventually blows the localStorage quota, which throws on write and would break
    // typing.
    const keys = Object.keys(h);
    if (keys.length > MAX_ENTRIES) {
      const newest = keys
        .map((k) => [k, Math.max(...h[k].map((e) => e.t))] as const)
        .sort((a, b) => b[1] - a[1])
        .slice(0, MAX_ENTRIES)
        .map(([k]) => k);
      const trimmed: History = {};
      for (const k of newest) trimmed[k] = h[k];
      h = trimmed;
    }
    localStorage.setItem(KEY, JSON.stringify(h));
  } catch {
    // Quota exceeded or storage disabled (private mode). Personalisation is a
    // nice-to-have; never let it break typing.
  }
}

/** Record that the user chose `word` for the romanized query `q`. */
export function remember(q: string, word: string) {
  const h = load();
  const key = q.toLowerCase();
  const list = h[key] ?? [];
  const found = list.find((e) => e.word === word);

  if (found) {
    found.n += 1;
    found.t = Date.now();
  } else {
    list.push({ word, n: 1, t: Date.now() });
  }

  h[key] = list.sort((a, b) => b.n - a.n).slice(0, 5);
  save(h);
}

/**
 * Re-rank candidates so previously-chosen words come first.
 *
 * Only reorders — never invents a candidate the engine did not return, and never
 * drops one. History is a preference, not a filter.
 */
export function rerank<T extends { word: string }>(q: string, items: T[]): T[] {
  const picks = load()[q.toLowerCase()];
  if (!picks?.length) return items;

  const rank = new Map(picks.map((p, i) => [p.word, i]));
  return [...items].sort((a, b) => {
    const ra = rank.has(a.word) ? rank.get(a.word)! : Infinity;
    const rb = rank.has(b.word) ? rank.get(b.word)! : Infinity;
    return ra - rb;
  });
}

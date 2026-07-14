/**
 * Draft persistence (plan §7.3).
 *
 * LOCAL, for now. The plan's drafts live in Postgres behind auth, with version
 * history and RLS — that is Phase 6, and it needs the §1 accounts. Until then, a
 * writer who closes the tab must not lose their work, and localStorage is the honest
 * way to guarantee that with zero infrastructure.
 *
 * The API is deliberately shaped like the eventual server one (list / get / save /
 * remove, ids and timestamps), so swapping the backing store for `/api/v1/drafts` is
 * a change to this file and nothing else.
 */

const KEY = "pt.drafts.v1";
const MAX_DRAFTS = 50;

export interface Draft {
  id: string;
  title: string;
  body: string;
  updatedAt: number;
}

function read(): Draft[] {
  if (typeof window === "undefined") return [];
  try {
    const raw = JSON.parse(localStorage.getItem(KEY) ?? "[]");
    return Array.isArray(raw) ? raw : [];
  } catch {
    return [];
  }
}

function write(drafts: Draft[]) {
  try {
    localStorage.setItem(
      KEY,
      JSON.stringify(drafts.slice(0, MAX_DRAFTS)),
    );
  } catch {
    // Quota exceeded. Losing the newest keystroke is bad; crashing the editor is
    // worse. Fail quietly — the in-memory document is still intact.
  }
}

/** Newest first. */
export function listDrafts(): Draft[] {
  return read().sort((a, b) => b.updatedAt - a.updatedAt);
}

export function getDraft(id: string): Draft | undefined {
  return read().find((d) => d.id === id);
}

/**
 * Derive a title from the first line, the way every notes app does — asking a writer
 * to name a document before they have written it is friction for no benefit.
 */
export function titleOf(body: string): string {
  const first = body.split("\n").map((l) => l.trim()).find(Boolean) ?? "";
  if (!first) return "Untitled";
  const runes = Array.from(first);
  return runes.length > 40 ? runes.slice(0, 40).join("") + "…" : first;
}

export function saveDraft(id: string, body: string): Draft {
  const drafts = read();
  const now = Date.now();
  const draft: Draft = { id, title: titleOf(body), body, updatedAt: now };

  const i = drafts.findIndex((d) => d.id === id);
  if (i >= 0) drafts[i] = draft;
  else drafts.unshift(draft);

  write(drafts.sort((a, b) => b.updatedAt - a.updatedAt));
  return draft;
}

export function removeDraft(id: string) {
  write(read().filter((d) => d.id !== id));
}

export function newId(): string {
  // crypto.randomUUID is unavailable on http:// origins in some browsers, and this
  // must never throw on the path that creates a document.
  return typeof crypto !== "undefined" && "randomUUID" in crypto
    ? crypto.randomUUID()
    : `d${Date.now()}${Math.random().toString(36).slice(2, 8)}`;
}

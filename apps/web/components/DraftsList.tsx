"use client";

import { useEffect, useState } from "react";
import Link from "next/link";

import { type Draft, formatDate, listDrafts, removeDraft } from "@/lib/drafts";

/**
 * The drafts screen (§17.2 screen 5).
 *
 * RETENTION IS STATED, NOT HIDDEN. The plan gives archived drafts a 7-day life. A user
 * who is not told that will discover it by losing something — so the badge says how long
 * is left, and deletion asks first. Data loss is the one mistake a writing tool cannot
 * apologise its way out of.
 *
 * Reads from localStorage today; the API is shaped like the eventual /api/v1/drafts, so
 * swapping the store is a change to lib/drafts.ts alone.
 */
export default function DraftsList() {
  const [drafts, setDrafts] = useState<Draft[]>([]);
  const [confirming, setConfirming] = useState<string | null>(null);
  const [query, setQuery] = useState("");

  useEffect(() => setDrafts(listDrafts()), []);

  const remove = (id: string) => {
    removeDraft(id);
    setDrafts(listDrafts());
    setConfirming(null);
  };

  const shown = drafts.filter(
    (d) =>
      !query.trim() ||
      d.title.toLowerCase().includes(query.toLowerCase()) ||
      d.body.toLowerCase().includes(query.toLowerCase()),
  );

  const words = (d: Draft) => d.body.trim().split(/\s+/).filter(Boolean).length;

  return (
    <>
      <div className="pt-drafts-head">
        <input
          className="pt-search"
          placeholder="Search your drafts…"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          aria-label="Search drafts"
        />
        <Link href="/write" className="pt-primary as-link sm">
          + New draft
        </Link>
      </div>

      {drafts.length === 0 && (
        <div className="pt-blank">
          <p className="ta">இன்னும் எதுவும் இல்லை</p>
          <p>Your drafts are saved automatically as you write. Nothing to see yet.</p>
          <Link href="/write" className="pt-primary as-link">
            Start writing →
          </Link>
        </div>
      )}

      {drafts.length > 0 && shown.length === 0 && (
        <p className="pt-empty">Nothing matches “{query}”.</p>
      )}

      <div className="pt-draft-grid">
        {shown.map((d) => (
          <div key={d.id} className="pt-draft-card">
            <Link href="/write" className="pt-draft-open">
              <h3 lang="ta">{d.title}</h3>
              <p lang="ta">{d.body.slice(0, 140) || "Empty draft"}</p>
            </Link>

            <div className="pt-draft-meta">
              <span>{words(d)} words</span>
              <span>·</span>
              <time>{formatDate(d.updatedAt)}</time>

              {confirming === d.id ? (
                <span className="pt-confirm">
                  Delete for good?
                  <button onClick={() => remove(d.id)}>Yes</button>
                  <button className="ghost" onClick={() => setConfirming(null)}>
                    No
                  </button>
                </span>
              ) : (
                <button
                  className="pt-del-btn"
                  onClick={() => setConfirming(d.id)}
                  aria-label={`Delete ${d.title}`}
                >
                  Delete
                </button>
              )}
            </div>
          </div>
        ))}
      </div>

      {drafts.length > 0 && (
        <p className="pt-retention">
          Drafts are stored on this device. Deleted drafts are kept for 7 days before they
          are gone for good. Signing in (Phase 6) will sync them across devices.
        </p>
      )}
    </>
  );
}

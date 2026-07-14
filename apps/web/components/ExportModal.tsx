"use client";

import { useState } from "react";
import Link from "next/link";

import { exportDocx, exportPdf, exportTxt } from "@/lib/documents";

type Fmt = "txt" | "docx" | "pdf";

/**
 * Export (§17.2 screen 6, §7.4).
 *
 * PRO-GATED — except .txt.
 *
 * Locking plain text would be indefensible: it is the user's own words, and refusing to
 * hand them back without payment is hostage-taking, not a business model. Word and PDF
 * are formatting work we do for them, and that is a fair thing to charge for.
 *
 * The gate is honest about being inert: billing is Phase 6, so everything is unlocked
 * today and the modal says so rather than quietly letting a "Pro" click through as if it
 * had checked something.
 */
const FORMATS: { id: Fmt; name: string; note: string; pro: boolean }[] = [
  { id: "txt", name: "Plain text (.txt)", note: "Always free — it is your writing.", pro: false },
  { id: "docx", name: "Word (.docx)", note: "Opens in Word or Google Docs, with a Tamil font.", pro: true },
  { id: "pdf", name: "PDF", note: "Uses your browser's print dialog, so Tamil renders correctly.", pro: true },
];

const IS_PRO = false; // Phase 6: billing.IsUserPro()

export default function ExportModal({
  text,
  onClose,
}: {
  text: string;
  onClose: () => void;
}) {
  const [fmt, setFmt] = useState<Fmt>("txt");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const locked = FORMATS.find((f) => f.id === fmt)?.pro && !IS_PRO;

  const run = async () => {
    setBusy(true);
    setError("");
    try {
      if (fmt === "txt") exportTxt(text);
      else if (fmt === "docx") await exportDocx(text);
      else exportPdf(text);
      onClose();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="pt-modal-bg" onClick={onClose}>
      <div
        className="pt-modal"
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-label="Export"
      >
        <div className="pt-writer-head">
          <h2>Export</h2>
          <button className="pt-x" onClick={onClose} aria-label="Close">
            ×
          </button>
        </div>

        <div className="pt-paths" role="radiogroup" aria-label="Format">
          {FORMATS.map((f) => (
            <button
              key={f.id}
              role="radio"
              aria-checked={fmt === f.id}
              className={fmt === f.id ? "on" : ""}
              onClick={() => setFmt(f.id)}
            >
              <strong>
                {f.name}
                {f.pro && !IS_PRO && <span className="pt-lock"> 🔒 Pro</span>}
              </strong>
              <span>{f.note}</span>
            </button>
          ))}
        </div>

        {locked ? (
          <div className="pt-gate">
            <strong>🔒 Word and PDF are a Pro feature</strong>
            <p>
              Plain text is always free. Billing is not connected yet, so this will export
              anyway — the paywall lands in Phase 6.
            </p>
            <Link href="/pricing" className="pt-inline-cta">
              See Pro →
            </Link>
          </div>
        ) : null}

        {error && <p className="pt-werror">{error}</p>}

        <div className="pt-wactions">
          <button onClick={run} disabled={busy || !text.trim()}>
            {busy ? "Exporting…" : `Export as ${fmt.toUpperCase()}`}
          </button>
          <button className="ghost" onClick={onClose}>
            Cancel
          </button>
        </div>
      </div>
    </div>
  );
}

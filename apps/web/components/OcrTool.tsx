"use client";

import { useRef, useState } from "react";
import Link from "next/link";

import { readImageLocally } from "@/lib/ocr-client";

/**
 * The standalone Tamil OCR tool (production's /tools/ocr). A drop zone, a language hint,
 * an Extract button, and the result with copy / open-in-editor actions.
 *
 * PRINTED text is read ON THIS DEVICE (Tesseract in the browser) — the image never leaves
 * the machine, which is the privacy promise the page makes. Handwriting, which needs an AI
 * model, has its own page.
 */
export default function OcrTool() {
  const [file, setFile] = useState<File | null>(null);
  const [busy, setBusy] = useState(false);
  const [pct, setPct] = useState(0);
  const [text, setText] = useState("");
  const [error, setError] = useState("");
  const [over, setOver] = useState(false);
  const [copied, setCopied] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  const pick = (f: File | null | undefined) => {
    if (!f) return;
    setFile(f);
    setText("");
    setError("");
  };

  const extract = async () => {
    if (!file) return;
    setBusy(true);
    setPct(0);
    setError("");
    setText("");
    try {
      const r = await readImageLocally(file, setPct);
      if (r.text?.trim()) setText(r.text.trim());
      else setError("No Tamil text was found. If this is handwriting, try the Handwriting OCR tool.");
    } catch {
      setError("Could not read that file. Use a clear JPG, PNG or PDF under 16 MB.");
    } finally {
      setBusy(false);
    }
  };

  const copy = async () => {
    await navigator.clipboard.writeText(text);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };

  return (
    <div className="pt-tool-card">
      <div
        className={`pt-dropzone ${over ? "over" : ""}`}
        role="button"
        tabIndex={0}
        onClick={() => inputRef.current?.click()}
        onKeyDown={(e) => (e.key === "Enter" || e.key === " ") && inputRef.current?.click()}
        onDragOver={(e) => {
          e.preventDefault();
          setOver(true);
        }}
        onDragLeave={() => setOver(false)}
        onDrop={(e) => {
          e.preventDefault();
          setOver(false);
          pick(e.dataTransfer.files?.[0]);
        }}
      >
        <div className="ico" aria-hidden="true">
          📄
        </div>
        <strong>{file ? file.name : "Click to upload or drag and drop"}</strong>
        <span>Supports: JPG, PNG, PDF, TIFF, BMP, GIF (Max 16MB)</span>
        <input
          ref={inputRef}
          type="file"
          accept="image/*,.pdf"
          hidden
          onChange={(e) => pick(e.target.files?.[0])}
        />
      </div>

      <label className="pt-field-label" htmlFor="ocr-lang">
        OCR Language:
      </label>
      <select id="ocr-lang" className="pt-select" defaultValue="en+ta">
        <option value="en+ta">English + Tamil (Recommended)</option>
        <option value="ta">Tamil Only</option>
        <option value="en">English Only</option>
      </select>

      <div style={{ textAlign: "center", marginTop: "1.4rem" }}>
        <button className="btn-hero" onClick={extract} disabled={!file || busy}>
          {busy ? `Extracting… ${pct}%` : "🚀 Extract Text"}
        </button>
      </div>

      {busy && (
        <div className="pt-bar" style={{ marginTop: "1rem" }}>
          <i style={{ width: `${pct}%` }} />
        </div>
      )}

      {error && <p className="pt-werror" style={{ textAlign: "center" }}>{error}</p>}

      {text && (
        <div className="pt-wout">
          <div className="pt-wtext" lang="ta">
            {text}
          </div>
          <div className="pt-wactions" style={{ marginTop: ".7rem" }}>
            <button onClick={copy}>{copied ? "Copied ✓" : "Copy text"}</button>
            <Link
              href={`/write?text=${encodeURIComponent(text)}`}
              className="pt-plan-cta solid"
              style={{ flex: 1, textAlign: "center", padding: ".45rem" }}
            >
              Open in editor →
            </Link>
          </div>
        </div>
      )}
    </div>
  );
}

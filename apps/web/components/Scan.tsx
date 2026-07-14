"use client";

import { useState } from "react";

import { API_BASE } from "@/lib/api";
import { readImageLocally } from "@/lib/ocr-client";

type Path = "device" | "server";

interface Props {
  onText: (text: string, note: string) => void;
  onClose: () => void;
}

/**
 * The scan dialog (§15.1, screen 9).
 *
 * IT MAKES THE ROUTING DECISION VISIBLE, rather than choosing silently, because the two
 * paths differ in ways a user genuinely cares about — privacy, and whether their
 * spelling gets quietly rewritten (R6). "Printed" is the default: it is private, free,
 * and faithful. "Handwriting" is opt-in, and says plainly what it costs.
 */
export default function Scan({ onText, onClose }: Props) {
  const [path, setPath] = useState<Path>("device");
  const [busy, setBusy] = useState(false);
  const [pct, setPct] = useState(0);
  const [error, setError] = useState("");
  const [drag, setDrag] = useState(false);

  const run = async (file: File) => {
    setBusy(true);
    setPct(0);
    setError("");

    try {
      if (path === "device") {
        const r = await readImageLocally(file, setPct);
        if (!r.text) {
          setError("No Tamil text found. If this is handwriting, try the other option.");
          return;
        }
        onText(
          r.text,
          `scanned on your device · ${Math.round(r.confidence * 100)}% legible · nothing was uploaded`,
        );
      } else {
        const fd = new FormData();
        fd.append("image", file);
        const res = await fetch(`${API_BASE}/api/v1/ocr`, { method: "POST", body: fd });
        const d = await res.json();
        if (!res.ok) throw new Error(d.error ?? `HTTP ${res.status}`);
        if (!d.text) {
          setError(d.warning ?? "No Tamil text found in that image.");
          return;
        }
        onText(
          d.text,
          `scanned on the server · ${Math.round((d.confidence ?? 0) * 100)}% legible · spelling may have been normalised`,
        );
      }
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
        aria-label="Scan an image"
      >
        <div className="pt-writer-head">
          <h2>📷 Scan Tamil text</h2>
          <button className="pt-x" onClick={onClose} aria-label="Close">
            ×
          </button>
        </div>

        <div className="pt-paths" role="radiogroup" aria-label="Where to read the image">
          <button
            role="radio"
            aria-checked={path === "device"}
            className={path === "device" ? "on" : ""}
            onClick={() => setPath("device")}
          >
            <strong>Printed text</strong>
            <span>
              Read on your device. The image is never uploaded, and your spelling is
              transcribed <em>exactly</em> as written — so the proofreader can show you
              the errors.
            </span>
          </button>

          <button
            role="radio"
            aria-checked={path === "server"}
            className={path === "server" ? "on" : ""}
            onClick={() => setPath("server")}
          >
            <strong>Handwriting</strong>
            <span>
              Uploaded and read by an AI model — far better at cursive Tamil. But it
              tends to <em>silently correct</em> spelling as it reads, so check the
              result against your page.
            </span>
          </button>
        </div>

        <label
          className={`pt-drop ${drag ? "over" : ""}`}
          onDragOver={(e) => {
            e.preventDefault();
            setDrag(true);
          }}
          onDragLeave={() => setDrag(false)}
          onDrop={(e) => {
            e.preventDefault();
            setDrag(false);
            const f = e.dataTransfer.files?.[0];
            if (f && !busy) run(f);
          }}
        >
          <input
            type="file"
            accept="image/*"
            // capture= opens the camera directly on a phone rather than a file picker.
            capture="environment"
            hidden
            disabled={busy}
            onChange={(e) => {
              const f = e.target.files?.[0];
              if (f) run(f);
              e.target.value = "";
            }}
          />
          {busy ? (
            <>
              <strong>Reading…</strong>
              {path === "device" && (
                <>
                  <div className="pt-bar">
                    <i style={{ width: `${pct}%` }} />
                  </div>
                  <span>
                    {pct}% · the Tamil model downloads once on first use (~15 MB)
                  </span>
                </>
              )}
            </>
          ) : (
            <>
              <strong>Drop an image, or click to choose</strong>
              <span>JPG or PNG · a photo of a page works</span>
            </>
          )}
        </label>

        {error && <p className="pt-werror">{error}</p>}
      </div>
    </div>
  );
}

"use client";

import { useEffect, useRef, useState } from "react";

import { API_BASE } from "@/lib/api";
import type { Suggestion } from "@/lib/types";

type Mode = "rewrite" | "template" | "continue";

interface Template {
  id: string;
  name: string;
  name_ta: string;
  fields: string[];
}

interface Props {
  /** The current document, used as input for rewrite/continue. */
  text: string;
  selection: string;
  onInsert: (text: string) => void;
  onClose: () => void;
}

const AXES: Record<string, string[]> = {
  formality: ["formal", "spoken"],
  tone: ["friendly", "neutral", "assertive"],
  clarity: ["clearer"],
  length: ["shorter", "longer"],
};

/**
 * The AI Content Writer panel (§16, §17.3).
 *
 * Generation is the expensive feature, which is why it is Pro-gated — and why the UI
 * shows a Stop button the moment streaming starts. A user who can see the output going
 * the wrong way should be able to stop paying for it mid-sentence.
 */
export default function Writer({ text, selection, onInsert, onClose }: Props) {
  const [mode, setMode] = useState<Mode>("rewrite");
  const [templates, setTemplates] = useState<Template[]>([]);
  const [templateId, setTemplateId] = useState("letter");
  const [fields, setFields] = useState<Record<string, string>>({});
  const [axis, setAxis] = useState("formality");
  const [target, setTarget] = useState("formal");

  const [out, setOut] = useState("");
  const [checks, setChecks] = useState<Suggestion[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const abort = useRef<AbortController | null>(null);

  useEffect(() => {
    fetch(`${API_BASE}/api/v1/write/templates`)
      .then((r) => r.json())
      .then((d) => setTemplates(d.templates ?? []))
      .catch(() => {});
    return () => abort.current?.abort();
  }, []);

  const tpl = templates.find((t) => t.id === templateId);

  const run = async () => {
    // Rewrite works on the selection if there is one, else the whole document —
    // rewriting a whole essay when the user highlighted one sentence would be both
    // surprising and expensive.
    const source = selection.trim() || text;

    const body =
      mode === "rewrite"
        ? { text: source, axis, target }
        : mode === "template"
          ? { template_id: templateId, fields }
          : { context: source };

    setOut("");
    setChecks([]);
    setError("");
    setBusy(true);

    abort.current?.abort();
    const controller = new AbortController();
    abort.current = controller;

    try {
      const res = await fetch(`${API_BASE}/api/v1/write/${mode}`, {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-User-Id": "demo" },
        body: JSON.stringify(body),
        signal: controller.signal,
      });
      if (!res.ok || !res.body) throw new Error(`HTTP ${res.status}`);

      const reader = res.body.getReader();
      const dec = new TextDecoder();
      let buf = "";

      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        buf += dec.decode(value, { stream: true });

        const frames = buf.split("\n\n");
        buf = frames.pop() ?? "";

        for (const f of frames) {
          const line = f.split("\n").find((l) => l.startsWith("data:"));
          if (!line) continue;
          let ev;
          try {
            ev = JSON.parse(line.slice(5).trim());
          } catch {
            continue;
          }

          if (ev.type === "chunk") setOut((p) => p + ev.text);
          else if (ev.type === "suggestions") setChecks(ev.suggestions ?? []);
          else if (ev.type === "error") {
            setError(
              ev.error === "pro_required"
                ? "pro"
                : ev.error === "quota_exceeded"
                  ? "quota"
                  : ev.error,
            );
          }
        }
      }
    } catch (e) {
      if ((e as Error).name !== "AbortError") setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const stop = () => {
    abort.current?.abort();
    setBusy(false);
  };

  return (
    <div className="pt-writer">
      <div className="pt-writer-head">
        <h2>AI Writer</h2>
        <button className="pt-x" onClick={onClose} aria-label="Close the writer">
          ×
        </button>
      </div>

      <div className="pt-modes" role="tablist">
        {(["rewrite", "template", "continue"] as Mode[]).map((m) => (
          <button
            key={m}
            role="tab"
            aria-selected={mode === m}
            className={mode === m ? "on" : ""}
            onClick={() => setMode(m)}
          >
            {m === "rewrite" ? "Rewrite" : m === "template" ? "Template" : "Continue"}
          </button>
        ))}
      </div>

      {mode === "rewrite" && (
        <div className="pt-wfields">
          <label>
            Axis
            <select
              value={axis}
              onChange={(e) => {
                setAxis(e.target.value);
                setTarget(AXES[e.target.value][0]);
              }}
            >
              {Object.keys(AXES).map((a) => (
                <option key={a}>{a}</option>
              ))}
            </select>
          </label>
          <label>
            Target
            <select value={target} onChange={(e) => setTarget(e.target.value)}>
              {AXES[axis].map((t) => (
                <option key={t}>{t}</option>
              ))}
            </select>
          </label>
          <p className="pt-wnote">
            {selection.trim()
              ? `Rewriting the ${Array.from(selection.trim()).length} selected characters.`
              : "Nothing selected — the whole document will be rewritten."}
          </p>
        </div>
      )}

      {mode === "template" && (
        <div className="pt-wfields">
          <label>
            Type
            <select value={templateId} onChange={(e) => setTemplateId(e.target.value)}>
              {templates.map((t) => (
                <option key={t.id} value={t.id}>
                  {t.name_ta} — {t.name}
                </option>
              ))}
            </select>
          </label>
          {tpl?.fields.map((f) => (
            <label key={f}>
              {f}
              <input
                value={fields[f] ?? ""}
                placeholder="optional"
                onChange={(e) => setFields((p) => ({ ...p, [f]: e.target.value }))}
              />
            </label>
          ))}
          <p className="pt-wnote">
            Blank fields become <code>[___]</code> placeholders. The model is told never
            to invent a fact you did not give it.
          </p>
        </div>
      )}

      {mode === "continue" && (
        <p className="pt-wnote">
          Continues from the end of your draft, matching its style and register.
        </p>
      )}

      <div className="pt-wactions">
        {busy ? (
          <button className="pt-stop" onClick={stop}>
            Stop
          </button>
        ) : (
          <button onClick={run}>Generate</button>
        )}
      </div>

      {/* Pro gate. It has no teeth yet — billing is Phase 6 — but the UI path exists. */}
      {error === "pro" && (
        <div className="pt-gate">
          <strong>🔒 Pro feature</strong>
          <p>AI writing is included with Pro. Upgrade to generate Tamil drafts.</p>
        </div>
      )}
      {error === "quota" && (
        <div className="pt-gate">
          <strong>Daily limit reached</strong>
          <p>You have used your generations for today. They reset tomorrow.</p>
        </div>
      )}
      {error && error !== "pro" && error !== "quota" && (
        <p className="pt-werror">{error}</p>
      )}

      {out && (
        <div className="pt-wout">
          <div className="pt-wtext" lang="ta">
            {out}
          </div>

          {/* §16.2 — the moat, made visible. The writer should SEE that the AI's own
              Tamil was checked by the same engine that checks theirs. */}
          {checks.length > 0 && (
            <p className="pt-wchecks">
              ⚠ Our proofreader found {checks.length} issue
              {checks.length === 1 ? "" : "s"} in this generated text. Insert it and
              they will appear as suggestions.
            </p>
          )}
          {!busy && checks.length === 0 && (
            <p className="pt-wchecks ok">✓ Checked by the proofreader — no issues.</p>
          )}

          {!busy && (
            <div className="pt-wactions">
              <button onClick={() => onInsert(out)}>Insert into draft</button>
              <button
                className="ghost"
                onClick={() => navigator.clipboard.writeText(out)}
              >
                Copy
              </button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

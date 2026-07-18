"use client";

import { useEffect, useRef, useState } from "react";

import { ProofreadClient } from "@/lib/api";
import type { Suggestion } from "@/lib/types";

const SAMPLE = "அந்த பையன் வந்தான். நாங்கள் நகரத்திற்கு போனேன்.";

/**
 * The anonymous demo (§17.2 screen 1).
 *
 * NOTHING IS SAVED, AND THE PAGE SAYS SO. A stranger pasting Tamil into a website they
 * have never used before is entitled to know it is not being kept — and saying it
 * plainly converts better than any feature list, because the objection it answers
 * ("where does my writing go?") is the one people actually have.
 *
 * It is the same cascade the real editor uses: no separate demo backend, so there is no
 * way for the demo to be better than the product.
 */
export default function Demo() {
  const [text, setText] = useState(SAMPLE);
  const [suggestions, setSuggestions] = useState<Suggestion[]>([]);
  const [state, setState] = useState<"idle" | "checking" | "done">("idle");

  const client = useRef(new ProofreadClient());
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const check = (value: string) => {
    if (!value.trim()) {
      setSuggestions([]);
      setState("idle");
      return;
    }
    setState("checking");
    const found: Suggestion[] = [];
    client.current.stream(value, {
      onSuggestions: (b) => {
        found.push(...b);
        setSuggestions([...found]);
      },
      onDone: () => setState("done"),
      onError: () => setState("done"),
    });
  };

  useEffect(() => {
    check(SAMPLE);
    return () => client.current.cancel();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const onChange = (v: string) => {
    setText(v);
    if (timer.current) clearTimeout(timer.current);
    timer.current = setTimeout(() => check(v), 600);
  };

  const words = text.trim() ? text.trim().split(/\s+/).length : 0;

  return (
    <section className="pt-demo">
      <div className="pt-demo-box">
        <div className="pt-demo-head">
          <div className="pt-demo-dots" aria-hidden="true">
            <i />
            <i />
            <i />
          </div>
          <span className="pt-demo-title">Tamil Editor</span>
          <span className="pt-demo-words">{words} words</span>
        </div>
        <textarea
          lang="ta"
          spellCheck={false}
          value={text}
          onChange={(e) => onChange(e.target.value)}
          aria-label="Paste Tamil to check"
          placeholder="தமிழில் எழுதுங்கள்…"
        />
        <div className="pt-demo-foot">
          <span>
            {state === "checking"
              ? "checking…"
              : suggestions.length
                ? `${suggestions.length} suggestion${suggestions.length === 1 ? "" : "s"}`
                : state === "done"
                  ? "no issues found"
                  : ""}
          </span>
          <span className="pt-privacy">Nothing is saved. No account needed.</span>
        </div>
      </div>

      <div className="pt-demo-out" aria-live="polite">
        {suggestions.length === 0 && state === "done" && (
          <p className="pt-empty">
            <span className="ta">உங்கள் தமிழ் சரியாக உள்ளது ✅</span>
            Clean. For correct Tamil — and for real words like புலி — silence is the right
            answer.
          </p>
        )}

        {suggestions.map((s, i) => (
          <div key={i} className="pt-demo-card">
            <span className="pt-fix">
              <del>{s.original}</del> <span className="pt-arrow">→</span>{" "}
              <ins>{s.suggestion}</ins>
            </span>
            <span className={`pt-tag type ${s.type}`}>{s.type}</span>
            <span className="pt-tag">
              {s.source_tier === 1 ? "instant · free" : "AI"}
            </span>
            {s.explanation && <p className="pt-why">{s.explanation}</p>}
          </div>
        ))}
      </div>

      <a className="pt-cta" href="/write">
        Open the full editor →
      </a>
    </section>
  );
}

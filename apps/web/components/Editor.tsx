"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { EditorContent, useEditor } from "@tiptap/react";
import { Extension } from "@tiptap/core";
import StarterKit from "@tiptap/starter-kit";
import { TextSelection } from "@tiptap/pm/state";

import { ProofreadClient, suggestTamil } from "@/lib/api";
import {
  type Draft,
  listDrafts,
  newId,
  removeDraft,
  saveDraft,
} from "@/lib/drafts";
import { exportDocx, exportPdf, exportTxt, importFile } from "@/lib/documents";
import { remember, rerank } from "@/lib/ime-history";
import {
  buildPositionMap,
  removeSuggestion,
  setSuggestions,
  suggestionPlugin,
  suggestionPluginKey,
} from "@/lib/suggestion-plugin";
import type { IMESuggestion, Suggestion } from "@/lib/types";

/**
 * Debounce before proofreading. Long enough that a normal typist is not firing a
 * model call between every keystroke (which would be expensive and useless, since
 * the sentence is half-written), short enough that a pause feels answered.
 */
const PROOFREAD_DEBOUNCE_MS = 700;

/** The IME must feel instant, so it barely debounces at all. */
const IME_DEBOUNCE_MS = 60;

/**
 * Autosave. Far more frequent than proofreading, because losing someone's writing is
 * catastrophic while a slightly-late spellcheck is merely annoying. Writing to
 * localStorage is cheap; the debounce exists only to avoid serialising on every
 * keypress.
 */
const AUTOSAVE_DEBOUNCE_MS = 400;

const SAMPLE = "அந்த பையன் வந்தான். அவர்கள் வனிகர்கள். நாங்கள் நகரத்திற்கு போனேன்.";

/**
 * The suggestion decorations, as a real TipTap Extension.
 *
 * This MUST be Extension.create(). An earlier version passed a plain object
 * ({ name, addProseMirrorPlugins }) cast to `never` — TipTap accepted it without
 * complaint and simply never registered the plugin, so the suggestion panel worked
 * while not a single underline was drawn. A silent no-op, caught only by asserting
 * on the rendered decorations in a browser.
 */
const SuggestionExtension = Extension.create({
  name: "prooftamilSuggestions",
  addProseMirrorPlugins() {
    return [suggestionPlugin()];
  },
});

interface IMEState {
  items: IMESuggestion[];
  query: string;
  /** Document position where the romanized word starts. */
  from: number;
  to: number;
  index: number;
  coords: { left: number; top: number };
}

/**
 * Plain text -> the HTML TipTap wants.
 *
 * One <p> per line, and the escaping matters: imported .docx/.pdf content is
 * untrusted input, and feeding it to setContent() as raw HTML would let a crafted
 * document inject markup into the editor.
 */
function toHtml(text: string): string {
  const esc = (s: string) =>
    s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  const paras = text.split(/\n/).map((l) => `<p>${esc(l) || "<br>"}</p>`);
  return paras.join("") || "<p></p>";
}

export default function Editor() {
  const [suggestions, setSugg] = useState<Suggestion[]>([]);
  const [status, setStatus] = useState("");
  const [imeOn, setImeOn] = useState(true);
  const [ime, setIme] = useState<IMEState | null>(null);

  const [drafts, setDrafts] = useState<Draft[]>([]);
  const [draftId, setDraftId] = useState<string>("");
  const [saved, setSaved] = useState("");
  const [notice, setNotice] = useState("");

  const proofreader = useMemo(() => new ProofreadClient(), []);
  const proofTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const imeTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const saveTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const imeAbort = useRef<AbortController | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);
  const draftIdRef = useRef("");
  draftIdRef.current = draftId;

  // The IME dropdown is driven from keydown, which does not re-render, so it needs
  // the current state in a ref rather than the closed-over value.
  const imeRef = useRef<IMEState | null>(null);
  imeRef.current = ime;
  const imeOnRef = useRef(imeOn);
  imeOnRef.current = imeOn;

  const editor = useEditor({
    // Next.js renders this on the server first; without the flag React complains the
    // client markup does not match.
    immediatelyRender: false,
    extensions: [
      StarterKit.configure({ heading: { levels: [1, 2] } }),
      SuggestionExtension,
    ],
    content: `<p>${SAMPLE}</p>`,
    editorProps: {
      attributes: { class: "pt-editor", lang: "ta", spellcheck: "false" },

      // Keyboard handling for the IME dropdown. Returning true swallows the key, so
      // Enter selects a candidate instead of inserting a newline.
      handleKeyDown: (_view, event) => {
        const st = imeRef.current;
        if (!st || !imeOnRef.current) return false;

        if (event.key === "ArrowDown") {
          setIme((s) => (s ? { ...s, index: (s.index + 1) % s.items.length } : s));
          return true;
        }
        if (event.key === "ArrowUp") {
          setIme((s) =>
            s ? { ...s, index: (s.index - 1 + s.items.length) % s.items.length } : s,
          );
          return true;
        }
        if (event.key === "Enter" || event.key === "Tab") {
          acceptIME(st.index);
          return true;
        }
        // Esc dismisses and KEEPS the Latin text. Typing romanized and ignoring the
        // dropdown must never silently rewrite what the user wrote.
        if (event.key === "Escape") {
          setIme(null);
          return true;
        }
        if (/^[1-9]$/.test(event.key)) {
          const i = Number(event.key) - 1;
          if (st.items[i]) {
            acceptIME(i);
            return true;
          }
        }
        return false;
      },
    },

    onUpdate: ({ editor }) => {
      const text = editor.getText();

      // Drop panel entries whose text the user has just edited away, IMMEDIATELY —
      // not after the next proofread returns.
      //
      // The underlines already vanish on their own (the plugin re-runs its anchor
      // check on every doc change), but the side panel would keep offering "Accept"
      // on words that no longer exist. Clicking it is guarded, but showing it at all
      // is a lie about the state of the document.
      const runes = Array.from(text);
      setSugg((prev) =>
        prev.filter(
          (s) => runes.slice(s.start, s.end).join("") === s.original,
        ),
      );

      // Autosave FIRST, and on a shorter debounce than anything else. Losing
      // someone's writing is catastrophic; a late spellcheck is merely annoying.
      if (saveTimer.current) clearTimeout(saveTimer.current);
      saveTimer.current = setTimeout(() => {
        const id = draftIdRef.current || newId();
        if (!draftIdRef.current) setDraftId(id);
        saveDraft(id, text);
        setDrafts(listDrafts());
        setSaved(
          new Date().toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }),
        );
      }, AUTOSAVE_DEBOUNCE_MS);

      // Proofreading: debounced, and every new run cancels the last. A person types
      // faster than a model answers, so results for text the user has already
      // replaced WILL arrive — the client must drop them.
      if (proofTimer.current) clearTimeout(proofTimer.current);
      proofTimer.current = setTimeout(() => runProofread(text), PROOFREAD_DEBOUNCE_MS);

      // IME: near-instant.
      if (imeTimer.current) clearTimeout(imeTimer.current);
      imeTimer.current = setTimeout(runIME, IME_DEBOUNCE_MS);
    },
  });

  /* ------------------------------------------------------------ proofreading */

  const runProofread = useCallback(
    (text: string) => {
      if (!editor || !text.trim()) {
        setSugg([]);
        return;
      }

      setStatus("checking…");
      const collected: Suggestion[] = [];

      proofreader.stream(text, {
        onSuggestions: (batch) => {
          collected.push(...batch);
          // Push into the plugin as they stream in: Tier 1 lands in milliseconds and
          // the model residue follows, so the writer sees spelling fixed while the
          // grammar check is still in flight.
          setSugg([...collected]);
          editor.view.dispatch(
            editor.view.state.tr.setMeta(suggestionPluginKey, setSuggestions([...collected])),
          );
        },
        onDone: (modelPending) => {
          setStatus(
            collected.length === 0
              ? modelPending
                ? "no issues found (model tier unavailable)"
                : "no issues found"
              : `${collected.length} suggestion${collected.length === 1 ? "" : "s"}`,
          );
        },
        onError: (e) => setStatus(`error: ${e}`),
      });
    },
    [editor, proofreader],
  );

  useEffect(() => {
    if (editor) runProofread(editor.getText());
    return () => proofreader.cancel();
  }, [editor, proofreader, runProofread]);

  const accept = (i: number) => {
    if (!editor) return;
    const s = suggestions[i];
    if (!s) return;

    const { map, text } = buildPositionMap(editor.state.doc);
    // The document may have changed since the suggestion was produced. Re-check the
    // anchor before touching the user's text; applying a stale span would corrupt it.
    const actual = Array.from(text).slice(s.start, s.end).join("");
    if (actual !== s.original || s.end > map.length) {
      setStatus("that text changed — skipped");
      return;
    }

    const from = map[s.start];
    const to = map[s.end - 1] + 1;

    editor
      .chain()
      .focus()
      .insertContentAt({ from, to }, s.suggestion)
      .run();

    const next = suggestions.filter((_, j) => j !== i);
    setSugg(next);
    editor.view.dispatch(
      editor.view.state.tr.setMeta(suggestionPluginKey, removeSuggestion(i)),
    );
  };

  const reject = (i: number) => {
    if (!editor) return;
    const next = suggestions.filter((_, j) => j !== i);
    setSugg(next);
    editor.view.dispatch(
      editor.view.state.tr.setMeta(suggestionPluginKey, removeSuggestion(i)),
    );
    // Phase 3 will publish this to the event log — a rejection is the highest-signal
    // training data there is, because it is a human saying "you were wrong".
  };

  /* -------------------------------------------------------------------- IME */

  const runIME = useCallback(async () => {
    if (!editor || !imeOnRef.current) {
      setIme(null);
      return;
    }

    const { state } = editor;
    const { from, empty } = state.selection;
    if (!empty) {
      setIme(null);
      return;
    }

    // The Latin run immediately before the caret — and only Latin. Tamil the user has
    // already inserted must never be "re-suggested".
    const before = state.doc.textBetween(Math.max(0, from - 32), from, "\n", "\n");
    const m = before.match(/[A-Za-z]+$/);
    if (!m || m[0].length < 2) {
      setIme(null);
      return;
    }

    const query = m[0];
    imeAbort.current?.abort();
    const controller = new AbortController();
    imeAbort.current = controller;

    try {
      const items = await suggestTamil(query, 8, controller.signal);
      if (!items.length) {
        setIme(null);
        return;
      }

      const coords = editor.view.coordsAtPos(from);
      const box = editor.view.dom.getBoundingClientRect();

      setIme({
        // Personal history first: people reuse their own vocabulary, and this ranking
        // costs no privacy because it never leaves the device.
        items: rerank(query, items),
        query,
        from: from - query.length,
        to: from,
        index: 0,
        coords: { left: coords.left - box.left, top: coords.bottom - box.top },
      });
    } catch {
      setIme(null);
    }
  }, [editor]);

  const acceptIME = useCallback(
    (i: number) => {
      const st = imeRef.current;
      if (!editor || !st) return;
      const pick = st.items[i];
      if (!pick) return;

      remember(st.query, pick.word);

      editor
        .chain()
        .focus()
        .insertContentAt({ from: st.from, to: st.to }, pick.word + " ")
        .command(({ tr, dispatch }) => {
          if (dispatch) {
            const end = tr.selection.from;
            dispatch(tr.setSelection(TextSelection.create(tr.doc, end)));
          }
          return true;
        })
        .run();

      setIme(null);
    },
    [editor],
  );

  /* -------------------------------------------------------- drafts & files */

  // Restore the most recent draft on load. A writer who closes the tab and comes back
  // must find their work, not a blank page.
  useEffect(() => {
    if (!editor) return;
    const all = listDrafts();
    setDrafts(all);

    const latest = all[0];
    if (latest?.body.trim()) {
      setDraftId(latest.id);
      editor.commands.setContent(toHtml(latest.body));
    } else {
      setDraftId(newId());
    }
    // Only on mount: re-running this would stomp on what the user is typing.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [editor]);

  const openDraft = (d: Draft) => {
    if (!editor) return;
    setDraftId(d.id);
    editor.commands.setContent(toHtml(d.body));
    setSugg([]);
    setNotice("");
  };

  const newDraft = () => {
    if (!editor) return;
    setDraftId(newId());
    editor.commands.setContent("<p></p>");
    setSugg([]);
    setNotice("");
    editor.commands.focus();
  };

  const deleteDraft = (id: string) => {
    removeDraft(id);
    const rest = listDrafts();
    setDrafts(rest);
    if (id === draftId) newDraft();
  };

  const onImport = async (file: File) => {
    if (!editor) return;
    setNotice(`importing ${file.name}…`);
    try {
      const { text, warnings } = await importFile(file);
      setDraftId(newId());
      editor.commands.setContent(toHtml(text));
      setNotice(warnings.join(" ") || `imported ${file.name}`);
    } catch (e) {
      setNotice((e as Error).message);
    }
  };

  const onExport = async (fmt: "txt" | "docx" | "pdf") => {
    if (!editor) return;
    const text = editor.getText();
    if (!text.trim()) return setNotice("nothing to export");
    try {
      if (fmt === "txt") exportTxt(text);
      else if (fmt === "docx") await exportDocx(text);
      else exportPdf(text);
      setNotice(`exported as ${fmt.toUpperCase()}`);
    } catch (e) {
      setNotice((e as Error).message);
    }
  };

  /* ------------------------------------------------------------------- view */

  if (!editor) return <div className="pt-loading">loading editor…</div>;

  return (
    <div className="pt-layout">
      <div className="pt-main">
        <div className="pt-filebar">
          <button onClick={newDraft}>New</button>

          <button onClick={() => fileInput.current?.click()}>Import</button>
          <input
            ref={fileInput}
            type="file"
            accept=".txt,.md,.docx,.pdf"
            hidden
            onChange={(e) => {
              const f = e.target.files?.[0];
              if (f) onImport(f);
              e.target.value = ""; // so the same file can be picked twice
            }}
          />

          <span className="pt-sep" />

          <button onClick={() => onExport("txt")}>.txt</button>
          <button onClick={() => onExport("docx")}>.docx</button>
          <button onClick={() => onExport("pdf")}>.pdf</button>

          <span className="pt-saved">
            {notice || (saved && `saved ${saved}`)}
          </span>
        </div>

        <div className="pt-toolbar">
          <label className="pt-toggle">
            <input
              type="checkbox"
              checked={imeOn}
              onChange={(e) => {
                setImeOn(e.target.checked);
                if (!e.target.checked) setIme(null);
              }}
            />
            <span>Tamil IME</span>
          </label>
          <span className="pt-hint">
            type <code>vanakkam</code> · <kbd>Enter</kbd> or <kbd>1–9</kbd> to insert ·{" "}
            <kbd>Esc</kbd> keeps the Latin
          </span>
          <span className="pt-status">{status}</span>
        </div>

        <div className="pt-editor-wrap">
          <EditorContent editor={editor} />

          {ime && imeOn && (
            <ul
              className="pt-ime"
              style={{ left: ime.coords.left, top: ime.coords.top + 6 }}
              role="listbox"
            >
              {ime.items.map((s, i) => (
                <li
                  key={s.word}
                  role="option"
                  aria-selected={i === ime.index}
                  className={i === ime.index ? "sel" : ""}
                  onMouseDown={(e) => {
                    e.preventDefault(); // keep focus in the editor
                    acceptIME(i);
                  }}
                >
                  <span className="num">{i + 1}</span>
                  <span className="word">{s.word}</span>
                  {/* A generated form is a guess, not a dictionary word. Say so. */}
                  {s.source === "generated" && <span className="gen">new</span>}
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>

      <aside className="pt-side">
      {drafts.length > 0 && (
        <div className="pt-panel pt-drafts">
          <h2>Drafts</h2>
          {drafts.slice(0, 8).map((d) => (
            <div
              key={d.id}
              className={`pt-draft ${d.id === draftId ? "cur" : ""}`}
              onClick={() => openDraft(d)}
            >
              <span className="pt-draft-title">{d.title}</span>
              <button
                className="pt-del"
                title="Delete"
                onClick={(e) => {
                  e.stopPropagation();
                  deleteDraft(d.id);
                }}
              >
                ×
              </button>
            </div>
          ))}
        </div>
      )}

      <div className="pt-panel">
        <h2>
          Suggestions <span className="pt-count">{suggestions.length}</span>
        </h2>

        {suggestions.length === 0 && (
          <p className="pt-empty">
            Nothing to fix. For clean Tamil — and for real words like புலி — silence is
            the correct answer.
          </p>
        )}

        {suggestions.map((s, i) => (
          <div key={`${s.start}-${s.original}-${i}`} className="pt-card">
            <div className="pt-fix">
              <del>{s.original}</del> <span className="pt-arrow">→</span>{" "}
              <ins>{s.suggestion}</ins>
            </div>

            <div className="pt-tags">
              <span className={`pt-tag ${s.source_tier === 1 ? "t1" : "tm"}`}>
                {s.source_tier === 1 ? "rules · free" : `model · tier ${s.source_tier}`}
              </span>
              <span className="pt-tag">{s.type}</span>
              <span className="pt-tag">{Math.round(s.confidence * 100)}%</span>
            </div>

            {s.explanation && <p className="pt-why">{s.explanation}</p>}

            <div className="pt-actions">
              <button onClick={() => accept(i)}>Accept</button>
              <button className="ghost" onClick={() => reject(i)}>
                Dismiss
              </button>
            </div>
          </div>
        ))}
      </div>
      </aside>
    </div>
  );
}

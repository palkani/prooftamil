"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { EditorContent, useEditor } from "@tiptap/react";
import { Extension } from "@tiptap/core";
import StarterKit from "@tiptap/starter-kit";
import { TextSelection } from "@tiptap/pm/state";

import { API_BASE, ProofreadClient, sendFeedback, suggestTamil } from "@/lib/api";
import {
  type Draft,
  listDrafts,
  newId,
  removeDraft,
  saveDraft,
} from "@/lib/drafts";
import { exportDocx, exportPdf, exportTxt, importFile } from "@/lib/documents";
import { remember, rerank } from "@/lib/ime-history";
import { preloadIMEIndex } from "@/lib/ime-local";
import Writer from "./Writer";
import Scan from "./Scan";
import ExportModal from "./ExportModal";
import { recordingSupported, startRecording, type Recorder } from "@/lib/recorder";
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

  const [checking, setChecking] = useState(false);
  const [writerOpen, setWriterOpen] = useState(false);
  const [scanOpen, setScanOpen] = useState(false);
  const [exportOpen, setExportOpen] = useState(false);
  const [voiceState, setVoiceState] = useState<"idle" | "recording" | "transcribing">("idle");
  const [micLevel, setMicLevel] = useState(0);
  const [filter, setFilter] = useState<string>("all");
  const recorder = useRef<Recorder | null>(null);
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
        // Fixed HH:MM, not toLocaleTimeString — locale-independent, so no hydration or
        // display surprise for a user whose browser is not en-US.
        const now = new Date();
        setSaved(
          `${String(now.getHours()).padStart(2, "0")}:${String(now.getMinutes()).padStart(2, "0")}`,
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
      setChecking(true);
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
          setChecking(false);
          setStatus(
            collected.length === 0
              ? modelPending
                ? "no issues found (model tier unavailable)"
                : "no issues found"
              : `${collected.length} suggestion${collected.length === 1 ? "" : "s"}`,
          );
        },
        onError: (e) => {
          setChecking(false);
          setStatus(`error: ${e}`);
        },
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

    sendFeedback("accept", s);

    const next = suggestions.filter((_, j) => j !== i);
    setSugg(next);
    editor.view.dispatch(
      editor.view.state.tr.setMeta(suggestionPluginKey, removeSuggestion(i)),
    );
  };

  // Filter by error type (§17.3). With a long document the panel becomes a wall; a
  // writer fixing spelling does not want to scroll past twenty style notes.
  const types = Array.from(new Set(suggestions.map((s) => s.type)));
  const shown = suggestions
    .map((s, i) => ({ s, i }))
    .filter(({ s }) => filter === "all" || s.type === filter);

  /** High-confidence = at or above the gate the server already applied. */
  const SAFE = 0.9;
  const safeCount = suggestions.filter((s) => s.confidence >= SAFE).length;

  /**
   * Apply every high-confidence fix at once.
   *
   * Applied HIGHEST OFFSET FIRST. Each replacement changes the length of the
   * document, so applying left-to-right would shift every span after it and the
   * second fix would land in the wrong place. Going backwards means the offsets ahead
   * of the cursor are never disturbed.
   */
  const applyAllSafe = () => {
    if (!editor) return;
    const safe = suggestions
      .filter((s) => s.confidence >= SAFE)
      .sort((a, b) => b.start - a.start);

    const { map, text } = buildPositionMap(editor.state.doc);
    const runes = Array.from(text);

    const chain = editor.chain().focus();
    let applied = 0;
    for (const s of safe) {
      if (runes.slice(s.start, s.end).join("") !== s.original) continue; // moved on
      if (s.end > map.length) continue;
      chain.insertContentAt({ from: map[s.start], to: map[s.end - 1] + 1 }, s.suggestion);
      applied++;
    }
    chain.run();

    setSugg((prev) => prev.filter((s) => s.confidence < SAFE));
    setStatus(`applied ${applied} fixes`);
  };

  const reject = (i: number) => {
    if (!editor) return;

    // The high-signal one: a human telling us this correction was wrong.
    const s = suggestions[i];
    if (s) sendFeedback("reject", s);

    const next = suggestions.filter((_, j) => j !== i);
    setSugg(next);
    editor.view.dispatch(
      editor.view.state.tr.setMeta(suggestionPluginKey, removeSuggestion(i)),
    );
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

  // Fetch the client IME index AFTER first paint, never during it. A user who never
  // types romanized Tamil must not pay 0.3 MB for it, and the editor has to be usable the
  // instant it renders — the server path answers until this lands.
  useEffect(() => {
    const w = window as unknown as { requestIdleCallback?: (cb: () => void) => void };
    if (w.requestIdleCallback) w.requestIdleCallback(() => preloadIMEIndex());
    else setTimeout(preloadIMEIndex, 1200);
    // Release the microphone if the component unmounts mid-recording.
    return () => recorder.current?.stop();
  }, []);

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

  /**
   * A scan lands in a NEW draft, not the current one: someone photographing a page
   * expects a document, not their work-in-progress to have a page pasted into it.
   */
  const onScanned = (text: string, note: string) => {
    if (!editor) return;
    setDraftId(newId());
    editor.commands.setContent(toHtml(text));
    setNotice(note);
    setScanOpen(false);
  };

  /**
   * Voice typing — record, then transcribe with Sarvam's Tamil ASR.
   *
   * This replaced a Web Speech implementation. Web Speech is Chrome/Safari-only (no
   * Firefox at all), ships audio to Google regardless, and recognises Tamil poorly
   * because Tamil is a rounding error in a general-purpose recogniser. Recording and
   * sending to Saarika works in every browser and is dramatically more accurate — Saarika
   * is built for Indian languages. Verified end to end: it transcribed real Tamil audio
   * exactly.
   *
   * The transcript arrives when you STOP, not word by word. For dictation that is the
   * better shape anyway: no half-formed text flickering as the model changes its mind.
   */
  const toggleVoice = async () => {
    if (!editor) return;

    // Already recording? Stop — transcription then runs and inserts.
    if (recorder.current) {
      recorder.current.stop();
      recorder.current = null;
      return;
    }

    // THE SAARIKA RECORDER IS THE DEFAULT, and it is what actually works.
    //
    // Web Speech "live" dictation was tried and it fails on real machines: Google's Tamil
    // backend returns nothing, silently, so the user got "listening" and no text. Worse,
    // pressing Stop during that dead period skipped the fallback entirely — voice appeared
    // completely broken. Record-then-transcribe with Saarika is verified end to end and
    // gives excellent Tamil, so it is the path everyone gets.
    //
    // (True live-as-you-speak needs Sarvam's streaming WebSocket — accurate AND live — which
    // is the right next step, but a separate build. Not Web Speech, which does not work here.)
    await startBatch();
  };

  /**
   * Batch dictation — the accurate fallback. Record, then transcribe with Saarika. Used
   * when the browser has no live recogniser (Firefox), so those users still get voice.
   */
  const startBatch = async () => {
    if (!editor) return;

    // CAPTURE THE INSERTION POINT NOW, before recording.
    //
    // The bug this fixes: transcription completes seconds later, and inserting then with
    // editor.chain().focus() restores whatever selection existed before the Speak BUTTON
    // was clicked. If any text was selected — or the whole document — insertContent
    // REPLACES it, so the dictation overwrites or scrambles the user's existing content
    // instead of adding to it. Pinning the position up front, while the doc is stable
    // (the user is about to speak, not type), makes the insert deterministic.
    const insertAt = editor.state.selection.to;

    const h = await startRecording({
      onLevel: setMicLevel,
      onStateChange: (state) => {
        setVoiceState(state);
        setNotice(state === "recording" ? "listening… speak Tamil, then press stop" : "transcribing…");
      },
      onText: (text) => {
        // Insert at the captured position — NOT at the current selection. Clamp to the
        // document size in case it somehow shrank, then leave the caret after the inserted
        // text so the user can carry on.
        const pos = Math.min(insertAt, editor.state.doc.content.size);
        editor
          .chain()
          .insertContentAt(pos, text + " ")
          .setTextSelection(pos + Array.from(text + " ").length)
          .focus()
          .run();
        setVoiceState("idle");
        setNotice("");
      },
      onError: (m) => {
        recorder.current = null;
        setVoiceState("idle");
        setNotice(m);
      },
    });
    if (!h) {
      setVoiceState("idle");
      return;
    }
    recorder.current = h;
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
            data-testid="doc-input"
            type="file"
            accept=".txt,.md,.docx,.pdf"
            hidden
            onChange={(e) => {
              const f = e.target.files?.[0];
              if (f) onImport(f);
              e.target.value = ""; // so the same file can be picked twice
            }}
          />

          <button onClick={() => setScanOpen(true)}>📷 Scan</button>

          {recordingSupported() && (
            <button
              className={voiceState === "recording" ? "rec" : ""}
              onClick={toggleVoice}
              disabled={voiceState === "transcribing"}
              aria-pressed={voiceState === "recording"}
            >
              {voiceState === "recording"
                ? "⏹ Stop"
                : voiceState === "transcribing"
                  ? "… transcribing"
                  : "🎙 Speak"}
            </button>
          )}

          <button onClick={() => setWriterOpen((v) => !v)}>
            ✨ AI Writer
          </button>

          <span className="pt-sep" />

          <button onClick={() => setExportOpen(true)}>⬇ Export</button>

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

        {/* Voice recording banner with a LIVE level meter. The meter is the whole point:
            if it moves, the mic is capturing and any failure is downstream; if it stays
            flat, the user can SEE their mic is feeding silence, which no error text conveys
            as fast as a dead needle. */}
        {voiceState === "recording" && (
          <div className="pt-rec-banner" role="status">
            <span className="pt-rec-dot" aria-hidden="true" />
            <span>Recording — speak Tamil, then press Stop</span>
            <span className="pt-rec-meter" aria-hidden="true">
              {[0, 1, 2, 3, 4, 5, 6, 7, 8, 9].map((i) => (
                <i key={i} className={micLevel * 10 > i ? "on" : ""} />
              ))}
            </span>
          </div>
        )}
        {voiceState === "transcribing" && (
          <div className="pt-rec-banner busy" role="status">
            <span className="pt-spin" aria-hidden="true" />
            <span>Transcribing your Tamil…</span>
          </div>
        )}

        <div className="pt-editor-wrap">
          <EditorContent editor={editor} />

          {/* Error-type legend (§17.1). Colour is never the only signal — each
              suggestion also carries a text badge naming its type — but the legend is
              what makes the underlines readable at a glance. */}
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

        <div className="pt-legend" aria-hidden="true">
          <span><i style={{ background: "var(--err-spelling)" }} /> spelling</span>
          <span><i style={{ background: "var(--err-sandhi)" }} /> sandhi (புணர்ச்சி)</span>
          <span><i style={{ background: "var(--err-grammar)" }} /> grammar / agreement</span>
          <span><i style={{ background: "var(--err-style)" }} /> style</span>
        </div>
      </div>

      {scanOpen && <Scan onText={onScanned} onClose={() => setScanOpen(false)} />}
      {exportOpen && editor && (
        <ExportModal text={editor.getText()} onClose={() => setExportOpen(false)} />
      )}

      <aside className="pt-side">
      {/*
        * Screen-reader announcement for streamed suggestions (§17.6).
        *
        * Suggestions arrive asynchronously — Tier 1 in milliseconds, the model a
        * second later. A sighted user sees underlines appear; a screen-reader user
        * would get NOTHING, because nothing they are focused on changed. aria-live
        * "polite" announces the count without stealing focus mid-sentence.
        */}
      <div aria-live="polite" aria-atomic="true" className="sr-only">
        {status}
      </div>

      {writerOpen && editor && (
        <div className="pt-panel">
          <Writer
            text={editor.getText()}
            selection={editor.state.doc.textBetween(
              editor.state.selection.from,
              editor.state.selection.to,
              " ",
            )}
            onClose={() => setWriterOpen(false)}
            onInsert={(t) => {
              // Insert at the caret, not by replacing the document: the writer asked
              // for a continuation or a rewrite of a passage, not for their draft to
              // be thrown away.
              editor.chain().focus().insertContent(toHtml(t)).run();
              setWriterOpen(false);
            }}
          />
        </div>
      )}

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

        {/* "Apply all safe" only offers the HIGH-confidence ones. Bulk-accepting a
            0.6-confidence guess is exactly how a writer ends up with mangled Tamil
            they did not read — the whole point of the confidence gate is that some
            suggestions deserve a human look. */}
        {safeCount > 1 && (
          <button className="pt-applyall" onClick={applyAllSafe}>
            Apply {safeCount} high-confidence fixes
          </button>
        )}

        {types.length > 1 && (
          <div className="pt-filters">
            <button
              className={filter === "all" ? "on" : ""}
              onClick={() => setFilter("all")}
            >
              all {suggestions.length}
            </button>
            {types.map((t) => (
              <button
                key={t}
                className={`${t} ${filter === t ? "on" : ""}`}
                onClick={() => setFilter(t)}
              >
                {t} {suggestions.filter((s) => s.type === t).length}
              </button>
            ))}
          </div>
        )}

        {suggestions.length === 0 && checking && (
          <div className="pt-skeleton" aria-hidden="true">
            <div style={{ width: "70%" }} />
            <div style={{ width: "45%" }} />
          </div>
        )}

        {suggestions.length === 0 && !checking && (
          <p className="pt-empty">
            <span className="ta">உங்கள் தமிழ் சரியாக உள்ளது ✅</span>
            Nothing to fix. For clean Tamil — and for real words like புலி — silence is
            the correct answer.
          </p>
        )}

        {shown.map(({ s, i }) => (
          <div key={`${s.start}-${s.original}-${i}`} className="pt-card">
            <div className="pt-fix">
              <del>{s.original}</del> <span className="pt-arrow">→</span>{" "}
              <ins>{s.suggestion}</ins>
            </div>

            <div className="pt-tags">
              <span className={`pt-tag type ${s.type}`}>{s.type}</span>
              <span className="pt-tag">
                {s.source_tier === 1 ? "rules · free" : `model · tier ${s.source_tier}`}
              </span>
              {/* Confidence as a BAR, not just a number: it is a glanceable cue that
                  tells the writer how much to trust this before they read it (§17.3). */}
              <span
                className="pt-conf"
                title={`confidence ${Math.round(s.confidence * 100)}%`}
              >
                <span className="pt-conf-bar">
                  <i style={{ width: `${Math.round(s.confidence * 100)}%` }} />
                </span>
                <span className="pt-conf-num">{Math.round(s.confidence * 100)}%</span>
              </span>
            </div>

            {s.explanation && <p className="pt-why">{s.explanation}</p>}

            <div className="pt-actions">
              <button
                onClick={() => accept(i)}
                aria-label={`Accept: replace ${s.original} with ${s.suggestion}`}
              >
                Accept
              </button>
              <button
                className="ghost"
                onClick={() => reject(i)}
                aria-label={`Dismiss the suggestion for ${s.original}`}
              >
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

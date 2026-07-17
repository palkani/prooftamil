/**
 * LIVE Tamil dictation via the Web Speech API — words appear as you speak.
 *
 * This is the "types as I speak" experience. It complements the Saarika recorder in
 * lib/recorder.ts, which is more accurate but only delivers text after you stop.
 *
 *   Web Speech   -> live, word by word. Chrome / Safari. Ships audio to Google. Tamil
 *                   accuracy is decent, not great.
 *   Saarika      -> accurate Tamil, but batch (record then transcribe). Every browser.
 *
 * The editor uses this when the browser supports it and falls back to Saarika otherwise —
 * so a Firefox user still gets voice typing, just in the record-then-transcribe shape.
 *
 * INTERIM vs FINAL is the crux, and getting it wrong is how dictation ends up duplicating
 * half of every sentence. The recogniser emits a provisional transcript that it REWRITES
 * as it hears more ("நான்" → "நான் வந்" → "நான் வந்தேன்"), then marks a segment final. So:
 *   - FINAL segments are committed to the document and never touched again;
 *   - the current INTERIM is shown as a live preview and REPLACED on every event.
 * The caller is handed both, separately, and must treat them that way.
 */

export interface LiveVoiceHandle {
  stop: () => void;
}

interface SR {
  lang: string;
  continuous: boolean;
  interimResults: boolean;
  maxAlternatives: number;
  start: () => void;
  stop: () => void;
  abort: () => void;
  onresult: ((e: SREvent) => void) | null;
  onerror: ((e: { error: string }) => void) | null;
  onend: (() => void) | null;
}

interface SREvent {
  resultIndex: number;
  results: {
    length: number;
    [i: number]: { isFinal: boolean; 0: { transcript: string } };
  };
}

function ctor(): (new () => SR) | null {
  if (typeof window === "undefined") return null;
  const w = window as unknown as {
    SpeechRecognition?: new () => SR;
    webkitSpeechRecognition?: new () => SR;
  };
  return w.SpeechRecognition ?? w.webkitSpeechRecognition ?? null;
}

export const liveVoiceSupported = () => ctor() !== null;

/**
 * Start live dictation.
 *
 *   onFinal(text)   — a finalised segment; commit it to the document.
 *   onInterim(text) — the current provisional text; show it, replace it next time.
 */
export function startLiveVoice(handlers: {
  onFinal: (text: string) => void;
  onInterim: (text: string) => void;
  onError?: (msg: string) => void;
  onEnd?: () => void;
  /** Fired when the recogniser produced nothing after a few seconds — Web Speech Tamil is
   *  not working on this machine. The caller should hand off to the accurate recorder. */
  onNoResults?: () => void;
}): LiveVoiceHandle | null {
  const Ctor = ctor();
  if (!Ctor) return null;

  const rec = new Ctor();
  rec.lang = "ta-IN";
  rec.continuous = true; // keep going across pauses, not one phrase then stop
  rec.interimResults = true; // the live preview depends on these
  rec.maxAlternatives = 1;

  let stopped = false;
  let gotAnyResult = false;

  // WEB SPEECH TAMIL IS UNRELIABLE. On many Chrome builds it accepts ta-IN, shows
  // "listening", captures audio — and returns NOTHING, silently, because Google's backend
  // has poor or no Tamil for that platform. There is no error event for this; the session
  // just produces no results.
  //
  // So we arm a watchdog: if the recogniser has produced nothing a few seconds after
  // starting, we treat live dictation as unavailable HERE and hand off to the accurate
  // Saarika recorder, rather than leaving the user talking into a void.
  const NO_RESULT_MS = 6000;
  const watchdog = setTimeout(() => {
    if (!gotAnyResult && !stopped) {
      console.warn("[voice] live dictation produced no results in", NO_RESULT_MS, "ms — Web Speech Tamil is not working here; falling back");
      stopped = true;
      try {
        rec.abort();
      } catch {
        /* ignore */
      }
      handlers.onNoResults?.();
    }
  }, NO_RESULT_MS);

  rec.onresult = (e) => {
    let interim = "";
    // Walk from the first result THIS event changed. Anything final is committed exactly
    // once; anything not final is the rolling preview.
    for (let i = e.resultIndex; i < e.results.length; i++) {
      const r = e.results[i];
      const text = r[0].transcript;
      if (text.trim()) {
        gotAnyResult = true;
        clearTimeout(watchdog);
      }
      console.info("[voice] result:", JSON.stringify(text), r.isFinal ? "(final)" : "(interim)");
      if (r.isFinal) handlers.onFinal(text.trim());
      else interim += text;
    }
    handlers.onInterim(interim);
  };

  rec.onerror = (e) => {
    clearTimeout(watchdog);
    // "no-speech" and "aborted" are normal — a pause, or the user pressing stop. Surfacing
    // them as errors would make dictation look broken every time someone breathes.
    if (e.error === "no-speech" || e.error === "aborted") return;
    handlers.onError?.(
      e.error === "not-allowed"
        ? "Microphone permission was denied."
        : e.error === "language-not-supported"
          ? "Your browser does not support live Tamil dictation — using the accurate recorder instead."
          : `Voice error: ${e.error}`,
    );
  };

  rec.onend = () => {
    clearTimeout(watchdog);
    // Chrome ends the session on its own after a pause. Restart it, or "continuous"
    // dictation quietly dies mid-sentence and the user thinks the app hung.
    if (!stopped) {
      try {
        rec.start();
        return;
      } catch {
        /* already starting */
      }
    }
    handlers.onInterim(""); // clear any dangling preview
    handlers.onEnd?.();
  };

  try {
    rec.start();
  } catch {
    return null;
  }

  return {
    stop: () => {
      stopped = true;
      rec.stop();
    },
  };
}

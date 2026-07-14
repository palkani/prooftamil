/**
 * Voice typing — Web Speech API, `ta-IN` (§17.2 screen 10).
 *
 * Speaking is often the fastest way into Tamil script for someone who has no Tamil
 * keyboard AND does not think in romanized spelling. It complements the IME rather
 * than competing with it.
 *
 * Everything here runs in the browser. There is no server call, no audio upload, and no
 * cost per word.
 *
 * CAVEAT WORTH KNOWING: on Chrome, Web Speech ships the audio to Google's servers for
 * recognition — it is "in the browser" but not "on the device". Safari and Firefox
 * differ. That is the platform's behaviour, not ours, but the UI should not imply the
 * audio never leaves the machine, because for most users it does.
 */

export interface VoiceHandle {
  stop: () => void;
}

interface SpeechRecognitionLike {
  lang: string;
  continuous: boolean;
  interimResults: boolean;
  start: () => void;
  stop: () => void;
  onresult: ((e: SpeechRecognitionEventLike) => void) | null;
  onerror: ((e: { error: string }) => void) | null;
  onend: (() => void) | null;
}

interface SpeechRecognitionEventLike {
  resultIndex: number;
  results: {
    length: number;
    [i: number]: { isFinal: boolean; 0: { transcript: string } };
  };
}

function getRecognition(): SpeechRecognitionLike | null {
  if (typeof window === "undefined") return null;
  const w = window as unknown as {
    SpeechRecognition?: new () => SpeechRecognitionLike;
    webkitSpeechRecognition?: new () => SpeechRecognitionLike;
  };
  const Ctor = w.SpeechRecognition ?? w.webkitSpeechRecognition;
  return Ctor ? new Ctor() : null;
}

export const voiceSupported = () =>
  typeof window !== "undefined" &&
  Boolean(
    (window as unknown as { SpeechRecognition?: unknown; webkitSpeechRecognition?: unknown })
      .SpeechRecognition ??
      (window as unknown as { webkitSpeechRecognition?: unknown }).webkitSpeechRecognition,
  );

/**
 * Start dictation.
 *
 * `onInterim` fires with the provisional transcript as the speaker is still talking, so
 * the UI can show it live — but it must NOT be inserted into the document, because it
 * gets rewritten as the recogniser hears more. Only `onFinal` text is committed.
 * Inserting interim results is how dictation ends up duplicating half of every sentence.
 */
export function startVoice(handlers: {
  onFinal: (text: string) => void;
  onInterim?: (text: string) => void;
  onError?: (msg: string) => void;
  onEnd?: () => void;
}): VoiceHandle | null {
  const rec = getRecognition();
  if (!rec) return null;

  rec.lang = "ta-IN";
  rec.continuous = true;
  rec.interimResults = true;

  let stopped = false;

  rec.onresult = (e) => {
    let interim = "";
    for (let i = e.resultIndex; i < e.results.length; i++) {
      const r = e.results[i];
      const text = r[0].transcript;
      if (r.isFinal) handlers.onFinal(text.trim());
      else interim += text;
    }
    if (interim) handlers.onInterim?.(interim);
  };

  rec.onerror = (e) => {
    // "no-speech" and "aborted" are normal: the user paused, or pressed stop. Reporting
    // them as errors would make dictation look broken every time someone takes a breath.
    if (e.error === "no-speech" || e.error === "aborted") return;
    handlers.onError?.(
      e.error === "not-allowed"
        ? "Microphone permission was denied."
        : `Voice error: ${e.error}`,
    );
  };

  rec.onend = () => {
    // Chrome ends the session on its own after a pause. Restart it, or "continuous"
    // dictation silently dies mid-sentence and the user thinks the app hung.
    if (!stopped) {
      try {
        rec.start();
        return;
      } catch {
        /* already starting */
      }
    }
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

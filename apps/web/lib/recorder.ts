/**
 * Microphone recorder — the universal voice-typing path.
 *
 * Records audio with MediaRecorder (supported in every current browser, unlike the Web
 * Speech API which is Chrome/Safari-only) and uploads it to /api/v1/transcribe, where
 * Sarvam's Saarika model does the Tamil recognition — far more accurate than the
 * browser's general-purpose recogniser, because Saarika is built for Indic languages.
 *
 * The flow the user experiences:
 *   press mic -> speak -> press stop -> a moment later, their Tamil appears.
 *
 * The tradeoff versus Web Speech is that this is not live — the transcript arrives after
 * they stop, not word by word. For dictation that is an acceptable, even preferable,
 * shape: fewer half-formed corrections flickering as the recogniser changes its mind.
 */

import { API_BASE } from "./api";

export interface Recorder {
  stop: () => void;
}

/**
 * Turn a getUserMedia failure into an accurate, actionable message.
 *
 * The naive mapping (NotFoundError -> "no microphone") is WRONG on macOS Chrome, and it
 * misled a user who had two mics connected. When the browser lacks OS-level microphone
 * permission, Chrome cannot even ENUMERATE the devices, so it throws NotFoundError — the
 * mic is there, the browser just is not allowed to see it.
 *
 * So instead of trusting the error name, we ask the browser to list its devices. That
 * distinguishes the two cases the error name conflates:
 *   - audio inputs exist but their labels are blank  -> permission is blocking access
 *   - no audio inputs at all                         -> genuinely no device (or a
 *     privacy browser hiding them)
 */
async function diagnose(err: DOMException): Promise<string> {
  let audioInputs = 0;
  let hasLabels = false;
  try {
    const devices = await navigator.mediaDevices.enumerateDevices();
    const inputs = devices.filter((d) => d.kind === "audioinput");
    audioInputs = inputs.length;
    // A non-empty label is only exposed AFTER permission is granted. Blank labels on a
    // present device are the tell-tale of a permission block.
    hasLabels = inputs.some((d) => d.label !== "");
  } catch {
    /* enumerateDevices can itself be blocked; fall through to the error-name mapping */
  }

  // A microphone is present but blocked — the case that misled the earlier message. This
  // is by far the most common on macOS: the browser has to be enabled in
  // System Settings › Privacy & Security › Microphone, AND fully quit and reopened, even
  // after the in-page prompt is accepted.
  if (audioInputs > 0 && !hasLabels) {
    return (
      "Your mic is connected but this browser is not allowed to use it. On a Mac, open " +
      "System Settings → Privacy & Security → Microphone, turn the browser on, then quit " +
      "and reopen it."
    );
  }

  switch (err.name) {
    case "NotAllowedError":
    case "SecurityError":
      return (
        "Microphone access was denied. Click the mic/lock icon in the address bar and allow " +
        "it for this site — and check the browser is enabled in your system microphone settings."
      );
    case "NotReadableError":
    case "TrackStartError" as string:
      return "The microphone is in use by another app (Zoom, Teams…). Close it and try again.";
    case "NotFoundError":
    case "DevicesNotFoundError" as string:
      // Reached only when enumerateDevices also found nothing — so it really is absent, or
      // a privacy browser (Brave shields, Firefox resist-fingerprinting) is hiding it.
      return (
        "No microphone is visible to the browser. If you have one connected, a privacy " +
        "setting or extension may be hiding it — try disabling fingerprint/shield protection " +
        "for this site."
      );
    default:
      return `Could not open the microphone (${err.name || "unknown error"}).`;
  }
}

/** MediaRecorder exists in all current browsers, but old ones and locked-down WebViews
 *  may not have it — check before offering the feature. */
export const recordingSupported = () =>
  typeof navigator !== "undefined" &&
  !!navigator.mediaDevices?.getUserMedia &&
  typeof MediaRecorder !== "undefined";

/**
 * Start recording. Returns a handle whose stop() ends the recording, uploads it, and
 * resolves the transcript through the callbacks.
 *
 * Returns null (and calls onError) when the mic cannot be opened — denied permission, no
 * device, or an insecure origin. getUserMedia requires HTTPS or localhost, so this simply
 * will not work over plain http:// on a LAN address, which is a browser rule, not ours.
 */
export async function startRecording(handlers: {
  onText: (text: string) => void;
  onStateChange?: (state: "recording" | "transcribing") => void;
  onError?: (msg: string) => void;
}): Promise<Recorder | null> {
  let stream: MediaStream;
  try {
    stream = await navigator.mediaDevices.getUserMedia({ audio: true });
  } catch (e) {
    const err = e as DOMException;
    console.warn("getUserMedia failed:", err.name, err.message);
    handlers.onError?.(await diagnose(err));
    return null;
  }

  // Opus in a WebM/OGG container is what browsers produce and Saarika accepts. Pick
  // whichever the browser actually supports rather than assuming.
  const mime =
    ["audio/webm;codecs=opus", "audio/webm", "audio/ogg;codecs=opus", "audio/mp4"].find(
      (m) => MediaRecorder.isTypeSupported(m),
    ) ?? "";

  const rec = new MediaRecorder(stream, mime ? { mimeType: mime } : undefined);
  const chunks: Blob[] = [];
  let cancelled = false;

  rec.ondataavailable = (e) => {
    if (e.data.size > 0) chunks.push(e.data);
  };

  rec.onstop = async () => {
    // Always release the mic, or the browser leaves the recording indicator on and holds
    // the device — which users read as the app spying on them.
    stream.getTracks().forEach((t) => t.stop());
    if (cancelled) return;

    const blob = new Blob(chunks, { type: rec.mimeType || "audio/webm" });
    if (blob.size < 1200) {
      // Practically silence — a tap rather than speech. Say nothing rather than sending an
      // empty clip and getting an empty transcript back.
      handlers.onError?.("That was too short — hold the mic and speak.");
      return;
    }

    handlers.onStateChange?.("transcribing");
    try {
      const fd = new FormData();
      fd.append("audio", blob, "voice.webm");
      const res = await fetch(`${API_BASE}/api/v1/transcribe`, { method: "POST", body: fd });
      const d = await res.json();
      if (!res.ok) throw new Error(d.error ?? `HTTP ${res.status}`);
      if (d.text?.trim()) handlers.onText(d.text.trim());
      else handlers.onError?.("Nothing was recognised — try again.");
    } catch (e) {
      handlers.onError?.((e as Error).message);
    }
  };

  rec.start();
  handlers.onStateChange?.("recording");

  return {
    stop: () => {
      if (rec.state !== "inactive") rec.stop();
    },
  };
}

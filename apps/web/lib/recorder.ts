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
    // Map the DOMException to something the user can ACT on. The old code collapsed
    // everything except NotAllowedError into "Could not open the microphone", which told
    // the user nothing about how to fix it — and the failures need completely different
    // fixes (grant permission vs plug in a mic vs quit the app holding it vs use HTTPS).
    const err = e as DOMException;
    let msg: string;
    switch (err.name) {
      case "NotAllowedError":
      case "SecurityError":
        // Browser permission OR the OS-level mic permission for the browser. On macOS,
        // System Settings › Privacy & Security › Microphone must have the browser ticked,
        // even after the site prompt is accepted.
        msg =
          "Microphone blocked. Allow mic access for this site, and check that your browser " +
          "has microphone permission in your system settings.";
        break;
      case "NotFoundError":
      case "DevicesNotFoundError" as string:
        msg = "No microphone was found. Plug one in and try again.";
        break;
      case "NotReadableError":
      case "TrackStartError" as string:
        // Another app (Zoom, Teams, a recorder) holds the device, or the OS refused it.
        msg = "The microphone is in use by another app. Close it and try again.";
        break;
      default:
        // Surface the real name so a report is actionable instead of a shrug.
        msg = `Could not open the microphone (${err.name || "unknown error"}).`;
    }
    // Also log the full error for diagnosis — the message above is for the user, this is
    // for whoever debugs it.
    console.warn("getUserMedia failed:", err.name, err.message);
    handlers.onError?.(msg);
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

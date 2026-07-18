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
  /** 0-1 live input level, ~20x/sec while recording. Drives the on-screen meter so the
   *  user can SEE whether the mic is capturing — a flat meter is silence, visibly. */
  onLevel?: (level: number) => void;
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

  // --- live level meter ---------------------------------------------------
  //
  // The single most useful thing for "I speak but nothing happens": a meter the user can
  // watch. If it moves, the mic works and the problem is elsewhere; if it stays flat, the
  // OS is feeding silence (muted, or the wrong input is the default) — which no error
  // message conveys as immediately as a dead needle.
  let audioCtx: AudioContext | null = null;
  let rafId = 0;
  let peak = 0; // was ANY sound captured across the whole recording?
  try {
    audioCtx = new AudioContext();
    // RESUME IT. Chrome creates an AudioContext SUSPENDED, and a suspended context's
    // analyser reads pure silence (a flat 128) — so the meter showed dead even when the
    // mic was capturing fine, and the "no sound" check then rejected a good recording.
    // The click that got us here is a user gesture, so resume() is allowed.
    if (audioCtx.state === "suspended") await audioCtx.resume().catch(() => {});
    const src = audioCtx.createMediaStreamSource(stream);
    const analyser = audioCtx.createAnalyser();
    analyser.fftSize = 512;
    src.connect(analyser);
    const buf = new Uint8Array(analyser.frequencyBinCount);

    const tick = () => {
      analyser.getByteTimeDomainData(buf);
      // RMS deviation from the 128 midpoint → a 0-1 loudness.
      let sum = 0;
      for (const v of buf) {
        const d = (v - 128) / 128;
        sum += d * d;
      }
      const level = Math.min(1, Math.sqrt(sum / buf.length) * 4);
      peak = Math.max(peak, level);
      handlers.onLevel?.(level);
      rafId = requestAnimationFrame(tick);
    };
    tick();
  } catch {
    /* the meter is a nicety; recording works without it */
  }

  const stopMeter = () => {
    cancelAnimationFrame(rafId);
    audioCtx?.close().catch(() => {});
    handlers.onLevel?.(0);
  };

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
    stopMeter();
    // Always release the mic, or the browser leaves the recording indicator on and holds
    // the device — which users read as the app spying on them.
    stream.getTracks().forEach((t) => t.stop());
    if (cancelled) return;

    const type = rec.mimeType || mime || "audio/webm";
    const blob = new Blob(chunks, { type });

    // One log line that turns "no text appeared" from a mystery into a diagnosis: whether
    // the mic captured anything (size + peak level) and in what format.
    console.info(`[voice] recorded ${blob.size} bytes as ${type}, peak level ${peak.toFixed(3)}`);

    // The meter is a UI aid, NOT a gate. It runs off an AudioContext that the browser can
    // leave suspended or throttle, so a flat peak does NOT prove silence — the MediaRecorder
    // captures audio on a wholly separate path. Rejecting a recording because the meter
    // stayed flat threw away perfectly good speech. So: log a low peak as a hint, but let
    // the actual audio through and let Saarika be the judge of whether there were words.
    if (peak < 0.02) {
      console.warn("[voice] meter peak stayed low (", peak.toFixed(3), ") — uploading anyway; the meter is not authoritative");
    }

    if (blob.size < 1200) {
      handlers.onError?.("The recording was too short — hold the mic button and speak.");
      return;
    }

    handlers.onStateChange?.("transcribing");
    try {
      // Name the file to MATCH the recorded format. Safari records audio/mp4; sending that
      // as "voice.webm" invites the server or Saarika to mis-detect the container by
      // extension and reject it. Extension follows the real mime type.
      const ext = type.includes("mp4") ? "mp4" : type.includes("ogg") ? "ogg" : "webm";
      const fd = new FormData();
      fd.append("audio", blob, `voice.${ext}`);

      const res = await fetch(`${API_BASE}/api/v1/transcribe`, { method: "POST", body: fd });
      const d = await res.json().catch(() => ({}));
      console.info(`[voice] transcribe -> HTTP ${res.status}`, d);

      if (!res.ok) throw new Error(d.error ?? `HTTP ${res.status}`);
      if (d.text?.trim()) handlers.onText(d.text.trim());
      else handlers.onError?.("Nothing was recognised — try speaking a little longer.");
    } catch (e) {
      console.warn("[voice] transcribe failed:", e);
      handlers.onError?.((e as Error).message);
    }
  };

  rec.onerror = () => {
    stopMeter();
    stream.getTracks().forEach((t) => t.stop());
    handlers.onError?.("Recording failed. Try again.");
  };

  // start(1000): emit a data chunk every second. Without a timeslice, some browsers only
  // fire ondataavailable at stop — and a bug there (or a very short recording) can leave
  // `chunks` empty and produce a zero-byte blob. A periodic chunk makes the capture
  // observable and robust.
  rec.start(1000);
  handlers.onStateChange?.("recording");

  return {
    stop: () => {
      if (rec.state !== "inactive") rec.stop();
    },
  };
}

/**
 * Client-side OCR — tesseract.js, in the browser (§15.1, and the fix for RISK R6).
 *
 * WHY THIS EXISTS, AND WHY IT IS THE DEFAULT FOR PRINTED TEXT.
 *
 * The server path uses a vision model, and a vision model SILENTLY CORRECTS THE TEXT.
 * Measured: a page reading வனிகர்கள் (a real misspelling) came back transcribed as
 * வணிகர்கள் — the corrected form — even though the prompt explicitly forbids improving
 * the text. It does this reproducibly, and prompting does not stop it.
 *
 * That is not a harmless convenience in a proofreading product:
 *   - the writer never learns they made the mistake, and cannot check the transcript
 *     against their own page;
 *   - and a rare-but-correct word or a PROPER NOUN can be "fixed" the same way. Someone's
 *     name could be quietly changed in the transcript of their own letter, with nothing
 *     to surface it.
 *
 * Tesseract cannot do this. It has no language model — it recognises glyphs. It is
 * FAITHFUL BY CONSTRUCTION: it will happily transcribe a misspelling as a misspelling,
 * which is exactly what a proofreader needs, because then our cascade can offer the fix
 * and the writer can SEE it and accept it.
 *
 * It is also private (the image never leaves the device) and free (no model call). The
 * cost is accuracy on handwriting, where Tesseract is genuinely poor — which is the one
 * case the vision path is worth its trade-off for.
 */

export interface ClientOCRResult {
  text: string;
  /** 0-1. Tesseract's own per-page confidence. */
  confidence: number;
  tookMs: number;
}

/**
 * Preprocess before recognition (§15.2).
 *
 * Tesseract is markedly better on clean, high-contrast, upright text. A phone photo is
 * none of those things. Greyscale + contrast stretch is the cheap 80% of the win; the
 * plan's full OpenCV pipeline (deskew, adaptive binarisation) belongs on the server
 * path, where OpenCV actually exists.
 *
 * It also UPSCALES small images: Tesseract wants roughly 300 DPI and degrades badly
 * below it, so a small screenshot recognises far worse than the same text larger.
 */
async function preprocess(file: File): Promise<Blob> {
  const bitmap = await createImageBitmap(file);

  // Aim for ~1800px on the long edge. Bigger than that costs time for no accuracy.
  const target = 1800;
  const scale = Math.min(
    Math.max(target / Math.max(bitmap.width, bitmap.height), 1),
    3,
  );

  const canvas = document.createElement("canvas");
  canvas.width = Math.round(bitmap.width * scale);
  canvas.height = Math.round(bitmap.height * scale);

  const ctx = canvas.getContext("2d");
  if (!ctx) return file;

  ctx.imageSmoothingQuality = "high";
  ctx.drawImage(bitmap, 0, 0, canvas.width, canvas.height);

  const img = ctx.getImageData(0, 0, canvas.width, canvas.height);
  const d = img.data;

  // Greyscale, then a contrast stretch around the midpoint. Tamil's thin strokes and
  // vowel signs are the first thing lost to a washed-out photo.
  for (let i = 0; i < d.length; i += 4) {
    const grey = 0.299 * d[i] + 0.587 * d[i + 1] + 0.114 * d[i + 2];
    const boosted = Math.min(255, Math.max(0, (grey - 128) * 1.4 + 128));
    d[i] = d[i + 1] = d[i + 2] = boosted;
  }
  ctx.putImageData(img, 0, 0);

  return new Promise((resolve) =>
    canvas.toBlob((b) => resolve(b ?? file), "image/png"),
  );
}

/**
 * Strip the invisible characters Tesseract sprinkles into Tamil output.
 *
 * Observed live: it emits ZERO-WIDTH NON-JOINERS after consonant clusters —
 * "அவர்கள்‌" instead of "அவர்கள்". They render identically, so nothing looks wrong,
 * and they are catastrophic anyway: they are real codepoints, so they shift every rune
 * offset the cascade computes, break dictionary lookups (the word is no longer equal to
 * itself), and travel silently into exports.
 *
 * An invisible character that corrupts offsets is the worst kind of bug — there is
 * nothing to see.
 */
function clean(text: string): string {
  return text
    .replace(/[​-‍﻿]/g, "") // ZWSP, ZWNJ, ZWJ, BOM
    .replace(/[ \t]+\n/g, "\n")
    .replace(/\n{3,}/g, "\n\n")
    .normalize("NFC")
    .trim();
}

/**
 * Recognise Tamil text in the browser.
 *
 * The Tamil traineddata (~15 MB) downloads on first use and is then cached by the
 * browser, so the cost is paid once. It is lazy-loaded — nobody who never scans an
 * image should pay for it.
 */
export async function readImageLocally(
  file: File,
  onProgress?: (pct: number) => void,
): Promise<ClientOCRResult> {
  const started = performance.now();

  // Dynamic import: tesseract.js and its WASM are large, and the editor must not carry
  // them in its initial bundle.
  const { createWorker } = await import("tesseract.js");

  const processed = await preprocess(file);

  const worker = await createWorker("tam", 1, {
    logger: (m: { status: string; progress: number }) => {
      if (m.status === "recognizing text" && onProgress) {
        onProgress(Math.round(m.progress * 100));
      }
    },
  });

  try {
    const { data } = await worker.recognize(processed);
    return {
      text: clean(data.text ?? ""),
      confidence: (data.confidence ?? 0) / 100,
      tookMs: Math.round(performance.now() - started),
    };
  } finally {
    // Always terminate. A leaked worker keeps its WASM heap (tens of MB) alive for the
    // life of the tab.
    await worker.terminate();
  }
}

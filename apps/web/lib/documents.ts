/**
 * Import and export (plan §7.4).
 *
 * Everything here runs in the BROWSER. The plan specifies a server-side export that
 * stores to R2 and returns a signed URL, gated on a Pro subscription — but there is
 * no auth, no billing and no R2 yet (Phase 6, §1 accounts). Client-side export works
 * today, needs no infrastructure, and never uploads the user's private writing
 * anywhere. When the Pro gate lands, the server path becomes the *additional*
 * capability (shareable links), not a replacement.
 */

import {
  AlignmentType,
  Document,
  Packer,
  Paragraph,
  TextRun,
} from "docx";

/* ------------------------------------------------------------------ import */

export interface ImportResult {
  text: string;
  /** What we could not preserve, so the UI can say so rather than silently lose it. */
  warnings: string[];
}

export async function importFile(file: File): Promise<ImportResult> {
  const name = file.name.toLowerCase();

  if (name.endsWith(".txt") || name.endsWith(".md")) {
    return { text: await file.text(), warnings: [] };
  }

  if (name.endsWith(".docx")) {
    const mammoth = await import("mammoth");
    const { value, messages } = await mammoth.extractRawText({
      arrayBuffer: await file.arrayBuffer(),
    });
    return {
      text: value,
      // Formatting is dropped on purpose: this is a proofreader, and it works on
      // text. Say so rather than pretending the bold survived.
      warnings: messages.length ? ["Formatting was not preserved — text only."] : [],
    };
  }

  if (name.endsWith(".pdf")) {
    const pdfjs = await import("pdfjs-dist");
    // pdf.js runs its parser in a worker. Point it at the copy in node_modules,
    // which Next serves; without this it silently falls back to a CDN that the CSP
    // would block.
    pdfjs.GlobalWorkerOptions.workerSrc = new URL(
      "pdfjs-dist/build/pdf.worker.min.mjs",
      import.meta.url,
    ).toString();

    const doc = await pdfjs.getDocument({ data: await file.arrayBuffer() }).promise;
    const pages: string[] = [];

    for (let i = 1; i <= doc.numPages; i++) {
      const page = await doc.getPage(i);
      const content = await page.getTextContent();
      pages.push(
        content.items
          .map((it) => ("str" in it ? it.str : ""))
          .join(" ")
          .replace(/\s+/g, " ")
          .trim(),
      );
    }

    const text = pages.join("\n\n");
    const warnings: string[] = [];
    if (!text.trim()) {
      // A scanned PDF is images, not text. Extracting nothing and saying nothing
      // would look like the file was empty.
      warnings.push(
        "No text found. This looks like a scanned PDF — it needs OCR, which is not wired up yet.",
      );
    }
    return { text, warnings };
  }

  throw new Error(`Unsupported file type: ${file.name}`);
}

/* ------------------------------------------------------------------ export */

function download(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  a.click();
  URL.revokeObjectURL(url);
}

export function exportTxt(text: string, filename = "prooftamil.txt") {
  download(new Blob([text], { type: "text/plain;charset=utf-8" }), filename);
}

export async function exportDocx(text: string, filename = "prooftamil.docx") {
  const paragraphs = text.split("\n").map(
    (line) =>
      new Paragraph({
        alignment: AlignmentType.LEFT,
        children: [
          new TextRun({
            text: line,
            // Word will not render Tamil in its default Latin font — it substitutes
            // and the result is boxes or mangled glyphs. Naming a Tamil-capable font
            // is what makes the exported file actually readable. Latha ships with
            // Windows; Nirmala UI is the modern default.
            font: { name: "Nirmala UI", eastAsia: "Latha" },
            size: 24, // half-points, so 12pt
          }),
        ],
      }),
  );

  const doc = new Document({ sections: [{ children: paragraphs }] });
  download(await Packer.toBlob(doc), filename);
}

/**
 * PDF export, via the browser's own print dialog ("Save as PDF").
 *
 * NOT a PDF library, and that is a deliberate call.
 *
 * Generating a PDF client-side (jsPDF, pdf-lib) means embedding a font, because the
 * PDF standard's built-in fonts are Latin-only — Tamil would come out as blank boxes.
 * A Tamil font with full glyph coverage is 200-400 KB, it would have to ship to every
 * visitor whether or not they ever export, and getting Tamil's stacked vowel signs to
 * shape correctly is a hard typography problem that jsPDF does not solve.
 *
 * The browser already has a font that renders Tamil correctly, and its print engine
 * already shapes complex scripts properly. Using it costs zero bytes and produces a
 * better document. The tradeoff is a print dialog instead of a direct download —
 * worth it.
 *
 * The server-side path (Phase 6, Pro-gated, stored in R2) is where a real generated
 * PDF belongs, because there the font lives on the server and is downloaded by nobody.
 */
export function exportPdf(text: string, title = "ProofTamil") {
  const w = window.open("", "_blank");
  if (!w) {
    throw new Error("Popup blocked — allow popups to export as PDF.");
  }

  const escaped = text
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;");

  w.document.write(`<!doctype html>
<html lang="ta"><head><meta charset="utf-8"><title>${title}</title>
<style>
  @page { margin: 2.5cm; }
  body {
    /* System Tamil fonts, in order of quality. The browser shapes these correctly;
       a PDF library would not. */
    font-family: "Noto Sans Tamil", "Nirmala UI", "Latha", "Tamil Sangam MN", serif;
    font-size: 12pt; line-height: 1.9; white-space: pre-wrap; color: #000;
  }
</style></head>
<body>${escaped}</body></html>`);
  w.document.close();
  w.focus();
  // Give the font a moment to load, or the first page prints in a fallback face.
  setTimeout(() => w.print(), 350);
}

import Link from "next/link";

import Related from "@/components/Related";

export const metadata = {
  title: "Handwriting OCR — Convert Handwritten Tamil to Text | ProofTamil",
  description:
    "Turn photos of handwritten Tamil notes into editable text with AI. Snap a page, get clean Tamil you can proofread and export.",
};

export default function HandwritingOcrPage() {
  return (
    <>
      <div className="pt-page">
        <div className="pt-page-head">
          <span className="pt-eyebrow">Handwriting OCR</span>
          <h1>
            Convert Handwritten <span className="grad">Tamil to Text</span>
          </h1>
          <p>
            Photograph a page of handwritten Tamil and get clean, editable text in seconds.
            Powered by AI trained on Tamil script — then proofread by the same engine that
            checks everything else you write.
          </p>
        </div>

        <div className="pt-tool-card">
          <div className="pt-dropzone" aria-hidden="false">
            <div className="ico" aria-hidden="true">
              ✍️
            </div>
            <strong>Open the scanner to upload handwriting</strong>
            <span>Snap or upload a photo · JPG, PNG, PDF · handwriting uses AI</span>
          </div>

          <div style={{ textAlign: "center", marginTop: "1.4rem" }}>
            <Link className="btn-hero" href="/write?scan=handwriting">
              ✍️ Open the scanner →
            </Link>
          </div>

          <p className="pt-plan-note" style={{ marginTop: "1rem" }}>
            Handwriting recognition runs on an AI model and is part of Pro. Printed text is
            free and reads entirely on your device —{" "}
            <Link href="/ocr" style={{ color: "var(--brand)", fontWeight: 600 }}>
              use the Tamil OCR tool
            </Link>
            .
          </p>
        </div>

        <div className="pt-tips">
          <h3>💡 Tips for Best Results</h3>
          <ul>
            <li>Write on unlined or lightly-lined paper for cleaner recognition</li>
            <li>Photograph straight-on in good, even light — avoid shadows</li>
            <li>Keep the page flat; curled corners distort letters</li>
            <li>Neater, well-spaced writing gives noticeably better results</li>
          </ul>
        </div>
      </div>

      <Related
        items={[
          {
            href: "/ocr",
            title: "Tamil OCR Online Free — Extract Tamil Text From Any Image or PDF",
          },
          {
            href: "/blog",
            title: "Handwritten Tamil to Text — Convert Tamil Notes Online Free",
          },
        ]}
      />
    </>
  );
}

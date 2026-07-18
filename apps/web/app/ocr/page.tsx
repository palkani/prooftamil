import OcrTool from "@/components/OcrTool";
import Related from "@/components/Related";

export const metadata = {
  title: "Tamil OCR — Extract Tamil Text from Images & PDFs | ProofTamil",
  description:
    "Upload images or PDFs containing Tamil text and get editable text instantly. On-device OCR with Tamil language support.",
};

export default function OcrPage() {
  return (
    <>
      <div className="pt-page">
        <div className="pt-page-head">
          <span className="pt-eyebrow">Tamil OCR Tool</span>
          <h1>
            Extract Tamil Text from <span className="grad">Images &amp; PDFs</span>
          </h1>
          <p>
            Upload images or PDFs containing Tamil text and get editable text instantly.
            Printed text is read <strong>on your device</strong> — the image never leaves
            your machine.
          </p>
        </div>

        <OcrTool />

        <div className="pt-tips">
          <h3>💡 Tips for Best Results</h3>
          <ul>
            <li>Use high-resolution images (300+ DPI) for better accuracy</li>
            <li>Ensure text is clear and not rotated</li>
            <li>Black text on a white background works best</li>
            <li>For Tamil text, select &quot;English + Tamil&quot; or &quot;Tamil Only&quot;</li>
          </ul>
        </div>
      </div>

      <Related
        items={[
          {
            href: "/blog",
            title: "Tamil OCR Online Free — Extract Tamil Text From Any Image or PDF",
          },
          {
            href: "/handwriting-ocr",
            title: "Handwritten Tamil to Text — Convert Tamil Notes Online Free",
          },
        ]}
      />
    </>
  );
}

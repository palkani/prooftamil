import Link from "next/link";

export const metadata = {
  title: "How to Use ProofTamil — Tamil Typing, Proofreading, OCR & Voice",
  description:
    "A quick guide to writing Tamil with ProofTamil: type in English letters, proofread as you write, scan printed and handwritten Tamil, and dictate by voice.",
};

const STEPS = [
  {
    n: 1,
    title: "Type in English letters (Tanglish)",
    body: "Type vanakkam and pick வணக்கம் from the dropdown — no Tamil keyboard needed. A 349,000-word dictionary ranks suggestions by how common each word really is.",
  },
  {
    n: 2,
    title: "Get proofread as you write",
    body: "Spelling, sandhi (புணர்ச்சி) and grammar are checked live. Wavy underlines mark issues; click one to see the fix and why. The engine stays silent when it is not sure.",
  },
  {
    n: 3,
    title: "Scan printed or handwritten Tamil",
    body: "Photograph a page to get editable Tamil. Printed text is read privately on your device; handwriting uses AI. Both drop straight into the editor.",
  },
  {
    n: 4,
    title: "Speak it — voice typing",
    body: "Press the mic and dictate. Your speech is transcribed to Tamil and inserted where your cursor is. Great for long passages and accessibility.",
  },
  {
    n: 5,
    title: "Draft with the AI writer, then export",
    body: "Rewrite a passage, continue from where you stopped, or generate from a template — every AI draft is proofread by the same engine. Export to Word, PDF or text.",
  },
];

export default function HowToUsePage() {
  return (
    <div className="pt-page">
      <div className="pt-page-head">
        <span className="pt-eyebrow">How to Use</span>
        <h1>
          Write better Tamil in <span className="grad">five steps</span>
        </h1>
        <p>
          ProofTamil is free and needs no download. Here is everything it does, in the order
          you will probably use it.
        </p>
      </div>

      <div className="pt-steps">
        {STEPS.map((s) => (
          <div key={s.n} className="pt-step">
            <div className="pt-step-num">{s.n}</div>
            <div>
              <h3>{s.title}</h3>
              <p>{s.body}</p>
            </div>
          </div>
        ))}
      </div>

      <div style={{ textAlign: "center", marginTop: "2rem" }}>
        <Link className="btn-hero" href="/write">
          Open the editor →
        </Link>
      </div>
    </div>
  );
}

import Link from "next/link";

import Related from "@/components/Related";

export const metadata = {
  title: "AI Content Writer — Blogs, Essays & Articles in Tamil & English | ProofTamil",
  description:
    "Generate blogs, essays, articles and stories in Tamil, English, or both — written by AI in seconds and proofread by ProofTamil.",
};

/**
 * The AI Content Writer landing (production's /tools/ai-content-writer): a serif display
 * headline over a sign-up gate. The generator itself lives in the editor's Writer panel;
 * this page sells it and routes accounts (Phase 6) to sign-up.
 */
export default function AiWriterPage() {
  return (
    <>
      <div className="pt-page" style={{ textAlign: "center" }}>
        <div style={{ fontSize: "2.4rem" }} aria-hidden="true">
          ✍️
        </div>
        <h1 className="pt-serif-en" style={{ fontSize: "clamp(2.2rem,5vw,3.2rem)", margin: ".6rem 0 .4rem" }}>
          <span className="grad">AI Content Writer</span>
        </h1>
        <p style={{ color: "var(--muted)", fontSize: "1.1rem", marginBottom: "2rem" }}>
          Create blogs, essays &amp; articles in Tamil &amp; English
        </p>

        <div className="pt-tool-card" style={{ textAlign: "left", maxWidth: 620, margin: "0 auto" }}>
          <span className="pt-eyebrow" style={{ display: "inline-flex" }}>
            🎁 Free forever · 2 generations every week
          </span>
          <h2 className="pt-serif-en" style={{ fontSize: "1.7rem", margin: ".8rem 0 .5rem" }}>
            Sign up free to start generating
          </h2>
          <p style={{ color: "var(--muted)", lineHeight: 1.7, margin: "0 0 1.1rem" }}>
            Blogs, essays, articles and stories in Tamil, English, or both — written by AI in
            seconds. Free accounts get 2 pieces every week. No credit card, no trial timer.
          </p>

          <ul className="pt-tips-inline">
            <li>✓ Tamil, English &amp; bilingual output</li>
            <li>✓ Blog post, essay, article, story — 4 formats</li>
            <li>✓ Choose tone: professional, casual, academic, creative, persuasive</li>
            <li>✓ Save drafts + revise later</li>
          </ul>

          <div className="pt-cta-row" style={{ justifyContent: "flex-start", marginTop: "1.3rem" }}>
            <Link className="btn-hero" href="/signup">
              ✨ Sign up free
            </Link>
            <Link className="btn-hero-outline" href="/login">
              I already have an account
            </Link>
          </div>

          <p className="pt-plan-note" style={{ textAlign: "left", marginTop: "1rem" }}>
            Need more than 2 per week?{" "}
            <Link href="/pricing" style={{ color: "var(--brand)", fontWeight: 600 }}>
              See Pro plans
            </Link>{" "}
            for unlimited generations.
          </p>
        </div>

        <p style={{ color: "var(--muted)", fontSize: ".85rem", marginTop: "1.5rem", fontStyle: "italic" }}>
          ✨ Powered by AI — high-quality content generation. Generated content may require
          proofreading and fact-checking.
        </p>
      </div>

      <Related
        items={[
          { href: "/blog", title: "10 Common Tamil Grammar Mistakes (And How to Fix Them)" },
          { href: "/blog", title: "Tamil Grammar Checker Online Free — Complete 2026 Guide" },
        ]}
      />
    </>
  );
}

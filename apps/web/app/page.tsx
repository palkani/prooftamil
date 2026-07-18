import Demo from "@/components/Demo";

/**
 * The landing page (§17.2 screen 1), rebuilt to match the production hero: an aurora wash,
 * the "Write Tamil with Confidence." gradient headline, the "AI proofreads, OCR digitises,
 * you shine." promise, and dual CTAs — over a LIVE editor the visitor can type into.
 *
 * It leads with the WORKING PRODUCT, not a pitch. Someone who writes Tamil can tell in five
 * seconds whether a Tamil proofreader is any good, so the fastest way to earn trust is to
 * let them try it, on their own text, before anything asks them for anything.
 */
export default function Home() {
  return (
    <>
      <div className="pt-hero-wrap">
        <header className="pt-hero">
          <span className="pt-eyebrow">✦ TAMIL PROOFREADING · OCR · AI</span>

          <h1>
            Write Tamil <span className="grad">with Confidence.</span>
            <span className="ta">AI proofreads, OCR digitises, you shine.</span>
          </h1>

          <p className="pt-sub">
            Catch grammar errors in <strong>2 seconds</strong>. Convert handwritten Tamil
            notes to text with one photo. Type in English letters and get Tamil — all free,
            no app download needed.
          </p>

          <div className="pt-cta-row">
            <a className="btn-hero" href="/write">
              Start Free — No Credit Card →
            </a>
            <a className="btn-hero-outline" href="/ocr">
              📷 Try Tamil OCR Free
            </a>
          </div>

          <div className="pt-trust">
            <div>
              <span className="ico" aria-hidden="true">📖</span>
              <span>
                <strong>349,633</strong>-word dictionary
              </span>
            </div>
            <div>
              <span className="ico" aria-hidden="true">⚡</span>
              <span>
                <strong>Instant</strong> corrections, offline
              </span>
            </div>
            <div>
              <span className="ico" aria-hidden="true">🔒</span>
              <span>
                <strong>Nothing</strong> is stored
              </span>
            </div>
          </div>
        </header>
      </div>

      <main className="pt-landing">
        <Demo />

        <section className="pt-features">
          <div>
            <h3>⌨️ Type in English letters</h3>
            <p>
              <code>vanakkam</code> → வணக்கம். A 349,000-word Tamil dictionary, ranked by
              how common each word really is. Works offline. Costs nothing.
            </p>
          </div>
          <div>
            <h3>✅ Corrections you can trust</h3>
            <p>
              Spelling, sandhi (புணர்ச்சி) and grammar. It stays silent when it is not sure —
              it will never “fix” புலி into புளி just because it can.
            </p>
          </div>
          <div>
            <h3>📷 Scan and ✨ write</h3>
            <p>
              Photograph a page and get editable Tamil. Rewrite, draft from a template, or
              continue writing — every AI draft is proofread by the same engine.
            </p>
          </div>
        </section>
      </main>
    </>
  );
}

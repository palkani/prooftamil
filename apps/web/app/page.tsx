import Demo from "@/components/Demo";
import Nav from "@/components/Nav";

/**
 * The landing page (§17.2 screen 1).
 *
 * It leads with the WORKING PRODUCT, not a pitch. Someone who writes Tamil can tell in
 * five seconds whether a Tamil proofreader is any good — so the fastest way to earn their
 * trust is to let them try it, on their own text, before anything asks them for anything.
 *
 * The hero follows the production design language (aurora wash, gradient headline, Tamil
 * subhead, dual CTA, trust strip) so a returning user recognises the product instead of
 * wondering whether they are in the right place.
 */
export default function Home() {
  return (
    <>
      <Nav />

      <div className="pt-hero-wrap">
        <header className="pt-hero">
          <span className="pt-eyebrow">✦ Built for Tamil. Unlike anything else.</span>

          <h1>
            Write Tamil <span className="grad">without a Tamil keyboard</span>
            <span className="ta">தமிழ் எழுத்துச் சரிபார்ப்பு</span>
          </h1>

          <p className="pt-sub">
            Type <code>vanakkam</code> and get வணக்கம். Spelling, sandhi and grammar checked
            as you write — by an engine that knows when to stay quiet.
          </p>

          <div className="pt-cta-row">
            <a className="btn-hero" href="/write">
              Start writing — free →
            </a>
            <a className="btn-hero-outline" href="/pricing">
              See Pro
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

        <footer className="pt-foot">
          <a href="/pricing">Pricing</a>
          <a href="/blog">Blog</a>
          <span>Nothing you paste here is stored.</span>
        </footer>
      </main>
    </>
  );
}

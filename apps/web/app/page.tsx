import Demo from "@/components/Demo";
import Nav from "@/components/Nav";

/**
 * The landing page (§17.2 screen 1).
 *
 * It leads with the working product, not with a pitch. Someone who writes Tamil can tell
 * in five seconds whether a Tamil proofreader is any good — so the fastest way to earn
 * their trust is to let them try it, on their own text, before anything asks them for
 * anything.
 */
export default function Home() {
  return (
    <>
      <Nav />
      <main className="pt-landing">
      <header className="pt-hero">
        <h1>ProofTamil</h1>
        <p className="pt-tag-ta">தமிழ் எழுத்துச் சரிபார்ப்பு</p>
        <p className="pt-sub">
          Write Tamil without a Tamil keyboard. Type <code>vanakkam</code>, get வணக்கம் —
          and have your grammar checked as you write.
        </p>
      </header>

      <Demo />

      <section className="pt-features">
        <div>
          <h3>⌨️ Type in English letters</h3>
          <p>
            <code>vanakkam</code> → வணக்கம். A 349,000-word Tamil dictionary, ranked by
            how common each word actually is. Works offline, costs nothing.
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
            continue your writing — and every AI draft is proofread by the same engine.
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

import Link from "next/link";

/**
 * The global footer — production's exact layout: a lavender newsletter block ("தமிழ்
 * செய்திமடல்") above a three-column sitemap (Tools / Learn / Company) with the brand mark.
 * Rendered once in the root layout so every page ends the same way.
 *
 * The year is hard-coded, not computed from `new Date()`. A date rendered on the server and
 * re-rendered on the client can disagree if the clock ticks over midnight between them,
 * which React reports as a hydration error — and the footer year is not worth that risk.
 */
export default function SiteFooter() {
  return (
    <footer className="pt-site-footer">
      {/* Newsletter */}
      <div className="pt-news-wrap">
        <div className="pt-news">
          <div className="pt-news-copy">
            <h2 className="ta">தமிழ் செய்திமடல்</h2>
            <p className="ta">
              வாரந்தோறும் தமிழ் எழுத்துக்கள், கதைகள் மற்றும் இலக்கணக் குறிப்புகள் உங்கள்
              மின்னஞ்சலுக்கு!
            </p>
            <p className="pt-news-en">
              Weekly Tamil writings, stories, and grammar tips delivered to your inbox.
            </p>
          </div>
          <div className="pt-news-form">
            <div className="pt-news-field">
              <input
                type="email"
                placeholder="உங்கள் மின்னஞ்சல் / Your email"
                aria-label="Email address"
              />
              <button type="button" className="pt-news-btn">
                Subscribe
              </button>
            </div>
            <p className="pt-news-note">
              No spam. Unsubscribe anytime. We respect your privacy.
            </p>
          </div>
        </div>
      </div>

      {/* Sitemap */}
      <div className="pt-foot-cols">
        <div className="pt-foot-brand">
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img src="/brand/tamil-logo.svg" alt="ProofTamil" width={180} height={40} />
          <p>
            AI-powered proofreading platform to write clear, accurate, and eloquent Tamil
            content with confidence.
          </p>
        </div>

        <nav className="pt-foot-col" aria-label="Tools">
          <h3>Tools</h3>
          <Link href="/write">Free Tamil Editor</Link>
          <Link href="/ocr">Tamil OCR</Link>
          <Link href="/handwriting-ocr">Handwriting OCR</Link>
          <Link href="/ai-writer">AI Content Writer</Link>
        </nav>

        <nav className="pt-foot-col" aria-label="Learn">
          <h3>Learn</h3>
          <Link href="/blog">Blog</Link>
          <Link href="/how-to-use">How to Use</Link>
          <Link href="/pricing">Pricing</Link>
        </nav>

        <nav className="pt-foot-col" aria-label="Company">
          <h3>Company</h3>
          <Link href="/">Home</Link>
          <Link href="/contact">Contact</Link>
          <Link href="/privacy">Privacy Policy</Link>
          <Link href="/terms">Terms of Service</Link>
        </nav>
      </div>

      <div className="pt-foot-legal">© 2026 ProofTamil. All rights reserved.</div>
    </footer>
  );
}

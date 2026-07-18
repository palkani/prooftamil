export const metadata = { title: "Terms of Service — ProofTamil" };

export default function TermsPage() {
  return (
    <div className="pt-legal">
      <h1>Terms of Service</h1>
      <p className="pt-legal-updated">Last updated: July 2026</p>

      <h2>Using ProofTamil</h2>
      <p>
        ProofTamil provides Tamil writing, proofreading, OCR, voice typing and AI content
        tools. You may use the free features for personal and commercial writing. Do not use
        the service to break the law or to abuse the AI endpoints (for example, automated
        bulk requests intended to exhaust capacity).
      </p>

      <h2>Your content</h2>
      <p>
        What you write is yours. We claim no ownership over your drafts or generated content.
        You are responsible for what you publish — AI-generated text may require proofreading
        and fact-checking before use.
      </p>

      <h2>Availability</h2>
      <p>
        The service is provided “as is” while in active development. Features may change, and
        some (accounts, billing, export) ship in later phases. We do not guarantee
        uninterrupted availability.
      </p>

      <h2>Contact</h2>
      <p>
        Questions about these terms? Email{" "}
        <a href="mailto:contact@prooftamil.com">contact@prooftamil.com</a>.
      </p>
    </div>
  );
}

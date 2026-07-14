import Link from "next/link";

import Nav from "@/components/Nav";

export const metadata = { title: "Pricing — ProofTamil" };

/**
 * Pricing (§17.2 screen 7).
 *
 * The Free column is deliberately generous, and that is a product argument, not
 * charity: the proofreader and the IME are what make someone able to WRITE Tamil at all.
 * Gating those would gate the reason to show up. What Pro sells is the expensive stuff —
 * generation, unlimited AI checks, export — which is also, not coincidentally, what
 * actually costs us money per use.
 *
 * Prices are shown in ₹ first. The audience is in India.
 */
const FREE = [
  ["Tamil IME — type vanakkam, get வணக்கம்", true],
  ["Spelling, sandhi & grammar checking", true],
  ["30 AI grammar checks per day", true],
  ["Scan printed text (on your device)", true],
  ["Voice typing", true],
  ["Unlimited drafts", true],
  ["Export to Word / PDF / text", false],
  ["AI writer — rewrite, templates, continue", false],
  ["Handwriting scan (AI)", false],
] as const;

const PRO = [
  ["Everything in Free", true],
  ["Unlimited AI grammar checks", true],
  ["Export to Word / PDF / text", true],
  ["AI writer — rewrite, templates, continue", true],
  ["Handwriting scan (AI)", true],
  ["Priority support", true],
] as const;

export default function Pricing() {
  return (
    <>
      <Nav />
      <main className="pt-landing">
        <header className="pt-hero">
          <h1>Simple pricing</h1>
          <p className="pt-sub">
            Writing Tamil is free, and always will be. Pro pays for the parts that cost us
            money — the AI.
          </p>
        </header>

        <div className="pt-plans">
          <div className="pt-plan">
            <h2>Free</h2>
            <p className="pt-price">
              ₹0<span>/ forever</span>
            </p>
            <ul>
              {FREE.map(([label, on]) => (
                <li key={label} className={on ? "" : "off"}>
                  <span aria-hidden="true">{on ? "✓" : "—"}</span> {label}
                </li>
              ))}
            </ul>
            <Link href="/write" className="pt-plan-cta ghost">
              Start writing
            </Link>
            <p className="pt-plan-note">No account needed.</p>
          </div>

          <div className="pt-plan best">
            <span className="pt-badge">Most popular</span>
            <h2>Pro</h2>
            <p className="pt-price">
              ₹249<span>/ month</span>
            </p>
            <ul>
              {PRO.map(([label]) => (
                <li key={label}>
                  <span aria-hidden="true">✓</span> {label}
                </li>
              ))}
            </ul>
            <Link href="/checkout/success" className="pt-plan-cta solid">
              Upgrade to Pro
            </Link>
            <p className="pt-plan-note">Cancel any time. Keeps working until the period ends.</p>
          </div>
        </div>

        <div className="pt-gate" style={{ marginTop: "1.5rem" }}>
          <strong>Billing is not connected yet</strong>
          <p>
            Payments ship in Phase 6 (Dodo Payments). Nothing on this page will charge you.
            Every Free feature above works today, without an account.
          </p>
        </div>

        <section className="pt-faq">
          <h2>Questions</h2>
          <details>
            <summary>Is my writing used to train anything?</summary>
            <p>
              No. Your drafts are yours. The typing suggestions endpoint takes no account
              identity at all, so what you type cannot be tied back to you.
            </p>
          </details>
          <details>
            <summary>Why is the proofreader free?</summary>
            <p>
              Most of the corrections come from a deterministic rules engine and a
              349,000-word dictionary running on our own servers — they cost us almost
              nothing per check. Only the AI grammar layer costs real money, which is what
              the daily limit and Pro are for.
            </p>
          </details>
          <details>
            <summary>Do I need a Tamil keyboard?</summary>
            <p>
              No — that is the point. Type <code>vanakkam</code> and pick வணக்கம் from the
              dropdown. Or dictate it with your voice.
            </p>
          </details>
        </section>
      </main>
    </>
  );
}

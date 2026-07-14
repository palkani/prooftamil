import Link from "next/link";

import Nav from "@/components/Nav";

export const metadata = { title: "Settings — ProofTamil" };

/**
 * Settings / Subscription (§17.2 screen 14).
 *
 * Cancel is CANCEL-AT-PERIOD-END, and it says so on the button. A subscriber who clicks
 * "Cancel" and instantly loses access they already paid for feels robbed — and it is not
 * what the billing model actually does.
 */
export default function Settings() {
  return (
    <>
      <Nav />
      <main className="pt-narrow">
        <header className="pt-header">
          <h1>Settings</h1>
        </header>

        <section className="pt-card-plain">
          <h2>Account</h2>
          <dl className="pt-kv">
            <dt>Email</dt>
            <dd className="muted">Not signed in</dd>
            <dt>Plan</dt>
            <dd>
              <span className="pt-tag">Free</span>
            </dd>
          </dl>
          <Link href="/login" className="pt-primary as-link sm">
            Sign in
          </Link>
        </section>

        <section className="pt-card-plain">
          <h2>Usage today</h2>
          <p className="pt-usage-num">
            0 <span>/ 30 AI grammar checks</span>
          </p>
          <div className="pt-bar">
            <i style={{ width: "0%" }} />
          </div>
          <p className="pt-plan-note">
            Spelling, sandhi and the Tamil IME are unlimited on every plan — they run on
            our own engine, not an AI model.
          </p>
        </section>

        <section className="pt-card-plain">
          <h2>Subscription</h2>
          <p className="pt-plan-note">
            You are on the Free plan. Pro unlocks export, the AI writer and handwriting
            scanning.
          </p>
          <div className="pt-row-btns">
            <Link href="/pricing" className="pt-primary as-link sm">
              Upgrade to Pro
            </Link>
            <button className="pt-danger-btn" disabled>
              Cancel at period end
            </button>
          </div>
        </section>

        <section className="pt-card-plain">
          <h2>Your data</h2>
          <p className="pt-plan-note">
            Drafts live on this device until accounts ship. Images you scan are never
            stored — they are read and discarded. What you type into the IME is never
            logged against an identity.
          </p>
        </section>

        <div className="pt-gate">
          <strong>Accounts and billing are not connected yet</strong>
          <p>Both ship in Phase 6. Everything else on the site works today.</p>
        </div>
      </main>
    </>
  );
}

import Link from "next/link";


export const metadata = { title: "Finish upgrading — ProofTamil" };

/**
 * Abandoned-checkout resume (§17.2 screen 15).
 *
 * Reached from the drip email. It exists to remove every step between the click and the
 * payment — the plan is pre-selected and the page does not ask the user to choose again,
 * because they already did, which is precisely why they are getting an email.
 */
export default function Resume() {
  return (
    <>
      <main className="pt-narrow">
        <div className="pt-auth">
          <h1>Pick up where you left off</h1>
          <p className="pt-auth-sub">
            Your Pro upgrade was not finished. Nothing was charged.
          </p>

          <div className="pt-resume-card">
            <div>
              <strong>ProofTamil Pro</strong>
              <span>Unlimited AI checks · export · AI writer</span>
            </div>
            <span className="pt-price sm">
              ₹249<span>/mo</span>
            </span>
          </div>

          <Link href="/checkout/success" className="pt-primary as-link">
            Complete the upgrade
          </Link>

          <div className="pt-gate">
            <strong>Billing is not connected yet</strong>
            <p>Dodo checkout and the 3-touch drip email ship in Phase 6.</p>
          </div>

          <p className="pt-auth-alt">
            Not interested? <Link href="/write">Keep using the free plan</Link> · you can
            unsubscribe from these emails at any time.
          </p>
        </div>
      </main>
    </>
  );
}

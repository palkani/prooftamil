import Link from "next/link";

import Nav from "@/components/Nav";

export const metadata = { title: "Welcome to Pro — ProofTamil" };

/** Checkout success (§17.2 screen 8). Reached after Dodo redirects back. */
export default function Success() {
  return (
    <>
      <Nav />
      <main className="pt-narrow">
        <div className="pt-success">
          <div className="pt-check" aria-hidden="true">
            ✓
          </div>
          <h1>வரவேற்கிறோம்!</h1>
          <p className="pt-auth-sub">You are on Pro. Export, the AI writer and handwriting
            scanning are unlocked.</p>

          <div className="pt-gate">
            <strong>Billing is not connected yet</strong>
            <p>
              This is the screen you will land on after paying. No payment was taken —
              Dodo Payments is wired up in Phase 6.
            </p>
          </div>

          <Link href="/write" className="pt-primary as-link">
            Start writing →
          </Link>
          <p className="pt-plan-note">
            A receipt would normally be emailed to you. Manage the plan in{" "}
            <Link href="/settings">Settings</Link>.
          </p>
        </div>
      </main>
    </>
  );
}

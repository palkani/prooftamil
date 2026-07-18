import Link from "next/link";

export const metadata = { title: "Pricing — ProofTamil" };

/**
 * Pricing (§17.2 screen 7) — production shows a "Coming Soon" screen, not a plan grid,
 * because billing is not live (Phase 6, Dodo Payments). Mirroring that is the honest
 * choice: everything the product does today is free, so a priced grid would imply a
 * paywall that does not exist yet.
 */
export default function Pricing() {
  return (
    <div className="pt-soon">
      <div className="pt-soon-ico" aria-hidden="true">
        🕐
      </div>
      <h1>Pricing Coming Soon</h1>
      <p>
        We&apos;re putting the finishing touches on our Pro plans. Join our mailing list to
        be notified when they launch.
      </p>
      <a className="btn-hero" href="#subscribe">
        Get notified
      </a>
      <p className="pt-soon-note">
        In the meantime, ProofTamil is <strong>completely free</strong> to use.{" "}
        <Link href="/write">Try it now →</Link>
      </p>
    </div>
  );
}

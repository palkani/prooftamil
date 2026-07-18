import Link from "next/link";

/**
 * The brand mark, lifted verbatim from production (public/brand/tamil-logo.svg): an indigo
 * gradient rounded-square with a bold proofreader's tick, an amber pen-dot, and the
 * "Proofதமிழ்" wordmark (Proof in indigo, தமிழ் in saffron). Using the real SVG — not a
 * re-drawn approximation — is what makes a returning user recognise the product instantly.
 */
export default function Logo({ className = "" }: { className?: string }) {
  return (
    <Link href="/" className={`pt-logo ${className}`} aria-label="ProofTamil — Home">
      {/* eslint-disable-next-line @next/next/no-img-element */}
      <img src="/brand/tamil-logo.svg" alt="ProofTamil" width={200} height={45} />
    </Link>
  );
}

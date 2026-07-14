"use client";

import { usePathname } from "next/navigation";
import Link from "next/link";

/**
 * The shell every screen sits in (§17.2).
 *
 * Without it each page is an orphan — a user who lands on /pricing has no way back to
 * the product, and the site reads as a pile of routes rather than one thing.
 *
 * SIGN IN / SIGN UP are rendered but INERT. Auth is Phase 6 and blocked on the §1
 * accounts, so the links go to screens that explain that rather than to a form that
 * silently fails. Hiding them would be the wrong call: the screens exist, they are just
 * not connected, and pretending otherwise makes the gap harder to find later.
 */
export default function Nav() {
  const path = usePathname();
  const on = (p: string) => (path === p ? "on" : "");

  return (
    <nav className="pt-nav">
      <Link href="/" className="pt-brand">
        ProofTamil
      </Link>

      <div className="pt-nav-links">
        <Link href="/write" className={on("/write")}>
          Editor
        </Link>
        <Link href="/drafts" className={on("/drafts")}>
          Drafts
        </Link>
        <Link href="/pricing" className={on("/pricing")}>
          Pricing
        </Link>
        <Link href="/blog" className={on("/blog")}>
          Blog
        </Link>
      </div>

      <div className="pt-nav-auth">
        <Link href="/login" className="ghost">
          Sign in
        </Link>
        <Link href="/signup" className="solid">
          Sign up
        </Link>
      </div>
    </nav>
  );
}

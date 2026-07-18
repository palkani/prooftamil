"use client";

import { usePathname } from "next/navigation";
import Link from "next/link";
import Logo from "./Logo";

/**
 * The top bar — production's shell (§17.2), rebuilt to match the live app exactly: the SVG
 * logo on the left; Blog / Contact plus a "Sign In" outline pill and a "Sign Up" gradient
 * pill on the right. Rendered once in the root layout, so every screen shares one header.
 *
 * SIGN IN / SIGN UP are wired to screens that exist but are not yet connected to accounts
 * (Phase 6, blocked on §1). They lead to a form that explains that rather than silently
 * failing — hiding them would make the gap harder to find later.
 */
export default function Nav() {
  const path = usePathname();
  const on = (p: string) => (path === p ? "on" : "");

  return (
    <nav className="pt-nav">
      <div className="pt-nav-inner">
        <Logo />

        <div className="pt-nav-links">
          <Link href="/blog" className={on("/blog")}>
            Blog
          </Link>
          <Link href="/contact" className={on("/contact")}>
            Contact
          </Link>
          <Link href="/login" className="pt-signin">
            Sign In
          </Link>
          <Link href="/signup" className="pt-signup">
            Sign Up
          </Link>
        </div>
      </div>
    </nav>
  );
}

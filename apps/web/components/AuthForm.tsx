"use client";

import { useState } from "react";
import Link from "next/link";
import Logo from "./Logo";

type Kind = "signup" | "login" | "forgot";

/**
 * Signup / Login / Password reset (§17.2 screens 2, 3) — production's centered auth card:
 * the logo, a welcome line, "Continue with Google", an "OR USE EMAIL" divider, the fields,
 * and a footer link. Sits on the aurora wash, matching the live app.
 *
 * THE FORMS ARE REAL AND THE BACKEND IS NOT. Auth is Phase 6 and blocked on the §1 accounts
 * (Supabase, the Google OAuth consent screen), so nothing here can create a session yet.
 * Submitting says so plainly instead of spinning forever — a form that pretends to work is
 * worse than one that admits it does not.
 */
export default function AuthForm({ kind }: { kind: Kind }) {
  const [notice, setNotice] = useState("");

  const heading =
    kind === "signup"
      ? "Create your account"
      : kind === "login"
        ? "Welcome Back"
        : "Reset your password";
  const sub =
    kind === "signup"
      ? "Start writing better Tamil in seconds"
      : kind === "login"
        ? "Sign in to your Tamil writing workspace"
        : "We'll email you a link to set a new password";

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    setNotice(
      "Accounts are not enabled yet — authentication ships in Phase 6. " +
        "Everything else (the editor, the IME, proofreading, scanning) works right now without an account.",
    );
  };

  return (
    <main className="pt-authwrap">
      <div className="pt-authcard pt-auth">
        <Logo />
        <h1>{heading}</h1>
        <p className="pt-auth-sub">{sub}</p>

        {kind !== "forgot" && (
          <>
            <button type="button" className="pt-google" onClick={submit}>
              <span aria-hidden="true">G</span> Sign in with Google
            </button>
            <div className="pt-or">
              <span>OR USE EMAIL</span>
            </div>
          </>
        )}

        <form onSubmit={submit}>
          {kind === "signup" && (
            <label>
              Full Name
              <input name="name" autoComplete="name" placeholder="உங்கள் பெயர்" />
            </label>
          )}

          <label>
            Email Address
            <input
              type="email"
              name="email"
              required
              autoComplete="email"
              placeholder="you@example.com"
            />
          </label>

          {kind !== "forgot" && (
            <label>
              Password
              <input
                type="password"
                name="password"
                required
                minLength={8}
                autoComplete={kind === "signup" ? "new-password" : "current-password"}
                placeholder="Enter your password"
              />
            </label>
          )}

          {kind === "login" && (
            <div className="pt-auth-row">
              <label>
                <input type="checkbox" name="remember" /> Remember me
              </label>
              <Link href="/forgot">Forgot password?</Link>
            </div>
          )}

          <button type="submit" className="pt-primary">
            {kind === "signup"
              ? "Create account"
              : kind === "login"
                ? "Sign In"
                : "Send reset link"}
          </button>
        </form>

        {notice && (
          <div className="pt-gate" role="status">
            <strong>Not yet available</strong>
            <p>{notice}</p>
            <Link href="/write" className="pt-inline-cta">
              Open the editor anyway →
            </Link>
          </div>
        )}

        <p className="pt-auth-foot">
          {kind === "login" && (
            <>
              Don&apos;t have an account? <Link href="/signup">Sign up for free</Link>
            </>
          )}
          {kind === "signup" && (
            <>
              Already have an account? <Link href="/login">Sign in</Link>
            </>
          )}
          {kind === "forgot" && (
            <>
              Remembered it? <Link href="/login">Sign in</Link>
            </>
          )}
        </p>

        <p className="pt-demo-note">
          ℹ️ <strong>Demo Mode:</strong> accounts ship in Phase 6 — the editor works now
          without one.
        </p>
      </div>
    </main>
  );
}

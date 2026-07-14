"use client";

import { useState } from "react";
import Link from "next/link";

type Kind = "signup" | "login" | "forgot";

/**
 * Signup / Login / Password reset (§17.2 screens 2, 3).
 *
 * THE FORMS ARE REAL AND THE BACKEND IS NOT. Auth is Phase 6 and blocked on the §1
 * accounts (Supabase, the Google OAuth consent screen), so nothing here can actually
 * create a session.
 *
 * Submitting therefore says so, plainly, instead of spinning forever or throwing a
 * network error. A form that pretends to work is worse than one that admits it does not:
 * the user knows within a second, and so does the next engineer.
 */
export default function AuthForm({ kind }: { kind: Kind }) {
  const [notice, setNotice] = useState("");

  const title =
    kind === "signup" ? "Create an account" : kind === "login" ? "Sign in" : "Reset your password";

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    setNotice(
      "Accounts are not enabled yet — authentication ships in Phase 6. " +
        "Everything else (the editor, the IME, proofreading, scanning) works right now without an account.",
    );
  };

  return (
    <div className="pt-auth">
      <h1>{title}</h1>
      <p className="pt-auth-sub">
        {kind === "forgot"
          ? "We will email you a link to set a new password."
          : "Your drafts sync across devices, and Pro unlocks export and the AI writer."}
      </p>

      {kind !== "forgot" && (
        <>
          <button type="button" className="pt-google" onClick={submit}>
            <span aria-hidden="true">G</span> Continue with Google
          </button>
          <div className="pt-or">
            <span>or</span>
          </div>
        </>
      )}

      <form onSubmit={submit}>
        {kind === "signup" && (
          <label>
            Name
            <input name="name" autoComplete="name" placeholder="உங்கள் பெயர்" />
          </label>
        )}

        <label>
          Email
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
              placeholder="at least 8 characters"
            />
          </label>
        )}

        <button type="submit" className="pt-primary">
          {kind === "signup"
            ? "Create account"
            : kind === "login"
              ? "Sign in"
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

      <p className="pt-auth-alt">
        {kind === "login" && (
          <>
            <Link href="/forgot">Forgot your password?</Link>
            <span> · New here? </span>
            <Link href="/signup">Create an account</Link>
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
    </div>
  );
}

"use client";

import { useState } from "react";

/**
 * The contact form. Submission is not wired to a backend yet (Phase 6), so it acknowledges
 * locally rather than posting into the void — and points the user at email meanwhile.
 */
export default function ContactForm() {
  const [sent, setSent] = useState(false);

  return (
    <form
      className="pt-contact-form"
      onSubmit={(e) => {
        e.preventDefault();
        setSent(true);
      }}
    >
      <label>
        Name
        <input name="name" required placeholder="உங்கள் பெயர்" />
      </label>
      <label>
        Email
        <input type="email" name="email" required placeholder="you@example.com" />
      </label>
      <label>
        Message
        <textarea name="message" required placeholder="How can we help?" />
      </label>
      <button type="submit" className="pt-primary">
        Send message
      </button>
      {sent && (
        <div className="pt-gate" role="status">
          <strong>Thanks — noted.</strong>
          <p>
            Messaging isn&apos;t wired to a backend yet (Phase 6). For anything urgent, email{" "}
            <a href="mailto:contact@prooftamil.com">contact@prooftamil.com</a>.
          </p>
        </div>
      )}
    </form>
  );
}

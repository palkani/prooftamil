import ContactForm from "@/components/ContactForm";

export const metadata = {
  title: "Contact — ProofTamil",
  description: "Questions, feedback or a bug to report? Get in touch with the ProofTamil team.",
};

export default function ContactPage() {
  return (
    <div className="pt-page">
      <div className="pt-page-head">
        <span className="pt-eyebrow">Contact</span>
        <h1>
          Get in <span className="grad">touch</span>
        </h1>
        <p>
          Questions, feedback, or a bug to report? We read everything — Tamil or English,
          both welcome.
        </p>
      </div>

      <div className="pt-contact-grid">
        <ContactForm />

        <aside className="pt-contact-aside">
          <h3>Other ways to reach us</h3>
          <p>
            Email:{" "}
            <a href="mailto:contact@prooftamil.com">contact@prooftamil.com</a>
          </p>
          <p>
            Found a proofreading mistake? Tell us the sentence and what it should be — that
            feedback trains the engine.
          </p>
          <p>
            We usually reply within a couple of days. For anything account- or billing-
            related, note that accounts ship in a later phase.
          </p>
        </aside>
      </div>
    </div>
  );
}

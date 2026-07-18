export const metadata = { title: "Privacy Policy — ProofTamil" };

export default function PrivacyPage() {
  return (
    <div className="pt-legal">
      <h1>Privacy Policy</h1>
      <p className="pt-legal-updated">Last updated: July 2026</p>

      <h2>The short version</h2>
      <p>
        ProofTamil is built so that the sensitive things you do — what you type, the images
        you scan, the audio you dictate — stay private by default. We do not sell your data,
        and most of the product works without an account at all.
      </p>

      <h2>What you type</h2>
      <p>
        Typing suggestions (the Tamil IME) and the spelling/sandhi engine run without any
        account identity. What you type into the editor is not logged against a person.
        Drafts are stored on your own device until accounts ship.
      </p>

      <h2>Images you scan</h2>
      <p>
        Printed-text OCR runs entirely in your browser — the image never leaves your device.
        Handwriting recognition sends the image to an AI model to transcribe it; the image is
        processed transiently and not retained.
      </p>

      <h2>Voice you dictate</h2>
      <p>
        Audio is sent to our speech provider to transcribe to Tamil and is never stored — not
        to disk, not to logs. It exists only for the moment it takes to return the text.
      </p>

      <h2>Contact</h2>
      <p>
        Questions about privacy? Email{" "}
        <a href="mailto:contact@prooftamil.com">contact@prooftamil.com</a>.
      </p>
    </div>
  );
}

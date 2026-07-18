import DraftsList from "@/components/DraftsList";

export const metadata = { title: "Drafts — ProofTamil" };

export default function Drafts() {
  return (
    <>
      <main className="pt-landing">
        <header className="pt-header">
          <h1>Drafts</h1>
          <p>Everything you have written. Saved as you type.</p>
        </header>
        <DraftsList />
      </main>
    </>
  );
}

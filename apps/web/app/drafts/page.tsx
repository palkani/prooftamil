import DraftsList from "@/components/DraftsList";
import Nav from "@/components/Nav";

export const metadata = { title: "Drafts — ProofTamil" };

export default function Drafts() {
  return (
    <>
      <Nav />
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

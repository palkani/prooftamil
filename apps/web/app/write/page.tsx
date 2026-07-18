import Editor from "@/components/Editor";

export const metadata = { title: "Editor — ProofTamil" };

export default function Write() {
  return (
    <>
      {/*
        No page heading here. The nav already says ProofTamil, and the editor is the hero
        of this screen — a second wordmark above it just pushes the writing surface down
        the page for no information. The workspace should feel like a document, not a
        marketing page with a textarea on it.
      */}
      <main className="pt-workspace">
        <Editor />
      </main>
    </>
  );
}

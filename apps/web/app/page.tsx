import Editor from "@/components/Editor";

export default function Home() {
  return (
    <main>
      <header className="pt-header">
        <h1>ProofTamil</h1>
        <p>
          Type romanized Tamil, write in Tamil script, and have it proofread as you go.
        </p>
      </header>
      <Editor />
    </main>
  );
}

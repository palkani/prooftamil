import Editor from "@/components/Editor";
import Nav from "@/components/Nav";

export default function Write() {
  return (
    <>
      <Nav />
      <main>
      <header className="pt-header">
        <h1>
          <a href="/">ProofTamil</a>
        </h1>
        <p>Type romanized Tamil, write in Tamil script, and have it proofread as you go.</p>
      </header>
      <Editor />
      </main>
    </>
  );
}

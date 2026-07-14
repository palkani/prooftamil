import AuthForm from "@/components/AuthForm";
import Nav from "@/components/Nav";

export const metadata = { title: "Sign in — ProofTamil" };

export default function Page() {
  return (
    <>
      <Nav />
      <main className="pt-narrow">
        <AuthForm kind="login" />
      </main>
    </>
  );
}

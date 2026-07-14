import AuthForm from "@/components/AuthForm";
import Nav from "@/components/Nav";

export const metadata = { title: "Sign up — ProofTamil" };

export default function Page() {
  return (
    <>
      <Nav />
      <main className="pt-narrow">
        <AuthForm kind="signup" />
      </main>
    </>
  );
}

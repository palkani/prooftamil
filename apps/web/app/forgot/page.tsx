import AuthForm from "@/components/AuthForm";
import Nav from "@/components/Nav";

export const metadata = { title: "Reset password — ProofTamil" };

export default function Page() {
  return (
    <>
      <Nav />
      <main className="pt-narrow">
        <AuthForm kind="forgot" />
      </main>
    </>
  );
}

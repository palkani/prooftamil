import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "ProofTamil — தமிழ் எழுத்துச் சரிபார்ப்பு",
  description: "Tamil writing and proofreading. Type in English letters, write in Tamil.",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="ta">
      <body>{children}</body>
    </html>
  );
}

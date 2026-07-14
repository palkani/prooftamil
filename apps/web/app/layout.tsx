import type { Metadata } from "next";
import { Inter, Noto_Sans_Tamil } from "next/font/google";
import "./globals.css";

/**
 * Tamil typography (§17.1) — the differentiator, and the thing a team that does not
 * read Tamil is most likely to get wrong.
 *
 * Self-hosted via next/font, not a <link> to Google Fonts. next/font inlines the
 * @font-face and preloads the file, so there is no FOUT — and with Tamil a FOUT is
 * not a cosmetic flicker, it is a REFLOW: the Latin fallback has different glyph
 * metrics, so the text visibly jumps and line count changes. It also removes a
 * third-party request from every page load, which the CSP will thank us for.
 *
 * Noto Sans Tamil has complete coverage of the stacked consonant+vowel forms
 * (ஔ, ஸ்ரீ, க்ஷ) that a Latin-first fallback renders as tofu boxes.
 */
const tamil = Noto_Sans_Tamil({
  subsets: ["tamil"],
  weight: ["400", "500", "600"],
  variable: "--font-tamil",
  display: "swap",
});

/** Inter for UI chrome only — buttons, labels, badges. Never for Tamil content. */
const ui = Inter({
  subsets: ["latin"],
  variable: "--font-ui",
  display: "swap",
});

export const metadata: Metadata = {
  title: "ProofTamil — தமிழ் எழுத்துச் சரிபார்ப்பு",
  description:
    "Tamil writing and proofreading. Type in English letters, write in Tamil.",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="ta" className={`${tamil.variable} ${ui.variable}`}>
      <body>{children}</body>
    </html>
  );
}

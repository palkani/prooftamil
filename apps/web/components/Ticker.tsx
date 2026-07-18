/**
 * The scrolling announcement bar under the nav — production's indigo ticker.
 *
 * It is the one place every feature gets named on every page, so a visitor who came for
 * "Tamil editor" discovers voice, OCR and the AI writer without hunting. Pure CSS marquee:
 * the track is duplicated so the loop is seamless, and it pauses under prefers-reduced-motion
 * (handled in globals.css) rather than strobing for users who asked motion to stop.
 */
const MESSAGES = [
  "🎙 Speak it, we type it.",
  "✨ AI Content Writer — Blogs, essays & articles in Tamil & English.",
  "✓ Smart Proofreading — Grammar, spelling & style in one place.",
  "📄 Tamil OCR — Extract Tamil text from images & PDFs.",
  "⌨️ Tanglish → Tamil — type in English letters, get Tamil.",
];

export default function Ticker() {
  // Two identical tracks back-to-back → when the first scrolls fully off, the second is
  // already in place, so there is no visible seam or reset jump.
  const track = (
    <div className="pt-ticker-track" aria-hidden="false">
      {MESSAGES.map((m, i) => (
        <span key={i} className="pt-ticker-item">
          {m}
          <span className="pt-ticker-dot" aria-hidden="true">•</span>
        </span>
      ))}
    </div>
  );
  return (
    <div className="pt-ticker" role="marquee" aria-label="Product announcements">
      <div className="pt-ticker-viewport">
        {track}
        <div className="pt-ticker-track" aria-hidden="true">
          {MESSAGES.map((m, i) => (
            <span key={`dup-${i}`} className="pt-ticker-item">
              {m}
              <span className="pt-ticker-dot" aria-hidden="true">•</span>
            </span>
          ))}
        </div>
      </div>
    </div>
  );
}

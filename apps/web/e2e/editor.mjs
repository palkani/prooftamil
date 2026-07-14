/**
 * End-to-end check: drive the real editor in a real browser.
 *
 * This exists because typecheck, build and curl ALL passed while the editor was
 * silently broken. The suggestion plugin had been registered as a plain object
 * instead of an Extension, so TipTap accepted it and never ran it — the side panel
 * listed suggestions perfectly while not one underline was drawn. Nothing short of
 * asserting on rendered DOM in a browser would have caught that.
 *
 * So: assert on what the user actually sees.
 *
 *   npm run e2e          (needs `make demo` running)
 */
import { chromium } from "playwright";
import fs from "node:fs";

const URL = process.env.E2E_URL ?? "http://localhost:4000";

let failures = 0;
const check = (name, ok, detail = "") => {
  if (!ok) failures++;
  console.log(`  ${ok ? "PASS" : "FAIL"}  ${name}${detail ? `  — ${detail}` : ""}`);
};

const browser = await chromium.launch();
const ctx = await browser.newContext({ acceptDownloads: true });
const page = await ctx.newPage();

const errors = [];
page.on("pageerror", (e) => errors.push(String(e)));
page.on("console", (m) => m.type() === "error" && errors.push(m.text()));

await page.goto(URL, { waitUntil: "networkidle" });
await page.waitForSelector(".pt-editor", { timeout: 15000 });

/* --- proofreading: underlines AND panel cards ---------------------------- */
await page.waitForSelector(".pt-card", { timeout: 30000 }).catch(() => {});
const cards = await page.locator(".pt-card").count();
const underlines = await page.locator(".pt-suggestion").count();
check("proofread returns suggestions", cards > 0, `${cards} cards`);
// The regression that motivated this file.
check("suggestions are UNDERLINED in the text", underlines > 0, `${underlines} decorations`);

/* --- accept applies the fix to the document ------------------------------ */
await page.locator(".pt-editor").click();
await page.keyboard.press("ControlOrMeta+A");
await page.keyboard.press("Backspace");
await page.keyboard.type("அந்த பையன் வந்தான்.", { delay: 8 });
await page.waitForSelector(".pt-card", { timeout: 30000 });
await page.locator(".pt-card button", { hasText: "Accept" }).first().click();
await page.waitForTimeout(500);
const afterAccept = await page.locator(".pt-editor").innerText();
check("Accept rewrites the text", afterAccept.includes("அந்தப்"), afterAccept.trim());

/* --- stale suggestions must vanish from BOTH text and panel --------------- */
await page.keyboard.press("ControlOrMeta+A");
await page.keyboard.press("Backspace");
await page.waitForTimeout(400);
const staleCards = await page.locator(".pt-card").count();
const staleUnderlines = await page.locator(".pt-suggestion").count();
check(
  "wiping the doc clears suggestions",
  staleCards === 0 && staleUnderlines === 0,
  `${staleCards} cards / ${staleUnderlines} underlines`,
);

/* --- IME: romanized -> Tamil --------------------------------------------- */
await page.keyboard.type("vanakkam", { delay: 60 });
await page.waitForSelector(".pt-ime li", { timeout: 10000 });
const cands = await page.locator(".pt-ime li .word").allInnerTexts();
check("IME offers candidates", cands.length > 0, cands.slice(0, 3).join(", "));

await page.keyboard.press("Enter");
await page.waitForTimeout(400);
const imeText = await page.locator(".pt-editor").innerText();
check("Enter inserts the Tamil word", imeText.includes("வணக்கம்"), imeText.trim());

/* --- autosave survives a reload ------------------------------------------ */
await page.keyboard.press("ControlOrMeta+A");
await page.keyboard.press("Backspace");
await page.keyboard.type("நான் பள்ளிக்கு சென்றேன்.", { delay: 8 });
await page.waitForTimeout(1000);

await page.reload({ waitUntil: "networkidle" });
await page.waitForTimeout(1200);
const restored = await page.locator(".pt-editor").innerText();
check("draft survives a reload", restored.includes("பள்ளிக்கு"), restored.trim());
check("draft is listed", (await page.locator(".pt-draft").count()) > 0);

/* --- import --------------------------------------------------------------- */
const fixture = "/tmp/pt-e2e-import.txt";
fs.writeFileSync(fixture, "அந்த பையன் வந்தான்.\nஇது இரண்டாவது வரி.");
await page.locator("input[type=file]").setInputFiles(fixture);
await page.waitForTimeout(1200);
const imported = await page.locator(".pt-editor").innerText();
check("import .txt", imported.includes("இரண்டாவது"));

/* --- export --------------------------------------------------------------- */
for (const fmt of [".txt", ".docx"]) {
  try {
    const dl = page.waitForEvent("download", { timeout: 15000 });
    await page.locator(".pt-filebar button", { hasText: fmt }).click();
    const d = await dl;
    const out = `/tmp/pt-e2e-export${fmt}`;
    await d.saveAs(out);
    const size = fs.statSync(out).size;
    // A DOCX that is a valid zip but has no Tamil in it would still be non-empty, so
    // the size check is a floor, not a proof. It catches the common failure: an empty
    // or truncated file.
    check(`export ${fmt}`, size > 100, `${d.suggestedFilename()}, ${size} bytes`);
  } catch (e) {
    check(`export ${fmt}`, false, String(e).slice(0, 60));
  }
}

check("no console errors", errors.length === 0, errors.slice(0, 2).join(" | "));

await browser.close();
console.log(failures === 0 ? "\n  ALL PASS" : `\n  ${failures} FAILED`);
process.exit(failures === 0 ? 0 : 1);

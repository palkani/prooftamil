---
title: "தமிழ் விசைப்பலகை இல்லாமல் தமிழ் எழுதுவது எப்படி"
titleEn: "How to write Tamil without a Tamil keyboard"
date: "2026-07-14"
excerpt: "Type vanakkam, get வணக்கம். How phonetic transliteration works, and why it is harder than it looks."
---

Most Tamil writers do not have a Tamil keyboard. They think in Tamil, and they type in
English letters — `vanakkam`, `pallikku`, `sendren`.

Turning that back into Tamil script is harder than it looks, because there is no single
correct way to romanize Tamil. The same word gets typed several ways by the same person on
the same day: `vanakkam`, `vanakam`, `wanakam`.

## The sound key

We fold both sides of the problem into one lossy "sound key". The Tamil word வணக்கம் keys
to `vanakam`; so does everything a human is likely to type for it. Doubled letters
collapse, vowel length is discarded, and the distinctions people never type reliably —
ல/ள/ழ, ண/ன/ந, ர/ற — all fold together.

That folding is *deliberate destruction of information*. It is exactly the information our
spellchecker exists to protect. The two features want opposite things from the same
dictionary, and that is fine: the IME offers candidates and a human picks, so guessing
widely is free. The proofreader asserts, so it must stay silent when unsure.

## The case that breaks a naive approach

`sendren` should find சென்றேன். It does not, if you take the obvious route — because
சென்றேன் keys to `senren`, not `sendren`.

That is not a bug. It is Tamil phonology: ன்ற is *pronounced* "ndr". Likewise ங்க is "ng",
ண்ட is "nd", ம்ப is "mb". A transliteration engine that models spelling but not sound will
fail on the most ordinary words in the language.

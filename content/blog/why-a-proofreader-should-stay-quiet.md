---
title: "நல்ல திருத்தி அமைதியாக இருக்கும்"
titleEn: "A good proofreader knows when to say nothing"
date: "2026-07-14"
excerpt: "புலி and புளி are both real words. The hardest part of building a Tamil spellchecker is teaching it to shut up."
---

புலி is a tiger. புளி is tamarind. Both are real Tamil words, one letter apart.

If a writer types புலி when they meant புளி, that is a real error — and a spellchecker
cannot see it, because nothing about the word is wrong. Only the sentence knows.

The tempting move is to guess. ழ is commonly mistyped as ள, so why not offer the swap and
let the user decide?

Because a false positive costs more than a miss. A writer who is told their correct Tamil
is wrong stops trusting *every* suggestion, including the right ones. A missed error costs
them one mistake. A wrong correction costs them the tool.

## The rule

Our deterministic layer only ever corrects a **non-word into a word**. If what you typed is
in the dictionary, it says nothing — even when a plausible "fix" is one letter away.

## The trap we walked into

That rule alone is not enough. Tamil place names are endless and most are rare. திருக்காணூர்
is not in a dictionary, and it is one letter from திருக்கானூர், which is — so the engine
"corrected" someone's town.

The fix is frequency. A genuine typo is vanishingly rare next to its correction: அணைவருக்கும்
appears twice in a corpus where அனைவருக்கும் appears 1,679 times. A rare *real* word sits at a
frequency comparable to its neighbour, because both are simply uncommon.

So we only correct when the candidate is overwhelmingly more common than what you actually
wrote. Everything else, we leave alone.

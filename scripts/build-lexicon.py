#!/usr/bin/env python3
"""Build the Tier 1 lexicon from a Tamil corpus (plan §11; addresses RISK R1).

Tier 1 flags a word only when it is ABSENT from the lexicon. So lexicon coverage
is not a nice-to-have — a correct word missing from the dictionary is a false
positive waiting to happen. The 167-word seed cannot go in front of real users.

Usage:
    build-lexicon.py --wiki tawiki-latest-pages-articles.xml.bz2 \
                     --out packages/tamil-rules/dictionaries/corpus.txt \
                     --min-count 5 --holdout 0.05

Two design decisions carry all the weight:

1. FREQUENCY THRESHOLD (--min-count).
   Wikipedia contains typos. A typo admitted to the lexicon is worse than a
   missing word: it does not merely cause a miss, it becomes a legitimate
   SUGGESTION TARGET. Tier 1 could then "correct" a perfectly good word INTO a
   Wikipedia typo, which is the exact failure the lexicon exists to prevent.
   Requiring a word to appear >= N times across independent articles makes that
   vanishingly unlikely, at the cost of dropping genuinely rare words (which only
   costs recall — they stay out-of-vocabulary and Tier 1 stays silent on them).

2. HELD-OUT SPLIT (--holdout).
   A lexicon evaluated on the corpus it was built from will always look perfect.
   Holding out a slice of articles gives us real Tamil sentences the lexicon has
   never seen, so eval/corpus_audit.py can measure the false-positive rate that
   an actual user would experience. That is the only honest test of R1.

The tokenizer is imported from the engine, not reimplemented. If the two ever
diverged, lexicon entries would not match the strings the engine looks up, and
coverage would silently collapse.
"""

from __future__ import annotations

import argparse
import bz2
import gzip
import json
import random
import re
import sys
from collections import Counter
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(ROOT / "apps" / "ml"))

from app.tamil.script import is_tamil, normalize, tokenize  # noqa: E402

# MediaWiki markup we strip before tokenizing. Left in, it would pour template
# names, URLs and CSS into the word counts.
_STRIP = [
    re.compile(r"<ref[^>]*>.*?</ref>", re.S),
    re.compile(r"<[^>]+>"),                      # html tags
    re.compile(r"\{\{.*?\}\}", re.S),            # templates
    re.compile(r"\[\[[^]|]*\|"),                 # piped link targets
    re.compile(r"https?://\S+"),
    re.compile(r"\[\[(?:File|Image|படிமம்):[^]]*\]\]", re.I),
    re.compile(r"'{2,}"),                        # bold/italic
    re.compile(r"[\[\]{}|=*#]"),
]

_TEXT_RE = re.compile(r"<text[^>]*>(.*?)</text>", re.S)


def clean(markup: str) -> str:
    for pat in _STRIP:
        markup = pat.sub(" ", markup)
    return markup


def iter_pages(path: Path):
    """Stream <text> bodies out of a MediaWiki XML dump.

    Streaming, not parsing into memory: the decompressed dump is well over a
    gigabyte and this has to run on a laptop.
    """
    buf = ""
    with bz2.open(path, "rt", encoding="utf-8", errors="replace") as fh:
        for chunk in iter(lambda: fh.read(1 << 20), ""):
            buf += chunk
            while True:
                m = _TEXT_RE.search(buf)
                if not m:
                    break
                yield m.group(1)
                buf = buf[m.end():]
            # Keep the tail in case a <text> element straddles the chunk boundary.
            if len(buf) > 1 << 22:
                buf = buf[-(1 << 20):]


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--wiki", required=True, type=Path)
    ap.add_argument("--out", required=True, type=Path)
    ap.add_argument("--holdout-out", type=Path, default=ROOT / "eval" / "holdout.txt.gz")
    ap.add_argument("--min-count", type=int, default=5,
                    help="a word must appear at least this often to be TRUSTED "
                         "(in-vocabulary, and eligible as a correction target)")
    ap.add_argument("--min-emit", type=int, default=2,
                    help="record counts down to this frequency; below it a word is "
                         "not recorded at all. Hapax legomena are dropped: they are "
                         "mostly typos and OCR noise.")
    ap.add_argument("--holdout", type=float, default=0.05,
                    help="fraction of articles reserved for the FP audit")
    ap.add_argument("--max-pages", type=int, default=0, help="0 = all")
    ap.add_argument("--seed", type=int, default=42)
    args = ap.parse_args()

    rng = random.Random(args.seed)
    counts: Counter[str] = Counter()
    holdout: list[str] = []

    pages = train_pages = 0
    for markup in iter_pages(args.wiki):
        pages += 1
        if args.max_pages and pages > args.max_pages:
            break

        text = clean(markup)

        # Reserve a slice of ARTICLES (not sentences) for the audit, so held-out
        # sentences share no vocabulary context with the training half.
        if rng.random() < args.holdout:
            for line in text.split("\n"):
                line = line.strip()
                # Keep substantial, prose-looking Tamil lines only.
                if 40 < len(line) < 300 and is_tamil(line):
                    holdout.append(normalize(line))
            continue

        train_pages += 1
        for tok in tokenize(text):
            w = tok.text
            # Tamil-only, and at least 2 characters: single letters are almost
            # always markup residue, and they would make every 1-letter typo
            # "valid".
            if len(w) >= 2 and is_tamil(w) and all(0x0B80 <= ord(c) <= 0x0BFF for c in w):
                counts[normalize(w)] += 1

        if pages % 20000 == 0:
            print(f"  {pages:,} pages, {len(counts):,} distinct words", file=sys.stderr)

    # Emit COUNTS, not just words, and emit sub-threshold words too (down to
    # --min-emit). The engine needs both:
    #
    #   count >= --min-count   the word is trusted: in-vocabulary, never flagged,
    #                          and eligible to be a suggestion target.
    #   1 < count < min-count  "seen but rare". NOT trusted enough to be a
    #                          suggestion target, but its existence in real Tamil
    #                          prose is evidence it might be a legitimate rare word
    #                          or a proper noun — so the engine uses the frequency
    #                          RATIO against a candidate before daring to correct it.
    #
    # Without the rare counts the engine cannot tell திருக்காணூர் (a real place
    # name, seen a handful of times) from அணைவருக்கும் (a genuine typo, seen once
    # against a candidate seen thousands of times). Both are simply "absent", and
    # it would confidently rewrite the place name.
    emitted = sorted(
        ((w, n) for w, n in counts.items() if n >= args.min_emit),
        key=lambda x: x[0],
    )
    trusted = sum(1 for _, n in emitted if n >= args.min_count)

    args.out.parent.mkdir(parents=True, exist_ok=True)
    _open = (lambda p: gzip.open(p, "wt", encoding="utf-8")) if args.out.suffix == ".gz" else (lambda p: p.open("w", encoding="utf-8"))
    with _open(args.out) as fh:
        fh.write(
            f"# Tamil lexicon built from {args.wiki.name}\n"
            f"# format: <word>\\t<corpus_count>\n"
            f"# pages={pages:,} (train={train_pages:,})\n"
            f"# min_count={args.min_count} (trusted/in-vocabulary threshold)\n"
            f"# min_emit={args.min_emit}  (below this a word is not recorded at all)\n"
            f"# trusted={trusted:,}  recorded={len(emitted):,}  distinct_seen={len(counts):,}\n"
            f"#\n"
            f"# Words below min_count are recorded but NOT trusted: they are never used\n"
            f"# as a correction target (a Wikipedia typo must not become something we\n"
            f"# 'correct' good Tamil into), but their counts let the engine spot rare\n"
            f"# real words and proper nouns and leave them alone.\n"
            f"# Generated by scripts/build-lexicon.py.\n"
        )
        for w, n in emitted:
            fh.write(f"{w}\t{n}\n")

    rng.shuffle(holdout)
    holdout = holdout[:5000]
    args.holdout_out.parent.mkdir(parents=True, exist_ok=True)
    _hopen = (lambda p: gzip.open(p, "wt", encoding="utf-8")) if args.holdout_out.suffix == ".gz" else (lambda p: p.open("w", encoding="utf-8"))
    with _hopen(args.holdout_out) as fh:
        fh.write("\n".join(holdout) + "\n")

    stats = {
        "pages": pages,
        "train_pages": train_pages,
        "distinct_words": len(counts),
        "trusted": trusted,
        "recorded": len(emitted),
        "min_count": args.min_count,
        "min_emit": args.min_emit,
        "holdout_lines": len(holdout),
    }
    print(json.dumps(stats, indent=2))
    print(f"\nwrote {args.out}  ({trusted:,} trusted / {len(emitted):,} recorded)", file=sys.stderr)
    print(f"wrote {args.holdout_out}  ({len(holdout):,} held-out lines)", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())

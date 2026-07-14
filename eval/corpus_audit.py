#!/usr/bin/env python3
"""Held-out false-positive audit — the honest test of RISK R1.

eval/run.py scores the engine on 26 hand-written cases. That catches regressions
but says nothing about how Tier 1 behaves on real Tamil prose it has never seen,
which is the thing that actually decides whether users trust it.

This does. It runs the engine over held-out Wikipedia sentences — articles
excluded from the lexicon build — and counts how often it flags something.

The key assumption, stated plainly: **held-out Wikipedia prose is treated as
CORRECT**. It is not perfectly correct; Wikipedia has real typos. So the number
this prints is an UPPER BOUND on the false-positive rate: some "false positives"
are genuine catches. That asymmetry is fine, because the number we must not fool
ourselves about is the one that destroys trust, and bounding it from above is the
conservative direction.

Every flag is printed. Read them: they are the fastest way to see what the
lexicon is still missing.

Run: make audit
"""

from __future__ import annotations

import argparse
import gzip
import sys
from collections import Counter
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(ROOT / "apps" / "ml"))

from app.tamil.engine import TamilEngine  # noqa: E402

CONFIDENCE_GATE = 0.85

# The rate at which Tier 1 may touch a sentence of correct prose. Above this,
# the writer sees the product as broken and stops trusting every suggestion,
# including the right ones.
MAX_SENTENCE_FPR = 0.02


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--holdout", type=Path, default=ROOT / "eval" / "holdout.txt.gz")
    ap.add_argument("--limit", type=int, default=5000)
    ap.add_argument("--show", type=int, default=25)
    ap.add_argument("--max-fpr", type=float, default=MAX_SENTENCE_FPR)
    args = ap.parse_args()

    if not args.holdout.exists():
        print(f"no held-out set at {args.holdout} — run `make lexicon` first.")
        return 2

    opener = gzip.open if args.holdout.suffix == ".gz" else open
    with opener(args.holdout, "rt", encoding="utf-8") as fh:
        lines = [ln.strip() for ln in fh]
    lines = [ln for ln in lines if ln][: args.limit]

    engine = TamilEngine()
    if engine.lexicon.is_empty:
        print("FATAL: lexicon empty.")
        return 2

    flagged_sentences = 0
    flags: list[tuple[str, str, str, str]] = []
    by_type: Counter[str] = Counter()

    for line in lines:
        got = [s for s in engine.analyze(line) if s.confidence >= CONFIDENCE_GATE]
        if not got:
            continue
        flagged_sentences += 1
        for s in got:
            by_type[s.type] += 1
            flags.append((s.type, s.original, s.suggestion, line))

    total = len(lines)
    fpr = flagged_sentences / total if total else 0.0

    print("=" * 72)
    print("  Held-out false-positive audit (RISK R1)")
    print("=" * 72)
    print(f"  lexicon                {len(engine.lexicon):,} words")
    print(f"  held-out sentences     {total:,}  (never seen by the lexicon build)")
    print()
    print(f"  sentences flagged      {flagged_sentences:,}")
    print(f"  total flags            {len(flags):,}")
    for t, n in by_type.most_common():
        print(f"      {t:<10} {n:,}")
    print()
    print(f"  sentence-level FPR     {fpr:.2%}   (gate: <= {args.max_fpr:.0%})")
    print("=" * 72)
    print(
        "\n  Wikipedia prose is treated as correct, so this is an UPPER BOUND:\n"
        "  some flags below are real typos the engine legitimately caught.\n"
    )

    if flags:
        print(f"  Sample flags (first {min(args.show, len(flags))}):\n")
        for t, orig, sugg, line in flags[: args.show]:
            snippet = line if len(line) <= 70 else line[:70] + "…"
            print(f"    [{t}] {orig} -> {sugg}")
            print(f"        {snippet}")
    print()

    if fpr > args.max_fpr:
        print(f"FAIL: sentence-level FPR {fpr:.2%} exceeds {args.max_fpr:.0%}.")
        print("      The lexicon is still too thin to put Tier 1 in front of users.")
        return 1

    print("PASS")
    return 0


if __name__ == "__main__":
    sys.exit(main())

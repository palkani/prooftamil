#!/usr/bin/env python3
"""Accuracy eval harness (plan §11) — how "99%" gets measured.

Runs the cascade's deterministic tiers over eval/testset.yaml and prints a
scorecard. Exits non-zero if precision or the clean-sentence false-positive rate
regresses past the thresholds below, so CI can gate on it.

Run: make eval

Metrics, and why these:

  precision   of the corrections we SHOWED, how many were right.
              The number that decides whether a writer trusts the product. A
              single confident wrong "correction" of their good Tamil costs more
              trust than ten misses.

  recall      of the errors that existed, how many we caught.
              Deliberately LOW for Tier 1 — it cannot see real-word errors,
              grammar, or agreement. Those are the model tiers' job. Low recall
              here is a design outcome, not a bug.

  clean FPR   of sentences that were already correct, how many we touched.
              This is the trust-killer metric. Target: zero.
"""

from __future__ import annotations

import sys
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(ROOT / "apps" / "ml"))

from app.tamil.engine import TamilEngine  # noqa: E402

# CI gates. Tier 1 either corrects a non-word into a word or says nothing, so it
# should essentially never be wrong when it does speak.
MIN_PRECISION = 0.99
MAX_CLEAN_FPR = 0.0

# The confidence gate the orchestrator applies (CONFIDENCE_GATE, §3.1). Evaluate
# what the USER would actually see, not everything the engine considered.
CONFIDENCE_GATE = 0.85


def match(got, want) -> bool:
    """A suggestion counts as correct only if it changes the right word INTO the
    right word. Getting the span right but the correction wrong is still wrong."""
    return (
        got.original == want["original"]
        and got.suggestion == want["suggestion"]
        and got.type == want["type"]
    )


def main() -> int:
    with (ROOT / "eval" / "testset.yaml").open(encoding="utf-8") as fh:
        cases = yaml.safe_load(fh)["cases"]

    engine = TamilEngine()
    if engine.lexicon.is_empty:
        print("FATAL: lexicon failed to load — every word would look misspelled.")
        return 2

    tp = fp = fn = 0
    clean_total = clean_touched = 0
    failures: list[str] = []

    for case in cases:
        text = case["text"]
        want = case.get("expect", []) or []

        got = [s for s in engine.analyze(text) if s.confidence >= CONFIDENCE_GATE]

        if not want:
            clean_total += 1
            if got:
                clean_touched += 1
                for s in got:
                    fp += 1
                    failures.append(
                        f"  FALSE POSITIVE  {text!r}\n"
                        f"      flagged {s.original!r} -> {s.suggestion!r} "
                        f"({s.type}, conf {s.confidence:.2f})\n"
                        f"      but this sentence is correct: {case.get('note','').strip()}"
                    )
            continue

        matched_want = set()
        for s in got:
            hit = next(
                (i for i, w in enumerate(want) if i not in matched_want and match(s, w)),
                None,
            )
            if hit is None:
                fp += 1
                failures.append(
                    f"  FALSE POSITIVE  {text!r}\n"
                    f"      flagged {s.original!r} -> {s.suggestion!r} ({s.type})"
                )
            else:
                tp += 1
                matched_want.add(hit)

        for i, w in enumerate(want):
            if i not in matched_want:
                fn += 1
                failures.append(
                    f"  MISSED          {text!r}\n"
                    f"      expected {w['original']!r} -> {w['suggestion']!r} ({w['type']})"
                )

    precision = tp / (tp + fp) if (tp + fp) else 1.0
    recall = tp / (tp + fn) if (tp + fn) else 1.0
    clean_fpr = clean_touched / clean_total if clean_total else 0.0

    print("=" * 68)
    print(f"  ProofTamil eval — cascade Tier 1        ({len(cases)} cases)")
    print("=" * 68)
    print(f"  lexicon              {len(engine.lexicon)} words")
    print(f"  sandhi rules         {len(engine.rules.get('rules', []))}")
    print(f"  confidence gate      {CONFIDENCE_GATE}")
    print()
    print(f"  true positives       {tp}")
    print(f"  false positives      {fp}")
    print(f"  missed (Tier 1)      {fn}")
    print()
    print(f"  precision            {precision:.1%}   (gate: >= {MIN_PRECISION:.0%})")
    print(f"  recall               {recall:.1%}   (low by design — see below)")
    print(f"  clean-sentence FPR   {clean_fpr:.1%}   (gate: <= {MAX_CLEAN_FPR:.0%})  "
          f"[{clean_touched}/{clean_total} correct sentences touched]")
    print("=" * 68)

    if failures:
        print("\nFAILURES\n")
        print("\n".join(failures))
        print()

    print(
        "\nNOTE: recall is measured against what Tier 1 is ASKED to catch. It cannot\n"
        "see real-word errors (புலி/புளி), grammar or agreement — those fall through\n"
        "to the model tiers by design. Do not 'fix' low recall by loosening Tier 1;\n"
        "that trades trust for coverage in the wrong direction.\n"
    )

    ok = True
    if precision < MIN_PRECISION:
        print(f"FAIL: precision {precision:.1%} < {MIN_PRECISION:.0%}")
        ok = False
    if clean_fpr > MAX_CLEAN_FPR:
        print(f"FAIL: clean-sentence FPR {clean_fpr:.1%} > {MAX_CLEAN_FPR:.0%}")
        ok = False

    if ok:
        print("PASS")
        return 0
    return 1


if __name__ == "__main__":
    sys.exit(main())

#!/usr/bin/env python3
"""Tamil IME accuracy harness (RFC-001 §8).

Without this we would be guessing about whether the IME actually works.

Metrics, and why these:

  top-1   the intended word is the DEFAULT (first in the dropdown). This is what a
          fast typist feels: they type, glance, and hit Enter without reading.

  top-3   the intended word is VISIBLE without scrolling. This is the metric that
          actually matters, because the IME is a chooser, not an oracle — the user is
          picking from a list. Gated in CI.

  MRR     overall ranking quality (1/rank, averaged). Catches "it's there, but at #7".

Note the contrast with eval/run.py (the proofreader). There, the gated metric is the
FALSE-POSITIVE rate: an extra suggestion is a failure, because the engine is asserting.
Here, an extra suggestion is free, and a MISSING one is the failure. Same lexicon,
opposite objectives (RFC-001 §2).

Run: make eval-ime
"""

from __future__ import annotations

import statistics
import sys
import time
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(ROOT / "apps" / "ml"))

from app.tamil.translit import Transliterator  # noqa: E402

# CI gates.
MIN_TOP1 = 0.80
MIN_TOP3 = 0.95   # the one that matters: is it on screen?
MAX_P99_MS = 50.0


def main() -> int:
    with (ROOT / "eval" / "ime_testset.yaml").open(encoding="utf-8") as fh:
        cases = yaml.safe_load(fh)["cases"]

    ime = Transliterator()
    if ime.size < 1000:
        print("FATAL: IME index did not build.")
        return 2

    top1 = top3 = 0
    rr: list[float] = []
    lat: list[float] = []
    misses: list[str] = []

    for case in cases:
        typed, want = case["type"], case["want"]

        t0 = time.perf_counter()
        got = [s.word for s in ime.suggest(typed, limit=8)]
        lat.append((time.perf_counter() - t0) * 1000)

        rank = got.index(want) + 1 if want in got else 0
        if rank == 1:
            top1 += 1
        if 1 <= rank <= 3:
            top3 += 1
        rr.append(1.0 / rank if rank else 0.0)

        if rank == 0 or rank > 3:
            shown = ", ".join(got[:3]) or "(nothing)"
            where = f"rank {rank}" if rank else "NOT FOUND"
            misses.append(f"  {typed:<14} want {want:<14} {where:<10} got: {shown}")

    n = len(cases)
    p_top1, p_top3 = top1 / n, top3 / n
    mrr = statistics.mean(rr)
    lat.sort()
    p99 = lat[min(int(len(lat) * 0.99), len(lat) - 1)]

    print("=" * 70)
    print(f"  ProofTamil IME eval — romanized -> Tamil        ({n} cases)")
    print("=" * 70)
    print(f"  index                {ime.size:,} words")
    print()
    print(f"  top-1  {p_top1:6.1%}   (gate >= {MIN_TOP1:.0%})  the default, hit Enter blind")
    print(f"  top-3  {p_top3:6.1%}   (gate >= {MIN_TOP3:.0%})  visible without scrolling  <-- the one that matters")
    print(f"  MRR    {mrr:6.3f}")
    print()
    print(f"  latency  p50 {statistics.median(lat):.2f}ms   p99 {p99:.2f}ms   (gate p99 <= {MAX_P99_MS:.0f}ms)")
    print("=" * 70)

    if misses:
        print(f"\n  Not in the top 3 ({len(misses)}):\n")
        print("\n".join(misses))

    print(
        "\n  Unlike the proofreader, an EXTRA candidate here costs nothing — the user\n"
        "  picks. A MISSING one is the failure. Do not 'fix' a miss by making the\n"
        "  proofreader chattier; they are separate engines with opposite contracts.\n"
    )

    ok = True
    if p_top1 < MIN_TOP1:
        print(f"FAIL: top-1 {p_top1:.1%} < {MIN_TOP1:.0%}")
        ok = False
    if p_top3 < MIN_TOP3:
        print(f"FAIL: top-3 {p_top3:.1%} < {MIN_TOP3:.0%}")
        ok = False
    if p99 > MAX_P99_MS:
        print(f"FAIL: p99 {p99:.1f}ms > {MAX_P99_MS:.0f}ms — typing would feel laggy")
        ok = False

    print("PASS" if ok else "")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())

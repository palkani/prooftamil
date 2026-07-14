#!/usr/bin/env python3
"""Build the client-side IME index (RFC-001 §4, steps 4-5).

Today every keystroke is a round trip to the server. That is fine on localhost and NOT
fine from India, where the RTT to the API alone can exceed the entire latency budget an
IME has to feel instant. This ships the common words to the browser so most keystrokes
never leave the device.

WHY TOP-N AND NOT EVERYTHING: Tamil word frequency is Zipfian, so a small head of the
distribution carries most real usage:

    top  5,000 words -> 62% of usage      0.04 MB gzipped
    top 30,000 words -> 83% of usage      0.30 MB gzipped   <-- shipped
    top 50,000 words -> 88% of usage      0.52 MB gzipped
    all 349,633 words                     ~4 MB

The last 17% is a long tail of rare words and proper nouns. Shipping it would multiply
the payload for a slice of keystrokes the server can answer perfectly well — so the
client handles the head, and anything it misses falls through to /api/v1/suggest.

THE FOLD RULES ARE SHIPPED IN THE ARTIFACT, NOT REIMPLEMENTED IN JS. They are the map
from "what a human types" to the sound key (ndr->nr, zh->l, th->t...), and they live in
packages/tamil-rules/translit/scheme.yaml. Hand-copying them into TypeScript would work
exactly until someone edits the YAML and forgets the copy — and then the client and the
server would disagree about what a word sounds like, silently, for the subset of users on
the fast path. One source of truth, serialised.
"""

from __future__ import annotations

import argparse
import gzip
import json
import math
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(ROOT / "apps" / "ml"))

from app.tamil.lexicon import CURATED, Lexicon  # noqa: E402
from app.tamil.translit import Transliterator  # noqa: E402

DEFAULT_OUT = ROOT / "apps" / "web" / "public" / "ime-index.v1.json"


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--words", type=int, default=30000,
                    help="how many of the most frequent words to ship")
    ap.add_argument("--out", type=Path, default=DEFAULT_OUT)
    args = ap.parse_args()

    lex = Lexicon.load()
    ime = Transliterator(lexicon=lex)

    if lex.is_empty:
        print("FATAL: lexicon did not load.", file=sys.stderr)
        return 2

    # Rank by frequency, with the CURATED seed entries FIRST.
    #
    # An earlier version sorted curated words to the BOTTOM, reasoning that their sentinel
    # count (1e9) is not a real frequency. That was a disaster: the seed list is exactly
    # the most common words in the language — தமிழ், நான், பள்ளி, நன்றி — so they fell out
    # of the top-30k entirely, and the client index answered `tamil` with தமிழா and `naan`
    # with நாண். The most common words in Tamil became the ones the fast path could not
    # find.
    #
    # A human vouching for a word is the strongest possible signal that it belongs in a
    # 30,000-word index. They cost 167 slots out of 30,000.
    scored = sorted(
        ((w, c) for w, c in lex.items() if c >= 5),
        key=lambda x: -x[1],
    )
    top = scored[: args.words]

    curated_in_top = sum(1 for _, c in top if c >= CURATED)

    # Each entry is [word, score] where score = round(log1p(count) * 100).
    #
    # THE COUNTS ARE NOT OPTIONAL, and I shipped an index without them once and the parity
    # gate caught it. The client must RANK, not merely order exact-before-prefix: typing
    # "tam" has to yield தமிழ் (79,058 occurrences, a prefix match) rather than தம் (a rare
    # exact match). Without a frequency the client cannot make that comparison, so it
    # cannot reproduce the server's answer — and a fast path that gives different answers
    # than the slow one is worse than no fast path.
    #
    # Log-scaled and integer-encoded to keep it cheap: two extra bytes per word, and the
    # client just divides by 100.
    index: dict[str, list[list]] = {}
    for word, count in top:
        key = ime.sound_key(word)
        if key:
            index.setdefault(key, []).append([word, round(math.log1p(count) * 100)])
    artifact = {
        "v": 1,
        "words": len(top),
        # The fold rules, straight from scheme.yaml. Order matters: multi-letter clusters
        # must be applied before the single letters they contain.
        "fold": [list(pair) for pair in ime._fold],
        "idx": index,
    }

    args.out.parent.mkdir(parents=True, exist_ok=True)
    blob = json.dumps(artifact, ensure_ascii=False, separators=(",", ":")).encode()
    args.out.write_bytes(blob)

    gz = len(gzip.compress(blob, 9))
    print(f"wrote {args.out.relative_to(ROOT)}")
    print(f"  words   {len(top):,}  ({curated_in_top} curated)")
    print(f"  keys    {len(index):,}")
    print(f"  raw     {len(blob) / 1e6:.2f} MB")
    print(f"  gzipped {gz / 1e6:.2f} MB   (what the browser actually downloads)")

    # --- PARITY GATE ------------------------------------------------------
    #
    # The client and the server must agree. If they do not, a user on the fast path gets
    # different — worse — answers than one whose index has not loaded yet, and nothing
    # anywhere would report it. That is the kind of bug that survives for months.
    #
    # So: take the eval set's queries, resolve each against THIS artifact exactly as the
    # browser will, and require the same top-1 the server produces. A mismatch fails the
    # build rather than shipping a fast path that quietly lies.
    print()
    mismatches = []
    for typed, want in _eval_cases():
        client_top = _resolve_like_client(index, artifact["fold"], typed)
        server_top = next(
            (s.word for s in ime.suggest(typed, limit=1) if s.source == "lexicon"), None
        )
        # The client legitimately has no answer for a word outside the top-N — that falls
        # through to the server at runtime, so it is not a mismatch. Disagreeing about a
        # word it DOES have is.
        if client_top is not None and client_top != server_top:
            mismatches.append((typed, client_top, server_top, want))

    if mismatches:
        print(f"PARITY FAILED — the client disagrees with the server on {len(mismatches)} queries:\n")
        for typed, c, s, want in mismatches[:12]:
            print(f"  {typed:<14} client={c!r:<16} server={s!r:<16} (intended {want!r})")
        print("\nA fast path that gives different answers than the slow one is worse than no")
        print("fast path at all. Not shipping this index.")
        return 1

    print("parity: client and server agree on every eval query")
    return 0


def _eval_cases() -> list[tuple[str, str]]:
    """The IME eval set — the same cases `make eval-ime` gates on."""
    import yaml

    path = ROOT / "eval" / "ime_testset.yaml"
    if not path.exists():
        return []
    with path.open(encoding="utf-8") as fh:
        return [(c["type"], c["want"]) for c in yaml.safe_load(fh)["cases"]]


def _resolve_like_client(
    idx: dict[str, list[list]], fold_rules: list[list], query: str
) -> str | None:
    """Resolve a query EXACTLY as lib/ime-local.ts does.

    Deliberately a re-implementation of the client, not a call into the server engine —
    the whole point is to catch the two drifting apart. It must mirror ime-local.ts step
    for step.
    """
    import re

    q = query.lower().strip()
    for a, b in fold_rules:
        q = q.replace(a, b)
    key = re.sub(r"(.)\1+", r"\1", q)
    if not key:
        return None

    EXACT_BONUS = 300  # matches the server's _EXACT_BONUS of 3.0, on the x100 scale

    pool: dict[str, int] = {}

    probes = [key]
    if len(key) > 2 and key.endswith("u"):
        probes.append(key[:-1])
    for probe in probes:
        for word, score in idx.get(probe, []):
            pool[word] = max(pool.get(word, 0), score + EXACT_BONUS)

    for k in sorted(idx):
        if k.startswith(key):
            for word, score in idx[k]:
                if word not in pool:
                    pool[word] = score

    if not pool:
        return None
    return max(pool.items(), key=lambda kv: kv[1])[0]


if __name__ == "__main__":
    sys.exit(main())

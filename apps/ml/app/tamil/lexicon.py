"""The Tamil lexicon — the precision backbone of cascade Tier 1.

Everything Tier 1 claims rests on this: a word is only ever flagged when it is
absent from the lexicon. So lexicon *coverage* is the single knob that trades
recall against false positives:

  - A word missing from the lexicon that is actually correct  -> FALSE POSITIVE.
  - A word present in the lexicon that is actually misspelled -> missed (a job
    for the model tiers, not for us).

Because a false positive is far more damaging to a writer's trust than a miss,
Tier 1 is deliberately biased toward silence: it stays quiet unless it can point
at a specific, in-lexicon replacement.

The seed list shipped in packages/tamil-rules/dictionaries/ is small and exists
to make the engine testable end-to-end. It is NOT sufficient for production —
see `scripts/build-lexicon.py` and plan §11: real coverage comes from a corpus
(Wikipedia + the project's own accepted-correction event log).
"""

from __future__ import annotations

import gzip
import logging
from pathlib import Path

from .script import normalize

log = logging.getLogger(__name__)

# packages/tamil-rules/dictionaries/, resolved from this file's location so the
# service works the same from a repo checkout and from inside a container.
#
# The repo-relative candidate is GUARDED: in a container the app lives at
# /app/app/tamil, which has no parents[4], and an eager index there raises
# IndexError at import — before the /data fallback is ever tried. So only offer the
# repo path when the source tree is actually deep enough.
_HERE = Path(__file__).resolve()
_DEFAULT_DIRS = tuple(
    d
    for d in (
        _HERE.parents[4] / "packages" / "tamil-rules" / "dictionaries"
        if len(_HERE.parents) > 4
        else None,
        Path("/data/tamil-rules/dictionaries"),  # container mount
    )
    if d is not None
)


# A word must be seen at least this often in the corpus to be TRUSTED: treated as
# in-vocabulary (never flagged) and eligible to be a correction target.
#
# Words seen less often are still RECORDED, with their counts. That distinction is
# what lets the engine tell a rare-but-real word (a place name like திருக்காணூர்,
# seen a handful of times) from a genuine typo (அணைவருக்கும், seen once against a
# candidate seen thousands of times). Without the rare counts both are merely
# "absent" and the engine would confidently rewrite the place name.
TRUSTED_MIN_COUNT = 5

# Hand-curated entries (the seed file, which has no counts) are given an effectively
# infinite count: a human vouched for them, so no corpus evidence can outvote that.
CURATED = 10**9


class Lexicon:
    def __init__(self, counts: dict[str, int] | None = None) -> None:
        self._counts: dict[str, int] = counts or {}

    def __len__(self) -> int:
        """The number of TRUSTED words — what "lexicon size" means everywhere else."""
        return sum(1 for n in self._counts.values() if n >= TRUSTED_MIN_COUNT)

    def __contains__(self, word: str) -> bool:
        """In-vocabulary = trusted. A recorded-but-rare word is NOT in-vocabulary:
        it can still be flagged, but only if the frequency ratio justifies it."""
        return self._counts.get(normalize(word), 0) >= TRUSTED_MIN_COUNT

    def count(self, word: str) -> int:
        """Corpus frequency; 0 if never seen. Used for the ratio guard."""
        return self._counts.get(normalize(word), 0)

    def items(self):
        """Every recorded (word, count), INCLUDING the rare ones below the trust
        threshold.

        The IME needs these: a rare word is still a word someone wants to type, and
        refusing to type it would be a failure. The proofreader must not use this —
        it needs `word in lexicon`, which enforces the trust threshold. (RFC-001 §2:
        the two features want opposite things from the same data.)
        """
        return self._counts.items()

    @property
    def is_empty(self) -> bool:
        return not self._counts

    def add(self, word: str, count: int = CURATED) -> None:
        word = normalize(word.strip())
        if word:
            # A word may appear in both the curated seed and the corpus; keep the
            # higher count so curation always wins.
            self._counts[word] = max(self._counts.get(word, 0), count)

    @classmethod
    def load(cls, directory: Path | None = None) -> Lexicon:
        """Load every *.txt in the dictionary directory.

        Two accepted line formats:
            <word>              a curated entry (seed.txt) — trusted unconditionally
            <word>\t<count>     a corpus entry (corpus.txt) — trusted iff count is high
        """
        lex = cls()

        dirs = [directory] if directory else list(_DEFAULT_DIRS)
        chosen = next((d for d in dirs if d and d.is_dir()), None)

        if chosen is None:
            # Loud, because an empty lexicon silently disables Tier 1 spelling
            # checks rather than crashing — the worst kind of failure: quiet.
            log.warning(
                "no Tamil dictionary directory found (looked in: %s); "
                "Tier 1 spelling checks are DISABLED",
                ", ".join(str(d) for d in dirs),
            )
            return lex

        # The corpus lexicon is ~28 MB of text but 3.8 MB gzipped, so it ships
        # compressed — small enough to live in git, which keeps CI hermetic
        # (no 270 MB Wikipedia download on every run).
        paths = sorted(chosen.glob("*.txt")) + sorted(chosen.glob("*.txt.gz"))
        for path in paths:
            opener = (
                (lambda p: gzip.open(p, "rt", encoding="utf-8"))
                if path.suffix == ".gz"
                else (lambda p: p.open(encoding="utf-8"))
            )
            with opener(path) as fh:
                for line in fh:
                    line = line.split("#", 1)[0].strip()
                    if not line:
                        continue
                    word, _, raw = line.partition("\t")
                    if raw:
                        try:
                            lex.add(word, int(raw))
                        except ValueError:
                            continue
                    else:
                        lex.add(word)  # curated

        log.info(
            "lexicon loaded from %s: %d trusted, %d recorded",
            chosen, len(lex), len(lex._counts),
        )
        return lex

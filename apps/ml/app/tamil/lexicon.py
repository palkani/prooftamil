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

import logging
from pathlib import Path

from .script import normalize

log = logging.getLogger(__name__)

# packages/tamil-rules/dictionaries/, resolved from this file's location so the
# service works the same from a repo checkout and from inside a container.
_DEFAULT_DIRS = (
    Path(__file__).resolve().parents[4] / "packages" / "tamil-rules" / "dictionaries",
    Path("/data/tamil-rules/dictionaries"),  # container mount
)


class Lexicon:
    def __init__(self, words: set[str] | None = None) -> None:
        self._words: set[str] = words or set()

    def __len__(self) -> int:
        return len(self._words)

    def __contains__(self, word: str) -> bool:
        return normalize(word) in self._words

    @property
    def is_empty(self) -> bool:
        return not self._words

    def add(self, word: str) -> None:
        word = normalize(word.strip())
        if word:
            self._words.add(word)

    @classmethod
    def load(cls, directory: Path | None = None) -> Lexicon:
        """Load every *.txt in the dictionary directory (one word per line,
        `#` comments ignored)."""
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

        for path in sorted(chosen.glob("*.txt")):
            with path.open(encoding="utf-8") as fh:
                for line in fh:
                    line = line.split("#", 1)[0].strip()
                    if line:
                        lex.add(line)

        log.info("lexicon loaded: %d words from %s", len(lex), chosen)
        return lex

"""Tamil script primitives: tokenization and confusable-letter sets.

Offsets produced here are **codepoint** offsets into the target string, which is
what the cascade contract (plan §8.1) specifies. The Go orchestrator indexes by
rune, not byte, so the two agree.
"""

from __future__ import annotations

import re
import unicodedata

# Tamil block: U+0B80–U+0BFF. A "word" is a run of Tamil letters, combining vowel
# signs and the virama (pulli). Latin/digits are matched too so mixed text
# tokenizes sanely, but only Tamil-bearing tokens are ever analyzed.
_WORD_RE = re.compile("[஀-௿]+|[A-Za-z]+|[0-9]+")

TAMIL_RANGE = (0x0B80, 0x0BFF)


def is_tamil(token: str) -> bool:
    return any(TAMIL_RANGE[0] <= ord(ch) <= TAMIL_RANGE[1] for ch in token)


class Token:
    __slots__ = ("text", "start", "end")

    def __init__(self, text: str, start: int, end: int) -> None:
        self.text = text
        self.start = start  # codepoint offset, inclusive
        self.end = end      # codepoint offset, exclusive

    def __repr__(self) -> str:  # pragma: no cover - debug aid
        return f"Token({self.text!r}, {self.start}, {self.end})"


def tokenize(text: str) -> list[Token]:
    """Split text into word tokens carrying their codepoint offsets."""
    return [Token(m.group(), m.start(), m.end()) for m in _WORD_RE.finditer(text)]


def normalize(text: str) -> str:
    """NFC-normalize.

    Tamil vowel signs can arrive decomposed — notably ெ + ா vs the single
    codepoint ொ. Two spellings that look identical must compare equal, or the
    lexicon would report false out-of-vocabulary hits on correctly spelled words.
    """
    return unicodedata.normalize("NFC", text)


# --- Confusable consonants -------------------------------------------------
#
# These are THE classic Tamil orthography confusions. Each set contains letters
# that are phonetically close and routinely mistyped for one another, but every
# member is a perfectly valid letter — which is exactly why a swap may only ever
# be *suggested* when the original word is out-of-vocabulary and the swap yields
# an in-vocabulary word. See engine.py.
CONFUSABLE_SETS: tuple[frozenset[str], ...] = (
    frozenset("லளழ"),   # la / ḷa / ḻa  — e.g. புலி (tiger) vs புளி (tamarind)
    frozenset("ணனந"),   # ṇa / na / n̪a
    frozenset("ரற"),    # ra / ṟa
)

# letter -> the other letters it is confusable with
CONFUSABLES: dict[str, tuple[str, ...]] = {
    ch: tuple(sorted(group - {ch}))
    for group in CONFUSABLE_SETS
    for ch in group
}


def confusable_variants(word: str) -> list[tuple[str, int, str, str]]:
    """Every single-substitution variant of `word` over the confusable sets.

    Returns (variant, index, original_letter, replacement_letter).

    Only ONE letter is swapped per variant. Edit distance is capped at 1 on
    purpose: a two-letter swap can reach an unrelated word, and the resulting
    suggestion would be a confident-sounding guess rather than a correction.

    Consonants and their vowel signs are separate codepoints in Tamil, so
    substituting the consonant leaves any following vowel sign intact —
    e.g. புலி -> புளி keeps the ி.
    """
    out: list[tuple[str, int, str, str]] = []
    for i, ch in enumerate(word):
        for alt in CONFUSABLES.get(ch, ()):
            out.append((word[:i] + alt + word[i + 1:], i, ch, alt))
    return out

"""Tamil IME — romanized input to Tamil script (RFC-001; plan GAP-1).

The user types `vanakkam` and wants வணக்கம். They have no Tamil keyboard; this is how
they enter text at all.

HOW IT WORKS
------------
Fold both sides into one lossy "sound key", then look up:

    வணக்கம்  --sound_key-->  "vanakam"  <--fold--  "vanakkam" / "wanakam"

The folding deliberately destroys the distinctions people do not reliably type —
ல/ள/ழ all become "l", ண/ன/ந all become "n", vowel length is discarded, doubled
consonants collapse. Candidates are then ranked by corpus frequency, which (measured)
already puts the right answer first for the common cases.

THE CONTRACT IS THE OPPOSITE OF THE PROOFREADER'S
-------------------------------------------------
Tier 1's spellchecker exists to CATCH the ல/ள/ழ confusion. The IME must deliberately
IGNORE it. Same lexicon, opposite objectives:

    proofreader : optimizes PRECISION. Asserts. Must stay silent when unsure,
                  because "correcting" good Tamil destroys trust.
    IME         : optimizes RECALL. Offers. Should show every plausible candidate,
                  because the human picks and a bad candidate costs nothing.

Showing 4 candidates when 1 is right is a SUCCESS here and a FAILURE there. So this
module must NOT reuse Tier 1's TRUSTED_MIN_COUNT or its frequency-ratio guard: those
enforce silence, and would make the IME refuse to type rare words — exactly when a
user most needs help.

No model is involved. Zero cost per keystroke.
"""

from __future__ import annotations

import bisect
import logging
import math
import re
from dataclasses import dataclass
from pathlib import Path

import yaml

from .lexicon import Lexicon
from .script import normalize

log = logging.getLogger(__name__)

# Guard the repo-relative path: in a container this file has no parents[4], so an
# eager index would crash at import before the /data fallback is reached.
_HERE = Path(__file__).resolve()
_SCHEME_DIRS = tuple(
    d
    for d in (
        _HERE.parents[4] / "packages" / "tamil-rules" / "translit"
        if len(_HERE.parents) > 4
        else None,
        Path("/data/tamil-rules/translit"),
    )
    if d is not None
)

# Words rarer than this are still typeable, just ranked below common ones. This is
# NOT Tier 1's trust threshold — the IME must be able to type rare words.
_MIN_COUNT = 2

_DOUBLES = re.compile(r"(.)\1+")

# Worth ~20x in corpus frequency (log scale). Enough to prefer a completed word over
# a longer one, never enough to bury a far commoner word beneath a rare exact match.
_EXACT_BONUS = 3.0

# Safety bound on the prefix scan. A 1-letter prefix matches tens of thousands of keys;
# scanning them all on every keystroke is the one thing that would make typing feel
# slow. At 2-3 letters — where a user actually reads the dropdown — the real range is
# far smaller than this and gets scanned in full.
_MAX_PREFIX_KEYS = 4000


@dataclass
class Suggestion:
    word: str
    score: float
    source: str  # "lexicon" | "generated"


class Transliterator:
    def __init__(self, lexicon: Lexicon | None = None, scheme: dict | None = None) -> None:
        self.scheme = scheme if scheme is not None else _load_scheme()
        self._cons: dict[str, str] = self.scheme.get("consonants", {})
        self._signs: dict[str, str] = self.scheme.get("vowel_signs", {})
        self._vowels: dict[str, str] = self.scheme.get("vowels", {})
        self._fold: list[tuple[str, str]] = [
            (a, b) for a, b in self.scheme.get("query_fold", [])
        ]
        self._gen: dict = self.scheme.get("generate", {})

        lex = lexicon if lexicon is not None else Lexicon.load()
        self._index: dict[str, list[str]] = {}
        self._counts: dict[str, int] = {}
        self._build(lex)

    # -- index ------------------------------------------------------------

    def _build(self, lex: Lexicon) -> None:
        """Reverse index: sound key -> the Tamil words that sound like it."""
        for word, count in lex.items():
            if count < _MIN_COUNT or not word:
                continue
            key = self.sound_key(word)
            if not key:
                continue
            self._index.setdefault(key, []).append(word)
            self._counts[word] = count

        # Rank each bucket once, at build time, so a keystroke is a dict lookup and
        # nothing more.
        for _key, words in self._index.items():
            words.sort(key=lambda w: -self._counts[w])

        # Sorted keys enable prefix search (the user types a partial word).
        self._keys = sorted(self._index)
        log.info("IME index: %d keys, %d words", len(self._index), len(self._counts))

    @property
    def size(self) -> int:
        return len(self._counts)

    # -- keys -------------------------------------------------------------

    def sound_key(self, word: str) -> str:
        """Tamil script -> canonical sound key."""
        word = normalize(word)
        out: list[str] = []
        i = 0
        while i < len(word):
            ch = word[i]
            if ch in self._vowels:
                out.append(self._vowels[ch])
            elif ch in self._cons:
                out.append(self._cons[ch])
                nxt = word[i + 1] if i + 1 < len(word) else ""
                if nxt == "்":       # pulli — consonant carries no vowel
                    i += 1
                elif nxt in self._signs:  # explicit vowel sign
                    out.append(self._signs[nxt])
                    i += 1
                else:
                    out.append("a")       # inherent 'a'
            i += 1
        return _DOUBLES.sub(r"\1", "".join(out))

    def fold(self, query: str) -> str:
        """User's romanized typing -> the same canonical sound key.

        Order matters: multi-letter clusters are folded before the single letters
        they contain, so `ndr` is handled before `d`. That ordering is what makes
        "sendren" (சென்றேன்) findable — ன்ற is pronounced "ndr" but keys as "nr".
        """
        q = query.lower().strip()
        for a, b in self._fold:
            q = q.replace(a, b)
        return _DOUBLES.sub(r"\1", q)

    # -- lookup -----------------------------------------------------------

    def suggest(self, query: str, limit: int = 8) -> list[Suggestion]:
        """Candidates for a romanized query, best first."""
        if not query or not query.strip():
            return []

        key = self.fold(query)
        if not key:
            return []

        seen: set[str] = set()
        out: list[Suggestion] = []

        # 1. Exact key match — the user finished the word.
        #
        #    Two keys are probed, not one. A Tamil word ending in a bare consonant is
        #    PRONOUNCED with a trailing schwa, so people type it: ஊர் keys as "ur" but
        #    is typed "ooru". Without the second probe that word is simply unreachable.
        for probe in _exact_probes(key):
            for word in self._index.get(probe, []):
                if word not in seen:
                    seen.add(word)
                    out.append(Suggestion(word, self._score(word, exact=True), "lexicon"))

        # 2. Prefix match — the user is mid-word, which is the common case while
        #    typing.
        #
        #    Every key in the prefix range is considered, not just the first few.
        #    An earlier version took the first `limit*3` keys in ALPHABETICAL order,
        #    which quietly cut off the best answers: "tami" returned தாமி/டாமி but not
        #    தமிழ், and "sendr" missed சென்றேன், because the common word sorted late.
        #    The candidates must be chosen by FREQUENCY, so the whole range has to be
        #    collected before ranking.
        #
        #    This runs unconditionally, even when the exact bucket already filled
        #    `limit`. Gating it on `len(out) < limit` was a second, subtler version of
        #    the same bug: typing "tami" found four rare words keying exactly to it and
        #    never looked further, so தமிழ் — frequency 79,058, obviously the intended
        #    word — was excluded before it could be ranked. Collect first, rank second.
        for k in self._prefix_keys(key):
            for word in self._index[k]:
                if word not in seen:
                    seen.add(word)
                    out.append(Suggestion(word, self._score(word, exact=False), "lexicon"))

        out.sort(key=lambda s: -s.score)
        out = out[:limit]

        # 3. Always offer a generated spelling. An IME that cannot type your own name
        #    is broken — names, loanwords and coinages will never be in the lexicon.
        #    Ranked last and marked, so it never masquerades as a dictionary word.
        generated = self.generate(query)
        if generated and generated not in seen:
            out.append(Suggestion(generated, 0.0, "generated"))

        return out

    def _prefix_keys(self, prefix: str) -> list[str]:
        """Every key starting with `prefix`, up to a safety bound.

        The bound exists for the degenerate case: a one-letter prefix matches tens of
        thousands of keys, and scanning them all on every keystroke would be the one
        thing that makes typing feel slow. At 2–3 letters (where a user actually looks
        at the dropdown) the range is small and fully scanned.
        """
        i = bisect.bisect_left(self._keys, prefix)
        hits: list[str] = []
        while i < len(self._keys) and self._keys[i].startswith(prefix):
            hits.append(self._keys[i])
            if len(hits) >= _MAX_PREFIX_KEYS:
                break
            i += 1
        return hits

    def _score(self, word: str, exact: bool) -> float:
        """Rank by corpus frequency, with a MODEST bonus for an exact key match.

        The bonus is deliberately small. An earlier version used +100, which made
        exactness dominate frequency absolutely — so typing "vanak" surfaced the rare
        words that key exactly to it (வண்ணக், வனக், வானக்) ABOVE வணக்கம், which is
        what the user obviously wanted. Mid-word, a common word that extends the
        prefix should beat a rare word that happens to end there.

        _EXACT_BONUS is on a log-frequency scale, so it is worth roughly a 20x
        frequency advantage: enough to break ties in favour of a completed word,
        not enough to bury a far commoner one.

        Personal history — the strongest signal, since people reuse their own
        vocabulary — is applied CLIENT-side, where the user's picks live and never
        have to reach a server (RFC-001 §5).
        """
        freq = math.log1p(self._counts.get(word, 1))
        return freq + (_EXACT_BONUS if exact else 0.0)

    # -- OOV --------------------------------------------------------------

    def generate(self, query: str) -> str:
        """Roman -> Tamil script, letter by letter, for words not in the lexicon.

        Greedy longest-match. Crude by nature — it has no idea whether the user meant
        ல, ள or ழ — but "here is my best attempt, press Enter" beats "cannot type
        that".
        """
        q = re.sub(r"[^a-z]", "", query.lower())
        if not q:
            return ""

        clusters: dict[str, str] = self._gen.get("clusters", {})
        cons: dict[str, str] = self._gen.get("consonants", {})
        vowels: dict[str, list[str]] = self._gen.get("vowels", {})

        out: list[str] = []
        i = 0
        at_start = True

        while i < len(q):
            # Vowel? Longest first (aa before a) so length is preserved when typed.
            matched = False
            for v in sorted(vowels, key=len, reverse=True):
                if q.startswith(v, i):
                    independent, matra = vowels[v]
                    if at_start or not out:
                        out.append(independent)
                    else:
                        # Attach the matra to the preceding consonant: strip its pulli.
                        if out and out[-1].endswith("்"):
                            out[-1] = out[-1][:-1]
                        out.append(matra)
                    i += len(v)
                    at_start = False
                    matched = True
                    break
            if matched:
                continue

            # Consonant cluster, then single consonant.
            for table in (clusters, cons):
                for c in sorted(table, key=len, reverse=True):
                    if q.startswith(c, i):
                        out.append(table[c])
                        i += len(c)
                        at_start = False
                        matched = True
                        break
                if matched:
                    break

            if not matched:
                i += 1  # unmappable character: skip rather than fail

        return normalize("".join(out))


def _exact_probes(key: str) -> list[str]:
    """The keys to try for an exact match.

    A Tamil word ending in a bare consonant (pulli) keys without a final vowel — ஊர்
    -> "ur" — but it is PRONOUNCED with a trailing schwa, so that is how people type
    it: "ooru". Probing the key with a trailing 'u' removed makes those words
    reachable. Cheap: one extra dict lookup.
    """
    probes = [key]
    if len(key) > 2 and key.endswith("u"):
        probes.append(key[:-1])
    return probes


def _load_scheme() -> dict:
    directory = next((d for d in _SCHEME_DIRS if d.is_dir()), None)
    if directory is None:
        log.warning("no translit scheme found; the IME is DISABLED")
        return {}
    with (directory / "scheme.yaml").open(encoding="utf-8") as fh:
        return yaml.safe_load(fh) or {}

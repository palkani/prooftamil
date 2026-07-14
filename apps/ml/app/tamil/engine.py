"""Cascade Tier 1 — the deterministic Tamil corrector.

Design contract, and the reason this tier is trustworthy:

    Tier 1 only ever corrects a NON-WORD into a word.

It flags a token only when the token is absent from the lexicon AND exactly one
confusable-letter substitution turns it into a token that is present. Everything
else it stays silent about.

That boundary is deliberate and it is what buys precision:

  * புலி (tiger) and புளி (tamarind) are BOTH real words. Writing one where you
    meant the other is a real-word error. Tier 1 cannot see it — detecting it
    needs sentence semantics — so it says nothing and lets the model tiers
    (§8.1/§8.2) handle it. Guessing here would mean "correcting" a writer's
    perfectly good tiger into a tamarind.
  * பரவை is not a word; பறவை (bird) is. One ர->ற swap connects them, and no
    other confusable swap of பரவை yields a word. That is a safe, unambiguous
    correction.
  * If a swap yields SEVERAL valid words, the tier is ambiguous by definition.
    It still surfaces the best candidate but with reduced confidence, so the
    confidence gate (CONFIDENCE_GATE, §3.1) can drop it.

The cost of this discipline is recall: Tier 1 misses every real-word error. That
is the correct trade. A false positive costs a writer's trust; a miss just costs
a model call.
"""

from __future__ import annotations

import logging
from dataclasses import dataclass, field
from pathlib import Path

import yaml

from .lexicon import Lexicon
from .script import Token, confusable_variants, is_tamil, normalize, tokenize

log = logging.getLogger(__name__)

_RULES_DIRS = (
    Path(__file__).resolve().parents[4] / "packages" / "tamil-rules" / "sandhi",
    Path("/data/tamil-rules/sandhi"),
)

# Confidence assigned when exactly one confusable swap yields a known word. High,
# but never 1.0: the lexicon is finite, so the "non-word" premise can always be
# wrong for a rare-but-valid word it does not contain.
_CONF_UNAMBIGUOUS = 0.90
# When several swaps yield known words we cannot choose on evidence available to
# this tier. Below the default CONFIDENCE_GATE of 0.85, so these are reported but
# suppressed unless an operator lowers the gate.
_CONF_AMBIGUOUS = 0.55

# How much more common a candidate must be than the word actually typed before we
# dare correct it. Guards proper nouns and rare real words, which are
# out-of-vocabulary but sit at a frequency comparable to their neighbours — unlike
# a true typo, which is swamped by its correction. See _spelling().
#
# Curated seed words carry an effectively infinite count, so a hand-vouched word is
# never overruled by this and a correction TO one always clears the bar.
_MIN_RATIO = 20


@dataclass
class Suggestion:
    start: int
    end: int
    original: str
    suggestion: str
    type: str
    explanation: str = ""
    confidence: float = 0.0
    source_tier: int = 1
    # Populated when a swap produced more than one valid word.
    alternatives: list[str] = field(default_factory=list)


class TamilEngine:
    def __init__(self, lexicon: Lexicon | None = None, rules: dict | None = None) -> None:
        self.lexicon = lexicon if lexicon is not None else Lexicon.load()
        self.rules = rules if rules is not None else _load_rules()

    # -- public ------------------------------------------------------------

    def analyze(self, target: str) -> list[Suggestion]:
        """Return Tier 1 suggestions for `target`, ordered by position."""
        if not target.strip():
            return []

        text = normalize(target)
        tokens = tokenize(text)

        suggestions = self._spelling(tokens)
        suggestions.extend(self._sandhi(tokens))
        suggestions.sort(key=lambda s: (s.start, s.end))
        return suggestions

    # -- rules -------------------------------------------------------------

    def _spelling(self, tokens: list[Token]) -> list[Suggestion]:
        """Confusable-letter correction, gated on the lexicon."""
        if self.lexicon.is_empty:
            # No lexicon means every word looks out-of-vocabulary. Flagging them
            # all would bury the user in false positives, so disable the check.
            return []

        out: list[Suggestion] = []
        for tok in tokens:
            if not is_tamil(tok.text):
                continue
            if tok.text in self.lexicon:
                continue  # a known word — including a real-word error we cannot see

            # Out-of-vocabulary. Can a single confusable swap rescue it?
            hits = [
                (variant, idx, src, dst)
                for variant, idx, src, dst in confusable_variants(tok.text)
                if variant in self.lexicon
            ]
            if not hits:
                # Unknown word with no near-miss. It may be a name, a loanword, a
                # rare word, or a typo we cannot repair — all indistinguishable
                # from here. Say nothing; the model tiers get a shot at it.
                continue

            # --- the proper-noun guard -----------------------------------
            #
            # Being out-of-vocabulary is not enough. Tamil proper nouns are
            # endless and most are rare, so a place name like திருக்காணூர் is
            # out-of-vocabulary AND one ண->ன swap from the real word திருக்கானூர்.
            # On the evidence so far it looks exactly like a typo, and the engine
            # would confidently rewrite someone's town.
            #
            # What separates the two is the frequency RATIO. A genuine typo is
            # vanishingly rare next to its correction (அணைவருக்கும் appears once;
            # அனைவருக்கும் thousands of times). A rare real word sits at a
            # comparable frequency to its confusable neighbour, because both are
            # simply uncommon.
            #
            # So: only correct when the candidate is overwhelmingly more common
            # than what the writer actually typed.
            observed = self.lexicon.count(tok.text)
            hits = [
                h for h in hits
                if self.lexicon.count(h[0]) >= _MIN_RATIO * max(observed, 1)
            ]
            if not hits:
                continue

            # Prefer the most frequent candidate: with several valid corrections,
            # the common word is the likelier intent.
            hits.sort(key=lambda h: self.lexicon.count(h[0]), reverse=True)

            best, _idx, src, dst = hits[0]
            unambiguous = len(hits) == 1

            out.append(
                Suggestion(
                    start=tok.start,
                    end=tok.end,
                    original=tok.text,
                    suggestion=best,
                    type="spelling",
                    explanation=f"'{src}' → '{dst}' — '{tok.text}' அகராதியில் இல்லை.",
                    confidence=_CONF_UNAMBIGUOUS if unambiguous else _CONF_AMBIGUOUS,
                    alternatives=[h[0] for h in hits[1:]],
                )
            )
        return out

    def _sandhi(self, tokens: list[Token]) -> list[Suggestion]:
        """வல்லினம் மிகுதல் after a demonstrative (packages/tamil-rules/sandhi)."""
        out: list[Suggestion] = []

        for rule in self.rules.get("rules", []):
            triggers = set(rule.get("triggers", []))
            doubling: dict[str, str] = rule.get("doubling", {})
            conf = float(rule.get("confidence", 0.9))
            explanation = rule.get("explanation_ta", "")

            # strict=False is intended: pairing each token with its successor
            # leaves the last token unpaired, and that shorter zip is the point.
            for cur, nxt in zip(tokens, tokens[1:], strict=False):
                if cur.text not in triggers or not nxt.text:
                    continue

                mey = doubling.get(nxt.text[0])
                if not mey:
                    continue  # next word does not begin with a வல்லினம்

                corrected = cur.text + mey
                out.append(
                    Suggestion(
                        start=cur.start,
                        end=cur.end,
                        original=cur.text,
                        suggestion=corrected,
                        type=rule.get("type", "sandhi"),
                        explanation=explanation,
                        confidence=conf,
                    )
                )
        return out


def _load_rules() -> dict:
    directory = next((d for d in _RULES_DIRS if d.is_dir()), None)
    if directory is None:
        log.warning("no sandhi rules directory found; sandhi checks are DISABLED")
        return {}

    merged: dict = {"rules": []}
    for path in sorted(directory.glob("*.yaml")):
        with path.open(encoding="utf-8") as fh:
            data = yaml.safe_load(fh) or {}
        merged["rules"].extend(data.get("rules", []))

    log.info("sandhi rules loaded: %d from %s", len(merged["rules"]), directory)
    return merged

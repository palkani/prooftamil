"""Tier 1 engine tests.

The false-positive tests below matter more than the true-positive ones. Tier 1's
whole claim is that it stays silent unless it is sure; a regression that makes it
chatty is worse than one that makes it miss.
"""

import pytest

from app.tamil.engine import TamilEngine
from app.tamil.lexicon import Lexicon
from app.tamil.script import confusable_variants, tokenize


@pytest.fixture(scope="module")
def engine() -> TamilEngine:
    eng = TamilEngine()
    assert not eng.lexicon.is_empty, "seed lexicon failed to load — check the path"
    return eng


# --- tokenizer -------------------------------------------------------------

def test_tokenizer_yields_codepoint_offsets():
    toks = tokenize("நான் வந்தேன்")
    assert [t.text for t in toks] == ["நான்", "வந்தேன்"]
    assert (toks[0].start, toks[0].end) == (0, 4)
    # Offsets must index the original string exactly — the Go orchestrator slices
    # by these, so an off-by-one corrupts the correction span.
    text = "நான் வந்தேன்"
    for t in toks:
        assert text[t.start:t.end] == t.text


def test_confusable_variants_preserve_vowel_signs():
    # புலி -> புளி must keep the trailing ி vowel sign intact.
    variants = {v for v, _, _, _ in confusable_variants("புலி")}
    assert "புளி" in variants
    assert "புழி" in variants


# --- true positives --------------------------------------------------------

def test_corrects_a_nonword_to_a_word(engine: TamilEngine):
    # வனிகர்கள் is not a word (0 corpus occurrences); வணிகர்கள் (merchants) is
    # (628). One ன->ண swap connects them, and the frequency ratio is decisive.
    out = engine.analyze("அவர்கள் வனிகர்கள்")
    spelling = [s for s in out if s.type == "spelling"]
    assert len(spelling) == 1

    s = spelling[0]
    assert s.original == "வனிகர்கள்"
    assert s.suggestion == "வணிகர்கள்"
    assert s.confidence >= 0.85
    assert s.source_tier == 1


def test_offsets_point_at_the_offending_word(engine: TamilEngine):
    text = "அவர்கள் வனிகர்கள்"
    s = next(s for s in engine.analyze(text) if s.type == "spelling")
    assert text[s.start:s.end] == "வனிகர்கள்"


def test_does_not_correct_a_rare_real_word(engine: TamilEngine):
    # பரவை (sea/expanse) is a REAL but uncommon word, 84 corpus occurrences, and
    # sits one ர->ற swap from the far more common பறவை (bird). An earlier version
    # of this suite asserted the opposite — the corpus proved the test wrong.
    # This is exactly the proper-noun/rare-word class the frequency ratio guards.
    assert [s for s in engine.analyze("அது ஒரு பரவை") if s.type == "spelling"] == []


# --- FALSE-POSITIVE GUARDS (the important ones) ----------------------------

def test_stays_silent_on_a_clean_sentence(engine: TamilEngine):
    assert engine.analyze("நான் பள்ளிக்கு சென்றேன்") == []


def test_never_corrects_a_real_word_into_another_real_word(engine: TamilEngine):
    # புலி (tiger) and புளி (tamarind) are both valid. If the writer meant
    # tamarind and wrote tiger, that is a real-word error Tier 1 CANNOT see.
    # It must not guess — "correcting" a valid word is the cardinal sin.
    for word in ("புலி", "புளி", "வலி", "வளி", "வழி", "பள்ளி", "பல்லி"):
        out = engine.analyze(word)
        assert [s for s in out if s.type == "spelling"] == [], (
            f"Tier 1 'corrected' the valid word {word} — false positive"
        )


def test_stays_silent_on_an_unknown_word_with_no_near_miss(engine: TamilEngine):
    # A name / loanword / rare word. Out-of-vocabulary, but no confusable swap
    # rescues it, so there is nothing safe to say. Silence, not a guess.
    out = engine.analyze("ஜெயக்குமார்")
    assert [s for s in out if s.type == "spelling"] == []


def test_disabled_without_a_lexicon():
    # An empty lexicon makes every word look out-of-vocabulary. Flagging them all
    # would bury the writer in noise, so the check must switch itself off.
    eng = TamilEngine(lexicon=Lexicon(), rules={})
    assert eng.analyze("அது ஒரு பரவை") == []


def test_ambiguous_correction_lands_below_the_confidence_gate():
    # ல, ள and ழ are mutually confusable. Give the lexicon வலி and வழி but NOT
    # வளி, then feed it வளி: swapping ள->ல reaches வலி and ள->ழ reaches வழி.
    # Two valid candidates, no way to choose between them on Tier 1 evidence.
    lex = Lexicon()
    for w in ("வலி", "வழி"):
        lex.add(w)
    eng = TamilEngine(lexicon=lex, rules={})

    out = eng.analyze("வளி")
    assert len(out) == 1
    s = out[0]

    # It must be reported (the writer typed a non-word) but land BELOW the
    # default CONFIDENCE_GATE of 0.85, so the orchestrator suppresses it rather
    # than showing a coin-flip as if it were a correction.
    assert s.confidence < 0.85, "an ambiguous correction must not clear the gate"
    assert {s.suggestion, *s.alternatives} == {"வலி", "வழி"}


# --- sandhi ----------------------------------------------------------------

def test_vallinam_doubling_after_demonstrative(engine: TamilEngine):
    # அந்த + பையன் -> அந்தப் பையன்
    out = engine.analyze("அந்த பையன் வந்தான்")
    sandhi = [s for s in out if s.type == "sandhi"]
    assert len(sandhi) == 1

    s = sandhi[0]
    assert s.original == "அந்த"
    assert s.suggestion == "அந்தப்"
    assert s.confidence >= 0.85


def test_vallinam_doubling_matches_the_following_consonant(engine: TamilEngine):
    s = next(s for s in engine.analyze("இந்த கதை நல்லது") if s.type == "sandhi")
    assert s.original == "இந்த"
    assert s.suggestion == "இந்தக்"  # க follows, so க் — not a fixed letter


def test_no_doubling_when_next_word_is_not_a_hard_consonant(engine: TamilEngine):
    # மழை starts with ம (மெல்லினம்), so no doubling applies.
    out = engine.analyze("அந்த மழை")
    assert [s for s in out if s.type == "sandhi"] == []


def test_no_doubling_when_already_correct(engine: TamilEngine):
    # அந்தப் already carries the doubling; it must not be doubled again.
    out = engine.analyze("அந்தப் பையன் வந்தான்")
    assert [s for s in out if s.type == "sandhi"] == []


def test_suggestions_are_ordered_by_position(engine: TamilEngine):
    out = engine.analyze("அந்த பையன் ஒரு பரவை பார்த்தான்")
    assert [s.start for s in out] == sorted(s.start for s in out)

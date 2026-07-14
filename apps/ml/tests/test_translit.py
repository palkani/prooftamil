"""Tamil IME tests (RFC-001).

Note the inverted contract vs the proofreader. In test_engine.py the important tests
are the ones asserting SILENCE. Here the important tests assert the intended word is
OFFERED — the user picks, so a wrong extra candidate costs nothing while a missing
one makes the tool unusable.
"""

import pytest

from app.tamil.translit import Transliterator


@pytest.fixture(scope="module")
def ime() -> Transliterator:
    t = Transliterator()
    assert t.size > 1000, "IME index failed to build — check the lexicon path"
    return t


def words(t: Transliterator, q: str, limit: int = 6) -> list[str]:
    return [s.word for s in t.suggest(q, limit=limit) if s.source == "lexicon"]


# --- the basics ------------------------------------------------------------

def test_common_words_rank_first(ime: Transliterator):
    for q, want in [
        ("vanakkam", "வணக்கம்"),
        ("tamil", "தமிழ்"),
        ("naan", "நான்"),
        ("palli", "பள்ளி"),
        ("nandri", "நன்றி"),
    ]:
        got = words(ime, q)
        assert got and got[0] == want, f"{q!r} -> {got[:3]}, wanted {want} first"


# --- phonology: the reason a single canonical key is not enough ------------

def test_consonant_clusters_that_sound_different_from_their_spelling(ime: Transliterator):
    # ன்ற is PRONOUNCED "ndr", so people type "sendren" for சென்றேன் — but the word
    # keys as "senren". Both spellings must find it. This is the case that proved a
    # naive one-key index does not work.
    for q in ("sendren", "senren"):
        assert "சென்றேன்" in words(ime, q), f"{q!r} must find சென்றேன்"

    # ங்க -> "ng", ண்ட -> "nd"
    assert "தங்கம்" in words(ime, "thangam")
    assert "வேண்டும்" in words(ime, "vendum")


def test_accepts_the_many_ways_people_spell_the_same_sound(ime: Transliterator):
    # ழ is typed zh / l / z; த as th/t/d/dh; வ as v/w; doubled consonants are
    # inconsistent. All must land on the same word.
    assert words(ime, "tamizh")[0] == "தமிழ்"
    assert words(ime, "tamil")[0] == "தமிழ்"

    assert "வந்தான்" in words(ime, "vandhan")
    assert "வந்தான்" in words(ime, "vanthan")

    assert words(ime, "vanakkam")[0] == "வணக்கம்"
    assert words(ime, "vanakam")[0] == "வணக்கம்"
    assert words(ime, "wanakam")[0] == "வணக்கம்"


# --- ranking ---------------------------------------------------------------

def test_a_common_word_beats_a_rare_exact_match(ime: Transliterator):
    # REGRESSION. "vanak" keys exactly to several rare words (வண்ணக், வனக், வானக்).
    # An early version gave exact matches a +100 bonus, which buried வணக்கம் — the
    # obviously intended word — beneath them. Exactness must be a nudge, not a veto.
    assert words(ime, "vanak")[0] == "வணக்கம்"


def test_prefix_search_is_ranked_by_frequency_not_alphabetically(ime: Transliterator):
    # REGRESSION. The prefix scan once took the first N keys in ALPHABETICAL order and
    # stopped, so "tam" surfaced தாமி/டாமி and never reached தமிழ் (frequency 79,058).
    # Candidates must be collected across the whole prefix range, THEN ranked.
    assert words(ime, "tam")[0] == "தமிழ்"
    assert words(ime, "nan")[0] == "நான்"


def test_suggestions_narrow_as_you_type(ime: Transliterator):
    # The intended word must stay reachable at every prefix length, not appear only
    # once the word is complete.
    for prefix in ("vana", "vanak", "vanakk", "vanakkam"):
        assert "வணக்கம்" in words(ime, prefix), f"lost வணக்கம் at prefix {prefix!r}"


# --- out of vocabulary -----------------------------------------------------

def test_generates_a_spelling_for_words_not_in_the_dictionary(ime: Transliterator):
    # An IME that cannot type your own name is broken. Names, loanwords and coinages
    # will never be in the lexicon, so there must always be a fallback.
    out = ime.suggest("zzyqx", limit=5)
    assert out, "must always offer something, even for an unknown word"
    assert out[-1].source == "generated"
    assert out[-1].word, "the generated form must not be empty"


def test_generated_forms_are_marked_and_ranked_last(ime: Transliterator):
    # A guess must never masquerade as a dictionary word.
    out = ime.suggest("vanakkam", limit=6)
    lexicon = [s for s in out if s.source == "lexicon"]
    generated = [s for s in out if s.source == "generated"]

    assert lexicon, "a known word must return lexicon hits"
    if generated:
        assert out.index(generated[0]) > out.index(lexicon[-1]), \
            "a generated guess must rank below every real word"


# --- edges -----------------------------------------------------------------

def test_empty_and_whitespace_queries(ime: Transliterator):
    assert ime.suggest("") == []
    assert ime.suggest("   ") == []


def test_does_not_reuse_tier1s_trust_threshold(ime: Transliterator):
    # RFC-001 §2: the IME must be able to type RARE words. Tier 1's lexicon only
    # trusts words seen >=5 times (so it stays silent), but the IME indexes down to 2 —
    # refusing to type a rare word is a failure, not caution.
    from app.tamil.lexicon import TRUSTED_MIN_COUNT
    from app.tamil.translit import _MIN_COUNT

    assert _MIN_COUNT < TRUSTED_MIN_COUNT, (
        "the IME must index rarer words than the proofreader trusts — it optimizes "
        "recall, not precision"
    )

from fastapi.testclient import TestClient

from app.main import app

client = TestClient(app)


def test_health_is_live():
    r = client.get("/health")
    assert r.status_code == 200
    assert r.json()["status"] == "ok"


def test_ready_reports_the_loaded_lexicon_and_rules():
    r = client.get("/ready")
    assert r.status_code == 200
    body = r.json()
    # An empty lexicon silently disables Tier 1 spelling checks, so readiness has
    # to surface the size rather than just claiming "ok".
    assert body["lexicon_words"] > 0
    assert body["sandhi_rules"] > 0
    assert body["status"] == "ready"


def test_analyze_returns_no_suggestions_for_a_clean_sentence():
    r = client.post("/analyze", json={"target": "நான் பள்ளிக்கு சென்றேன்"})
    assert r.status_code == 200
    body = r.json()
    assert body["suggestions"] == []
    # Tier 1 can prove a word is misspelled but never that a sentence is CLEAN —
    # it sees no semantics. So it always reports unresolved and the orchestrator
    # falls through to the cache and model tiers.
    assert body["resolved"] is False


def test_analyze_surfaces_a_tier1_correction_over_http():
    r = client.post("/analyze", json={"target": "அது ஒரு பரவை"})
    assert r.status_code == 200
    body = r.json()

    s = body["suggestions"][0]
    assert s["original"] == "பரவை"
    assert s["suggestion"] == "பறவை"   # ர -> ற
    assert s["type"] == "spelling"
    assert s["source_tier"] == 1
    assert body["resolved"] is False


def test_analyze_carries_context_without_correcting_it():
    r = client.post(
        "/analyze",
        json={
            "target": "அவன் வந்தான்",
            "context_before": "நேற்று மாலை.",
            "context_after": "பிறகு சென்றான்.",
        },
    )
    assert r.status_code == 200


def test_analyze_rejects_a_missing_target():
    r = client.post("/analyze", json={"context_before": "ஏதோ"})
    assert r.status_code == 422

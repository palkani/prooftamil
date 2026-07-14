from fastapi.testclient import TestClient

from app.main import app

client = TestClient(app)


def test_health_is_live():
    r = client.get("/health")
    assert r.status_code == 200
    assert r.json()["status"] == "ok"


def test_ready_reports_loaded_models():
    r = client.get("/ready")
    assert r.status_code == 200
    assert "models_loaded" in r.json()


def test_analyze_accepts_tamil_and_returns_the_cascade_contract():
    r = client.post("/analyze", json={"target": "நான் பள்ளிக்கு சென்றேன்"})
    assert r.status_code == 200
    body = r.json()
    assert body["suggestions"] == []
    # Tier 1 is a stub until Phase 1; it must report itself unresolved so the
    # orchestrator falls through to the cache and model tiers rather than
    # treating "no suggestions" as "sentence is clean".
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

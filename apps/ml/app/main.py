"""ProofTamil ML service — Tier 1 of the proofreading cascade (plan §9, Phase 1).

Phase 0 stands up the service contract and health surface. Phase 1 fills in the
real linguistic engine: ThamizhiMorph (morphological analysis) and ThamizhiUDp
(dependency parsing), which together decide whether a surface form is a legal
Tamil word and propose deterministic corrections before any model is called.
"""

from __future__ import annotations

import logging
import os
import time
from typing import Literal

from fastapi import FastAPI
from pydantic import BaseModel, Field

LOG_LEVEL = os.getenv("LOG_LEVEL", "info").upper()
logging.basicConfig(level=getattr(logging, LOG_LEVEL, logging.INFO))
log = logging.getLogger("prooftamil.ml")

APP_ENV = os.getenv("APP_ENV", "dev")
SERVICE_REGION = os.getenv("SERVICE_REGION", "local")

app = FastAPI(
    title="ProofTamil ML",
    description="Deterministic Tamil analysis: morphology, parsing, embeddings.",
    version="0.1.0",
)

_STARTED = time.monotonic()

SuggestionType = Literal["spelling", "sandhi", "grammar", "agreement", "style"]


class AnalyzeRequest(BaseModel):
    """A single target sentence plus its neighbours, matching the corrector
    prompt contract in plan §8.1 — only the target is ever corrected."""

    target: str = Field(..., description="The sentence to analyze and correct.")
    context_before: str = Field("", description="Preceding sentence, for context only.")
    context_after: str = Field("", description="Following sentence, for context only.")


class Suggestion(BaseModel):
    start: int = Field(..., description="Character offset into `target`.")
    end: int
    original: str
    suggestion: str
    type: SuggestionType
    explanation: str = ""
    confidence: float = Field(..., ge=0.0, le=1.0)
    # Which cascade tier produced this. Tier 1 = deterministic (this service).
    source_tier: int = 1


class AnalyzeResponse(BaseModel):
    suggestions: list[Suggestion] = []
    # True when the deterministic tier is confident it has fully handled the
    # sentence, letting the orchestrator skip the paid model tiers entirely.
    resolved: bool = False
    took_ms: int = 0


@app.get("/health")
def health() -> dict:
    """Liveness. Touches nothing — mirrors the Go service's contract."""
    return {
        "status": "ok",
        "env": APP_ENV,
        "region": SERVICE_REGION,
        "uptime_sec": int(time.monotonic() - _STARTED),
    }


@app.get("/ready")
def ready() -> dict:
    """Readiness. Phase 1 extends this to assert the morph/parser data files in
    THAMIZHI_DATA_PATH are loaded before the instance accepts traffic."""
    return {"status": "ready", "models_loaded": []}


@app.post("/analyze", response_model=AnalyzeResponse)
def analyze(req: AnalyzeRequest) -> AnalyzeResponse:
    """Tier 1 deterministic analysis.

    Phase 0 returns no suggestions and `resolved=False`, which makes the Go
    orchestrator treat every sentence as a cascade miss and fall through to the
    cache and model tiers. That is the correct conservative default: an empty
    Tier 1 never produces a false positive, it just does not save any cost yet.
    """
    start = time.perf_counter()
    took_ms = int((time.perf_counter() - start) * 1000)
    return AnalyzeResponse(suggestions=[], resolved=False, took_ms=took_ms)

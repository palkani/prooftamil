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

from .tamil.engine import TamilEngine

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

# Built once at import. The lexicon and rule set are read-only after load, so a
# single shared engine is safe across the worker's threads — and rebuilding it
# per request would put a file read on the hot path of every keystroke.
_engine = TamilEngine()


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
    """Readiness. The instance is only useful once the lexicon is loaded — an
    empty lexicon silently disables Tier 1 spelling checks, so report it rather
    than serving traffic that quietly does nothing."""
    lexicon_size = len(_engine.lexicon)
    return {
        "status": "ready" if lexicon_size else "degraded",
        "lexicon_words": lexicon_size,
        "sandhi_rules": len(_engine.rules.get("rules", [])),
    }


@app.post("/analyze", response_model=AnalyzeResponse)
def analyze(req: AnalyzeRequest) -> AnalyzeResponse:
    """Cascade Tier 1 — deterministic analysis of the target sentence.

    `resolved` is always False. Tier 1 can prove a word is misspelled, but it can
    never prove a sentence is CLEAN: it sees no semantics, so it cannot rule out
    grammar, agreement or real-word errors. Claiming resolution here would skip
    the model tiers and silently drop real errors, so the orchestrator always
    falls through. Tier 1's payoff is latency — corrections in single-digit
    milliseconds, long before a model responds — not skipped model calls.
    """
    start = time.perf_counter()
    found = _engine.analyze(req.target)
    took_ms = int((time.perf_counter() - start) * 1000)

    return AnalyzeResponse(
        suggestions=[
            Suggestion(
                start=s.start,
                end=s.end,
                original=s.original,
                suggestion=s.suggestion,
                type=s.type,
                explanation=s.explanation,
                confidence=s.confidence,
                source_tier=s.source_tier,
            )
            for s in found
        ],
        resolved=False,
        took_ms=took_ms,
    )

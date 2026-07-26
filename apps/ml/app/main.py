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
from .tamil.translit import Transliterator

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

# The IME (RFC-001). Shares the engine's lexicon rather than re-reading 28 MB from
# disk. Read-only after construction, so it is safe to share across threads.
_ime = Transliterator(lexicon=_engine.lexicon)

# Handwritten-Tamil OCR (Gemini vision pipeline), mounted DEFENSIVELY: it pulls
# heavy optional deps (opencv, google-generativeai). If any fail to import — or the
# router raises at wiring time — we log and carry on with an unaffected proofreading
# engine rather than crash the service. The OOV confidence check reuses _engine's
# already-loaded lexicon (no second 28 MB read). Requires GEMINI_API_KEY at request
# time; without it the pipeline degrades to a best-effort Tesseract path.
try:
    from .ocr.router import router as ocr_router
    from .ocr.router import set_lexicon as _set_ocr_lexicon

    _set_ocr_lexicon(_engine.lexicon)
    app.include_router(ocr_router)
    log.info("OCR pipeline mounted at /api/ocr (lexicon shared)")
except Exception as _ocr_exc:  # pragma: no cover - defensive import guard
    log.warning("OCR pipeline NOT mounted (%s) — proofreading unaffected", _ocr_exc)


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


class IMESuggestion(BaseModel):
    word: str
    score: float
    # "lexicon" = a real Tamil word we know. "generated" = a best-effort spelling for
    # something not in the dictionary (a name, a loanword). The UI must show the
    # difference — a generated form is a guess, not a word.
    source: Literal["lexicon", "generated"]


class SuggestResponse(BaseModel):
    query: str
    suggestions: list[IMESuggestion] = []
    took_ms: int = 0


@app.get("/suggest", response_model=SuggestResponse)
def suggest(q: str, limit: int = 8) -> SuggestResponse:
    """Tamil IME — romanized input to Tamil script (RFC-001).

    `vanakkam` -> வணக்கம். The user has no Tamil keyboard; this is how they type.

    PRIVACY: `q` is a raw keystroke prefix from someone's writing — the most sensitive
    thing this product touches. It is never logged, and the endpoint takes no user
    identity, so the results are identical for everyone and cacheable at the edge.
    Do not add a user_id to this signature.
    """
    start = time.perf_counter()
    found = _ime.suggest(q, limit=max(1, min(limit, 20)))
    return SuggestResponse(
        query=q,
        suggestions=[
            IMESuggestion(word=s.word, score=round(s.score, 3), source=s.source)
            for s in found
        ],
        took_ms=int((time.perf_counter() - start) * 1000),
    )


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

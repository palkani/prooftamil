"""
router.py — the OCR HTTP surface, mounted onto the ML app by app.main.

Routes (path-compatible with the frontend's Express proxy, which POSTs to
`${HANDWRITING_OCR_URL}/api/ocr/extract-words`):
  POST /api/ocr/extract-words   multipart: file, context, mode → transcription
  POST /api/ocr/log-correction  json: request_id, corrected_text → dataset label
  GET  /api/ocr/health          pipeline readiness

app.main injects the shared lexicon via set_lexicon() so the OOV confidence check
reuses the engine's 349k words rather than loading a second copy.
"""

from __future__ import annotations

import logging
import time
from pathlib import Path
from typing import List, Optional

from fastapi import APIRouter, File, Form, HTTPException, UploadFile
from pydantic import BaseModel

from . import ocr_engine, data_logger
from .pipeline import gemini_available, run_pipeline, tesseract_available

logger = logging.getLogger(__name__)

router = APIRouter(prefix="/api/ocr", tags=["ocr"])

MAX_IMAGE_SIZE = 10 * 1024 * 1024
ALLOWED_EXTENSIONS = {".png", ".jpg", ".jpeg", ".bmp", ".tiff", ".webp"}

# The shared ProofTamil lexicon, injected by app.main. Read-only after set.
_LEXICON: Optional[object] = None


def set_lexicon(lexicon: object) -> None:
    """Called once at startup by app.main to share the engine's loaded lexicon."""
    global _LEXICON
    _LEXICON = lexicon


class FlaggedWord(BaseModel):
    word: str
    line: int
    reason: str


class OCRResponse(BaseModel):
    success: bool
    engine: str
    mode: str
    full_text: str = ""          # kept for the existing Express integration
    text: str = ""               # same value, the plan's field name
    flagged_words: List[FlaggedWord] = []
    confidence_pct: Optional[float] = None
    lines_count: int = 0
    request_id: Optional[str] = None
    processing_time_ms: float = 0.0
    message: str = ""


class CorrectionIn(BaseModel):
    request_id: str
    corrected_text: str


def _validate(file: UploadFile) -> None:
    if not file.content_type or not file.content_type.startswith("image/"):
        raise HTTPException(status_code=400, detail="Invalid file type")
    ext = Path(file.filename or "").suffix.lower()
    if ext and ext not in ALLOWED_EXTENSIONS:
        raise HTTPException(status_code=400, detail="Unsupported image extension")


@router.get("/health")
async def health():
    return {
        "status": "healthy",
        "gemini": gemini_available(),
        "gemini_model": ocr_engine.MODEL,
        "tesseract_fallback": tesseract_available(),
        "data_logging": data_logger.enabled(),
        "lexicon_shared": _LEXICON is not None,
    }


@router.post("/extract-words", response_model=OCRResponse)
async def extract_words(
    file: UploadFile = File(...),
    context: str = Form(""),
    mode: str = Form("accurate"),
):
    start = time.time()
    _validate(file)
    content = await file.read()
    if len(content) > MAX_IMAGE_SIZE:
        raise HTTPException(status_code=400, detail="File too large (max 10 MB)")

    try:
        result = run_pipeline(
            content, context.strip(), fast=(mode.lower() == "fast"), lexicon=_LEXICON
        )
    except Exception as e:
        logger.exception("OCR pipeline failed")
        raise HTTPException(status_code=502, detail="OCR pipeline error") from e

    result["success"] = True
    result["processing_time_ms"] = round((time.time() - start) * 1000, 2)
    return result


@router.post("/log-correction")
async def log_correction(body: CorrectionIn):
    """Store a human-verified transcription against a prior request — the ground
    truth that grows the fine-tuning dataset (only when DATA_LOGGING=true)."""
    saved = data_logger.log_correction(body.request_id, body.corrected_text)
    return {"success": True, "saved": saved}

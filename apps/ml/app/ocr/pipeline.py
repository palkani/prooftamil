"""
pipeline.py — the OCR orchestration (plan §2), reusable and framework-free.

  image → preprocess → segment lines → 2-pass Gemini OCR → Tamil correction
        → confidence flagging → { text, flagged_words, confidence_pct }

Modes:
  accurate (default) — full pipeline, 2 OCR passes + correction (Gemini Pro)
  fast               — single pass, no correction (cheaper), or Tesseract if no key

Kept separate from the FastAPI router so it can be unit-tested and reused. The
`lexicon` argument is the shared ProofTamil ML lexicon, threaded through to the
confidence layer's OOV check.
"""

from __future__ import annotations

import logging
import os
from typing import Dict, Optional

from . import preprocessing, segmentation, ocr_engine, postprocess, confidence, data_logger

logger = logging.getLogger(__name__)

DEBUG = os.getenv("OCR_DEBUG", "false").lower() == "true"

# Optional legacy Tesseract fallback (fast/offline mode when no Gemini key). The ML
# image does not ship the tesseract binary, so this stays inert in production — the
# Gemini key is always present there — but keeps local/offline runs from hard-failing.
try:
    import pytesseract  # noqa: F401
    _TESSERACT = True
except Exception:
    _TESSERACT = False


def gemini_available() -> bool:
    return bool(os.getenv("GEMINI_API_KEY"))


def tesseract_available() -> bool:
    return _TESSERACT


def _tesseract_full(image_bytes: bytes) -> str:
    """Fast/offline fallback: Tesseract Tamil on the whole (preprocessed) image."""
    if not _TESSERACT:
        return ""
    try:
        pil = preprocessing.preprocess(image_bytes)
        return (pytesseract.image_to_string(pil, lang="tam") or "").strip()
    except Exception as e:
        logger.warning("tesseract fallback failed: %s", e)
        return ""


def run_pipeline(
    image_bytes: bytes,
    context_hint: str,
    fast: bool,
    lexicon: Optional[object] = None,
) -> Dict:
    """The full OCR pipeline. Returns a dict matching the OCRResponse fields."""
    # No key, or explicitly fast with no key → Tesseract (best effort).
    if not gemini_available():
        text = _tesseract_full(image_bytes)
        return {
            "engine": "tesseract", "mode": "fast",
            "full_text": text, "text": text,
            "flagged_words": [], "confidence_pct": None,
            "lines_count": len(text.split("\n")) if text else 0,
            "request_id": None,
            "message": "GEMINI_API_KEY not set — used Tesseract fallback",
        }

    pil = preprocessing.preprocess(image_bytes, debug=DEBUG)
    segments = segmentation.segment_lines(pil)
    line_images = [s.image for s in segments]

    if fast:
        # Single pass, no correction — cheaper, still Gemini-quality per line.
        lines = [ocr_engine.transcribe(img, context_hint) for img in line_images]
        text = "\n".join(l for l in lines).strip()
        rid = data_logger.log_request(line_images, text, context_hint, extra={"mode": "fast"})
        return {
            "engine": ocr_engine.MODEL, "mode": "fast",
            "full_text": text, "text": text,
            "flagged_words": [], "confidence_pct": None,
            "lines_count": len(segments), "request_id": rid, "message": "",
        }

    # Accurate: two passes → correction → confidence.
    passes = ocr_engine.transcribe_document(line_images, context_hint)
    raw_text = "\n".join(passes["pass_a"]).strip()
    corrected, _diff = postprocess.correct_tamil(raw_text, context_hint)
    scored = confidence.score(passes["pass_a"], passes["pass_b"], corrected, lexicon=lexicon)
    rid = data_logger.log_request(line_images, scored["text"], context_hint, extra={"mode": "accurate"})
    return {
        "engine": ocr_engine.MODEL, "mode": "accurate",
        "full_text": scored["text"], "text": scored["text"],
        "flagged_words": scored["flagged_words"],
        "confidence_pct": scored["confidence_pct"],
        "lines_count": len(segments), "request_id": rid, "message": "",
    }

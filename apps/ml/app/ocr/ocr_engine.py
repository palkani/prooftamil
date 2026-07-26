"""
ocr_engine.py — Gemini vision transcription (plan §4.3, §5.1).

Replaces the old Tesseract/torch path. Each line image is transcribed by a strong
vision model under a strict "reproduce exactly, do not translate/autocorrect"
prompt. We run TWO passes per line (Pass A / Pass B): where the two disagree is a
strong signal the model is guessing — confidence.py uses that to flag words.

Env:
  GEMINI_API_KEY           required
  GEMINI_MODEL             primary model      (default gemini-2.5-pro)
  GEMINI_MODEL_FALLBACK    on primary error   (default gemini-2.5-flash)
"""

from __future__ import annotations

import logging
import os
from typing import Dict, List

import google.generativeai as genai
from PIL import Image

logger = logging.getLogger(__name__)

MODEL = os.getenv("GEMINI_MODEL", "gemini-2.5-pro")
MODEL_FALLBACK = os.getenv("GEMINI_MODEL_FALLBACK", "gemini-2.5-flash")

TRANSCRIBE_PROMPT = """You are an expert Tamil handwriting transcription engine.
Transcribe the Tamil text in this image EXACTLY as written.

Rules:
1. Reproduce every Tamil character precisely, including compound letters
   (உயிர்மெய்) and ligatures (க்ஷ, ஸ்ரீ, ஶ்ரீ).
2. Preserve line breaks, punctuation, spacing, and numerals as written.
3. Do NOT translate, transliterate, autocorrect, summarise, or add anything.
4. If a character is truly unreadable, output [?] in its place.
5. Output ONLY the transcribed text — no notes, no markdown, no quotes.

Context (may help disambiguate handwriting): {context_hint}
"""

_CONFIGURED = False


def _ensure_configured() -> None:
    global _CONFIGURED
    if _CONFIGURED:
        return
    key = os.getenv("GEMINI_API_KEY")
    if not key:
        raise RuntimeError("GEMINI_API_KEY is not set")
    genai.configure(api_key=key)
    _CONFIGURED = True


# Deterministic: we want the same reading every time so a Pass A/B disagreement
# reflects genuine ambiguity in the handwriting, not sampling randomness.
_GEN_CONFIG = {"temperature": 0.0, "top_p": 1.0, "max_output_tokens": 2048}


def _call(model_name: str, image: Image.Image, prompt: str) -> str:
    model = genai.GenerativeModel(model_name)
    resp = model.generate_content([prompt, image], generation_config=_GEN_CONFIG)
    return (getattr(resp, "text", "") or "").strip()


def transcribe(image: Image.Image, context_hint: str = "") -> str:
    """Transcribe one image. Retries once on the fallback model, then gives up with
    '' so the caller (and the whole document) still completes."""
    _ensure_configured()
    prompt = TRANSCRIBE_PROMPT.format(context_hint=context_hint or "(none)")
    try:
        return _call(MODEL, image, prompt)
    except Exception as e:
        logger.warning("primary model %s failed (%s); trying %s", MODEL, e, MODEL_FALLBACK)
        try:
            return _call(MODEL_FALLBACK, image, prompt)
        except Exception as e2:
            logger.error("fallback model %s also failed: %s", MODEL_FALLBACK, e2)
            return ""


def transcribe_document(line_images: List[Image.Image], context_hint: str = "") -> Dict[str, List[str]]:
    """Two full passes over every line. Returns {'pass_a': [...], 'pass_b': [...]}
    with one entry per input line, aligned by index."""
    pass_a = [transcribe(img, context_hint) for img in line_images]
    pass_b = [transcribe(img, context_hint) for img in line_images]
    return {"pass_a": pass_a, "pass_b": pass_b}

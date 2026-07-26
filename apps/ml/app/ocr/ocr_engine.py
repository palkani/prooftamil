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
from concurrent.futures import ThreadPoolExecutor

import google.generativeai as genai
from PIL import Image

logger = logging.getLogger(__name__)

# How many line requests are in flight at once. Modest by default so a mid-tier
# Gemini key doesn't 429; tune via OCR_CONCURRENCY.
_CONCURRENCY = max(1, int(os.getenv("OCR_CONCURRENCY", "4")))

# gemini-2.5-pro is retired for new API keys ("no longer available to new users"),
# so defaulting to it made every line 404 → retry → fall back, adding ~30s+ of dead
# time per document. Default to 2.5-flash (fast, and what the frontend already uses on
# this key); override with GEMINI_MODEL if a stronger model becomes available.
MODEL = os.getenv("GEMINI_MODEL", "gemini-2.5-flash")
MODEL_FALLBACK = os.getenv("GEMINI_MODEL_FALLBACK", "gemini-2.0-flash")

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
# reflects genuine ambiguity in the handwriting, not sampling randomness. The token
# ceiling is generous so a dense line (plus any model "thinking") is never truncated.
_GEN_CONFIG = {"temperature": 0.0, "top_p": 1.0, "max_output_tokens": 4096}


def _extract_text(resp) -> str:
    """Pull text out of a Gemini response WITHOUT the `.text` quick accessor, which
    RAISES ("requires a valid Part") when a candidate finished with no text part —
    e.g. an empty/blank line or a safety stop. We want an empty string there, not an
    exception that aborts the whole page. Concatenate the text parts if present."""
    try:
        for cand in getattr(resp, "candidates", None) or []:
            content = getattr(cand, "content", None)
            parts = getattr(content, "parts", None) or []
            text = "".join(getattr(p, "text", "") or "" for p in parts)
            if text.strip():
                return text.strip()
    except Exception:  # never let response shape quirks abort a document
        pass
    return ""


def _call(model_name: str, image: Image.Image, prompt: str) -> str:
    model = genai.GenerativeModel(model_name)
    resp = model.generate_content([prompt, image], generation_config=_GEN_CONFIG)
    return _extract_text(resp)


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


def transcribe_lines(line_images: list[Image.Image], context_hint: str = "") -> list[str]:
    """Transcribe every line, in parallel. Each call is network-bound (a Gemini
    request), so a thread pool overlaps them — turning N sequential round-trips into
    ceil(N / OCR_CONCURRENCY) waves. Order is preserved (ex.map is ordered), which the
    Pass A/B alignment in confidence scoring depends on. Concurrency is kept modest by
    default to stay under the API key's rate limit; raise OCR_CONCURRENCY on a key with
    generous RPM."""
    _ensure_configured()  # once, up front, so worker threads don't race on configure()
    if _CONCURRENCY <= 1 or len(line_images) <= 1:
        return [transcribe(img, context_hint) for img in line_images]
    with ThreadPoolExecutor(max_workers=_CONCURRENCY) as ex:
        return list(ex.map(lambda img: transcribe(img, context_hint), line_images))


def transcribe_document(
    line_images: list[Image.Image], context_hint: str = ""
) -> dict[str, list[str]]:
    """Two full passes over every line. Returns {'pass_a': [...], 'pass_b': [...]}
    with one entry per input line, aligned by index."""
    pass_a = transcribe_lines(line_images, context_hint)
    pass_b = transcribe_lines(line_images, context_hint)
    return {"pass_a": pass_a, "pass_b": pass_b}

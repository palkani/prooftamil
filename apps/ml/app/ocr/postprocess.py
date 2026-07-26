"""
postprocess.py — Tamil dictionary + context correction (plan §4.4, §5.2).

Two jobs:
  1. A dictionary the confidence layer uses to flag out-of-vocabulary words.
  2. A single LLM "correction" pass that fixes obvious OCR slips using Tamil
     spelling/grammar/context — WITHOUT translating or rewriting valid words.

Dictionary source: folded into the ML service, the OOV check reuses the already-
loaded ProofTamil lexicon (app.tamil.lexicon.Lexicon, ~349k trusted words) passed
in via the `lexicon` argument — no second word list is read. A `word in lexicon`
membership test is all this module needs. When no lexicon is supplied it falls
back to a file word list at TAMIL_DICT_PATH, and if neither exists the OOV signal
simply no-ops (confidence then rests on the two-pass agreement signal alone).
"""

from __future__ import annotations

import gzip
import logging
import os
import re
import unicodedata
from difflib import ndiff
from functools import lru_cache

import google.generativeai as genai

logger = logging.getLogger(__name__)

CORRECTION_PROMPT = """You are a Tamil proofreading assistant. Below is raw OCR output from
handwritten Tamil notes. Fix ONLY clear OCR recognition errors using Tamil spelling,
grammar, and sentence context.

Strict rules:
- Do NOT change words that are already valid Tamil.
- Do NOT translate, rephrase, add, or remove content.
- Keep line breaks and formatting identical.
- If a word is ambiguous and you are not confident, leave it unchanged.

Context: {context_hint}

Raw OCR text:
{raw_text}

Return only the corrected Tamil text.
"""

# Tamil word = a run of Tamil-block codepoints (U+0B80–U+0BFF).
_WORD_RE = re.compile(r"[஀-௿]+")


def _norm(w: str) -> str:
    return unicodedata.normalize("NFC", w).strip()


@lru_cache(maxsize=1)
def load_dictionary() -> frozenset:
    """Load a Tamil word set from a file once (fallback path only, used when no
    shared lexicon is injected). Empty (and logged) if TAMIL_DICT_PATH is unset
    or missing — the pipeline still runs, just without the OOV signal."""
    path = os.getenv("TAMIL_DICT_PATH", "")
    if not path or not os.path.exists(path):
        logger.info(
            "TAMIL_DICT_PATH not set / not found (%r) — file dictionary disabled", path
        )
        return frozenset()
    opener = gzip.open if path.endswith(".gz") else open
    words: set[str] = set()
    with opener(path, "rt", encoding="utf-8", errors="ignore") as fh:
        for line in fh:
            # Support "word" and "word<tab>count" formats.
            w = _norm(line.split("\t", 1)[0].split()[0] if line.strip() else "")
            if w:
                words.add(w)
    logger.info("loaded %d Tamil dictionary words from %s", len(words), path)
    return frozenset(words)


def tamil_words(text: str) -> list[str]:
    return [_norm(m.group(0)) for m in _WORD_RE.finditer(text)]


def unknown_words(text: str, lexicon: object | None = None) -> list[str]:
    """Tamil words in `text` that are out-of-vocabulary (OOV).

    If `lexicon` is provided it is used via `word in lexicon` (the shared
    ProofTamil ML lexicon, which normalises internally). Otherwise falls back to
    the file dictionary. Empty list when neither is available."""
    words = tamil_words(text)
    if lexicon is not None:
        return [w for w in words if w and w not in lexicon]
    dic = load_dictionary()
    if not dic:
        return []
    return [w for w in words if w and w not in dic]


# ── correction LLM pass ──────────────────────────────────────────────────────

# gemini-2.5-pro is retired for new API keys — default to 2.5-flash (see ocr_engine).
_MODEL = os.getenv("GEMINI_MODEL", "gemini-2.5-flash")
_MODEL_FALLBACK = os.getenv("GEMINI_MODEL_FALLBACK", "gemini-2.0-flash")
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


def _resp_text(resp) -> str:
    """Safely read text from a Gemini response — the `.text` accessor RAISES when a
    candidate has no text part, so read the parts directly and return '' instead."""
    try:
        for cand in getattr(resp, "candidates", None) or []:
            content = getattr(cand, "content", None)
            parts = getattr(content, "parts", None) or []
            text = "".join(getattr(p, "text", "") or "" for p in parts)
            if text.strip():
                return text.strip()
    except Exception:
        pass
    return ""


def _diff(before: str, after: str) -> list[str]:
    """Compact word-level change list, for logging/inspection."""
    return [d for d in ndiff(before.split(), after.split()) if d[0] in "+-"]


def correct_tamil(text: str, context_hint: str = "") -> tuple[str, list[str]]:
    """Run the correction pass. Returns (corrected_text, diff). On any failure the
    original text is returned unchanged — correction must never lose content."""
    if not text.strip():
        return text, []
    try:
        _ensure_configured()
        prompt = CORRECTION_PROMPT.format(
            context_hint=context_hint or "(none)", raw_text=text
        )
        for model_name in (_MODEL, _MODEL_FALLBACK):
            try:
                model = genai.GenerativeModel(model_name)
                resp = model.generate_content(
                    prompt, generation_config={"temperature": 0.1, "top_p": 1.0}
                )
                corrected = _resp_text(resp)
                if corrected:
                    return corrected, _diff(text, corrected)
            except Exception as e:
                logger.warning("correction via %s failed: %s", model_name, e)
        return text, []
    except Exception as e:
        logger.error("correction pass failed, returning raw text: %s", e)
        return text, []

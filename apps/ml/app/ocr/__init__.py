"""Handwritten-Tamil OCR pipeline, folded into the ML service (cascade Tier 1).

This subpackage is deliberately self-contained and mounted defensively by
app.main: if any of its heavy optional deps (opencv, google-generativeai) fail to
import, the router is simply not mounted and the proofreading engine is unaffected.

Pipeline (mirrors services/tamil-handwriting-ocr in the frontend repo):
  image -> preprocess -> segment lines -> 2-pass Gemini OCR -> Tamil correction
        -> confidence flagging -> { text, flagged_words, confidence_pct }

The OOV confidence signal reuses the ML service's already-loaded 349k-word lexicon
(app.main injects it via router.set_lexicon) rather than reading a second copy.
"""

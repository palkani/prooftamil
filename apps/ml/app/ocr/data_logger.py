"""
data_logger.py — capture (image, text) pairs for future fine-tuning (plan §4.6, §9).

Every processed page can be saved as line images + their transcribed text, and later
updated with the human-corrected text when a user edits the result. Over time this
becomes a labelled Tamil-handwriting dataset — the strategic asset that lets you
train a dedicated model (TrOCR/Kraken) and push past the LLM's ceiling.

PRIVACY: this stores users' handwriting images and text, so it is OPT-IN. It does
nothing unless DATA_LOGGING=true. Storage dir: TRAINING_DATA_DIR (default
./training_data). Disclose this collection in your privacy policy before enabling.

NOTE: on Cloud Run the container filesystem is ephemeral — samples written here do
NOT survive a restart. Leave DATA_LOGGING unset in production unless
TRAINING_DATA_DIR points at a mounted, durable volume.
"""

from __future__ import annotations

import json
import logging
import os
import uuid
from datetime import datetime, timezone
from typing import Dict, List, Optional

from PIL import Image

logger = logging.getLogger(__name__)

_ENABLED = os.getenv("DATA_LOGGING", "false").lower() == "true"
_ROOT = os.getenv("TRAINING_DATA_DIR", "training_data")


def enabled() -> bool:
    return _ENABLED


def log_request(
    line_images: List[Image.Image],
    final_text: str,
    context_hint: str = "",
    extra: Optional[Dict] = None,
) -> Optional[str]:
    """Persist the line images + transcription as a labelled sample. Returns a
    request id (used later by log_correction), or None if logging is disabled."""
    if not _ENABLED:
        return None
    try:
        rid = uuid.uuid4().hex[:16]
        stamp = datetime.now(timezone.utc).strftime("%Y%m%d")
        sample_dir = os.path.join(_ROOT, stamp, rid)
        os.makedirs(sample_dir, exist_ok=True)

        text_lines = final_text.split("\n")
        lines_meta = []
        for i, img in enumerate(line_images):
            fname = f"line_{i:03d}.png"
            img.save(os.path.join(sample_dir, fname))
            lines_meta.append(
                {"index": i, "image": fname, "text": text_lines[i] if i < len(text_lines) else ""}
            )

        manifest = {
            "id": rid,
            "created_at": datetime.now(timezone.utc).isoformat(),
            "context": context_hint,
            "final_text": final_text,
            "corrected_text": None,  # filled in when a human edits (log_correction)
            "lines": lines_meta,
            **(extra or {}),
        }
        _write_manifest(sample_dir, manifest)
        logger.info("logged training sample %s (%d lines)", rid, len(line_images))
        return rid
    except Exception as e:  # never let logging break a user request
        logger.warning("data_logger.log_request failed: %s", e)
        return None


def log_correction(request_id: str, corrected_text: str) -> bool:
    """Attach the human-verified text to a previously logged sample — this is the
    ground truth that makes the sample worth training on."""
    if not _ENABLED or not request_id:
        return False
    try:
        stamp = datetime.now(timezone.utc).strftime("%Y%m%d")
        # The correction may arrive on a later UTC day than the request; scan recent days.
        for day in _recent_days():
            sample_dir = os.path.join(_ROOT, day, request_id)
            path = os.path.join(sample_dir, "manifest.json")
            if os.path.exists(path):
                with open(path, "r", encoding="utf-8") as fh:
                    manifest = json.load(fh)
                manifest["corrected_text"] = corrected_text
                manifest["corrected_at"] = datetime.now(timezone.utc).isoformat()
                _write_manifest(sample_dir, manifest)
                logger.info("attached correction to sample %s", request_id)
                return True
        logger.warning("log_correction: sample %s not found", request_id)
        return False
    except Exception as e:
        logger.warning("data_logger.log_correction failed: %s", e)
        return False


def _write_manifest(sample_dir: str, manifest: Dict) -> None:
    with open(os.path.join(sample_dir, "manifest.json"), "w", encoding="utf-8") as fh:
        json.dump(manifest, fh, ensure_ascii=False, indent=2)


def _recent_days(n: int = 7) -> List[str]:
    from datetime import timedelta

    today = datetime.now(timezone.utc)
    return [(today - timedelta(days=d)).strftime("%Y%m%d") for d in range(n)]

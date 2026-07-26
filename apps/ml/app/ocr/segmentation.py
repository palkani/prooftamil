"""
segmentation.py — split a page into text lines (plan §4.2).

Transcribing one line at a time is markedly more accurate than a whole page: the
model has less to track, and a mistake on one line can't cascade. We find lines
with a horizontal projection profile (ink per row → bands separated by gaps).

If the profile can't find sensible lines (very cursive or overlapping writing),
we fall back to a single segment covering the whole page — the vision model reads
full pages competently too, so this degrades rather than fails.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import List

import cv2
import numpy as np
from PIL import Image


@dataclass
class Segment:
    index: int
    image: Image.Image
    # bbox in the coordinates of the image passed to segment_lines (x, y, w, h),
    # kept for flagging/logging/overlay later.
    bbox: tuple = field(default=(0, 0, 0, 0))


# A line band must be at least this tall (px) to count — filters specks/underlines.
MIN_LINE_HEIGHT = 12
# Vertical padding added around each cropped line so tall vowel signs (ஔ, ொ) and
# descenders (ழ, ஜ) aren't clipped.
LINE_PAD = 6


def _ink_rows(gray: np.ndarray) -> np.ndarray:
    """Per-row ink count. Input is grayscale with dark text on light paper."""
    ink = (gray < 128).astype(np.uint8)  # 1 where there's ink
    return ink.sum(axis=1)


def segment_lines(image: Image.Image) -> List[Segment]:
    """Return top-to-bottom line segments; falls back to [whole page]."""
    rgb = np.array(image.convert("RGB"))
    gray = cv2.cvtColor(rgb, cv2.COLOR_RGB2GRAY)
    h, w = gray.shape[:2]

    proj = _ink_rows(gray)
    if not np.any(proj > 0):
        return [Segment(0, image, (0, 0, w, h))]

    # A row "has text" if its ink is above a small fraction of the busiest row.
    threshold = max(1.0, proj.max() * 0.06)

    bands: List[tuple] = []
    in_band = False
    start = 0
    for y, val in enumerate(proj):
        if not in_band and val > threshold:
            in_band, start = True, y
        elif in_band and val <= threshold:
            if y - start >= MIN_LINE_HEIGHT:
                bands.append((start, y))
            in_band = False
    if in_band and h - start >= MIN_LINE_HEIGHT:
        bands.append((start, h))

    # Fallback: no usable bands, or the whole page is basically one blob.
    if not bands or (len(bands) == 1 and bands[0][1] - bands[0][0] > 0.9 * h):
        return [Segment(0, image, (0, 0, w, h))]

    segments: List[Segment] = []
    for i, (y1, y2) in enumerate(bands):
        yy1 = max(0, y1 - LINE_PAD)
        yy2 = min(h, y2 + LINE_PAD)
        crop = image.crop((0, yy1, w, yy2))
        segments.append(Segment(i, crop, (0, yy1, w, yy2 - yy1)))
    return segments

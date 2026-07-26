"""
preprocessing.py — image cleanup before OCR (plan §4.1).

Turns a raw uploaded photo of handwritten Tamil into a clean, high-contrast,
straightened image that a vision model reads far more accurately. Order matters:
grayscale → deskew → denoise → CLAHE contrast → adaptive binarize → upscale.

`preprocess(image_bytes)` returns a PIL RGB image ready for the OCR engine.
Set `debug=True` to dump each intermediate step to disk for tuning.
"""

from __future__ import annotations

import logging
import os
from typing import Optional

import cv2
import numpy as np
from PIL import Image

logger = logging.getLogger(__name__)

# Below this shorter-side pixel count the image is upscaled — small handwriting is
# the single biggest accuracy killer, and models read larger glyphs much better.
MIN_SHORT_SIDE = 1500
MAX_SHORT_SIDE = 4000  # don't blow tiny images up past this; wastes tokens/time


def _decode(image_bytes: bytes) -> np.ndarray:
    arr = np.frombuffer(image_bytes, np.uint8)
    img = cv2.imdecode(arr, cv2.IMREAD_COLOR)
    if img is None:
        raise ValueError("could not decode image")
    return img


def _deskew(gray: np.ndarray) -> np.ndarray:
    """Straighten the page. Estimate the dominant text angle from the ink pixels
    (minAreaRect over a thresholded copy) and rotate to level it.

    Only small skews are corrected — a large estimated angle usually means the
    heuristic latched onto noise, so we leave the image alone rather than rotate
    it 45°."""
    thr = cv2.threshold(gray, 0, 255, cv2.THRESH_BINARY_INV | cv2.THRESH_OTSU)[1]
    coords = np.column_stack(np.where(thr > 0))
    if coords.shape[0] < 50:  # too little ink to trust an angle
        return gray

    angle = cv2.minAreaRect(coords)[-1]
    # minAreaRect returns angle in [-90, 0); normalise to a small correction.
    if angle < -45:
        angle = 90 + angle
    if abs(angle) < 0.3 or abs(angle) > 20:  # negligible, or almost certainly noise
        return gray

    h, w = gray.shape[:2]
    m = cv2.getRotationMatrix2D((w / 2, h / 2), angle, 1.0)
    return cv2.warpAffine(
        gray, m, (w, h),
        flags=cv2.INTER_CUBIC, borderMode=cv2.BORDER_REPLICATE,
    )


def _upscale(img: np.ndarray) -> np.ndarray:
    short = min(img.shape[:2])
    if short >= MIN_SHORT_SIDE:
        return img
    scale = min(MIN_SHORT_SIDE / short, MAX_SHORT_SIDE / short, 3.0)
    if scale <= 1.01:
        return img
    return cv2.resize(
        img, None, fx=scale, fy=scale, interpolation=cv2.INTER_CUBIC
    )


def preprocess(image_bytes: bytes, debug: bool = False, debug_dir: str = "debug") -> Image.Image:
    """Clean a raw image for OCR and return it as a PIL RGB Image."""
    steps = {}

    img = _decode(image_bytes)
    gray = cv2.cvtColor(img, cv2.COLOR_BGR2GRAY)
    steps["01_gray"] = gray

    gray = _deskew(gray)
    steps["02_deskew"] = gray

    # Illumination flattening: divide out a smoothed background so uneven lighting
    # / shadows on a phone photo don't turn into false strokes after binarizing.
    ksize = max(31, min(gray.shape[:2]) // 20)
    ksize += 1 - (ksize % 2)  # force odd
    kernel = cv2.getStructuringElement(cv2.MORPH_ELLIPSE, (ksize, ksize))
    background = cv2.morphologyEx(gray, cv2.MORPH_CLOSE, kernel)
    flat = cv2.divide(gray, background, scale=255)
    steps["03_flatten"] = flat

    denoised = cv2.fastNlMeansDenoising(flat, h=10)
    steps["04_denoise"] = denoised

    clahe = cv2.createCLAHE(clipLimit=2.0, tileGridSize=(8, 8))
    enhanced = clahe.apply(denoised)
    steps["05_clahe"] = enhanced

    # Black text on white (not inverted) — the model expects normal polarity.
    binary = cv2.adaptiveThreshold(
        enhanced, 255,
        cv2.ADAPTIVE_THRESH_GAUSSIAN_C, cv2.THRESH_BINARY,
        31, 10,
    )
    steps["06_binary"] = binary

    upscaled = _upscale(binary)
    steps["07_upscale"] = upscaled

    if debug:
        os.makedirs(debug_dir, exist_ok=True)
        for name, arr in steps.items():
            cv2.imwrite(os.path.join(debug_dir, f"{name}.png"), arr)
        logger.info("preprocess debug steps written to %s/", debug_dir)

    return Image.fromarray(upscaled).convert("RGB")


def to_pil(image: np.ndarray) -> Image.Image:
    """Helper: OpenCV BGR/gray ndarray → PIL RGB Image."""
    if image.ndim == 3:
        return Image.fromarray(cv2.cvtColor(image, cv2.COLOR_BGR2RGB))
    return Image.fromarray(image).convert("RGB")

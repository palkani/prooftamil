"""
confidence.py — per-word confidence & flagging (plan §4.5).

Two independent signals decide whether a word is trustworthy:
  1. AGREEMENT — the two transcription passes (A vs B) should read a clear word
     identically. Where they disagree, the model was guessing.
  2. VOCABULARY — a word the Tamil dictionary has never seen is often a misread.

A word flagged by either signal is surfaced for optional human review. The returned
`confidence_pct` is the share of words that passed both checks — an honest, if
rough, "how much of this can you trust" number.
"""

from __future__ import annotations

from difflib import SequenceMatcher
from typing import Dict, List, Optional

from .postprocess import tamil_words, unknown_words


def _disagreeing_words(a: str, b: str) -> set:
    """Words that appear in one pass's version of a line but not aligned in the
    other — the tokens inside replace/delete/insert blocks."""
    aw, bw = a.split(), b.split()
    sm = SequenceMatcher(a=aw, b=bw, autojunk=False)
    out = set()
    for op, i1, i2, j1, j2 in sm.get_opcodes():
        if op != "equal":
            out.update(aw[i1:i2])
            out.update(bw[j1:j2])
    return out


def score(
    pass_a_lines: List[str],
    pass_b_lines: List[str],
    corrected_text: str,
    lexicon: Optional[object] = None,
) -> Dict:
    """Merge the agreement + vocabulary signals over the corrected document.

    `lexicon` (optional) is the shared ProofTamil ML lexicon used for the OOV
    check; when omitted the vocabulary signal falls back to the file dictionary.

    Returns {text, flagged_words: [{word, line, reason}], confidence_pct}.
    """
    # Per-line set of words the two passes disagreed on.
    disagree_per_line: List[set] = []
    n = max(len(pass_a_lines), len(pass_b_lines))
    for i in range(n):
        a = pass_a_lines[i] if i < len(pass_a_lines) else ""
        b = pass_b_lines[i] if i < len(pass_b_lines) else ""
        disagree_per_line.append(_disagreeing_words(a, b))
    all_disagree = set().union(*disagree_per_line) if disagree_per_line else set()

    oov = set(unknown_words(corrected_text, lexicon=lexicon))

    flagged: List[Dict] = []
    seen = set()
    total = 0
    trusted = 0

    corrected_lines = corrected_text.split("\n")
    for line_no, line in enumerate(corrected_lines):
        for w in tamil_words(line):
            total += 1
            reasons = []
            if w in all_disagree:
                reasons.append("passes_disagree")
            if w in oov:
                reasons.append("not_in_dictionary")
            if reasons:
                key = (w, line_no)
                if key not in seen:
                    seen.add(key)
                    flagged.append(
                        {"word": w, "line": line_no, "reason": ",".join(reasons)}
                    )
            else:
                trusted += 1

    confidence_pct = round(trusted / total, 4) if total else 1.0

    return {
        "text": corrected_text,
        "flagged_words": flagged,
        "confidence_pct": confidence_pct,
    }

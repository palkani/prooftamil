# RFC-001 — Tamil IME (romanized input → Tamil script)

**Status:** proposed, awaiting sign-off
**Author:** Claude (with ProofTamil)
**Date:** 2026-07-14
**Addresses:** plan GAP-1

---

## 1. Why this exists

Most Tamil writers do not have a Tamil keyboard. They type `vanakkam` and expect வணக்கம்.

Without this, ProofTamil can *proofread* Tamil but cannot help anyone *write* it — which makes the
entire proofreading cascade unreachable for a large share of users. **This is more load-bearing
than proofreading, and it is missing from the v2 plan entirely** (v1 shipped it: `internal/ime`,
`internal/translit`).

### Anti-goal

**This is not English→Tamil translation.** `hello` must NOT become வணக்கம். It is *phonetic*: the
user is typing Tamil, using Latin letters, because their keyboard has no Tamil on it. Translation
is a different feature with a different UX, and conflating them makes both confusing.

---

## 2. The core insight: this is the OPPOSITE contract to the proofreader

Both features use the same 349k-word lexicon, and they want opposite things from it.

| | Proofreader (Tier 1) | IME |
|---|---|---|
| Optimizes for | **precision** | **recall** |
| On ambiguity | **stay silent** | **show all candidates** |
| Who decides | the engine (autonomously edits) | **the user** (picks from a list) |
| Cost of being wrong | high — a false "correction" of good Tamil destroys trust | low — the user just ignores a bad candidate |

The proofreader must never guess, because it is *asserting* something about the writer's text. The
IME is *offering* options and the human picks — so guessing widely is not just acceptable, it is
required. Showing 4 candidates when only 1 is right is a **success** for an IME and a **failure**
for a proofreader.

Consequence: **do not reuse Tier 1's `TRUSTED_MIN_COUNT` or its frequency-ratio guard here.** Those
exist to enforce silence. Applying them to the IME would make it refuse to type rare words — which
is precisely when a user most needs help.

---

## 3. Approach (measured, not assumed)

### 3.1 Reverse index over the existing lexicon

For every Tamil word, precompute the roman spellings a human might type, and index
`roman → [tamil words]`. Rank hits by corpus frequency.

The lexicon already carries frequency counts (built for Tier 1), so ranking is free. Measured on
the real 349k lexicon, plain frequency ranking puts the right answer **first**:

```
vanakkam  -> வணக்கம்(1427)  வானகம்(28)  வண்ணகம்(6)
palli     -> பள்ளி(11738)   பாலி(2001)  பலி(700)   பல்லி(576)
tamil     -> தமிழ்(79058)   தம்மில்(33)  டமில்(9)
naan      -> நான்(9416)     நாண்(169)   நன்(136)
```

Ambiguity is low: **88.3% of keys have exactly one candidate**, mean 1.18.

### 3.2 One word → MANY keys (this is the crux)

A single canonical romanization does **not** work. Measured: with one key per word, `sendren`
(சென்றேன்) returns **nothing**.

That is not a bug, it is Tamil phonology. Consonant clusters change sound:

| written | pronounced/typed |
|---|---|
| ன்ற | **ndr** (சென்றேன் → "sendren") |
| ங்க | **ng** |
| ண்ட | **nd** |
| ஞ்ச | **nj** |
| ம்ப | **mb** |

…and the same letter has several accepted romanizations anyway (ழ → `zh`/`l`/`z`, த → `th`/`t`/`d`/`dh`,
வ → `v`/`w`, ட → `t`/`d`). People also double consonants inconsistently (`vanakkam`/`vanakam`).

**So: generate the cross-product of plausible spellings per word and index all of them**, collapsing
doubled letters on both sides. This is the difference between a toy and a usable IME. Verified — all
of these now resolve, right answer first:

```
sendren -> சென்றேன்      wanakam     -> வணக்கம்
senren  -> சென்றேன்      tamizh      -> தமிழ்
vandhan -> வந்தான்       irukkiradhu -> இருக்கிறது
vanthan -> வந்தான்       nandri      -> நன்றி
```

Cost: 349k words → **5.1M keys**. Cheap to build, and it is a static artifact.

### 3.3 Out-of-vocabulary fallback — non-negotiable

**An IME that cannot type your own name is broken.** The index only knows lexicon words, so names,
loanwords, new coinages and long agglutinated forms will miss.

Fallback: a **rule-based generator** (roman → Tamil script, letter by letter) that always produces
*something*, marked visually as a generated form rather than a dictionary word. Ranked last.

This is the difference between "sorry, can't type that" and "here is my best attempt, press Enter."

---

## 4. Where it runs: hybrid client + server

Latency budget is a **keystroke**. Perceived-instant is <50ms, which a network round trip to Mumbai
cannot reliably meet — and this fires on every keypress.

Measured payload for a client-side index (gzipped, keys + words):

| words shipped | keys | gzipped | share of real Tamil usage covered |
|---|---|---|---|
| top 10,000 | 130k | **0.50 MB** | 64% |
| **top 20,000** | 269k | **1.03 MB** | **80%** |
| top 30,000 | 410k | 1.57 MB | 83% |
| top 50,000 | 699k | 2.67 MB | 88% |

**Decision: ship the top 20,000 words (~1 MB gzipped) to the client; serve the tail from the API.**

- **Tier 0 (client):** 80% of keystrokes answered locally in <10ms, offline, zero network, zero cost.
  Loaded lazily *after* first paint so it never blocks the editor.
- **Tier 1 (server):** `GET /api/v1/suggest` covers the full 349k lexicon + the OOV generator, for
  the 20% tail. Debounced, cached at the edge (these results are identical for every user — a
  perfect CDN/KV cache key).

This is the plan's "Tier 0 WASM dictionary" (§9 Phase 1), finally given a concrete shape. Plain JS +
a compact index is likely enough; **WASM only if profiling shows JS is too slow** — do not pay the
WASM complexity cost on speculation.

Model cost per keystroke: **zero**. No LLM is involved anywhere in this feature.

---

## 5. Ranking

Order candidates by:

1. **Personal history** (strongest). If the user picked வழி for `vali` last time, it goes first.
   Stored client-side; a per-user boost. This single signal beats every clever heuristic, because
   people reuse their own vocabulary.
2. **Corpus frequency** (the baseline that already works — see §3.1).
3. **Exact-key match** ahead of prefix match.
4. **Generated (OOV) forms last**, visually distinguished.

Deliberately **not** in v1: context-aware ranking (using the previous word to disambiguate
வலி/வழி). It needs a language model on the hot path, and the data says frequency already ranks the
right answer first in the common case. Revisit only if the eval says so.

---

## 6. UX (decided with the user)

Inline dropdown, cursor-anchored, as you type.

```
  நான் பள்ளிக்கு sendr|
                 ┌────────────────────┐
                 │ சென்றேன்        ★  │  ← Enter / click / Tab
                 │ சென்றான்           │
                 │ சென்றார்           │
                 │ ⌨ செண்ட்ர்   (new) │  ← generated, not in dictionary
                 └────────────────────┘
```

- Fires on a run of Latin letters; Tamil/punctuation/numbers pass straight through.
- `Enter`/`Tab`/click inserts. `Esc` dismisses and keeps the Latin text.
- Digits `1-9` select directly (power users).
- **A user with a real Tamil keyboard must be able to turn this off** — a toggle, persisted.
- Typing Latin and *ignoring* the dropdown must leave the Latin text alone. Never auto-replace.

### Interaction with the proofreader

None, by design. The proofreader's tokenizer already ignores non-Tamil tokens, so un-converted
Latin text is silently skipped rather than flagged as misspelled Tamil. (Already tested:
`test_analyze_carries_context_without_correcting_it` / mixed-script case.)

---

## 7. API surface

```
GET /api/v1/suggest?q=<roman>&limit=8
  -> { "suggestions": [
         { "word": "சென்றேன்", "score": 0.94, "source": "lexicon" },
         { "word": "செண்ட்ர்", "score": 0.10, "source": "generated" }
       ] }
```

- **Unauthenticated** and **user-independent** → cacheable at the Cloudflare edge forever (keyed by
  `q` + index version). Most requests should never reach an origin.
- No user data in the request. Keystrokes are the most sensitive thing a writing app touches; this
  endpoint must not become a keylogger. **Do not log `q` with a user ID attached.**

```
POST /api/v1/translit   (batch, for paste/import)
  -> converts a whole romanized document at once
```

Index artifact, versioned and immutable:
```
GET /static/ime-index.v<N>.json.gz     (~1 MB, top-20k, long cache TTL)
```

---

## 8. How we know it works (eval — mandatory, per §11)

Without this we are guessing.

- **Test set:** ≥500 `(roman_typed → intended_tamil)` pairs. Seeded from the lexicon by generating
  plausible romanizations, then **corrected by a human**, because the whole point is to model what
  people actually type, not what our generator thinks they type.
- **Metrics:**
  - **top-1 accuracy** — the intended word is the default (this is what users feel)
  - **top-3 accuracy** — it is visible without scrolling
  - **MRR** — overall ranking quality
  - **p99 latency**, client and server
  - **OOV rate** — how often we fall through to the generator
- **CI gate:** top-3 accuracy must not regress. Same discipline as `make eval`.

**Target: top-1 ≥ 85%, top-3 ≥ 95%.** Below that, users fight the tool.

---

## 9. Risks

| Risk | Mitigation |
|---|---|
| **Index staleness.** Rebuild the lexicon → old cached index serves wrong/missing words. | Version the artifact (`ime-index.v<N>`), immutable URL, same discipline as the cascade's cache-version salt. |
| **1 MB client payload** hurts first load, especially on Indian mobile. | Lazy-load after first paint; editor is usable immediately (server endpoint answers until it lands). Ship top-10k (0.5 MB) if RUM says 1 MB hurts. |
| **Agglutination.** Tamil compounds are unbounded; long forms will be OOV. | The OOV generator always answers. Longest-prefix segmentation is a possible v2 — **not** in v1. |
| **5.1M-key server index memory.** | Measure before shipping. If it is fat, use a trie/FST rather than a hash map, or serve from Redis. |
| **Privacy.** This endpoint sees every keystroke. | Unauthenticated, user-independent, edge-cached, `q` never logged with identity. Client tier means 80% never leave the device at all. |
| Bad romanization scheme choices make it feel wrong to native typists. | The variant table is **data** (`packages/tamil-rules/translit/*.yaml`), not code — a Tamil speaker can fix it without touching Go/Python. Same pattern as the sandhi rules. |

---

## 10. Plan

| Step | Work | Output |
|---|---|---|
| 1 | Variant table (`translit/*.yaml`) + key generator + index builder | `make ime-index` → versioned artifact |
| 2 | Server: `GET /api/v1/suggest` (full lexicon + OOV generator) | endpoint, edge-cacheable |
| 3 | Eval harness + labelled test set | `make eval-ime`, CI-gated |
| 4 | Client Tier 0: lazy-loaded index, prefix lookup, personal-history boost | <10ms local suggestions |
| 5 | Editor integration: dropdown, keybindings, on/off toggle | usable IME |

Steps 1–3 are independent of the Phase 4 editor and can land now. Steps 4–5 need the editor and
belong with Phase 4 — **but the dev-test harness can carry a crude version of the dropdown today**,
so the feature is testable long before the real editor exists.

---

## 11. Open questions for sign-off

1. **Top-20k client index (1 MB)** — right trade, or start server-only and add the client tier once
   RUM tells us the real latency from India?
2. **Do we need the OOV generator in v1?** I say yes — an IME that cannot type your own name is
   broken — but it is the largest single chunk of work here.
3. **Is `Tab` or `Enter` the insert key?** Enter is more discoverable; Tab does not fight with
   newline. Recommend **Enter to accept when the dropdown is open**, Esc to dismiss.

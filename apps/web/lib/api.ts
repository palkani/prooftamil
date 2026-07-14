import { suggestLocally } from "./ime-local";
import type { IMESuggestion, ProofreadResult, StreamEvent, Suggestion } from "./types";

export const API_BASE =
  process.env.NEXT_PUBLIC_API_BASE ?? "http://localhost:8080";

/**
 * Proofread, streaming.
 *
 * STALE-RESPONSE DROP IS THE POINT (plan §9, Phase 4).
 *
 * A person types faster than a model answers. Gemini takes ~1s; a fast typist has
 * rewritten the sentence twice by then. Without cancellation the editor would
 * receive suggestions describing text that no longer exists and underline the wrong
 * words — the classic "spellchecker fighting the user" bug.
 *
 * Two mechanisms, both needed:
 *   - AbortController kills the in-flight request when a newer one starts, so we do
 *     not pay for corrections nobody will read.
 *   - A generation counter drops any response that arrives after a newer request was
 *     issued, because abort is not instantaneous.
 */
export class ProofreadClient {
  private controller: AbortController | null = null;
  private generation = 0;

  cancel() {
    this.controller?.abort();
    this.controller = null;
  }

  /** Fire-and-forget streaming proofread. `onSuggestions` may be called many times. */
  async stream(
    text: string,
    handlers: {
      onSuggestions: (s: Suggestion[]) => void;
      onDone?: (modelPending: boolean) => void;
      onError?: (e: string) => void;
    },
  ): Promise<void> {
    this.cancel();

    const mine = ++this.generation;
    const controller = new AbortController();
    this.controller = controller;

    const url = `${API_BASE}/api/v1/proofread/stream?text=${encodeURIComponent(text)}`;

    try {
      const res = await fetch(url, {
        signal: controller.signal,
        headers: { Accept: "text/event-stream" },
      });
      if (!res.ok || !res.body) throw new Error(`HTTP ${res.status}`);

      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buffer = "";

      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        // A newer request has superseded this one; stop reading and let it go.
        if (mine !== this.generation) return;

        buffer += decoder.decode(value, { stream: true });

        // SSE frames are separated by a blank line. A chunk can split a frame, so
        // keep the remainder in the buffer rather than parsing half a message.
        const frames = buffer.split("\n\n");
        buffer = frames.pop() ?? "";

        for (const frame of frames) {
          const line = frame.split("\n").find((l) => l.startsWith("data:"));
          if (!line) continue;

          let ev: StreamEvent;
          try {
            ev = JSON.parse(line.slice(5).trim());
          } catch {
            continue; // a malformed frame must not kill the stream
          }

          if (mine !== this.generation) return;

          if (ev.type === "suggestions" && ev.suggestions?.length) {
            handlers.onSuggestions(ev.suggestions);
          } else if (ev.type === "done") {
            handlers.onDone?.(Boolean(ev.model_pending));
          } else if (ev.type === "error") {
            handlers.onError?.(ev.error ?? "unknown error");
          }
        }
      }
    } catch (e) {
      // An abort is the normal way a stream ends here (the user kept typing), not a
      // failure worth reporting.
      if ((e as Error).name === "AbortError") return;
      handlers.onError?.((e as Error).message);
    }
  }

  /** Non-streaming, for an explicit "check now". */
  async proofread(text: string): Promise<ProofreadResult> {
    const res = await fetch(`${API_BASE}/api/v1/proofread`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ text }),
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    return res.json();
  }
}

/**
 * IME lookup (RFC-001).
 *
 * Server-backed for now. RFC-001 §4 specifies a client-side index (top-20k words,
 * ~1MB gz) so 80% of keystrokes never leave the device — that is a payload decision
 * to make against real RUM data, not a guess. Until then, every keystroke is a round
 * trip: fine on localhost, and NOT fine from India.
 *
 * Privacy: this endpoint takes no identity and its answers are user-independent, so
 * it is edge-cached. Do not add a user id here to personalise ranking — personal
 * history belongs in localStorage, where the user's picks never leave the machine.
 */
export async function suggestTamil(
  q: string,
  limit = 8,
  signal?: AbortSignal,
): Promise<IMESuggestion[]> {
  if (q.length < 2) return [];

  // LOCAL FIRST (RFC-001 §4). The client index carries the 30,000 most common words —
  // ~83% of real Tamil usage — and answers in microseconds with no network at all. This
  // is the whole point: an IME has to feel like part of the keyboard, and from India a
  // round trip alone can exceed the entire latency budget.
  //
  // null means "the index cannot answer this" — either it has not loaded yet, or the
  // word is in the 17% tail we deliberately left on the server. The caller cannot tell
  // the two apart, and should not: both mean ask the server.
  const local = suggestLocally(q, limit);
  if (local) return local;

  const res = await fetch(
    `${API_BASE}/api/v1/suggest?q=${encodeURIComponent(q)}&limit=${limit}`,
    { signal },
  );
  if (!res.ok) return [];
  const data = await res.json();
  return data.suggestions ?? [];
}

/**
 * Correction feedback (§7.2, Phase 3).
 *
 * A REJECTION IS THE MOST VALUABLE EVENT THE PRODUCT PRODUCES: a human saying "you were
 * wrong" about a specific correction, with the tier and confidence that produced it
 * attached. That is exactly the labelled data the eval set is starved of.
 *
 * Fire-and-forget, and errors are swallowed on purpose. Telemetry is worth a lot to US
 * and nothing to the person who just clicked Accept — it must never be able to make their
 * click fail, or block the edit behind a network round trip.
 */
export function sendFeedback(
  action: "accept" | "reject",
  s: Suggestion,
): void {
  void fetch(`${API_BASE}/api/v1/corrections/${action}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      original: s.original,
      suggestion: s.suggestion,
      type: s.type,
      source_tier: s.source_tier,
      confidence: s.confidence,
    }),
    keepalive: true, // survives the page being closed right after a click
  }).catch(() => {
    /* telemetry must never surface to the user */
  });
}

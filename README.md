# ProofTamil v2

Tamil (தமிழ்) proofreading platform. A deterministic cascade resolves most corrections
before any paid model is called; a model router (Sarvam primary, Gemini verifier) handles
the residue.

Built against `ProofTamil_v2_Implementation_Plan.md`. Section references below (§N) point
into that plan.

---

## Quick start

```bash
make bootstrap   # install Go, Python and tooling deps (idempotent)
make dev         # api on :8080, ml on :8081
make smoke       # probe health + readiness
```

Or the full stack with backing services:

```bash
docker compose up   # + postgres, redis, qdrant
```

## Layout

```
apps/
  api/        Go serving plane — cascade orchestrator, ModelRouter (§3.1)
  ml/         Python FastAPI — Tamil morphology + parsing, cascade Tier 1 (§3.2)
  worker/     Temporal workers — billing, drip email, training pipeline (§3.3)
  web/        Next.js frontend — TipTap editor, WASM Tier 0 (§3.4)
packages/
  proto/          gRPC/Connect definitions (api <-> ml)
  shared-go/      shared Go libs
  tamil-rules/    sandhi tables, dictionaries, versioned model prompts (§8)
infra/
  env-manifest.yaml   ← single source of truth for EVERY env var (§3)
  terraform/          all cloud resources, per-env workspaces (§5)
  migrations/         advisory-locked SQL migrations (§6)
  docker/             Dockerfiles per service
eval/           accuracy harness + labeled Tamil test set (§11)
scripts/        envctl, smoke-test, rollback
```

## The cascade

Cost and latency both fall as a correction is resolved earlier:

| Tier | Where | What |
|---|---|---|
| 0 | browser (WASM) | dictionary lookup, debounce, segment-diff |
| 1 | `apps/ml` | ThamizhiMorph / UDp — deterministic legal-form + rule checks |
| 2 | Redis → Qdrant | exact cache, then semantic cache |
| 3 | Sarvam | primary corrector (§8.1) |
| 4 | Gemini | verifier on low confidence / disagreement (§8.2) |

Only a Tier 1+2 miss reaches a paid model.

## Environment variables

**No env var is ever hand-typed into a cloud console.** `infra/env-manifest.yaml` is the
only source of truth; everything else is generated from it.

```bash
make env-example              # regenerate every apps/*/.env.example
make secrets ENV=prod         # list the Secret Manager secrets to create
make set-env ENV=prod REGION=asia-south1        # dry run — prints the exact gcloud calls
make set-env-apply ENV=prod REGION=asia-south1  # execute
```

Two invariants the tooling enforces:

- **Secrets are bound by reference**, never by value: `KEY=projects/P/secrets/S:latest`.
  A raw secret never passes through this repo, your shell history, or CI logs. Rotating a
  key means adding a Secret Manager version and rolling a revision — never a code change.
- **Unresolved `${PLACEHOLDER}`s are fatal outside dev.** Those interpolate from
  `terraform output`; shipping an empty `ML_SERVICE_URL` to prod is an outage, so
  `set-env` refuses rather than deploying a service that is quietly broken.

Adding a variable = one line in the manifest + `make set-env-apply`.

## Health endpoints

- `GET /internal/health` — liveness. Touches no dependency on purpose: a database outage
  must not cause Cloud Run to kill otherwise-healthy instances.
- `GET /internal/ready` — readiness. Probes every dependency in parallel. **Required**
  deps (Postgres, Redis, ML) failing → `503`, which takes the instance out of rotation and
  arms auto-rollback. **Optional** deps (Qdrant, read replica) failing → still `200`, but
  reported: a semantic-cache outage degrades to a cache miss, it is not an outage.

## Status

Phase 0 (Foundations) is complete: monorepo, both services building and tested, env
automation, CI, Docker, local stack.

Phase 1 (deterministic cascade + cache) is next, and is where the cost and latency win
actually lands — see the plan's §9.

**Blocked on human gates (§1):** cloud provisioning cannot start until the GCP projects,
Supabase, Cloudflare, Upstash, Qdrant, Sarvam and Dodo accounts exist and their first API
keys are issued. Everything above runs locally without them.

.DEFAULT_GOAL := help
SHELL := /bin/bash

TOOLS_PY := ./.venv-tools/bin/python
ML_PY    := ./apps/ml/.venv/bin/python
ENV      ?= dev
REGION   ?= asia-south1

.PHONY: help
help: ## Show available targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

# ---------------------------------------------------------------- setup

.PHONY: bootstrap
bootstrap: ## Install all local toolchains (idempotent)
	python3 -m venv .venv-tools && ./.venv-tools/bin/pip install -q --upgrade pip pyyaml
	cd apps/ml && python3 -m venv .venv && ./.venv/bin/pip install -q --upgrade pip && ./.venv/bin/pip install -q -e ".[dev]"
	cd apps/api && go mod download
	@echo "bootstrap complete"

# ---------------------------------------------------------------- dev

# Local secrets. Git-ignored; see .env.
# Exported so the services inherit them without any key ever appearing on a
# command line (where it would land in shell history and `ps` output).
ifneq (,$(wildcard .env))
include .env
export
endif

.PHONY: dev
dev: ## Run api + ml locally (Ctrl-C stops both). Loads .env if present.
	@echo "api  -> http://localhost:8080/internal/health"
	@echo "ml   -> http://localhost:8081/health"
	@test -f .env && echo "secrets -> .env loaded" || echo "secrets -> none (.env absent; models disabled)"
	@trap 'kill 0' INT TERM EXIT; \
	( cd apps/ml  && APP_ENV=dev ./.venv/bin/uvicorn app.main:app --port 8081 --reload ) & \
	( cd apps/api && APP_ENV=dev PORT=8080 ML_SERVICE_URL=http://localhost:8081 go run ./cmd/server ) & \
	wait

# 4173, not 3000: port 3000 is the default for every Node dev server on the planet,
# and a collision does not fail loudly — the static server just loses the bind and
# you silently browse whatever else is listening.
DEMO_PORT ?= 4173

.PHONY: demo
demo: ## Run the full stack + a browser test page (Ctrl-C stops all)
	@for p in $(DEMO_PORT) 8080 8081; do \
	  if lsof -nP -iTCP:$$p -sTCP:LISTEN >/dev/null 2>&1; then \
	    echo "ERROR: port $$p is already in use. Free it, or: make demo DEMO_PORT=xxxx"; exit 1; \
	  fi; \
	done
	@echo ""
	@echo "  ProofTamil dev stack"
	@echo "  --------------------"
	@echo "  test page  ->  http://localhost:$(DEMO_PORT)/dev-test.html   <-- open this"
	@echo "  api        ->  http://localhost:8080"
	@echo "  ml         ->  http://localhost:8081"
	@test -f .env && echo "  keys       ->  .env loaded (model tiers ON)" \
	              || echo "  keys       ->  none (.env absent; Tier 1 only)"
	@echo ""
	@trap 'kill 0' INT TERM EXIT; \
	( cd apps/ml  && APP_ENV=dev ./.venv/bin/uvicorn app.main:app --port 8081 ) & \
	( cd apps/api && APP_ENV=dev PORT=8080 ML_SERVICE_URL=http://localhost:8081 go run ./cmd/server ) & \
	( cd apps/web && python3 -m http.server $(DEMO_PORT) ) & \
	wait

.PHONY: smoke
smoke: ## Probe a running stack's health + readiness
	@scripts/smoke-test.sh

# ---------------------------------------------------------------- quality

.PHONY: build
build: ## Compile every service
	cd apps/api && go build ./...

.PHONY: test
test: ## Run all tests
	cd apps/api && go test ./...
	cd apps/ml  && ./.venv/bin/pytest -q

.PHONY: lint
lint: ## Lint every language
	cd apps/api && go vet ./... && gofmt -l . | (! grep .) || (echo "gofmt: files need formatting"; exit 1)
	cd apps/ml  && ./.venv/bin/ruff check .

.PHONY: fmt
fmt: ## Auto-format
	cd apps/api && gofmt -w .
	cd apps/ml  && ./.venv/bin/ruff check --fix . && ./.venv/bin/ruff format .

# ---------------------------------------------------------------- env automation (§3)

.PHONY: env-example
env-example: ## Regenerate every app's .env.example from the manifest
	$(TOOLS_PY) scripts/envctl.py gen-example

.PHONY: secrets
secrets: ## List the Secret Manager secrets required for ENV
	$(TOOLS_PY) scripts/envctl.py secrets --env $(ENV)

.PHONY: set-env
set-env: ## Dry-run pushing all env vars to Cloud Run (ENV=, REGION=)
	$(TOOLS_PY) scripts/envctl.py set-env --env $(ENV) --region $(REGION)

.PHONY: set-env-apply
set-env-apply: ## Actually push all env vars to Cloud Run (ENV=, REGION=)
	$(TOOLS_PY) scripts/envctl.py set-env --env $(ENV) --region $(REGION) --apply

# ---------------------------------------------------------------- eval (§11)

.PHONY: eval
eval: ## Run the accuracy harness over the labeled Tamil test set (§11)
	$(ML_PY) eval/run.py

.PHONY: audit
audit: ## Held-out false-positive audit on real Tamil prose (RISK R1)
	$(ML_PY) eval/corpus_audit.py

.PHONY: eval-ime
eval-ime: ## IME accuracy: romanized -> Tamil, top-1/top-3/MRR (RFC-001)
	$(ML_PY) eval/ime_eval.py

.PHONY: lexicon
lexicon: ## Rebuild the lexicon from a Tamil Wikipedia dump (WIKI=path/to/dump.xml.bz2)
	@test -n "$(WIKI)" || (echo "usage: make lexicon WIKI=tawiki-latest-pages-articles.xml.bz2"; exit 1)
	$(ML_PY) scripts/build-lexicon.py --wiki $(WIKI) \
	  --out packages/tamil-rules/dictionaries/corpus.txt.gz

.PHONY: clean
clean:
	rm -rf apps/api/bin apps/web/.next .ruff_cache

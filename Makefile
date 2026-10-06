# Local development and CI entry points (README → Local development).
# `make check` runs exactly what .github/workflows/ci.yml runs.

SHELL := /bin/bash
-include .env
export

FUNCTIONS := api mcp stripe-webhook webhook-worker jobs
GOFLAGS_LAMBDA := CGO_ENABLED=0 GOOS=linux GOARCH=arm64

.PHONY: help up down reset migrate migration seed run gen gen-check test test-integration test-contract \
        quickstart lint fmt-check check build tf-fmt tf-validate tf-plan job jobs stripe-listen stripe-catalog

help: ## List targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-18s %s\n", $$1, $$2}'

up: ## Start Postgres, LocalStack, and Mailpit
	docker compose up -d --wait postgres
	docker compose up -d localstack mailpit

down: ## Stop local services (data is kept)
	docker compose down

reset: ## Wipe local data and start fresh
	docker compose down -v
	$(MAKE) up migrate

migrate: ## Apply migrations to local Postgres
	go run ./cmd/migrate

migration: ## Create the next migration: make migration name=add_x
	go run ./cmd/migrate -new $(name)

seed: ## Bootstrap a local superadmin (ADMIN_EMAIL / ADMIN_PASSWORD)
	ADMIN_PASSWORD="$(ADMIN_PASSWORD)" go run ./cmd/admin bootstrap -email "$(ADMIN_EMAIL)"

run: ## Run the devserver on LISTEN_ADDR (default :8080)
	go run ./cmd/devserver

gen: ## Regenerate code from docs/openapi.yaml (routes + MCP tools)
	go generate ./internal/api/gen
	go run ./cmd/mcpgen

gen-check: gen ## Fail if generated code is stale
	@git diff --exit-code -- internal/api/gen internal/mcp/tools.json || \
	  (echo "generated code is stale: run 'make gen' and commit the result" >&2; exit 1)

test: ## Unit tests (no containers)
	go test ./internal/access/... ./internal/schema/... ./internal/auth/... ./internal/db/... ./internal/httpx/...

test-integration: ## Integration + contract tests against Postgres (needs `make up`)
	go test -count=1 ./...

test-contract: test-integration ## Contract tests run inside the integration suite (every response is validated)

quickstart: ## docs/quickstart.md as a script, against the running devserver
	SUBSTRATAL_API=$${SUBSTRATAL_API:-http://localhost$(LISTEN_ADDR)/v1} scripts/quickstart.sh

fmt-check:
	@test -z "$$(gofmt -l cmd internal migrations)" || (gofmt -l cmd internal migrations; exit 1)

lint: fmt-check ## gofmt, go vet, golangci-lint (if installed)
	go vet ./...
	@if command -v golangci-lint >/dev/null; then golangci-lint run; else echo "golangci-lint not installed; CI runs it"; fi

check: lint gen-check test-integration build tf-fmt ## Everything CI runs

build: ## Lambda artifacts for every function into dist/
	@mkdir -p dist
	@for f in $(FUNCTIONS); do \
	  echo "build $$f"; \
	  $(GOFLAGS_LAMBDA) go build -trimpath -tags lambda.norpc -ldflags="-s -w" -o dist/$$f/bootstrap ./cmd/$$f || exit 1; \
	  (cd dist/$$f && rm -f ../$$f.zip && zip -q -X ../$$f.zip bootstrap); \
	done
	@ls -la dist/*.zip

tf-fmt: ## terraform fmt -check
	terraform fmt -check -recursive infra/terraform

tf-validate: ## terraform validate every root
	@for d in infra/terraform/bootstrap infra/terraform/prod; do \
	  (cd $$d && terraform init -backend=false -input=false >/dev/null && terraform validate) || exit 1; done

tf-plan: ## terraform plan for production (your own read-only credentials)
	cd infra/terraform/prod && terraform init -input=false && terraform plan

job: ## Run one scheduled job locally: make job name=entitlement-sweep
	go run ./cmd/jobs -job $(name)

jobs: ## List scheduled jobs
	go run ./cmd/jobs -list

stripe-listen: ## Forward Stripe test-mode webhooks to the devserver
	stripe listen --forward-to localhost$(LISTEN_ADDR)/internal/stripe/webhook

stripe-catalog: ## Create the Stripe Products/Prices by lookup key (idempotent; test mode)
	go run ./cmd/stripe-catalog

SHELL := /bin/bash
.DEFAULT_GOAL := help

FRONTEND_PORT ?= 7111
BACKEND_PORT  ?= 7112
GO_MIN        := 1.26
ENV_BACKEND   ?= secrets/.env.backend.local
ENV_FILES     := secrets/.env.backend.local secrets/.env.backend.prod secrets/.env.frontend.local secrets/.env.frontend.prod

# Build identity, the same ldflags the Dockerfile and the Tiltfile use.
API_PKG := github.com/forefy/vig/backend/internal/api
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -s -w -X $(API_PKG).Version=$(VERSION) -X $(API_PKG).Commit=$(COMMIT)
IMAGE   ?= vig:local
SITE_URL ?=

help: ## Show targets
	@grep -E '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-16s %s\n", $$1, $$2}'

# ---- setup ----

setup: ## Install toolchain deps (go modules, npm, git-secret)
	@command -v go >/dev/null || { echo "install Go $(GO_MIN)+"; exit 1; }
	@go version | grep -qE 'go(1\.(2[6-9]|[3-9][0-9])|[2-9]\.)' || { echo "Go $(GO_MIN)+ required, found: $$(go version)"; exit 1; }
	@command -v node >/dev/null || { echo "install Node 20+"; exit 1; }
	@command -v git-secret >/dev/null || brew install git-secret
	cd backend && go mod download
	cd frontend && npm install

$(ENV_BACKEND):
	@echo "missing $@: run 'make decrypt-env' (needs your GPG key) or 'cp secrets/.env.backend.example $@'"; exit 1

# ---- database ----

db-up: ## Start local Postgres on :7132 (docker compose)
	docker compose -f deploy/local/compose.yaml up -d --wait

db-down: ## Stop local Postgres
	docker compose -f deploy/local/compose.yaml down

db: ## Embedded Postgres on :7132 (VIG_DB_PORT) for machines without Docker; data in backend/data/pg
	cd backend && go run ./cmd/vig db

migrate: $(ENV_BACKEND) ## Apply migrations to DATABASE_URL from secrets/.env.backend.local
	@set -a; source $(ENV_BACKEND); set +a; cd backend && go run ./cmd/vig migrate up

migrate-status: $(ENV_BACKEND) ## Migration status
	@set -a; source $(ENV_BACKEND); set +a; cd backend && go run ./cmd/vig migrate status

migrate-down: $(ENV_BACKEND) ## Roll back the last migration
	@set -a; source $(ENV_BACKEND); set +a; cd backend && go run ./cmd/vig migrate down

seed: $(ENV_BACKEND) ## Write the development dataset (idempotent, refuses in prod)
	@set -a; source $(ENV_BACKEND); set +a; cd backend && go run ./cmd/vig seed

# ---- dev loop ----

dev-backend: $(ENV_BACKEND) ## Run the API on :7112 with docs at /docs
	@set -a; source $(ENV_BACKEND); set +a; cd backend && PORT=$(BACKEND_PORT) go run ./cmd/vig

dev-frontend: ## Run Vite on :7111
	cd frontend && npm run dev -- --port $(FRONTEND_PORT)

dev: ## Run backend and frontend together
	@$(MAKE) -j2 dev-backend dev-frontend

# ---- contract ----

openapi: ## Write backend/openapi.json from the Go types (via a temp file, never a half-written spec)
	@cd backend && go run ./cmd/vig openapi > openapi.json.tmp && mv openapi.json.tmp openapi.json || { rm -f openapi.json.tmp; exit 1; }

gen: openapi ## Regenerate the TypeScript client from the spec
	cd frontend && npm run gen

# ---- quality ----

lint: ## gofmt, go vet, oxlint
	@cd backend && out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	cd backend && go vet ./...
	cd frontend && npm run lint

test: ## Backend tests, frontend typecheck, lint and unit tests
	cd backend && go vet ./... && RATE_LIMIT_DISABLED=1 go test ./...
	cd frontend && npm run typecheck && npm run lint && npm test --if-present

# ---- build ----

build: ## Production build: vig and vig-op with Version/Commit, prerendered Vite dist
	cd backend && CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/vig ./cmd/vig
	cd backend && CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/vig-op ./cmd/vig-op
	cd frontend && SITE_URL=$(SITE_URL) npm run build

docker: ## Build the production image vig:local (IMAGE=..., SITE_URL=https://host bakes canonical URLs)
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg SITE_URL=$(SITE_URL) --build-arg VITE_SITE_URL=$(SITE_URL) -t $(IMAGE) .

docker-run: $(ENV_BACKEND) ## Run vig:local on :7112 with secrets/.env.backend.local (DATABASE_URL must be reachable from the container)
	docker run --rm -p $(BACKEND_PORT):7112 --env-file $(ENV_BACKEND) -e VIG_ENV=dev -e PORT=7112 $(IMAGE)

# ---- prod-parity cluster (deploy/dev/README.md) ----

k3d-up: ## Create the local k3d cluster and registry
	@for t in k3d kubectl helm tilt; do command -v $$t >/dev/null || { echo "install $$t"; exit 1; }; done
	./deploy/dev/k3d-up.sh

tilt: ## Deploy the prod chart to k3d with hot reload
	tilt up

k3d-down: ## Delete the local k3d cluster
	k3d cluster delete vig-dev

chart-lint: ## helm lint the chart with prod and dev values
	helm lint deploy/chart
	helm lint deploy/chart -f deploy/chart/values.dev.yaml

# ---- secrets (git-secret) ----

setup-secrets: ## Initialise git-secret with the forefy key
	@command -v git-secret >/dev/null || brew install git-secret
	@[ -d .gitsecret ] || git secret init
	@git secret tell admin@forefy.com
	@for f in $(ENV_FILES); do [ -f $$f ] && git secret add $$f || true; done

encrypt-env: ## Encrypt secrets/*.local and *.prod for commit
	@for f in $(ENV_FILES); do [ -f $$f ] && git secret add $$f || true; done
	git secret hide -m

decrypt-env: ## Decrypt secrets (needs your GPG key)
	git secret reveal -f

clean: ## Remove build output (keeps backend/openapi.json, which is committed)
	rm -rf backend/bin frontend/dist frontend/node_modules/.vite

.PHONY: help setup db-up db-down db migrate migrate-status migrate-down seed dev-backend dev-frontend dev openapi gen lint test build docker docker-run k3d-up tilt k3d-down chart-lint setup-secrets encrypt-env decrypt-env clean

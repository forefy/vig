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

# ---- demo index ----

# The demo's Atlas indexes come from real code: every seeded audit gets an
# index built by vig-op's own indexer from its repository at the pinned
# commit, and the Mattermost plugin audit gets the whole engagement, the
# twelve plugin repositories the public Bugcrowd scope lists. Needs git and
# a cgo build of vig-op (the default on a workstation). A clone that is
# already at its commit is not fetched again. The clones hold Go packages,
# so backend/data carries a go.mod of its own that keeps `go test ./...`
# out of them.
DEMO_INDEX_ROOT ?= backend/data/demo
# The engagement: one target over twelve repositories, catalogued against
# the plugin-github audit.
DEMO_ENGAGEMENT_REPO ?= mattermost/mattermost-plugin-github
DEMO_ENGAGEMENT_REPOS := \
	mattermost/mattermost-plugin-jira=074eb689cb505475b46c7243a335b93e78a24b1d \
	mattermost/mattermost-plugin-zoom=828ca56b3b1ebf4790f797d31c43b4b10b2749a3 \
	mattermost/mattermost-plugin-github=2b9840ce473dfeb222ee69038f31f5d0b428e8ae \
	mattermost/mattermost-plugin-gitlab=8916a039f1ba0f27bd550c17adb8507018f95074 \
	mattermost/mattermost-plugin-calls=b3cc0387ff8f54a927db3e766ae63eb42dba88ba \
	mattermost/mattermost-plugin-playbooks=e2ad0b6892c29001a03dda9f35bbe9a8363105b4 \
	mattermost/mattermost-plugin-boards=136c219e42996a935c2c14cf21fa7284d26dbb62 \
	mattermost/mattermost-plugin-ai=ecbc93994b59bbd3aee780c5194783b52dec78a5 \
	mattermost/mattermost-plugin-mscalendar=c79b1c661b292fcc91bc339e1b30343ede34b767 \
	mattermost/mattermost-plugin-msteams-meetings=1b1b8717d505b2c6436cb93b30cdd9aeef9211d0 \
	mattermost/mattermost-plugin-confluence=ea159ad5e5c8c4762c4963061d2ac16dadbeb267 \
	mattermost/mattermost-plugin-msteams=d9ad4dd7030b052bf0fecce0e15a78cd08621975
# The other seeded audits (seed.engagements), each its own single-repository build.
# Sites the demo captures with a headless browser (frontend/scripts/har.mjs)
# and indexes from the capture alone, against the site audit the seed queues.
DEMO_SITES := https://openrouter.ai

DEMO_AUDIT_REPOS := \
	freedomofpress/securedrop=9f86aedc9b7005ec99042242df157c4b31ac5155 \
	mattermost/mattermost-plugin-calls=b3cc0387ff8f54a927db3e766ae63eb42dba88ba \
	auth0/auth0-spa-js=0ea366c6e9e82e7cdc58af5b12efa29bad6f8837 \
	mattermost/mattermost-plugin-jira=074eb689cb505475b46c7243a335b93e78a24b1d \
	trustwallet/wallet-core=2a8aa02011ec42b43689667682fa99ae905418d5 \
	fireblocks/mpc-lib=00ae08b7f1bdbb887aedece384aca2f942aae432 \
	ClickHouse/ClickHouse=43e51854bef9371780c185401efdfe5904b559bc

# fetch-at: clone a repository at one commit under the demo root, once.
define fetch-at
	repo=$${entry%%=*}; sha=$${entry##*=}; dir=$(DEMO_INDEX_ROOT)/src/$$repo; \
	if [ ! -d $$dir/.git ]; then mkdir -p $$dir && git -C $$dir init -q && git -C $$dir remote add origin https://github.com/$$repo.git; fi; \
	if [ "$$(git -C $$dir rev-parse HEAD 2>/dev/null)" != "$$sha" ]; then echo "fetching $$repo @ $${sha%????????????????????????????????}"; git -C $$dir fetch -q --depth 1 origin $$sha && git -C $$dir checkout -q --detach $$sha; fi
endef

demo-index: $(ENV_BACKEND) ## Build the demo Atlas indexes from the real repositories with vig-op and catalogue them
	@mkdir -p backend/data && test -f backend/data/go.mod || printf 'module demo\n\ngo 1.26\n' > backend/data/go.mod
	@for entry in $(DEMO_ENGAGEMENT_REPOS) $(DEMO_AUDIT_REPOS); do $(fetch-at); done
	@cd backend && VIG_OP_CONTAINER=1 VIG_OP_HOME=$$(mktemp -d) go run ./cmd/vig-op index build --target mattermost --out ../$(DEMO_INDEX_ROOT) \
		$$(for entry in $(DEMO_ENGAGEMENT_REPOS); do repo=$${entry%%=*}; printf -- '--repo %s=../%s/src/%s ' $$repo $(DEMO_INDEX_ROOT) $$repo; done)
	@set -a; source $(ENV_BACKEND); set +a; cd backend && go run ./cmd/vig index import --repo $(DEMO_ENGAGEMENT_REPO) --root ../$(DEMO_INDEX_ROOT) --target mattermost --tool-version dev
	@for entry in $(DEMO_AUDIT_REPOS); do repo=$${entry%%=*}; slug=$$(echo $$repo | tr '/' '-' | tr 'A-Z' 'a-z'); \
		(cd backend && VIG_OP_CONTAINER=1 VIG_OP_HOME=$$(mktemp -d) go run ./cmd/vig-op index build --target $$slug --out ../$(DEMO_INDEX_ROOT) --repo $$repo=../$(DEMO_INDEX_ROOT)/src/$$repo) && \
		(set -a; source $(ENV_BACKEND); set +a; cd backend && go run ./cmd/vig index import --repo $$repo --root ../$(DEMO_INDEX_ROOT) --target $$slug --tool-version dev); \
	done
	@for site in $(DEMO_SITES); do host=$${site#*://}; slug=$$(echo $$host | tr '.' '-'); har=$(DEMO_INDEX_ROOT)/har/$$slug/session-1.har; \
		test -f $$har || (cd frontend && node scripts/har.mjs $$site ../$$har) && \
		(cd backend && VIG_OP_CONTAINER=1 VIG_OP_HOME=$$(mktemp -d) go run ./cmd/vig-op index build --target $$slug --out ../$(DEMO_INDEX_ROOT) --har ../$$har) && \
		(set -a; source $(ENV_BACKEND); set +a; cd backend && go run ./cmd/vig index import --repo $$site --root ../$(DEMO_INDEX_ROOT) --target $$slug --tool-version dev); \
	done

# ---- dev loop ----

# The live chain watch is off for a dev process (VIG_WATCH=0): a local
# database full of old audits would open a session to mainnet per audit on
# every start. `make dev-backend VIG_WATCH=1` turns it on.
VIG_WATCH ?= 0

dev-backend: $(ENV_BACKEND) ## Run the API on :7112 with docs at /docs
	@p=$$(lsof -tiTCP:$(BACKEND_PORT) -sTCP:LISTEN); [ -z "$$p" ] || { echo "stopping the API still on :$(BACKEND_PORT) (pid $$p)"; kill $$p; sleep 1; }
	@set -a; source $(ENV_BACKEND); set +a; cd backend && PORT=$(BACKEND_PORT) VIG_WATCH=$(VIG_WATCH) go run ./cmd/vig

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

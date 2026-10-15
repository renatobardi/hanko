# Hanko — entrypoint de build. No container de build use: make GO=gorun <alvo>
GO       ?= go
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BIN      := bin/hanko
PKG      := ./cmd/hanko
LDFLAGS  := -X main.version=$(VERSION)
COMPOSE  := docker compose -f deploy/compose.yaml --env-file .env
# Testes de integração leem HANKO_TEST_DATABASE_URL do ambiente (e fazem skip se vazio).

.PHONY: help build test test-integration lint fmt fmt-check vet generate openapi dev up down logs clean

help: ## lista os alvos
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-18s %s\n", $$1, $$2}'

build: ## compila bin/hanko (gera templ antes, se houver)
	@if find . -name '*.templ' -not -path './node_modules/*' | grep -q .; then templ generate; fi
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) $(PKG)

test: ## testes unitários (integração roda se HANKO_TEST_DATABASE_URL apontar para um Postgres)
	$(GO) test -race ./...

test-integration: ## testes com -tags integration (exige Postgres em HANKO_TEST_DATABASE_URL)
	$(GO) test -race -p 1 -tags integration ./...

lint: ## golangci-lint
	golangci-lint run ./...

fmt: ## formata (gofumpt se existir, senão gofmt)
	@if command -v gofumpt >/dev/null; then gofumpt -l -w .; else gofmt -l -w .; fi

fmt-check: ## falha se houver arquivo sem formatar
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "sem formatar:"; echo "$$out"; exit 1; fi

vet: ## go vet
	$(GO) vet ./...

generate: ## sqlc + templ
	sqlc generate
	templ generate

openapi: build ## regenera api/openapi.json
	$(BIN) openapi > api/openapi.json

dev: ## serve local (lê .env se existir)
	@if [ -f .env ]; then set -a; . ./.env; set +a; fi; \
	$(GO) run -ldflags "$(LDFLAGS)" $(PKG) serve

up: ## sobe a stack de deploy/compose.yaml (hanko + postgres + ai-memory + otel-lgtm)
	$(COMPOSE) up -d --build

down: ## derruba a stack
	$(COMPOSE) down

logs: ## segue os logs da stack
	$(COMPOSE) logs -f --tail=100

clean: ## remove artefatos
	rm -rf bin dist

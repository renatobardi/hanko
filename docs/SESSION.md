# Estado da última sessão
Data: 2026-10-14 (madrugada) · Orquestração: Opus 5.5 · Execução: Sonnet 5.5 (8 trilhas) · Revisão: Fable 5.1

## Feito
- **M0:** go.mod (Go 1.27), config (`HANKO_*` + `hanko.yaml`), OpenTelemetry mandatório (traces/métricas/logs OTLP + `/metrics`), pool pgx com otelpgx, migrations goose + river, wiring em `internal/app`, Makefile, Dockerfile(s), `deploy/compose.yaml` (hanko + pg17 + ai-memory + otel-lgtm + caddy/edge), CI e release (GitHub Actions + goreleaser), golangci, RUNBOOK.
- **M1:** domínio + schema (ADR-0016), store sqlc, service (1 tx por caso de uso, activity na mesma tx, publicação pós-commit, idempotência por event_id, numeração atômica, checkout atômico), heartbeat river (wake coalescido, execute_run, tick periódico, órfãs no boot), adapters `claude` (stream-json), `codex` (exec --json) e `fake`, API REST huma `/api/v1` + SSE + `api/openapi.json`, CLI completa (`hanko company|persona|task|run|workspace|project …`).
- **M2:** UI templ + htmx no Kubo DS (Painel, Tasks, Task + conversa, Nova task, Personas, Contratar, Persona, Execuções, Execução ao vivo via SSE, Atividade, Onboarding; mobile 390 px; claro/escuro), servidor MCP `/mcp` (8 tools `hanko_*`, auth por run token), ai-memory ligado por padrão (briefing + handoff no despacho, fatos via `memory_outbox`, `finalize-session`, proxy `/memory-mcp` que nunca expõe o token da memória).
- **Revisão independente (Fable)** achou 15 problemas (3 críticos de segurança); todos corrigidos com teste: run token validado na API e no MCP (só com run ativo), persona sem poderes de admin, env do harness por allowlist (segredos do servidor não vazam), redação de segredos no log das execuções, checagem de Host (DNS rebinding), workspace confinado a `HANKO_WORKSPACES_ROOT`, basic_auth no edge, FinishRun com retry sem reexecutar o agente, comentário durante execução gera nova execução, persona não trava em erro (Reativar/Pausar na UI), órfãs reacordadas, fechar task cancela execução, outbox de memória com backoff sem perder fato.
- **Testes:** suíte completa verde (unitários + integração com Postgres real, `-race`). Cobertura: service ~90%, domain 100%, heartbeat ~81%, mcp 92%, aimemory 93%. E2E com o binário real: empresa → personas → tasks → execuções (fake e script que imita `claude -p`) → comentário humano → nova execução; 401 com token inválido; 421 com Host estranho.

## Não verificado aqui (rodar no Mac)
- `docker compose up`, imagens Docker, GitHub Actions, goreleaser, golangci-lint.
- `claude`/`codex` reais (só com script que imita a saída); ai-memory real (só servidor falso); digest da imagem do ai-memory (`AI_MEMORY_IMAGE` no `.env`).
- `go mod tidy` e `go mod verify` contra o proxy oficial (aqui foi espelho local montado do GitHub).

## Teste em 5 minutos (no Mac)
```bash
cd ~/Projects/Github/Hanko
go mod download && go build -o bin/hanko ./cmd/hanko && go test ./...
docker run -d --name hanko-pg -e POSTGRES_HOST_AUTH_METHOD=trust -p 127.0.0.1:5432:5432 postgres:17
export HANKO_DATABASE_URL="postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable" HANKO_SECRET=$(openssl rand -hex 32)
./bin/hanko serve &            # abre http://127.0.0.1:7420
./bin/hanko company create "Oute Lab" && export HANKO_COMPANY=oute-lab
./bin/hanko persona hire Ada --agent claude --title Engenheira
./bin/hanko task create "Diga olá e descreva o repositório" -p ada
./bin/hanko run logs -f $(./bin/hanko run list --json | jq -r '.[0].id')
```

## Decisões pendentes do dono
- **ADR-0017:** modo trusted assume agentes e operador no mesmo host; agente local pode falar com o loopback sem token. Escolher: rodar agentes isolados (container/LXC) ou antecipar o modo `authenticated` (Firebase) do M5.
- UI usa CSS próprio com tokens Kubo (não Tailwind) — confirmar ou reabrir ADR-0005.
- Gates M1/M2: gravar as demos e criar as tags `v0.1.0-m1`/`v0.1.0-m2`.

## Próximo
M3 (Organograma + Gates) — ver ROADMAP.

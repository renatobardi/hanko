> English: [docs/en/STACK.md](en/STACK.md)

# HANKO — Stack & Convenções (v0.1)

> **Estado real do código (2026-10-15).** Este documento é o plano de stack; onde ele diverge do código, vale o código e as ADRs:
> - CLI real: `serve, migrate, version, openapi, company, workspace, project, persona, task, run, gate, org, cost, budget, flow, auth, subscription` — `onboard`, `test-drive`, `agent`, `export`, `import` não existem.
> - Adapters em `internal/adapter`: `claude`, `codex`, `fake`. O adapter `http` (webhook) não foi feito.
> - MCP: servidor `/mcp` (tools `hanko_*`) e proxy `/memory-mcp` para o ai-memory; não há gateway allow/ask/off.
> - CSS próprio com tokens Kubo (adendo da ADR-0005), sem Tailwind; o JS do Firebase está versionado em `internal/ui/static/`, sem `tools/bundle`.
> - Não existem ainda: `internal/secrets`, `internal/storage`, `tools/bundle`, embedded-postgres, testcontainers (os testes usam `HANKO_TEST_DATABASE_URL`), `go-i18n`.
> - Imagem: `ghcr.io/renatobardi/hanko`.


Control plane open-source para orquestrar times de agentes de IA (tasks, org chart, heartbeat, governança, budget). URL: https://hanko.oute.pro
Conceitos de orquestração de agentes (company, agent, issue, heartbeat, run, approval, budget, routine) implementados do zero em Go. Nenhum código de terceiros.
Decisões em `docs/adr/`. Este arquivo é o resumo vigente; ADR vence em conflito.

## 1. Princípios
- Single binary: `hanko` sobe API + UI embutida + scheduler. Zero Node em runtime; Node só como ferramenta de build das ilhas (dev/CI). (ADR-0005)
- Postgres é a única dependência obrigatória. ai-memory vem ligado por padrão e é degradável (outbox no Postgres). Sem Redis, sem filas externas. (ADR-0011, ADR-0015)
- Chato na infra, novo no domínio: novidade vai em heartbeat/governança/budget, nunca em DB, fila ou framework.
- Code-first: Go é a fonte da verdade; OpenAPI é gerado e versionado. (ADR-0006)
- Stdlib primeiro. Dependência só quando a stdlib não resolve.
- Observabilidade OpenTelemetry completa e sempre ligada (traces, métricas, logs correlacionados). Exporta para o collector do operador. Zero telemetria de produto, zero phone-home. (ADR-0013)
- Multi-org desde o dia 1: `company_id` em toda tabela de domínio. (ADR-0010)
- Entrega visível a cada 2 semanas; escopo cede, prazo não. (ADR-0009)

## 2. Backend (Go)
| Área | Escolha | Observação |
|---|---|---|
| Linguagem | Go última stable (pin via `toolchain` no go.mod) | gofumpt, `go vet`, `govulncheck` |
| HTTP | `net/http` + `http.ServeMux` (method+path, Go ≥1.22) | middleware próprio: request-id, recover, slog, auth, CORS |
| API | `huma` v2 sobre `humago` — code-first: structs → `api/openapi.json` + validação + `/docs` | `make openapi` gera; CI falha se diff não commitado. Serve CLI, MCP, agentes externos e ilhas |
| Realtime | SSE (`text/event-stream`) p/ run logs, activity, threads | sem WebSocket, sem GraphQL |
| DB | PostgreSQL 17 | timestamptz UTC, JSONB p/ metadata, UUIDv7 como PK |
| Acesso DB | `pgx/v5` + `sqlc` | proibido ORM e `database/sql` cru; toda query em `db/queries/*.sql` |
| Migrations | `goose` v3, SQL puro, `//go:embed` | `hanko migrate` roda no boot (configurável) |
| Jobs / heartbeat | `river` (fila Postgres-nativa sobre pgx) | wakeup queue, retries, periodic jobs (routines), unique jobs = coalescing |
| Checkout atômico | `SELECT ... FOR UPDATE SKIP LOCKED` + execution lock por issue | 1 assignee, 1 run ativo |
| Adapters | interface `Adapter` em `internal/adapters`. v0.1: `cli` (`claude -p --output-format stream-json --verbose`, `codex exec --json`) e `http` (webhook) | `acp` = v0.2 (ADR-0004); plugins out-of-process = v1 |
| MCP | `github.com/modelcontextprotocol/go-sdk` | Hanko como MCP **server** (tools de task) e **gateway** (allow/ask/off) |
| Memória | ai-memory **ligado por padrão** (ADR-0015). Personas: MCP+hooks por run, projeto = workspace, sessão = run_id, `finalize-session`. Hanko: MCP client (`go-sdk`) — escreve fatos (task, gate, run, delegação) via `memory_outbox` + river; lê `memory_briefing` + handoff no despacho; busca na UI. Indisponível = degrada, nunca bloqueia | Governança fica no Postgres |
| LLM SDKs | `anthropic-sdk-go`, `openai-go` | só evals e metadata de custo; core não chama LLM |
| Auth v0.1 | providers: `local` (default self-host: email/senha argon2id + cookie) e `firebase` (Google + email/senha gerenciados pelo Firebase; servidor valida ID token via JWKS do Google, sem Admin SDK). API keys de agente (SHA-256); run JWT curto (`golang-jwt/v5`) | `HANKO_AUTH_PROVIDER=local|firebase`. hanko.oute.pro usa firebase. Multi-humano v0.2; OIDC v1 (ADR-0007, ADR-0014) |
| Secrets | AES-256-GCM envelope, master key em `HANKO_MASTER_KEY` ou arquivo | refs injetadas no run, nunca em config |
| Storage | FS local (default) ou S3-compatível via `aws-sdk-go-v2` | attachments, work products |
| Config | env `HANKO_*` + `hanko.yaml`; `caarlos0/env/v11` + `yaml.v3` | env sobrescreve yaml |
| Logs | `log/slog` JSON via `otelslog` (trace_id/span_id automáticos) | request-id, company_id, run_id em todo log |
| Observabilidade | OpenTelemetry **mandatório**: `otelhttp` (server), `otelpgx` (DB), middleware OTel no river (span por job), span por run nos adapters (`company_id`, `agent_id`, `run_id`), `otelslog` bridge p/ logs, métricas OTel + `/metrics` Prometheus. Exporter OTLP gRPC, default `localhost:4317` | Instrumentação não desliga. Collector ausente = SDK descarta em silêncio, app nunca falha por OTel. Sem Sentry/PostHog (ADR-0013) |
| CLI | `cobra`: `hanko serve \| onboard \| migrate \| test-drive \| agent \| export \| import` | |
| IDs/Tempo | `google/uuid` (UUIDv7), `time.Time` UTC | |
| Local one-shot | `fergusstrange/embedded-postgres` só no `hanko test-drive` | produção = PG externo |
| Testes | `go test` + `go-cmp`; `testcontainers-go` (PG 17); table-driven | ≥70% em `internal/domain` e `internal/heartbeat` |
| Lint | `golangci-lint` v2 (staticcheck, errcheck, gosec, revive), `gofumpt` | CI bloqueia |
| Build/Release | `goreleaser` v2: linux/darwin × amd64/arm64, distroless → `ghcr.io/oute/hanko`, checksums, SBOM | `Makefile` como entrypoint |

## 3. Frontend — host + ilhas (ADR-0005)
**Host (templ + htmx)** é a app: roteamento, auth, layout, formulários, listas, board, threads, approvals, budget, hire, routines, settings. SSE troca fragmentos HTML.
**Ilhas (React)** são folhas para 3 peças pesadas, todas v0.2+.

| Área | Escolha |
|---|---|
| Templates | `templ` — componentes tipados em `internal/ui/components` |
| Interação | `htmx` 2 + extensão `sse`; Alpine.js só p/ estado local trivial |
| CSS | Tailwind **standalone CLI** (binário, sem Node); tokens em CSS vars; dark mode; **1 CSS** p/ host e ilhas |
| JS vendorizado | `sortable.js` (kanban). Máx. 3 arquivos vendorizados |
| Scripts | `tools/bundle` (esbuild via Go) também empacota o Firebase Auth SDK (modular) p/ o provider `firebase` — é script do host, não conta como ilha |
| Ilhas | React + TS bundladas por **esbuild via Go** (`go run ./tools/bundle`); deps em `ui/islands/package.json` (npm só dev/CI). Previstas: log viewer virtualizado, org chart (react-flow), editor (CodeMirror 6). Teto 3; a 4ª exige ADR |
| Markdown | `goldmark` server-side |
| i18n | `go-i18n`: pt-BR (M1), en (M6) |
| E2E | `chromedp` ou `rod` contra o binário |
| Mobile | responsivo; sem PWA no v0.1 |
| Embed | `internal/ui/static` via `go:embed`; SPA fallback não existe (host é server-rendered) |

Contrato host/ilha:
1. Host decide rota, auth, layout, navegação, dados de lista.
2. Ilha é folha: recebe props via `data-*`/JSON no HTML, emite eventos DOM ou chama a API. Nunca roteia, nunca tem estado global, nunca lista.
3. Um CSS, mesmos tokens. Sem shadcn global; Radix só dentro da ilha que precisar.
4. Bundling só por esbuild (Go). Sem Vite, pnpm workspace, React Router, TanStack.
5. Teto de 3 ilhas até v0.2. Checkpoint pós-M2: UI >50% das horas → reavaliar.

## 4. Layout do repositório
```
hanko/
  cmd/hanko/main.go
  internal/
    api/         handlers huma (fino: valida → service)
    domain/      entidades e regras (company, agent, issue, run, budget, approval, routine)
    service/     casos de uso; transações; emite activity events
    store/       sqlc gerado + repositórios
    heartbeat/   river workers, scheduler, wakeup, recovery
    adapters/    cli/, http/   (acp/ em v0.2)
    mcp/         server + gateway
    auth/ secrets/ storage/ config/ activity/
    ui/          templ pages + components, static/ (embed.go)
  ui/islands/    React/TS das ilhas (v0.2+), package.json
  tools/bundle/  esbuild via Go
  db/migrations/ goose SQL
  db/queries/    sqlc SQL
  api/openapi.json   gerado por `make openapi`
  deploy/        Dockerfile, compose.yaml (hanko+pg17+caddy), lxc/
  docs/          STACK.md, ROADMAP.md, adr/
  ee/            reservado (vazio no v0.1) — ADR-0003
  Makefile sqlc.yaml .goreleaser.yaml .golangci.yaml
```
Camadas: handler → service → store. Handler não toca DB. Store não contém regra de negócio. Domain não importa infra.

## 5. Deploy (ADR-0008)
- oute-server (OCI, LXD): containers `hanko-stg` (branch `develop`) e `hanko-prd` (tag `v*`). Compose: `hanko` + `postgres:17` + `caddy` (TLS) + `grafana/otel-lgtm` (collector + Tempo + Loki + Mimir + Grafana em 1 container; ~1,5 GB RAM). Split-horizon via Tailscale, runbook do kura-dev reaproveitado.
- Backup: `pg_dump` diário + `restic` → R2. Restore testado 1x/mês.
- CI (GitHub Actions): lint → vet → govulncheck → test (PG service) → openapi diff → bundle ilhas → build. Tag `v*` → goreleaser → ghcr.io + GitHub Release.
- VPS externo só quando houver usuário externo.

## 6. Convenções de código (Go)
- Erros: sempre `fmt.Errorf("op: %w", err)`; erros de domínio tipados em `internal/domain/errors.go`; mapeados p/ HTTP em um único lugar.
- `context.Context` sempre 1º parâmetro. Sem estado global. Sem `init()` com efeito colateral.
- Transação por caso de uso (`service`), nunca no handler nem no store.
- Toda escrita de domínio gera `activity_event` na mesma transação.
- Migrations append-only; nunca editar migration aplicada.
- Pacotes curtos minúsculos; sem `utils`, `common`, `helpers`.
- Testes ao lado do código; fixtures via builders, não SQL solto.
- Nenhum `panic` fora de `main`. Nenhum `log.Fatal` fora de `cmd/`.

## 7. Fora de escopo v0.1
SQLite; WebSocket; GraphQL; ACP; ilhas React; plugins out-of-process; RLS; sandboxes remotos (e2b/Daytona); OIDC; multi-humano; troca de org na UI; desktop; mobile nativo; PWA; Agent Chat; chat connectors (Slack/Discord/Telegram); editor com comentários ancorados; org chart interativo; log viewer virtualizado.

## 8. Licença (ADR-0003)
Apache-2.0 + DCO (`git commit -s`). `ee/` reservado p/ features enterprise sob licença comercial. Sem código de terceiros copiado; `THIRD_PARTY_NOTICES` via SBOM do goreleaser.

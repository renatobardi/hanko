# Contrato de implementação — M0, M1, M2

Fonte de verdade para as trilhas paralelas. Vocabulário: `docs/GLOSSARIO.md`. Stack: `docs/STACK.md` + ADRs.
Já existe e é contrato (não mudar sem o orquestrador): `internal/domain`, `db/migrations/00001_init.sql`,
`internal/adapter/adapter.go`, `internal/events`, `internal/memory/memory.go`, `internal/service/service.go`
(tipos), assinaturas de `internal/service/api.go`, `internal/auth/runtoken.go`, `go.mod`.

## Ambiente (container de build)
- Use **`gorun`** no lugar de `go` (espelho local de módulos; `proxy.golang.org` bloqueado). Ex.: `gorun build ./...`, `gorun test ./internal/x/...`.
- **Nunca** rode `go mod tidy`. Toda dependência do v0.1 já está no `go.mod` (`internal/deps`). Se precisar de outra, pare e reporte.
- Postgres 16 local: `postgres://postgres@127.0.0.1:5432/<db>?sslmode=disable`. Cada trilha tem o seu banco de teste (abaixo). Testes de integração leem `HANKO_TEST_DATABASE_URL` e fazem `t.Skip` se vazio.
- Binários: `templ` em `~/go/bin/templ`. sqlc: ver trilha A.

## Regras de código
- Camadas: handler (api/cli/mcp/ui) → `service` → store. Handler não toca DB. Heartbeat só chama `service` e `adapter`.
- `context.Context` primeiro parâmetro; erros `fmt.Errorf("op: %w", err)`; erros de domínio de `internal/domain/errors.go`.
- Sem `panic` fora de `main`; sem `log.Fatal` fora de `cmd/`; sem estado global; sem `init()` com efeito.
- Logs: `log/slog` recebido por injeção (`*slog.Logger`), nunca `slog.Default()` em pacote de biblioteca.
- Pacotes sem `utils`/`common`/`helpers`. Testes ao lado do código, table-driven, `go-cmp`.
- Nunca citar produtos de terceiros (nomes de concorrentes) em código, comentários, UI ou docs.
- Toda escrita de domínio gera `activity_event` na mesma transação (verbos: `company.created`, `workspace.created`, `project.created`, `persona.hired`, `persona.updated`, `task.created`, `task.updated`, `task.assigned`, `task.status_changed`, `task.closed`, `comment.added`, `run.queued`, `run.started`, `run.finished`, `run.cancelled`).

## Git
- Cada trilha trabalha **só** no seu worktree `/home/claude/wt/<trilha>` (branch `track/<trilha>`) e **só** nos seus diretórios.
- Commits pequenos, `git commit -s`, mensagem conventional em pt-BR, terminando com:
  ```
  Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01XtgKHjJbDv4oXGnmk6vQyN
  ```
- Antes do último commit: `gofmt -l .` vazio, `gorun vet ./...` e `gorun build ./...` verdes, testes da trilha verdes.

## Construtores (o que a trilha F conecta em `internal/app`)
| Pacote | Construtor | Monta em |
|---|---|---|
| `service` | `service.New(pool, jobs, broker)` | — |
| `heartbeat` | `heartbeat.New(pool *pgxpool.Pool, opts heartbeat.Options) (*Heartbeat, error)`; `(*Heartbeat) Attach(svc *service.Service, reg adapter.Registry, mem memory.Provider, broker *events.Broker, tokens auth.RunTokens)`; `Start(ctx) error`; `Stop(ctx) error`; implementa `service.Jobs`; `heartbeat.Migrate(ctx, pool) error` (rivermigrate) | — |
| `adapter/claude`, `adapter/codex`, `adapter/fake` | `claude.New(claude.Options{Bin string, Logger *slog.Logger})` etc. | `adapter.Registry{"claude":…, "codex":…, "fake":…}` |
| `api` | `api.Mount(mux *http.ServeMux, d api.Deps)`; `Deps{Svc *service.Service; Broker *events.Broker; Logger *slog.Logger; Version string}` | `/api/v1/*`, `/openapi.json`, `/docs` |
| `ui` | `ui.Mount(mux *http.ServeMux, d ui.Deps)`; `Deps{Svc; Broker; Logger; Version}` | `/`, `/c/{company}/...`, `/static/*` |
| `mcp` | `mcp.Handler(d mcp.Deps) http.Handler`; `Deps{Svc; Tokens auth.RunTokens; Logger; Version}` | `/mcp` |
| `memory/aimemory` | `aimemory.New(aimemory.Options{URL, Token string; HTTP *http.Client; Logger})` → `memory.Provider` | — |
| `store` (trilha A) | interno ao service | — |

`heartbeat.Options{Workers int; PublicURL string /* base que o agente usa p/ MCP */; RunTimeout time.Duration; PeriodicInterval time.Duration; Logger *slog.Logger}`.

## Fluxo do heartbeat (trilha B)
1. `service` enfileira `Wake` (river job `wake`, `UniqueOpts{ByArgs, ByState pendentes}` = coalescência) na mesma tx da escrita.
2. Worker `wake` → `svc.CheckoutRun(w)`; `ErrActiveRun`/`ErrInvalid` = no-op (log). `CheckoutRun` enfileira `execute_run`.
3. Worker `execute_run` (timeout = RunTimeout): `LoadRunContext` → memória: `mem.AcceptHandoff` + `mem.Briefing` (erro = aviso no log do run, segue) → monta `adapter.Spec` (System = instruções da persona + protocolo Hanko; Prompt = task + thread + memória; `Env` com `OTEL_RESOURCE_ATTRIBUTES=hanko.company_id=…,hanko.persona_id=…,hanko.run_id=…,hanko.task_id=…`, `CLAUDE_CODE_ENABLE_TELEMETRY=1`, `HANKO_URL`, `HANKO_RUN_TOKEN`; `MCPConfig` com servidor `hanko` = `{"type":"http","url":PublicURL+"/mcp","headers":{"Authorization":"Bearer <runtoken>"}}` + `mem.AgentMCP`) → `MarkRunStarted` → `adapter.Run` com sink que chama `svc.AppendRunEvent` → `FinishRun` → `mem.FinalizeSession` (erro só loga). Cancelamento: assina `events.RunTopic(run)` e cancela o ctx em `run.cancel`.
4. Periódico (river `PeriodicJob`, `PeriodicInterval`, default 1 min): `svc.WakeCandidates` → enfileira `wake` (trigger heartbeat).
5. Worker `deliver_memory`: `svc.GetMemoryItem` → `mem.Record` → `MarkMemorySent`/`MarkMemoryFailed` + erro (river faz retry com backoff). Periódico de varredura: `svc.PendingMemory` → reenfileira.
6. Boot: `svc.InterruptOrphanRuns`.
Span OTel por job e por run (atributos `hanko.*`); `otelriver` middleware.

## Adapters (trilha B)
- `claude`: `claude -p --output-format stream-json --verbose [--model M] [--append-system-prompt S] [--mcp-config file] [--resume ref] [--permission-mode X]` com o prompt via stdin; `X` de `adapter_config.permission_mode` (default `acceptEdits`); `adapter_config.extra_args` []string. Parse das linhas JSON (`system`/init → session_id; `assistant` → text/thinking/tool_use; `user` → tool_result; `result` → summary, `total_cost_usd`, `usage`, `is_error`). Binário de `Options.Bin` (default `claude`; env `HANKO_CLAUDE_BIN` lida no app).
- `codex`: `codex exec --json [-m M] [-C dir] -` com prompt no stdin; parse JSONL (eventos `thread.started`, `item.*` com `agent_message`/`command_execution`/`reasoning`, `turn.completed` com usage). Tolerante a campos desconhecidos.
- `fake`: sem processo; emite assistant "Analisando <TaskKey>…", um tool_use/tool_result fictício, result com summary determinístico e custo 0. Útil p/ demo e testes.
- Fixture `internal/adapter/claude/testdata/fake-claude.sh` que imprime stream-json realista; teste de parser + teste do processo real com o script.

## API (trilha C) — huma v2 code-first, prefixo `/api/v1`
`{company}` = id ou slug. Erros: `domain.ErrNotFound`→404, `ErrInvalid`→422, `ErrConflict`/`ErrActiveRun`→409, `ErrNotAllowed`→403 (um único mapeador).
- `GET /healthz`, `GET /readyz` (ping DB) — fora do huma, texto puro.
- `POST/GET /companies`, `GET /companies/{company}`
- `POST/GET /companies/{company}/workspaces`, `POST/GET /companies/{company}/projects`
- `POST/GET /companies/{company}/personas`, `GET/PATCH /companies/{company}/personas/{persona}`
- `POST/GET /companies/{company}/tasks` (filtros `status` csv, `persona`, `project`, `q`, `limit`, `offset`), `GET/PATCH /companies/{company}/tasks/{task}` (`{task}` = id ou chave), `POST /…/tasks/{task}/comments`, `GET /…/tasks/{task}/comments`, `POST /…/tasks/{task}/wake`
- `GET /companies/{company}/runs` (`task`, `persona`, `status`, `limit`), `GET /…/runs/{run}`, `GET /…/runs/{run}/events?after=&limit=`, `POST /…/runs/{run}/cancel`, `GET /…/runs/{run}/stream` (SSE: replay a partir de `?after=`/`Last-Event-ID`, depois ao vivo pelo broker; `id:` = seq; evento `run.status` no fim)
- `GET /companies/{company}/activity`, `GET /companies/{company}/dashboard`
Ator: modo `trusted` = `domain.HumanActor`. `Authorization: Bearer <run token>` válido = persona do token (via `auth.RunTokens` em `Deps`… opcional; se `Deps.Tokens` nil, ignora).
`openapi.json` gerado em `api/openapi.json` por `hanko openapi` (CLI) — teste que falha se o arquivo divergir.

## CLI (trilha C) — cobra, `internal/cli`
`hanko serve | migrate | version | openapi` (serve/migrate chamam `app` — trilha F expõe `app.Serve(ctx, cfg)`/`app.Migrate(ctx, cfg)`), e cliente HTTP do próprio servidor (`HANKO_URL`, default `http://127.0.0.1:7420`; empresa por `--company`/`HANKO_COMPANY`):
`hanko company create|list`, `hanko persona hire|list|show`, `hanko task create|list|show|assign|status|comment|wake`, `hanko run list|show|logs [--follow]`, `hanko workspace create|list`, `hanko project create|list`. Saída tabela (default) ou `--json`.

## UI (trilha D) — templ + htmx, Kubo DS (referência: `/home/claude/design-ref/`, fora do repo)
Tokens Kubo (stone, primary quase-preto, âmbar só para gate, vermelho só tinted em erro, charts monocromáticos, mono para ids/custos/timestamps). CSS próprio em `internal/ui/static/hanko.css` (sem Tailwind no v0.1), `htmx.min.js` + `htmx-ext-sse` vendorizados em `internal/ui/static/vendor/`. Marca: sakura 5 pétalas (SVG) + "Hanko".
Shell: sidebar como no DS (Buscar, Painel, Caixa de entrada, Chat, Decisões / TRABALHO: Tasks, Projetos, Fluxos, Metas, Artefatos, Workspaces / ORGANIZAÇÃO: Personas, Organograma, Skills, Integrações, Gates / SUPERVISÃO: Execuções, Custos, Atividade; rodapé owner) — itens fora do escopo levam a uma página "Em breve · Mx".
Telas M2: Onboarding mínimo (sem empresa → criar empresa), Painel (4 KPIs + primeiros passos + vazio), Tasks (lista agrupada por status, filtros por aba, busca), Task (detalhe + thread + execuções + comentar + atribuir/status), Nova task, Personas (lista), Contratar persona (form), Persona (detalhe + runs), Execuções (lista), Execução (log ao vivo via SSE `hx-ext="sse"`), Atividade. Responsivo até 390 px.

## MCP (trilha E) — `github.com/modelcontextprotocol/go-sdk/mcp`, streamable HTTP em `/mcp`
Auth: Bearer run token → ator persona + empresa do token; sem token só em modo trusted (loopback) → ator human, empresa por argumento `company`.
Tools: `hanko_task_list`, `hanko_task_get`, `hanko_task_create`, `hanko_task_update` (status, prioridade, atribuição), `hanko_task_comment`, `hanko_persona_list`, `hanko_run_get`, `hanko_whoami`. Escritas aceitam `event_id` (idempotência). Teste com cliente MCP do SDK.

## Memória (trilha E) — `internal/memory/aimemory`
Cliente MCP (streamable HTTP) para `<URL>/mcp` com `Authorization: Bearer <token>`. Scope: `workspace` = slug da empresa, `project` = slug do workspace Hanko (ou `default`).
`Briefing` → tool `memory_briefing{workspace,project}` (texto ≤ 8 KB); `AcceptHandoff` → `memory_handoff_accept{workspace,project}`; `Record` → `memory_write_page{path,body,title,kind:"hanko",tag,workspace,project}`; `FinalizeSession` → `POST <URL>/hook/batch` com `[{"url":"<URL>/hook?event=session-end&agent=<agent>&cwd=<cwd>&workspace=<w>&project=<p>","body":{"session_id":"<id>","cwd":"<cwd>"}}]`; `AgentMCP` → `{"ai-memory":{"type":"http","url":"<URL>/mcp","headers":{"Authorization":"Bearer <token>","X-Memory-Workspace":"<w>","X-Memory-Project":"<p>"}}}`; `Ping` → `GET <URL>/health` (ou `memory_status`).
Testes com servidor MCP falso (go-sdk) + httptest para `/hook/batch`.

## Fatos de memória gravados pelo service (trilha A)
`memory_outbox.kind` / `payload` (= `memory.Fact` JSON): `task.created`, `task.closed`, `comment.human`, `run.finished`. Path: `hanko/tasks/<KEY>.md`, `hanko/runs/<YYYY-MM-DD>-<KEY>-<run8>.md`. `event_id` determinístico (`<kind>:<subject_id>[:<n>]`).

## Bancos de teste por trilha
A `hanko_test_a` · B `hanko_test_b` · C `hanko_test_c` · D `hanko_test_d` · E `hanko_test_e` · F `hanko_test_f`.

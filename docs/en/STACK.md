> Português: [docs/STACK.md](../STACK.md)

# HANKO — Stack & Conventions (v0.1)

An open-source control plane for orchestrating teams of AI agents (tasks, org chart, heartbeat, governance, budget). URL: https://hanko.oute.pro
The orchestration concepts (company, agent, issue, heartbeat, run, approval, budget, routine) are implemented from scratch in Go. No third-party code.
Decisions live in `docs/adr/` (see the [ADR index](adr/README.md)). This file is the current summary; an ADR wins on conflict.

Note on status: this is the v0.1 target stack. Not yet in the code (as of 2026-10-14): the LLM SDKs, `embedded-postgres`/`test-drive`, `testcontainers-go`, `go-i18n`, `tools/bundle` (the Firebase script is committed as `internal/ui/static/auth-firebase.js`), and the generic `http` adapter. Check the code before relying on any row.

Note on vocabulary: the generic names above were later replaced by the domain terms in the [glossary](GLOSSARY.md) (ADR-0016): company, persona, task, run, gate, flow.

## 1. Principles
- Single binary: `hanko` starts the API, the embedded UI and the scheduler. No Node at runtime; Node is only a build tool for the islands (dev/CI). (ADR-0005)
- Postgres is the only mandatory dependency. ai-memory is on by default and degradable (outbox in Postgres). No Redis, no external queues. (ADR-0011, ADR-0015)
- Boring infrastructure, new domain: novelty goes into heartbeat/governance/budget, never into the database, queue or framework.
- Code-first: Go is the source of truth; OpenAPI is generated and versioned. (ADR-0006)
- Standard library first. A dependency only when the stdlib cannot do it.
- Complete, always-on OpenTelemetry observability (traces, metrics, correlated logs). Exports to the operator's collector. No product telemetry, no phone-home. (ADR-0013)
- Multi-org from day one: `company_id` on every domain table. (ADR-0010)
- Visible delivery every 2 weeks; scope gives, deadline does not. (ADR-0009)

## 2. Backend (Go)
| Area | Choice | Notes |
|---|---|---|
| Language | Latest stable Go (pinned via `toolchain` in go.mod) | gofumpt, `go vet`, `govulncheck` |
| HTTP | `net/http` + `http.ServeMux` (method+path, Go >= 1.22) | own middleware: request-id, recover, slog, auth, CORS |
| API | `huma` v2 over `humago`, code-first: structs -> `api/openapi.json` + validation + `/docs` | `make openapi` generates it; CI fails on an uncommitted diff. Serves the CLI, MCP, external agents and islands |
| Realtime | SSE (`text/event-stream`) for run logs, activity, threads | no WebSocket, no GraphQL |
| DB | PostgreSQL 17 | timestamptz UTC, JSONB for metadata, UUIDv7 as PK |
| DB access | `pgx/v5` + `sqlc` | ORMs and raw `database/sql` are forbidden; every query lives in `db/queries/*.sql` |
| Migrations | `goose` v3, plain SQL, `//go:embed` | `hanko migrate` runs at boot (configurable) |
| Jobs / heartbeat | `river` (Postgres-native queue on pgx) | wakeup queue, retries, periodic jobs (flows), unique jobs = coalescing |
| Atomic checkout | `SELECT ... FOR UPDATE SKIP LOCKED` + per-task execution lock | 1 assignee, 1 active run |
| Adapters | `Adapter` interface in `internal/adapter`. Engines: `claude` (`claude -p --output-format stream-json --verbose`), `codex` (`codex exec --json`), `acp` (any agent that speaks ACP v1 over stdio, ADR-0023) and `fake` (tests). A generic `http` (webhook) engine is described in the Portuguese doc but is not in the code yet | `acp` (ACP v1 over stdio, own JSON-RPC client) has existed since v0.2 (ADR-0004, ADR-0023); out-of-process plugins = v1 |
| MCP | `github.com/modelcontextprotocol/go-sdk` | Hanko as an MCP **server** (task and gate tools) and as a memory **proxy** (`/memory-mcp`) |
| Memory | ai-memory **on by default** (ADR-0015). Personas: MCP + hooks per run, project = workspace, session = run_id, `finalize-session`. Hanko: MCP client (`go-sdk`), writes facts (task, gate, run, delegation) through `memory_outbox` + river; reads `memory_briefing` + handoff at dispatch; search in the UI. Unavailable = degrade, never block | Governance stays in Postgres |
| LLM SDKs | `anthropic-sdk-go`, `openai-go` | evals and cost metadata only; the core never calls an LLM |
| Auth v0.1 | providers: `local` (self-host default: email/password argon2id + cookie) and `firebase` (Google + email/password managed by Firebase; the server validates the ID token through Google's JWKS, no Admin SDK). User API keys (SHA-256); short run JWT (`golang-jwt/v5`) | `HANKO_AUTH_PROVIDER=local\|firebase`. hanko.oute.pro uses firebase. Multi-human v0.2; OIDC v1 (ADR-0007, ADR-0014) |
| Secrets | AES-256-GCM (`secretbox`) with `HANKO_SECRET_KEY`; company/user secrets referenced as `${secret:NAME}`; optional external vault (KV v2) | resolved at dispatch, never in config (ADR-0050, §11) |
| Storage | `BlobStore`: Postgres (default) or S3-compatible with an in-house SigV4 over the stdlib (no `aws-sdk-go-v2`) | artifact content (ADR-0051, §11) |
| Config | env `HANKO_*` + `hanko.yaml`; `caarlos0/env/v11` + `yaml.v3` | env overrides yaml |
| Logs | `log/slog` JSON via `otelslog` (automatic trace_id/span_id) | request-id, company_id, run_id on every log line |
| Observability | OpenTelemetry **mandatory**: `otelhttp` (server), `otelpgx` (DB), OTel middleware in river (a span per job), a span per run in the adapters (`company_id`, `agent_id`, `run_id`), `otelslog` bridge for logs, OTel metrics + Prometheus `/metrics`. OTLP gRPC exporter, default `localhost:4317` | Instrumentation cannot be turned off. Collector absent = the SDK silently drops data, the app never fails because of OTel. No error-tracking or product-analytics SaaS (ADR-0013) |
| CLI | `cobra`: `hanko serve \| migrate \| version \| openapi \| company \| workspace \| project \| persona \| task \| run \| gate \| org \| cost \| budget \| flow \| auth` | the Portuguese doc also lists `onboard`, `test-drive`, `agent`, `export`, `import`; they do not exist in the code |
| IDs/Time | `google/uuid` (UUIDv7), `time.Time` UTC | |
| Local one-shot | `fergusstrange/embedded-postgres` only in `hanko test-drive` | production = external PG (the `test-drive` command is not implemented yet) |
| Tests | `go test` + `go-cmp`; `testcontainers-go` (PG 17); table-driven | >= 70% in `internal/domain` and `internal/heartbeat` |
| Lint | `golangci-lint` v2 (staticcheck, errcheck, gosec, revive), `gofumpt` | CI blocks |
| Build/Release | `goreleaser` v2: linux/darwin x amd64/arm64, distroless -> `ghcr.io/renatobardi/hanko`, checksums, SBOM | `Makefile` as the entrypoint |

## 3. Frontend: host + islands (ADR-0005)
**Host (templ + htmx)** is the app: routing, auth, layout, forms, lists, board, threads, approvals, budget, hire, flows, settings. SSE swaps HTML fragments.
**Islands (React)** are leaves for 3 heavy pieces, all v0.2+.

| Area | Choice |
|---|---|
| Templates | `templ`, typed components in `internal/ui/components` |
| Interaction | `htmx` 2 + the `sse` extension; Alpine.js only for trivial local state |
| CSS | The host uses its own CSS (`internal/ui/static/hanko.css`) with Kubo DS tokens as CSS vars, light/dark theme, no Tailwind (ADR-0005 addendum, 2026-10-14). The Portuguese STACK still lists Tailwind standalone CLI |
| Vendored JS | `sortable.js` (kanban). At most 3 vendored files |
| Scripts | `tools/bundle` (esbuild via Go) also bundles the Firebase Auth SDK (modular) for the `firebase` provider; it is a host script and does not count as an island |
| Islands | React + TS in `ui/islands/` (exact versions + lockfile), bundled by **esbuild (npm, dev/CI only)** into `internal/ui/static/islands/` (committed, `make islands`; CI checks the diff). Delivered in v0.2: virtualized run log, interactive org chart, anchored comments (ADR-0026). Cap of 3; a 4th requires an ADR |
| Markdown | `goldmark`, server-side |
| i18n | `go-i18n`: pt-BR (M1), en (M6) |
| E2E | Playwright in `e2e/` (ADR-0058): `bin/hanko` + real Postgres + the `fake` adapter; dev/CI only, outside the binary |
| Mobile | responsive; no PWA in v0.1 |
| Embed | `internal/ui/static` via `go:embed`; there is no SPA fallback (the host is server-rendered) |

Host/island contract:
1. The host decides route, auth, layout, navigation and list data.
2. An island is a leaf: it receives props via `data-*`/JSON in the HTML, emits DOM events or calls the API. It never routes, never holds global state, never lists.
3. One CSS, the same tokens. No global component kit; headless primitives only inside the island that needs them.
4. Bundling only through esbuild (npm, ADR-0026). No Vite, pnpm workspace, React Router or TanStack.
5. Cap of 3 islands until v0.2. Post-M2 checkpoint: if UI takes more than 50% of the hours, reassess.

## 4. Repository layout
```
hanko/
  cmd/hanko/main.go
  internal/
    api/         huma handlers (thin: validate -> service)
    domain/      entities and rules (company, persona, task, run, budget, gate, flow)
    service/     use cases; transactions; emits activity events
    store/       generated sqlc + repositories
    heartbeat/   river workers, scheduler, wakeup, recovery
    adapter/     claude/, codex/, fake/   (acp/ in v0.2)
    mcp/         server + memory proxy
    auth/ config/ events/ memory/ telemetry/ ui/ ...
  ui/islands/    React/TS islands (v0.2+), package.json
  tools/bundle/  esbuild via Go
  db/migrations/ goose SQL
  db/queries/    sqlc SQL
  api/openapi.json   generated by `make openapi`
  deploy/        compose.yaml, caddy/, lxc/, oute-server/
  docs/          STACK.md, ROADMAP.md, adr/, en/
  ee/            reserved (empty in v0.1), ADR-0003
  Makefile sqlc.yaml
```
Layers: handler -> service -> store. A handler never touches the DB. The store holds no business rules. The domain does not import infrastructure.

## 5. Deploy (ADR-0008)
- oute-server (OCI, LXD): containers `hanko-stg` (branch `develop`) and `hanko-prd` (tag `v*`). Compose: `hanko` + `postgres:17` + `caddy` (TLS) + `grafana/otel-lgtm` (collector + Tempo + Loki + Mimir + Grafana in one container; about 1.5 GB RAM). Split-horizon DNS via Tailscale.
- Backup: daily `pg_dump` + `restic`. A restore is tested monthly. Details in the [runbook](RUNBOOK.md).
- CI (GitHub Actions): lint -> vet -> govulncheck -> test (PG service) -> openapi diff -> bundle islands -> build. A `v*` tag -> goreleaser -> ghcr.io + GitHub Release.
- An external VPS only when there are external users.

## 6. Code conventions (Go)
- Errors: always `fmt.Errorf("op: %w", err)`; typed domain errors in `internal/domain/errors.go`; mapped to HTTP in a single place.
- `context.Context` is always the first parameter. No global state. No `init()` with side effects.
- One transaction per use case (`service`), never in the handler or the store.
- Every domain write produces an `activity_event` in the same transaction.
- Migrations are append-only; never edit an applied migration.
- Short lowercase package names; no `utils`, `common`, `helpers`.
- Tests next to the code; fixtures through builders, not loose SQL.
- No `panic` outside `main`. No `log.Fatal` outside `cmd/`.

## 7. Out of scope for v0.1
SQLite; WebSocket; GraphQL; React islands; out-of-process plugins; RLS; remote sandboxes; OIDC; multi-human roles; org switching in the UI; desktop; native mobile; PWA; agent chat; chat connectors (Slack/Discord/Telegram); editor with anchored comments; interactive org chart; virtualized log viewer.

## 8. License (ADR-0003)
Apache-2.0 + DCO (`git commit -s`). `ee/` is reserved for enterprise features under a commercial license. No third-party code copied; `THIRD_PARTY_NOTICES` from the goreleaser SBOM.

## 9. ACP adapter (ADR-0023)

`agent = "acp"` runs **any agent that speaks the Agent Client Protocol v1 over stdio**: Hanko is the client, starts the persona's
command (`adapter_config.command`) as a child process and talks JSON-RPC 2.0 (`internal/adapter/acp`, stdlib only).
Lifecycle: `initialize` → `session/new` (or `session/load` to resume) → `session/prompt` → `stopReason`.

`adapter_config` (validated when the persona is hired and when a file is imported; unknown keys are rejected):

| Key | Type | Meaning |
|---|---|---|
| `command` | string, required | the executable only (resolved on the server's `PATH`); arguments go in `args` |
| `args` | list of strings | arguments, one per element (no shell) |
| `env` | object name→value | extra variables for the agent; `HANKO_*` is reserved; added to the server allowlist (`HANKO_AGENT_ENV_ALLOW`) |
| `permission` | `allow` \| `reject` \| `read_only` | policy for the agent's permission requests; default `read_only` (approves read/search, refuses edit/execute); review runs are always `read_only` |
| `model` | string | best-effort model, used only when the persona has no `model`; the agent must advertise model selection, otherwise a warning is logged |
| `init_timeout_sec` | integer 0–300 | wait for `initialize` (0 = 30 s) |

Example (**illustrative**: replace it with the command of the agent installed on the server):

```json
{"agent": "acp", "adapter_config": {"command": "my-acp-agent", "args": ["--stdio"], "env": {"MY_AGENT_HOME": "/data/agent"}, "permission": "read_only"}}
```

The prompt is two text blocks (persona instructions + Hanko protocol, then the task briefing). Files: the agent reads and writes only
inside the run directory through `fs/read_text_file` and `fs/write_text_file` (writes only with `permission: allow`). HTTP MCP servers
(Hanko's own `/mcp`) are passed only if the agent advertises `mcpCapabilities.http`. Out of scope: `terminal/*`, interactive
authentication and ACP v2. `acp` cannot be an alternate review harness (`reviewer_agent`), only a reviewer persona.
Installing the agent and troubleshooting: `docs/en/RUNBOOK.md`.

## 10. Artifacts and goals (ADR-0028)
Artifacts live in Postgres (`artifacts` for metadata and `artifact_blobs` for the `bytea`), so there is no extra directory to back
up; limits per file and per company are `HANKO_ARTIFACT_MAX_BYTES` and `HANKO_ARTIFACT_QUOTA_BYTES`. `internal/artifactsrv` is the
only place that serves the bytes (nosniff, a sandboxing CSP, inline only for plain text, Markdown and raster images) and
`internal/textdiff` computes the text diff between versions with no new dependency. Goals (`goals`, `goal_updates`,
`tasks.goal_id`) compute progress in the service and the UI draws the history as server-side SVG, with no script. Export/import:
`artifacts` is optional (`--with-artifacts`, up to 20 MiB); goals do not travel.

## 11. Secrets, instance settings and BlobStore (ADR-0050, ADR-0051)
**Secrets.** `secrets` (per company, `company` or `user` scope) stores the encrypted value (AES-256-GCM, AAD `secret/<id>`) or, for the
`vault` provider, only the `path#field` reference into an external vault (KV v2 API, `HANKO_VAULT_*`). Reference: `${secret:NAME}`
(company) and `${secret:user/NAME}` (owner only; refused in shared configuration). It works in MCP integration headers/env and in the
persona `adapter_config`; `HirePersona` swaps a plaintext `env` for `persona.<slug>.<VAR>` and boot migrates the legacy ones. Expansion
happens at dispatch (`RunExtras`, `ResolveAdapterConfig`); the values go into `adapter.Spec.Secrets` and the log redaction. Reveal: once
per version. Packages: `internal/secretref` (parse/expand), `internal/extvault` (external provider: interface + KV v2; ADR-0068 §9).

**Settings.** `instance_settings` (no `company_id`, with `instance_setting_audit`): only the keys of the `internal/settings` registry
(`acp.*`, `integrations.*`, `artifacts.*`, `run.reserve_usd`, `alerts.*`), env > database > yaml/default, read at use time.

**BlobStore.** `internal/storage`: `BlobStore` (`Put/Get/Delete/Stat`), drivers `postgres` (inside the artifact transaction) and `s3`
(`HANKO_S3_*`, upload happens before the transaction, with compensation). `artifacts.storage` says where each version lives;
`hanko blobs migrate` moves between drivers. Per-file limit: 16 MiB (postgres) or 64 MiB (s3).


## 12. First-owner claim, rate limit, errors via OTel and dev labs (ADR-0058)
**Claim.** With no users (authenticated + local provider) boot issues a claim (`owner_claims`, only the token's SHA-256, 1 h) and prints
`<HANKO_PUBLIC_URL>/claim/<token>` to stderr (not to the logger, which exports over OTLP). `GET/POST /claim/{token}` creates the owner and
invalidates the link in the same transaction, with an `admin_audit` row; `hanko auth claim-link` reissues; `create-owner` stays as a fallback.

**Rate limit.** `internal/ratelimit`: the pure bucket model (`Step`, the oracle of the property tests), the `Limiter` (metric
`hanko.ratelimit.decisions`) and the middleware, inside `auth.Guard`. State in `rate_limit_buckets` (UNLOGGED), one
`INSERT … ON CONFLICT` per decision (`store.TakeRateToken`). Keys: API key hash, user, valid run token, IP. Ceilings in
`instance_settings` (`ratelimit.*`, env `HANKO_RATELIMIT_*`). 429 + `Retry-After`; fail-open.

**Errors.** `telemetry.Record` (`exception` span event + `ERROR` log with stack + `hanko.errors` counter) is the single point for `api.fail`, the
API/app recovers and `ui.fail`; `POST /telemetry/errors` (`telemetry.BrowserErrors`) receives `hanko.js` errors. Dashboard in `deploy/grafana/`.

**Dev labs.** `/design-guide` and `/dev/ux/*` (`internal/ui/devlabs.go`), trusted mode or owner/admin only; synthetic data, nothing written.

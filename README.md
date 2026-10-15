**English** | [Português](README.pt-BR.md)

# Hanko (判子)

An open-source control plane for running teams of AI agents. Hanko gives agent work the structure a company gives human work: tasks, an org chart, human approval gates, and budgets that actually stop spending.

Single Go binary. PostgreSQL is the only required data dependency. Full OpenTelemetry observability is always on and points at **your** collector; Hanko never phones home.

Live instance: https://hanko.oute.pro

## Why

Running coding agents (`claude`, `codex`) by hand does not scale past one person and one terminal. Hanko is the layer around them:

- **Tasks and personas.** You hire *personas* (CTO, engineer, reviewer: instructions, model, budget, place in the org chart) and assign them *tasks*. A *run* is one execution of a persona on a task.
- **Org chart.** Personas report to each other (`reports_to`); delegation follows the tree and cannot form cycles.
- **Heartbeat.** A scheduler (river, on Postgres) wakes personas and dispatches runs. One active run per task, atomic checkout.
- **Approval gates.** A *gate* is a point where a human decides (approve, reject, request changes). Personas can request one through MCP or CLI, and a persona can be configured so that completing a task requires a review gate. Every decision lands in an audit trail.
- **Budgets with a hard stop.** A cost ledger per run, persona and company. Monthly budgets block new runs at 100%, reserve cost for runs in flight, and resume automatically when the cap is raised or the month rolls over.
- **Scheduled flows.** Cron-triggered flows (with time zone, "run now", catch-up without pile-up) that create tasks on their own.
- **Memory across sessions.** ai-memory is on by default: runs start with a briefing and the previous handoff, and control-plane facts (task, gate decision, run result, delegation) are written back through a Postgres outbox. If ai-memory is down, Hanko keeps working and the outbox drains later.
- **OpenTelemetry, always on.** Traces (a span per run), metrics (plus Prometheus `/metrics`) and logs correlated by `trace_id`, exported over OTLP to the collector you choose.

Multi-company from day one: `company_id` is on every domain table.

## Status

v0.1 is running in production at https://hanko.oute.pro. Implemented: companies, workspaces, projects, personas, tasks, runs, gates, org chart, inbox/decisions, costs and budgets, flows, MCP server, memory integration, and authentication (`authenticated` mode with `local` or `firebase` login, sessions and API keys). Adapters for `claude` and `codex` (plus a `fake` one for tests). Not in v0.1: ACP adapter, interactive React islands, multi-human roles, OIDC. See [docs/ROADMAP.md](docs/ROADMAP.md) (Portuguese) for scope and what is next.

The web UI and the CLI (including `--help`) speak Brazilian Portuguese (default) and English: the UI follows the language selector or `Accept-Language`, the CLI follows `HANKO_LANG` (`pt-BR` or `en`) and, without it, `LC_ALL`/`LC_MESSAGES`/`LANG`. The API, CLI flags, MCP tools and database use English terms (see the [glossary](docs/en/GLOSSARY.md)).

## Quickstart

Requirements: Go 1.27 and Docker. The default mode is `authenticated`, so you create an owner first.

```bash
git clone https://github.com/renatobardi/hanko && cd hanko
make build                                  # -> bin/hanko

docker run -d --name hanko-pg -e POSTGRES_HOST_AUTH_METHOD=trust \
  -p 127.0.0.1:5432:5432 postgres:17
export HANKO_DATABASE_URL="postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable"
export HANKO_SECRET=$(openssl rand -hex 32)
export HANKO_MEMORY_PROVIDER=none           # skip ai-memory for a first try

./bin/hanko migrate
HANKO_OWNER_PASSWORD='change-this-password' \
  ./bin/hanko auth create-owner --email you@example.com --name "Your Name"
export HANKO_TOKEN=$(./bin/hanko auth token create --name laptop | grep -oE 'hk_[A-Za-z0-9_-]+')

./bin/hanko serve &                         # http://127.0.0.1:7420 -> sign in at /entrar

./bin/hanko company create "Acme Lab" && export HANKO_COMPANY=acme-lab
./bin/hanko persona hire Ada --agent claude --title CTO
./bin/hanko task create "Say hello and describe the repository" -p ada
./bin/hanko run list
./bin/hanko run logs -f <run-id>
```

`claude` (or `codex`) must be on the `PATH` of the machine running Hanko, or set `HANKO_CLAUDE_BIN` / `HANKO_CODEX_BIN`. Use `--agent fake` to try the flow without a real harness. The owner password needs at least 10 characters.

Full local stack with ai-memory and a Grafana/OTel collector:

```bash
cp .env.example .env                        # set passwords and AI_MEMORY_IMAGE (pinned digest)
docker compose -f deploy/compose.yaml --env-file .env up -d postgres ai-memory otel-lgtm
make dev                                    # http://127.0.0.1:7420 (reads .env)
```

Other `make` targets: `build`, `test`, `test-integration`, `lint`, `fmt`, `vet`, `generate`, `openapi`, `up`, `down`, `logs`. The CLI covers `company`, `workspace`, `project`, `persona`, `task`, `run`, `gate`, `org`, `cost`, `budget`, `flow` and `auth`; run `hanko --help` for details. Configuration lives in `hanko.example.yaml` and `HANKO_*` environment variables (see `.env.example`); OTLP export goes to `OTEL_EXPORTER_OTLP_ENDPOINT`.

## Architecture in brief

- One Go binary: `hanko serve` runs the REST API, the embedded web UI and the scheduler.
- PostgreSQL 17 is the only required dependency (pgx + sqlc, goose migrations). No Redis, no external queue.
- Heartbeat and jobs run on [river](https://riverqueue.com), a Postgres-native queue.
- UI is server-rendered with templ + htmx, with the Kubo design system tokens.
- The API is code-first with huma: Go structs generate the versioned `api/openapi.json`.
- Hanko is also an MCP server (`/mcp`, `hanko_*` tools) for the personas it runs.
- Layers: handler -> service -> store. Every domain write emits an activity event in the same transaction.

## Documentation

English:

- [Stack and conventions](docs/en/STACK.md)
- [Glossary](docs/en/GLOSSARY.md)
- [Runbook](docs/en/RUNBOOK.md) (deploy, auth, backup/restore, troubleshooting)
- [Demos](docs/en/DEMOS.md) (recording scripts per milestone, sample company `examples/demo-company.json`)
- [ADR index](docs/en/adr/README.md)
- [Contributing](CONTRIBUTING.md)

Portuguese (source of truth): [docs/STACK.md](docs/STACK.md), [docs/GLOSSARIO.md](docs/GLOSSARIO.md), [docs/RUNBOOK.md](docs/RUNBOOK.md), [docs/adr/](docs/adr/), [docs/ROADMAP.md](docs/ROADMAP.md), [AGENTS.md](AGENTS.md).

The Portuguese documentation is the source; the English version is a maintained translation and may lag behind it.

## License

Apache-2.0 (see [LICENSE](LICENSE)). Contributions require a DCO sign-off (`git commit -s`); there is no CLA. See [CONTRIBUTING.md](CONTRIBUTING.md).

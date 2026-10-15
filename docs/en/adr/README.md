> Português: [docs/adr/](../../adr/) (the ADRs are written in Portuguese and are not translated; this index summarizes each one)

# Architecture Decision Records: index

An ADR wins over `docs/STACK.md` when they conflict. Add a new ADR instead of reopening a decision. All ADRs below are accepted.

| ADR | Title | Decision |
|---|---|---|
| [0001](../../adr/0001-reescrita-em-go.md) | Implementation from scratch in Go | Hanko implements the category's concepts from scratch as a single Go binary, with no third-party code. |
| [0002](../../adr/0002-escopo-v01-milestones.md) | v0.1 scope: full core in 6 milestones with gates | v0.1 ships the full core (org chart, approvals, budget, flows, UI), cut into milestones M1-M6 that each end with a recorded demo and a tag. |
| [0003](../../adr/0003-licenca-apache-dco.md) | Apache-2.0 + DCO; `ee/` reserved | The core is Apache-2.0 with a DCO sign-off and no CLA; `ee/` is reserved for enterprise features under a commercial license. |
| [0004](../../adr/0004-adapter-cli-primeiro-acp-depois.md) | `cli` adapter first, ACP in v0.2 | v0.1 runs agents through an `Adapter` interface with CLI engines (`claude`, `codex`); ACP over stdio comes in v0.2. |
| [0005](../../adr/0005-ui-host-templ-htmx-ilhas-react.md) | UI: templ + htmx host, React islands | The UI is a server-rendered templ + htmx host, with React islands (bundled by esbuild through Go) allowed only as leaves; an addendum (2026-10-14) replaces Tailwind with the project's own CSS built on Kubo DS tokens. |
| [0006](../../adr/0006-api-code-first-huma.md) | Code-first API with huma | Go structs define the API through huma, which generates and versions `api/openapi.json`; CI fails on an uncommitted diff. |
| [0007](../../adr/0007-auth-v01.md) | Auth v0.1: `local` and `firebase` providers | Two modes, `trusted` and `authenticated`; in `authenticated` the login provider is `local` (argon2id, cookie) or `firebase`, plus hashed API keys and short run JWTs. |
| [0008](../../adr/0008-deploy-oute-server.md) | Deploy on LXC at oute-server | `hanko-stg` and `hanko-prd` run as LXC containers on the owner's server with Compose, daily `pg_dump` + restic backups and a monthly restore test. |
| [0009](../../adr/0009-cadencia-milestones.md) | Cadence: short milestones, recorded demos | Milestones are short and end with a recorded demo; scope is variable and the deadline is not, and speed comes from hours, parallel tracks and cutting scope, not from skipping tests. |
| [0010](../../adr/0010-multi-org-dia-1.md) | Multi-org from M1 via `company_id` | `company_id` is on every domain table from the first migration; no RLS and no schema-per-tenant in v0.1. |
| [0011](../../adr/0011-postgres-unica-dependencia.md) | PostgreSQL 17 is the only dependency | PostgreSQL 17 is the only mandatory dependency (a document-database alternative was rejected); ai-memory is an optional, degradable dependency added by ADR-0015. |
| [0012](../../adr/0012-fila-river.md) | Queue and scheduler: river on Postgres | `river` provides the wakeup queue, retries, periodic jobs (flows) and unique jobs (heartbeat coalescing) inside Postgres, with no Redis. |
| [0013](../../adr/0013-otel-mandatorio.md) | Complete, mandatory OpenTelemetry | OpenTelemetry instrumentation (traces, metrics, correlated logs) is always on and exports to the operator's collector; product telemetry and phone-home do not exist. |
| [0014](../../adr/0014-auth-provider-firebase.md) | `firebase` auth provider | The `firebase` provider lets the browser sign in with Google or email/password and the server validates the ID token against Google's public keys before opening its own session; `local` stays the self-host default. |
| [0015](../../adr/0015-memoria-ai-memory-provider.md) | Memory: ai-memory on by default | ai-memory is on by default for both personas (MCP + hooks per run) and Hanko itself (briefing at dispatch, facts written through a Postgres outbox), and it degrades without data loss when unavailable. |
| [0016](../../adr/0016-linguagem-ubiqua.md) | Ubiquitous language | Code, API, MCP, database and docs use the English equivalent of the UI term (company, persona, task, run, gate, flow...), one term per concept; the UI shows pt-BR. See the [glossary](../GLOSSARY.md). |
| [0017](../../adr/0017-modo-trusted-e-limites.md) | Trusted mode: assumptions, limits, mitigations | In `trusted` mode agents and operator share the host as the trust boundary, so the ADR documents the limits (e.g. a local agent can call the API as the human) and the mitigations; `authenticated` removes the main limit. |
| [0018](../../adr/0018-endurecimento-authenticated.md) | Hardening the authenticated mode | After the independent review: an owner must be named or admission lists set on public URLs, argon2id concurrency is capped, `X-Forwarded-For` is trusted only from `HANKO_TRUSTED_PROXIES`, brute force is limited per email, and sessions have a 90-day absolute cap. |

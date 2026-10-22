> Português: [docs/RUNBOOK.md](../RUNBOOK.md) (source; this is a translation of it)

# RUNBOOK — Hanko

Day-to-day operations. Decisions in `docs/adr/` (0007/0014 auth, 0008 deploy, 0011 Postgres, 0013 OTel, 0015 memory; see the [ADR index](adr/README.md)). Section numbers mirror the Portuguese runbook, including its order (1, 2, 5, 3, 4).

## 1. Run locally in 5 minutes

Prerequisites: Go 1.27, Docker with Compose v2.

```bash
cp .env.example .env
# edit .env: POSTGRES_PASSWORD, AI_MEMORY_IMAGE (pinned digest), AI_MEMORY_AUTH_TOKEN (= HANKO_MEMORY_TOKEN)
# adjust HANKO_DATABASE_URL with the same password

docker compose -f deploy/compose.yaml --env-file .env up -d postgres ai-memory otel-lgtm
make dev          # starts Hanko locally at http://127.0.0.1:7420 (migrations run at boot)
```

- UI: <http://127.0.0.1:7420> · API/docs: `/docs` · metrics: `/metrics`
- Grafana (traces, logs, metrics): <http://127.0.0.1:3000> (LGTM; initial login `admin`/`admin`)
- Everything in containers (including Hanko): `make up`; logs: `make logs`; stop: `make down`.
- Without ai-memory: `HANKO_MEMORY_PROVIDER=none`.
- The `claude`/`codex` CLIs must be on the `PATH` of the machine where Hanko runs (or in `HANKO_CLAUDE_BIN`/`HANKO_CODEX_BIN`).
  The default (distroless) image does not include them; use `Dockerfile.agents` to run agents inside the container.

Tests: `export HANKO_TEST_DATABASE_URL=postgres://postgres:...@127.0.0.1:5432/hanko_test?sslmode=disable; make test`
(integration tests skip without the variable).

**UI language.** The web UI speaks pt-BR (default) and English. The language comes from the `hanko_lang` cookie (set by the selector in the sidebar footer and on the sign-in screen, via `POST /idioma`) and, without it, from `Accept-Language` (`en*` is English, anything else pt-BR). User data, run logs and API errors are never translated.

**CLI language.** The CLI (`hanko …`) speaks pt-BR (default) and English too, including `--help`, tables, messages and prompts. The language is resolved once at start-up: `HANKO_LANG` (`pt-BR` or `en`) wins; without it `LC_ALL`, `LC_MESSAGES` and `LANG` apply, in that order (the first one that is set decides; `en*` is English, any other value is pt-BR); with none, pt-BR. E.g. `HANKO_LANG=en hanko task list`. In English, dates print as `yyyy-mm-dd hh:mm` and amounts as `$1.00`. Messages coming from the API or the domain, `--json` output and the parsing errors cobra itself raises (unknown flag etc.) are not translated. New CLI texts go in `internal/i18n/en_cli.go` (same rule as the UI catalog: the key is the pt-BR text) and `TestCatalogCoversSources` covers both UI and CLI.

## 2. Deploy on LXC (oute-server)

**Production (current path):** `deploy/oute-server/deploy-prd.sh`. It runs on the Mac and follows the conventions of the `lab` repo
(`docs/oute-server-app-setup.md`): an unprivileged LXC `hanko-prd` with nesting at `10.173.117.43`, `limits.memory` 4GiB,
the app in `/opt/app` with the `docker-compose.yml` at the repo root (port `3810`), a `.env` generated inside the LXC, and a public edge
through nginx + certbot on the host (`--edge`, same pattern as `lab/scripts/ops/deploy-vault-vhost.sh`; the vhost also listens on
`100.66.254.24:443` because of the tailnet's split DNS). The container and the vhost are in the lab's `inventory.yaml` (#280).
ai-memory runs under the `memory` profile (`COMPOSE_PROFILES=memory` in `.env`); the digest is pinned in `AI_MEMORY_IMAGE` on the
first deploy. To update it, change that line in the LXC's `.env` and run the deploy again.
The text below describes the generic variant using `deploy/compose.yaml`.

Two containers: `hanko-stg` (branch `develop`) and `hanko-prd` (tag `v*`). Access through Tailscale (split-horizon, as in the kura-dev runbook).

1. On oute-server: `deploy/lxc/setup.sh hanko-stg` (creates the Ubuntu 24.04 container with Docker, restic and psql).
2. Copy the stack: `lxc file push -r deploy .env hanko-stg/opt/hanko/` and fix ownership (`chown -R hanko:hanko /opt/hanko`).
3. Fill in `/opt/hanko/.env`:
   - `HANKO_VERSION` = tag (prd) or `develop` (stg); `POSTGRES_PASSWORD`; `HANKO_SECRET` (`openssl rand -hex 32`);
   - `AI_MEMORY_IMAGE` by digest; `AI_MEMORY_AUTH_TOKEN`; `HANKO_PUBLIC_URL=https://hanko.oute.pro` (prd);
   - `HANKO_DOMAIN`;
   - edge (prd): in `trusted` mode, `HANKO_EDGE_USER` and `HANKO_EDGE_PASSWORD_HASH` (see "Edge with basic_auth" below); in
     `authenticated` mode, `HANKO_CADDYFILE=Caddyfile.authenticated` and the variables from §5 (no basic_auth: Hanko does the authenticating).
4. Start: `cd /opt/hanko && docker compose -f deploy/compose.yaml --env-file .env up -d` (prd with a TLS edge: `--profile edge`).
5. Verify: `curl -fsS http://127.0.0.1:7420/healthz` and `/readyz`; open Grafana through a tunnel (port 3000 is loopback-only).
6. Update the version: change `HANKO_VERSION`, then `docker compose pull hanko && docker compose up -d hanko`. Migrations run at boot
   (`HANKO_MIGRATE_ON_BOOT=true`); to run them separately: `docker compose run --rm --entrypoint /hanko hanko migrate`.
7. Rollback: set `HANKO_VERSION` back and start again. Migrations are append-only; a schema rollback is a restore from backup.

Secrets never go into git: `.env` stays only in the container.

### Edge with basic_auth (trusted mode)

In `trusted` mode Hanko does not authenticate humans: whoever reaches the port acts as the operator. That is why
`deploy/caddy/Caddyfile` requires `basic_auth` and strips `Authorization` before forwarding to Hanko. Generate the bcrypt hash:

```bash
docker run --rm caddy:2 caddy hash-password --plaintext 'STRONG-PASSWORD'   # or: caddy hash-password
```

In `.env`, set `HANKO_EDGE_USER=operator` and `HANKO_EDGE_PASSWORD_HASH=...` with **every `$` in the hash written as `$$`** (compose
interpolates `$`). To change the password, regenerate the hash and run `docker compose up -d caddy`. Without both variables Caddy refuses
to start (invalid config), on purpose. With `authenticated` mode, follow §5 and skip basic_auth.

### Hardening the trusted mode

- A `HANKO_SECRET` shorter than 32 bytes is refused at boot; empty generates one per boot.
- The `Host` header is only accepted for `localhost`, `127.0.0.1`, `[::1]`, the host of `HANKO_PUBLIC_URL` and `HANKO_ALLOWED_HOSTS`
  (csv); anything else gets `421`. Behind a proxy/tunnel with another name, add it to `HANKO_ALLOWED_HOSTS`.
- Agents do not inherit the server's environment: only `PATH, HOME, USER, LANG, LC_*, TERM, TMPDIR, TZ, SHELL, SSL_CERT_*`,
  proxies, the prefixes `ANTHROPIC_ CLAUDE_ OPENAI_ CODEX_ OTEL_ NODE_ XDG_ GIT_ GH_` and whatever `HANKO_AGENT_ENV_ALLOW` (csv, accepts
  a prefix with `*`) allows. The server's `HANKO_*` variables never pass through.
- Agent output is scrubbed for secrets (run token, Bearer, JWT, API keys, passwords in database URLs) before it becomes a run log.
- Run tokens are only valid while the run is `queued`/`running`.
- Memory: the agent talks to `<HANKO_PUBLIC_URL>/memory-mcp` using the run token; Hanko injects `HANKO_MEMORY_TOKEN` and the scope
  (company/workspace) on the server side. The ai-memory token never reaches the agent.
- Workspaces can only point inside the company folder of the root, `HANKO_WORKSPACES_ROOT/<company-slug>/...` (default root `$HOME/hanko-workspaces`), with an absolute, clean path; the root is shared by companies and the subfolder isolates them. At run time the path is revalidated (symlinks resolved): outside the root, the run uses a temporary directory and leaves a warning.
- Budget: before opening a run, Hanko sums the month's spend, the reservation of every `queued`/`running` run, and the reservation of the new one;
  if that exceeds the persona's or the company's cap, the run does not open. A run's reservation is the larger of the average cost of the persona's last
  10 runs and `HANKO_RUN_RESERVE_USD` (default `1.00`). A persona paused by budget comes back on its own when the cap is raised
  or the month rolls over. A refusal caused only by the reservation (nothing was paused) takes the persona out of the heartbeat safety net for 10 min, to avoid
  a wake storm; "Run now" (manual wake) keeps trying. Whoever sets the persona's status explicitly (e.g.
  Pause) owns the pause: the budget reason is cleared and the automatic resume does not undo it.
- Limits and isolation recommendation: `docs/adr/0017-modo-trusted-e-limites.md` (Portuguese). `authenticated` mode (§5) removes the main limit.

## 5. Authenticated mode (login, sessions and API keys)

`HANKO_MODE=authenticated` requires login for everything except `/healthz`, `/readyz` and `/static`. It is the default mode.

| Route | Who gets in |
|---|---|
| UI (`/`, `/c/...`) | session (`hanko_session` cookie); without one, redirects to `/entrar` |
| `/api/v1/*` | session (state-changing methods require same origin), **API key** (`Authorization: Bearer hk_…`) or a persona run token |
| `/mcp` | run token or API key (a cookie does not count) |
| `/memory-mcp` | run token |
| `/metrics` | a direct loopback connection (no `X-Forwarded-For`) or an API key |

Boot refusals: `HANKO_SECRET` shorter than 32 bytes (or empty), and a `HANKO_PUBLIC_URL` that is not `https` (except `localhost`/loopback).
The cookie carries `Secure` when `HANKO_PUBLIC_URL` is https. Session: a random 32-byte token (only its SHA-256 hash goes to the database),
a **sliding** 30-day lifetime (renewed at most once a minute) with an **absolute cap of 90 days** since login (after that, sign in again
even with continuous use), `HttpOnly`, `SameSite=Lax`; replaced on every login. Sign out: the sidebar footer menu.

**Client IP and proxies.** The login rate limit and the IP stored on the session use the client IP. `X-Forwarded-For` is only accepted when the
connection peer is in `HANKO_TRUSTED_PROXIES` (CIDRs or IPs, comma-separated; default `127.0.0.1/32,::1/128`); the chain is read from the
right to the left and the first address that is not a trusted proxy wins. Outside the list, or with an empty list, `RemoteAddr` is used.
**Behind the compose Caddy** (which talks to hanko over the docker network) add the compose subnet, otherwise every client appears to have
Caddy's IP: `docker network inspect <project>_hanko -f '{{(index .IPAM.Config 0).Subnet}}'` and then, for example,
`HANKO_TRUSTED_PROXIES=127.0.0.1/32,::1/128,172.18.0.0/16` (`deploy/compose.yaml` passes the variable through). Do not list networks that untrusted
clients can reach.

**Password cost.** argon2id hashing and verification (64 MiB each) go through a semaphore of `max(2, number of CPUs)` slots; with no slot within 2 s
login answers 503 with `Retry-After` ("try again") and does not count as a failure.

### 5.1 `local` provider (default, `HANKO_AUTH_PROVIDER=local`)

Email and password (argon2id, 64 MiB, 3 passes), with failure limits per 15 min (in memory; they reset with the process): 5 per IP+email,
25 per IP and **20 per email across all IPs** (distributed brute force), the latter with progressive backoff (from the 6th failure for that email,
a wait of 1 s, 2 s, 4 s... up to 5 min).

```bash
export HANKO_MODE=authenticated HANKO_SECRET=$(openssl rand -hex 32) HANKO_PUBLIC_URL=https://hanko.example.com
hanko migrate
read -rs HANKO_OWNER_PASSWORD && export HANKO_OWNER_PASSWORD      # minimum 10 characters
hanko auth create-owner --email you@example.com --name "Your Name"
hanko serve
```

**First owner by a single-use link (ADR-0058).** With no users at all, boot prints a block with the link `<HANKO_PUBLIC_URL>/claim/<token>` to the
process **stderr** (`docker compose logs hanko`): open it, enter name, email and password, and the first owner is created and signed in. The link is
valid for 1 h and works once (used, expired or tampered links answer the same 404); the token only ever exists on stderr: the database keeps the hash,
and it goes neither to the structured log nor to OTLP. Lost the link or it expired? `hanko auth claim-link` issues another one (invalidating the
previous ones) without a restart. The `hanko auth create-owner` above still works as an alternative.

**Create the owner (by link or CLI) before exposing the server.** The web form "Create owner account" (first come, first owner) only exists when
`HANKO_PUBLIC_URL` is loopback (`localhost`/`127.0.0.1`) or `HANKO_AUTH_WEB_BOOTSTRAP=true`. On a public URL with no users, `/entrar` only tells you
to run `hanko auth create-owner` and `POST /entrar/dono` answers 404. There is only one owner in v0.1 (multi-human is v0.2); all users can do everything.

### 5.2 `firebase` provider (`HANKO_AUTH_PROVIDER=firebase`)

Login with Google and email/password through Firebase Auth. The server **does not use the Admin SDK**: the browser authenticates with the web SDK, sends the
ID token to `POST /auth/firebase` (same origin only) and Hanko validates the RS256 JWT with Google's public keys
(`securetoken@system.gserviceaccount.com`, cached per `Cache-Control`, refreshed when the `kid` is new; if the refresh fails, cached keys
stay valid for up to 1 h after expiring, with a log warning, and after that login fails closed with 503), the project's `iss`/`aud`,
`exp`/`iat`/`auth_time`, `sub` and `email_verified`; it then opens its own session. Revoking = expiring the Hanko session.

1. Firebase console: create the project (or reuse one) and register a **Web app**; note `projectId`, `apiKey` and `authDomain`
   (public values, not secrets).
2. Authentication → Sign-in method: enable **Google** and **Email/password**.
3. Authentication → Settings → **Authorized domains**: add Hanko's domain (e.g. `hanko.oute.pro`); `localhost` is already there.
4. In Hanko's `.env`:
   ```bash
   HANKO_MODE=authenticated
   HANKO_PUBLIC_URL=https://hanko.oute.pro
   HANKO_SECRET=...                       # openssl rand -hex 32
   HANKO_AUTH_PROVIDER=firebase
   HANKO_FIREBASE_PROJECT_ID=my-project
   HANKO_FIREBASE_API_KEY=AIza...
   HANKO_FIREBASE_AUTH_DOMAIN=my-project.firebaseapp.com
   HANKO_AUTH_ALLOWED_EMAILS=you@example.com,other@example.com   # and/or
   HANKO_AUTH_ALLOWED_DOMAINS=example.com                        # and/or
   HANKO_AUTH_OWNER_EMAIL=you@example.com                        # at least one of the three is required on a public URL
   ```
5. Edge: `HANKO_CADDYFILE=Caddyfile.authenticated` (no basic_auth, forwards `Authorization`) and `docker compose --profile edge up -d`.

**Admission policy.** With `HANKO_AUTH_ALLOWED_EMAILS` and/or `HANKO_AUTH_ALLOWED_DOMAINS`, only people on the lists get in, and leaving the list
**immediately drops** that user's sessions and API keys (checked on every request). The first to sign in becomes `owner`; the rest become `member`.
`HANKO_AUTH_OWNER_EMAIL` names the owner: only that email becomes `owner` (and gets in even if it is off the lists); the others only get in if they are on the
lists, as `member`.

**Boot refusal.** With `HANKO_PUBLIC_URL` outside loopback, the `firebase` provider requires an admission list **or** `HANKO_AUTH_OWNER_EMAIL`:
with neither, the first visitor who logged in would become owner, so the server does not start. Only on loopback, with no lists and no named owner,
development mode applies: the first to sign in becomes owner and the rest are refused (boot logs a warning).
`email_verified` is required: whoever creates an email/password account receives a verification link and must confirm it before signing in.
An email already registered through another method (e.g. the local owner) is not linked automatically: it is refused (409).
`hanko auth create-owner` does not apply to the firebase provider.

### 5.3 API keys and the CLI

Create them on the **Settings** screen (sidebar footer menu) or, directly against the database, without the server running:

```bash
hanko auth token create --name laptop        # prints the key exactly once (hk_...); --email if there is more than one user
hanko auth token list
hanko auth token revoke <id>
export HANKO_URL=https://hanko.example.com HANKO_TOKEN=hk_...
hanko task list -c acme                       # the CLI sends HANKO_TOKEN as a Bearer token
```

Only the key's SHA-256 hash is stored (plus a prefix to identify it). Once revoked, the key stops working immediately.
The `hanko auth` commands use `HANKO_DATABASE_URL` (and the rest of the configuration, including `HANKO_SECRET` in authenticated mode).

### 5.4 Agents and authenticated mode

Run tokens keep working (API, `/mcp`, `/memory-mcp`). Local agents that **omitted the token** and talked to loopback as if they were
the human (ADR-0017) now get **401**; use the run token (already injected by Hanko) or a dedicated API key.

### 5.5 Installable PWA

The UI is installable (manifest at `/manifest.webmanifest`, service worker at `/sw.js`, icons under `/static/icons/`) and shows an
`/offline` page when the network drops. The service worker only caches versioned static assets (`/static/…?v=`): authenticated HTML,
the API, SSE, `/auth/*`, `/convite/*` and any POST never go through the cache. It needs https (or `localhost`); the manifest, `sw.js` and
`/offline` are public (they carry no data). Every deploy changes `sw.js` (binary version), which updates the worker and drops the old cache.
If the installed app looks stale: reload twice or "Unregister" the service worker in the browser devtools. Regenerate the PNGs from the
seal with `python3 scripts/gen-pwa-icons.py` (Pillow).

## 3. Backup and restore

### 3.1 Production (`hanko-prd`, oute-server)

Two layers, which fail in different ways:

| Layer | What | When | Where |
|---|---|---|---|
| Local (LXC) | `deploy/oute-server/backup.sh`: `pg_dump -Fc` of the `hanko` database + a tar of the ai-memory volume (container paused during the tar so the SQLite index is consistent) | daily ~01:30 UTC (`hanko-backup` timer, installed by `deploy-prd.sh`) | `/var/backups/hanko` in the LXC, 3 generations + `SHA256SUMS` |
| Lab (restic) | a `pg_dump` of the database made by the lab itself + the directory above (`backup_paths`) | weekly, Saturday 02:00 (`oute-backup-weekly`) | restic in `/var/backups/restic-oute` on the host + offsite sync; alert in Kuma |

The timer runs before Saturday 02:00, so restic picks up a fresh local copy. `agent-home` (the claude/codex login) and
`lgtm` (telemetry) are **not** backed up: the login can be redone (see "Claude login" at the end of the deploy) and telemetry is disposable.
The source of truth for governance is entirely in Postgres (ADR-0015, rule 5); ai-memory is recall (not a source of truth),
but its tar is kept so session history is not lost.

Verify (from the Mac):

```bash
ssh oute-server "lxc exec hanko-prd -- systemctl list-timers hanko-backup.timer --no-pager"
ssh oute-server "lxc exec hanko-prd -- bash -lc 'systemctl start hanko-backup.service && ls -l /var/backups/hanko && cd /var/backups/hanko && sha256sum -c SHA256SUMS'"
```

**Monthly drill (does not touch prd):** restores the newest dump into a throwaway `hanko_restore_check` database, prints the number of
tables and the last applied migration, and drops the database.

```bash
ssh oute-server "lxc exec hanko-prd -- /opt/app/deploy/oute-server/restore.sh --check"
# expected output: tables=<N> / migration=<version> / "drill ok (prd untouched)"
```

**Restore from the local copy (same LXC):** stops hanko and ai-memory, renames the current database to `hanko_pre_restore_<ts>` (it is
never dropped), restores the dump into a new `hanko` database, puts back the ai-memory volume, starts the stack and waits for `/readyz`.
If `pg_restore` fails the old name comes back and the stack starts by itself. Only after readyz does the script print the command to drop
the old database (`DROP DATABASE hanko_pre_restore_<ts>`). It asks for confirmation (`--yes` skips it).

```bash
ssh -t oute-server "lxc exec hanko-prd -- /opt/app/deploy/oute-server/restore.sh --apply"
ssh oute-server "lxc exec hanko-prd -- curl -fsS http://127.0.0.1:3810/readyz"
# a specific generation:
#   restore.sh --apply --pgdump /var/backups/hanko/hanko-<UTC>.pgdump --aimemory /var/backups/hanko/aimemory-<UTC>.tar.gz
```

**Restore from the lab's restic (LXC lost or local copy corrupted):** on oute-server, as root (it needs the lab's
`/etc/oute/backup.env` for the repository and the restic password). Recreate the LXC with `deploy-prd.sh` first (it brings up an empty
stack), then:

```bash
. /etc/oute/backup.env; export RESTIC_REPOSITORY RESTIC_PASSWORD
restic snapshots --tag hanko-prd | tail
restic dump latest containers/hanko-prd/hanko.pgdump > /tmp/hanko.pgdump       # the lab's own dump
restic dump latest containers/hanko-prd/files/hanko.tar > /tmp/hanko-local.tar  # local copy + memory
mkdir -p /tmp/hanko-local && tar xf /tmp/hanko-local.tar -C /tmp/hanko-local && ls /tmp/hanko-local
lxc file push /tmp/hanko.pgdump hanko-prd/root/hanko.pgdump
lxc file push /tmp/hanko-local/aimemory-*.tar.gz hanko-prd/root/aimemory.tar.gz   # if present
lxc exec hanko-prd -- /opt/app/deploy/oute-server/restore.sh --apply --yes \
  --pgdump /root/hanko.pgdump --aimemory /root/aimemory.tar.gz
rm -f /tmp/hanko.pgdump /tmp/hanko-local.tar; rm -rf /tmp/hanko-local
```

(`--aimemory` is optional; without it only the database is restored. The exact file name in restic is shown by `restic ls latest --tag hanko-prd`.)
After the restore: `/readyz` ok, login through Firebase, and record the date and result in `docs/SESSION.md`. The `.env` (secrets) is **not**
in any backup: it is generated inside the LXC the first time. When you recreate the LXC, the new `POSTGRES_PASSWORD` works because the
restored database is recreated by the compose postgres itself, but a new `HANKO_SECRET` invalidates issued sessions and API keys. Keep a
copy of the `.env` in your vault after the first deploy.

### 3.2 Generic variant (dev/stg compose)

Daily backup (cron of the `hanko` user in the container): `pg_dump` + `restic` to an R2 bucket.

```bash
# /opt/hanko/backup.sh  (export RESTIC_REPOSITORY=s3:https://<account>.r2.cloudflarestorage.com/<bucket>,
# RESTIC_PASSWORD, AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY in the cron environment)
set -euo pipefail
cd /opt/hanko
docker compose -f deploy/compose.yaml exec -T postgres pg_dump -U "$POSTGRES_USER" -Fc "$POSTGRES_DB" > /tmp/hanko.dump
restic backup /tmp/hanko.dump --tag hanko-db
# ai-memory: volume with a git-backed wiki + SQLite index
docker run --rm -v hanko_aimemory:/data:ro -v /tmp:/out alpine tar czf /out/aimemory.tgz -C /data .
restic backup /tmp/aimemory.tgz --tag hanko-memory
rm -f /tmp/hanko.dump /tmp/aimemory.tgz
restic forget --tag hanko-db --tag hanko-memory --keep-daily 7 --keep-weekly 4 --keep-monthly 6 --prune
```

Cron: `17 3 * * * /opt/hanko/backup.sh >> /var/log/hanko-backup.log 2>&1`.

**Restore test (once a month, on `hanko-stg`):**

```bash
restic restore latest --tag hanko-db --target /tmp/restore
docker compose -f deploy/compose.yaml exec -T postgres createdb -U "$POSTGRES_USER" hanko_restore
docker compose -f deploy/compose.yaml exec -T postgres pg_restore -U "$POSTGRES_USER" -d hanko_restore --no-owner < /tmp/restore/tmp/hanko.dump
docker compose -f deploy/compose.yaml exec -T postgres psql -U "$POSTGRES_USER" -d hanko_restore -c "select count(*) from companies; select max(version_id) from goose_db_version;"
docker compose -f deploy/compose.yaml exec -T postgres dropdb -U "$POSTGRES_USER" hanko_restore
```

Record the date and result in `docs/SESSION.md` or in the operations issue.

## 4. Troubleshooting

| Symptom | Likely cause | What to do |
|---|---|---|
| Hanko does not start: `HANKO_DATABASE_URL é obrigatório` (is required) | env/yaml without a URL | set `HANKO_DATABASE_URL`; check `HANKO_CONFIG` |
| `db: ping após 5 tentativa(s)` (ping after 5 attempts) | Postgres down / wrong password | `docker compose ps postgres`, healthcheck, password in `.env` |
| Warning `falha de exportação OpenTelemetry` (OpenTelemetry export failure) | collector (LGTM) down | **does not take the app down**: telemetry has nowhere to go until it is back. `docker compose up -d otel-lgtm`; check `OTEL_EXPORTER_OTLP_ENDPOINT`. `/metrics` keeps working (pull) |
| Warning `ai-memory indisponível no boot` (unavailable at boot) | ai-memory down or wrong token | the app **degrades**: runs open without a briefing, facts stay in `memory_outbox` and drain when it returns. `docker compose logs ai-memory`; check `HANKO_MEMORY_URL`/`HANKO_MEMORY_TOKEN` |
| Warning `HANKO_SECRET não definido` (not set) | secret generated per boot | set `HANKO_SECRET`; otherwise run tokens of in-flight runs are invalidated at every restart |
| Runs stay `queued` | heartbeat stopped, low `Workers`, or the agent CLI is missing | hanko log; `HANKO_WORKERS`; `claude`/`codex` on the PATH (or `Dockerfile.agents`) |
| `/mcp` answers 401 from outside | trusted mode only accepts no token on loopback | expected: agents use the run token; humans use the API/UI |
| `421 host não permitido` (host not allowed) | `Host` header not in the list | add the name to `HANKO_ALLOWED_HOSTS` or adjust `HANKO_PUBLIC_URL` |
| `HANKO_SECRET tem N bytes` (has N bytes) | short secret | `openssl rand -hex 32` |
| `path: must be inside the workspaces root` when creating a workspace | path outside `HANKO_WORKSPACES_ROOT/<company-slug>` | use a path inside the root company folder or change the root |
| The agent cannot see an environment variable | filtered env (allowlist) | `HANKO_AGENT_ENV_ALLOW=NAME` |
| `HANKO_SECRET exige…` / `HANKO_PUBLIC_URL exige https` at boot | authenticated mode without a 32-byte secret, or with an http URL outside loopback | `openssl rand -hex 32`; use `https://…` (or loopback for a local test) |
| `/entrar` shows "Criar conta de dono" (Create owner account) | database has no users | open the `/claim/…` link printed to stderr at boot, or `hanko auth claim-link` / `hanko auth create-owner` (§5.1) before exposing it; on a public URL the form only exists with `HANKO_AUTH_WEB_BOOTSTRAP=true` |
| `429 Too Many Requests` with `Retry-After` | general rate limit (writes, MCP, incoming webhooks, error reports) | expected under bursts: wait for `Retry-After`; for big batches per API key raise `ratelimit.writes_per_min` (Settings › Instance) or turn it off with `ratelimit.enabled=false`. Metric `hanko.ratelimit.decisions` and the "Hanko · Errors" dashboard |
| Boot refused: `HANKO_AUTH_PROVIDER=firebase com HANKO_PUBLIC_URL público exige…` (with a public URL requires…) | firebase with neither lists nor a named owner | set `HANKO_AUTH_ALLOWED_EMAILS/DOMAINS` or `HANKO_AUTH_OWNER_EMAIL` (§5.2) |
| Login 503 "Muitos logins ao mesmo tempo" (too many logins at once) | all argon2id slots busy for 2 s | try again; if it persists there is a login burst (check the rate limit at the edge) |
| Login rate limit blocks everyone together behind Caddy | `HANKO_TRUSTED_PROXIES` missing the compose subnet | include the docker subnet (§5) |
| Firebase: "Este e-mail não tem acesso" (this email has no access) | not in `HANKO_AUTH_ALLOWED_EMAILS/DOMAINS` (or empty list and an owner already exists) | adjust the lists (§5.2) |
| Firebase: "domínio não autorizado" (domain not authorized) in the popup | domain not in Authorized domains | Console → Authentication → Settings |
| Firebase: 503 `Não foi possível validar o login` (could not validate the login) | server cannot reach `www.googleapis.com` | allow the server's HTTPS egress to Google's JWKS |
| A local agent gets 401 after authenticated mode is turned on | it omitted the token (ADR-0017) | use the injected run token or an API key (§5.4) |
| `acp` run fails with "não respondeu ao initialize" (did not answer initialize) | the command does not speak ACP v1 over stdio (or takes longer than `init_timeout_sec`) | run the command by hand; the error carries the last stdout/stderr lines (§ ACP agents) |
| `acp` run ends in seconds without editing anything | the `read_only` policy (default) refused the edit | look for "permissão negada" (permission denied) events in the log; hire with `permission: allow` if intended |
| Port 7420 in use | another instance | `HANKO_ADDR` |

Logs are JSON on stderr with `trace_id`/`span_id`: copy the `trace_id` and look it up in Tempo (Grafana) to see the full trace of the request/run.

## Task coordination (ADR-0040)

- **Dependencies.** `hanko task dep add HNK-12 HNK-7` makes 12 depend on 7: it is dispatched only when every dependency closes (done **or**
  cancelled), and its assignee is woken in the same transaction in which the last one closes. No cycles (409 with the chain), no crossing
  companies, at most 50 per task. "Blocked" is computed; the `blocked` status stays the parked-waiting-for-someone one. A persona uses
  `hanko_task_dependency` and `depends_on` on `hanko_task_create`.
- **Tree.** A subtask is `--parent`. The parent wakes when its last child closes and cannot complete with an open child (422); a human completes
  it anyway with `hanko task status HNK-1 done --force --reason "..."` (the reason stays in the `task.close_override` fact). 10 levels at most.
- **Hold.** `hanko task hold HNK-1 --reason "..."` freezes the task and its whole subtree: dispatch refuses, the heartbeat safety net skips it,
  queued runs are cancelled and a run already going finishes on its own. `hanko task resume HNK-1` wakes the requests that were refused. The inbox
  has a **Paused** tab. The hold root resumes it (a child that only inherits points at the origin).
- **Per-task/project review policy.** `hanko task review-policy HNK-1 --require --reviewer-agent codex` (admin); `--no-reviewer` goes straight to the
  human gate; `--clear` inherits again. `hanko project review-policy <project>` applies to tasks without an override. The run screen shows which
  layer (task, project or persona) the policy in force came from.
- **Operations.** Additive migration 00100 (new tables, 2 integrity triggers, 1 function); `Down` undoes it. OTel counter `hanko.task.coordination`
  (`kind`, `op`) and spans `service.hold_task`, `service.resume_task`, `service.add_task_dependency`, `service.remove_task_dependency`,
  `service.checkout_run`. Limits: export/import (`hanko.company.v1`) carries the tree (and rejects more than 10 levels) but not dependencies, holds or
  per-task policies.

## Skills and integrations (ADR-0029)

Operator settings (a company cannot turn these on by itself):

| Variable | Default | Effect |
|---|---|---|
| `HANKO_SECRET_KEY` | empty | Key (32+ characters) that encrypts integration secrets (AES-256-GCM). Empty: Hanko starts but refuses to store integration secrets. |
| `HANKO_SECRET_KEY_PREVIOUS` | empty | The previous key, only during rotation. |
| `HANKO_INTEGRATIONS_ALLOW_STDIO` | `false` | Allows `stdio` MCP servers, which run a command **on the server machine**. |
| `HANKO_INTEGRATIONS_ALLOW_PRIVATE` | `false` | Allows webhooks and connection tests to loopback, link-local and private networks (otherwise SSRF blocking applies). |
| `HANKO_SKILL_SOURCES` | `false` | Turns on git skill sources (ADR-0035): the server clones public repositories over https with the `git` on the PATH. Internal hosts only with `HANKO_INTEGRATIONS_ALLOW_PRIVATE`. |
| `HANKO_GIT_WORKSPACES` | `false` | Turns on execution workspaces with Git (ADR-0054): the server keeps a mirror clone of the workspace repository under `<HANKO_WORKSPACES_ROOT>/<company>/.hanko/mirrors` and creates one worktree per task under `.hanko/worktrees` (git 2.31+ on the PATH). The repository credential is a `${secret:NAME}` secret (ADR-0050). Internal hosts only with `HANKO_INTEGRATIONS_ALLOW_PRIVATE`. An admin turns Git on per workspace (`hanko workspace git enable`). |

**Generate the key:** `openssl rand -hex 32`. Keep it with `HANKO_SECRET` and in the operator's backup; losing it loses the
stored secrets (integrations remain, without credentials; enter them again).

**Rotate the key:** set the new key in `HANKO_SECRET_KEY` and the old one in `HANKO_SECRET_KEY_PREVIOUS`, restart, run
`hanko integration rekey` (or `POST /api/v1/integrations/rekey-secrets`; installation owner only), then remove
`HANKO_SECRET_KEY_PREVIOUS` and restart.

**Webhooks.** Each delivery carries `X-Hanko-Signature: t=<unix>,v1=<hex>`, `X-Hanko-Event` and `X-Hanko-Delivery`. The receiver
computes `HMAC-SHA256(secret, "<t>.<body>")`, compares in constant time and rejects an old `t`. Failures retry up to 7
attempts (30 s, 2 min, 8 min, 32 min, ~2 h, 6 h). Delivery is at-least-once; deduplicate on `X-Hanko-Delivery`.
## Decision model and the devin, pi, cursor and http harnesses (ADR-0056)

**Decision model.** A per-company external classifier that Hanko itself calls (triage, phase classifier, name and summary); every call
lands in Costs with the `system` actor. Configure it under Settings › Decision model, or from the CLI:

```sh
hanko secret create openrouter --value-stdin            # the provider key, as a company secret
hanko decision catalog --api-key-ref '${secret:openrouter}'   # catalog models and prices
hanko decision set anthropic/claude-haiku-4.5 --api-key-ref '${secret:openrouter}'
hanko decision feature triage on
hanko decision triage HNK-12                            # comments the suggestion on the task (does not assign)
hanko decision invocations                              # ledger: model, tokens, cost, source
```

The default endpoint is OpenRouter's; `--base-url` points to any chat-completions-compatible API (an internal destination needs
`integrations.allow_private`). The key is never stored in the configuration: only the `${secret:NAME}` reference, resolved on every
call. Features start turned off. The cost is the response's `usage.cost` when present, otherwise tokens × catalog price, and it adds to
the company's monthly spend (budget).

**New harnesses.** All are hired with `hanko persona hire --agent <name>` (or the card in the hire screen); the executable is fixed by the
operator and authentication happens outside Hanko:

| Harness | Executable (env / yaml) | Notes |
| --- | --- | --- |
| `devin` | `HANKO_DEVIN_BIN` (`devin`) | runs `devin acp` over the ACP engine; not subject to `HANKO_ACP_ALLOW`; `adapter_config` accepts `permission`, `model`, `env`, `init_timeout_sec` |
| `pi` | `HANKO_PI_BIN` (`pi`) | `pi --mode json -p`; Hanko tools go through the `hanko` CLI with the run token (the CLI must be on the server's PATH) |
| `cursor` | `HANKO_CURSOR_BIN` (`cursor-agent`) | `cursor-agent -p --output-format stream-json`; same tools rule; `force: true` lets it apply changes |
| `http` | — | no CLI: Hanko runs the tool-use loop over `base_url` (chat completions) with Hanko's MCP tools; `adapter_config`: `base_url`, `api_key_ref` (`${secret:X}`), `model`, `max_steps`, `temperature`, `timeout_sec`, `tools` |
| `acp` + `preset` | `HANKO_ACP_ALLOW` | `{"preset":"gemini"}` or `"opencode"` fills in command and arguments; the operator gate applies |

**Check before running.** `hanko persona probe <persona>` (admin) runs a probe with no prompt and no cost, with named steps (binary,
protocol, authentication, endpoint, model). For `devin`/`acp` the probe does initialize and session/new: an agent that requires login
fails the "authentication" step, quoting the methods it advertises. For `pi`/`cursor` it only checks the binary (`--version`).

**Local model (e.g. oMLX).** An `http` persona with the local server's `base_url` (`integrations.allow_private` on) and a model with
proven tool calling; review runs (read-only) offer no tools. The harness neither executes commands nor writes to disk.
## Tool policy (ADR-0053)

On top of tool profiles (ADR-0036), a **policy** says `off` (refuse), `ask` (ask first, opens a `tool` gate) or `allow` (go straight through) for one persona
or the whole company, over any integration tool, one integration, one tool, or a harness/plugin tool by name.

- **Write:** the **Policy** tab under Integrations, `hanko tool-policy set <off|ask|allow> [--persona P] [--integration I] [--tool T | --native NAME]`
  (admin). With no target flag it covers everything. Precedence: persona > company, tool > integration > everything, and on a tie off beats ask beats allow.
  A policy beats the profile in runs; quarantined, disabled or removed tools are never opened by a policy; on a named gateway a policy only tightens.
- **Always allow:** `hanko gate approve <gate> --always` (or the "Approve and always allow" button; admin only) saves `allow` for that persona and tool.
- **`tool` gate timeout:** 24 h (integration/plugin) and 10 min (native call: the run waits). The `memory_sweep` (5 min) expires pending gates; the next
  call opens a new gate.
- **Harness-native tools:** claude gets the `PreToolUse` hook (`hanko hook pretooluse`, the server's own executable) **only when a native policy applies to the
  persona**; it uses the run token from the environment and fails closed (if Hanko does not answer, it denies). ACP asks the policy on every permission
  request. Remote environments (SSH/sandbox) do not get the hook. Codex has no hook. The audit log records the origin (`hook`, `acp`).
- **Test:** `hanko integration tools <i> --test <tool> --as <persona> [--args JSON] [--execute]` or the form on the Policy tab.
- **Single endpoint:** `HANKO_TOOLS_SINGLE_ENDPOINT=true` (yaml `tools_single_endpoint`) mounts one `hanko-tools` server (`/mcp/run`) for the run's
  integrations, with tools named `<integration>__<tool>`. Off by default because it changes the tool names the harness sees.
- **Audit:** `/c/<company>/ferramentas/auditoria` or `GET …/tool-invocations` (admin): origin, actor, duration, redacted result excerpt and the deciding
  policy. 30 days and 10k rows per company are kept.

## ACP agents (`acp` harness)

The `acp` harness (ADR-0023) runs any agent that speaks the Agent Client Protocol v1 over stdio. Hanko ships no agent: the operator
installs the program and the persona points at it.

**Turning the harness on (operator decision).** Whoever writes a persona's command is a company admin, and the command runs on the server
machine with Hanko's privileges. So the harness ships **off**: with `HANKO_ACP_ALLOW=false` (default), hiring or importing an `acp` persona
returns 422 ("o harness acp está desligado neste servidor", the acp harness is off on this server) and an older persona fails at run start with
the same reason. Turn it on with `HANKO_ACP_ALLOW=true` and preferably narrow it with `HANKO_ACP_COMMANDS=my-agent,/opt/agents/other` (a bare
name, resolved through `PATH`, or an absolute path; when the list is set only those executables are accepted). The check runs on
hire/import and again at the start of every run (a config changed later does not slip through).

**Installing the agent.** The executable must exist on the `PATH` of the Hanko process (or be used by absolute path) and be authenticated
outside Hanko (interactive authentication is not supported). The official (distroless) `Dockerfile` cannot run agents; use the
`Dockerfile.agents` variant or an image of your own derived from it, for example (**illustrative example**, the package is fictional):

```dockerfile
FROM my-hanko-agents-image
USER root
RUN npm install -g acp-agent-package && npm cache clean --force
USER node
```

Without a container, install the agent on the server host/LXC (global npm, a binary in `/usr/local/bin`, …) for the same user that runs Hanko.
Agent credentials (API key, …) go in through `HANKO_AGENT_ENV_ALLOW` (server variables) or `adapter_config.env` (on the persona).

**Hiring an ACP persona.** In the UI, pick the ACP harness and fill in command, arguments and permissions. From the CLI (lists and objects
need `--config-json`) or the API (`adapter_config`):

```sh
# EXAMPLE: replace "my-acp-agent" with the real command of your agent
hanko persona hire "External" --agent acp \
  --config-json '{"command":"my-acp-agent","args":["--stdio"],"permission":"read_only"}'
```

`permission`: `read_only` (default) approves reading, search, thinking and external lookups (`read`, `search`, `think`, `fetch`) and refuses
edit, delete, move, execute and the rest; `allow` approves everything (including running commands: use it only with agents and workspaces you
trust); `reject` refuses everything. Writing through `fs/write_text_file` is accepted only with `allow`. Review runs ignore the setting and use
`read_only`. Every decision shows up in the run log as a "permissão concedida/negada" (permission granted/denied) event.

**Known limits.**
- Confinement to the workspace directory applies to what goes through Hanko (`fs/*`). The agent process has the privileges of the user running
  Hanko; to really isolate it, run Hanko in a container/user with no access to what the agent must not see.
- Hanko's own MCP server is http; an agent that does not advertise `mcpCapabilities.http` runs without the `hanko_*` tools (a warning is logged).
- No terminal (`terminal/*`), no interactive authentication, no ACP v2. Changing a persona's command means hiring another persona.
- Cancelling a run sends `session/cancel` and waits up to 3 s; an agent that does not obey is killed (the whole process group).
- An ACP persona can be a **reviewer persona**, but `acp` cannot be the alternate review harness (`--reviewer-agent`).
- Exporting a company strips credential keys and flags from `adapter_config`; after importing, put the agent's key back.

**Diagnosis.** Common run errors: "não respondeu ao initialize em N s" (did not answer initialize in N s: the command does not speak ACP over
stdio; try it by hand and read the stdout/stderr lines in the error), "versão do ACP incompatível" (incompatible ACP version: the agent speaks
another protocol version), "o agente exige autenticação" (the agent requires authentication: authenticate it outside Hanko) and "não foi possível
iniciar o agente ACP" (could not start the ACP agent: the command is not on Hanko's `PATH`).

## Projects and workspaces

A **workspace** is a directory (optionally with `repo_url` and a default branch, metadata only) and a **project** groups tasks, points to a
workspace and may have a monthly budget (ADR-0025). Screens `/projetos` and `/workspaces`; CLI `hanko project …`, `hanko workspace …`,
`hanko task create --project`, `hanko cost --project`, `hanko budget set --project`.

- **Run directory:** task workspace > project workspace > persona workspace > temporary directory. The origin is stored in
  `runs.workspace_source` (`task|project|persona|none`).
- **Path:** absolute, clean and inside `HANKO_WORKSPACES_ROOT/<company-slug>` (old workspaces outside the company folder keep working and the UI warns). If the directory is missing on the machine the UI shows "missing directory"
  and runs use a temporary one; create the folder (Hanko does not `git clone`).
- **Deleting a workspace in use** returns 409; `--unlink` (CLI), `?unlink=true` (API) or the checkbox in the UI unlink personas, projects and tasks.
- **Project budget:** once exceeded, the run is refused with the reason and the persona is not paused; at 80% an alert is opened in the inbox.
  Raise the cap (`hanko budget set --project <slug> <usd>`) to release it. Paid cost (subscription) is not allocated per project.
- **Importing from another machine:** a path this installation does not accept is imported empty and `workspace_paths_cleared` reports it;
  fix with `hanko workspace update <slug> --path …`.

## Consolidated memory (governed curation)

Requires `HANKO_MEMORY_PROVIDER=ai-memory` (ADR-0030). With `none`, the Memory section of company Settings shows "Memory off" and the API answers 422 with the reason.

- **Enable:** company Settings → Memory (or `hanko memory config --enable --interval 24 --curator <persona>`). Interval 1 to 720 h; off by default. Prefer a curator on a cheap model.
- **Run now:** "Consolidate now" button or `hanko memory consolidate [workspace]`. Creates one system task per workspace; cost goes through the ledger and budget like any run. One open round per workspace; one that never ran expires after 6 h.
- **`memory` gate:** removals, merges and rewrites by the curator never reach ai-memory; they become a proposal and a gate in the Inbox "Memory" tab (diff and sha256). Approving queues `apply_memory_proposal`; rejecting discards. CLI: `hanko memory proposals|approve|reject <id>`.
- **Apply failure:** the proposal stays `failed` with the error and is retried (1 min to 6 h, 20 attempts). After that: `hanko memory retry <id>` or the retry button. Nothing is lost.
- **Grants:** a persona only reaches its own workspace memory. For another workspace of the company: `hanko memory grant <persona> <workspace> --access read|write` (`grants`, `revoke` to list and remove).
- **Diff with current content:** the default is `memory_read_page` (ai-memory's real tool); `HANKO_MEMORY_READ_TOOL` (`memory.read_tool` in YAML) overrides the name; with none configured the diff shows only the proposed content.

| Symptom | Likely cause | Action |
|---|---|---|
| "Memory off" / 422 when enabling | `HANKO_MEMORY_PROVIDER=none` | configure ai-memory and restart |
| Proposal `failed` | ai-memory down or refused | see the error in the Memory section; retry is automatic, or `hanko memory retry` |
| Many proposals | unknown tool names are held for safety | approve/reject; see ADR-0030 |
| Consolidation does not run | curator paused or out of budget | check Inbox/alerts; expires in 6 h |

## Chat with a persona

A direct conversation between a human and a persona, outside a task (ADR-0027). In the UI: **Chat** in the menu (`/c/{company}/chat`). On the CLI:

```
hanko chat cto "What is the risk of Friday's migration?"       # opens the conversation, waits and prints the answer
hanko chat cto "And plan B?" --conversation 01a13f51           # continues a conversation (id or prefix)
hanko chat cto "Summarize the day" --no-wait                   # only sends; follow with `hanko chat show <id>`
hanko chat list --persona cto
```

- Each human message triggers a **run** with the `chat` trigger: it shows in Runs (marked as Chat, linked to the conversation) and in Costs
  (a "Chat" row), and counts toward the persona and company budgets and the subscription weekly guard.
- A persona that is paused, in error, over budget or behind the weekly guard **does not receive the message**: the UI, the API (409) and the CLI show why.
  Reactivate the persona or raise the limit and send again.
- Messages sent while the persona is answering wait (up to 20) and the next run answers them together. A chat run that fails, is cancelled or
  interrupted leaves a system notice in the conversation; waiting messages stay queued until the next message.
- With `claude` (and `fake`) the harness session is resumed between messages; a harness without that ability gets the recent history
  (up to 24 KiB) in the prompt on every message.
- The persona can open a task from the conversation (`hanko_task_create`); its description cites the conversation. A chat run has no task,
  so `hanko_gate_request` needs the `task` argument.
- To see how an answer was produced, open the message's "run" link (the run's live log).

## Artifacts and goals

**Artifacts** (ADR-0028, ADR-0064) are documents with a stable identity and immutable versions numbered **MAJOR.MINOR**. Changing
one means publishing with the document id (`--artifact`, `artifact=` in MCP): it becomes the next version even under another name
or on another task. Without an id, the same name on the same task also versions. Publishing the same content again creates no
version. A change is **major** (heavy) or **minor** (small): the publisher declares it and the server measures the line diff
(≥ 40 % of the lines suggests major, < 10 % suggests minor); when the measure contradicts the declaration, the declaration wins and
the version is flagged as "classification questioned". Project, workspace and free tags live on the document and drive filters.
The `/artefatos` screen (filters by project, task, persona, tag, type and name; upload; a detail page with preview, the major/minor
history, a text diff, **New version**, **restore** an earlier version (it becomes the latest, always major), **reclassify** the
latest one, and tags) and sections on the task, run and project pages. Roles: viewer reads; member publishes, restores,
reclassifies and edits tags; admin deletes (the whole document, with no trash). Deleting a task keeps its artifact versions (only
attachments go).

| Variable | Default | Effect |
|---|---|---|
| `HANKO_ARTIFACT_MAX_BYTES` | 5 MiB (1 to 16 MiB; 64 MiB with S3) | Maximum size of one file. |
| `HANKO_ARTIFACT_QUOTA_BYTES` | 512 MiB | Company quota, summing every version. |

- **Publishing:** the UI upload, `hanko artifact put <file|-> [--task HNK-1] [--artifact DOC --change major|minor --reason …]
  [--tag t]` (also `list [--tag] [--workspace]`, `get <id> [-o file] [--info]` or `get <document> --version 1.2`,
  `diff <id> [--against <id>]`, `restore <id>`, `reclassify <id> major|minor`, `tag <document> --tag …`, `rm <id>`, `usage`), the
  `…/artifacts` and `…/artifact-documents/{document}` API and the MCP tools `hanko_artifact_put`, `hanko_artifact_list`,
  `hanko_artifact_get` and `hanko_artifact_restore`. A persona reads any document of the company and versions those of its task's
  project (or with no project); the run briefing lists the task's and project's documents and the ones it published, with the id to
  change them.
- **Safe serving:** content never becomes a page. Only plain text, Markdown and raster images (png, jpeg, gif, webp) open in the
  browser; HTML, SVG, PDF and everything else download. The response carries `nosniff`, a CSP with `sandbox` and
  `Cross-Origin-Resource-Policy`.
- **Exporting with artifacts:** `hanko export --with-artifacts` (API `with_artifacts=true`); artifact content is capped at 20 MiB
  in the file. Without the option the file is as before; old files import normally. Goals are not exported.
- **Backup:** files live in Postgres, so the dump grows; check `hanko artifact usage` before raising the quota.

**Goals** (ADR-0028): a start value, a target (it may be lower, for reduction goals) and a current value; progress = (current −
start) / (target − start), from 0 to 100%. Recording a value that reaches the target marks the goal as achieved, writes the
`goal.achieved` activity and shows an informational inbox item for 7 days. The `/metas` screen (active, achieved, abandoned tabs), a
goal page with the history, linked tasks (one active goal per task) and a "Metas" panel on the project. CLI: `hanko goal
create|list|show|update|progress`, `hanko task create --goal`. Active goals go into the run briefing, and the persona records the
measured value with `hanko_goal_update` (`hanko_goal_list` lists them). Roles: viewer reads; member and persona record progress
and link tasks; admin creates, edits, abandons and reopens.

| Symptom | Cause | Action |
|---|---|---|
| Upload refused: "over the N MiB per-file limit" | file above `HANKO_ARTIFACT_MAX_BYTES` | split the file or raise the limit (up to 16 MiB) |
| Upload refused: company artifact quota | versions sum above `HANKO_ARTIFACT_QUOTA_BYTES` | delete old artifacts (admin) or raise the quota |
| Export with `--with-artifacts` refused | content above 20 MiB | export without the option |
| HTML or PDF downloads instead of opening | by design: only text, Markdown and raster images open | open the downloaded file |
| An achieved goal rejects progress | status `achieved` | reopen it (admin) by switching to active; refused while the current value already reaches the target |

## Secrets, instance settings and artifact storage (ADR-0050, ADR-0051)

### Company secrets

**Settings › Secrets** screen, `hanko secret …` CLI, `/companies/{c}/secrets` API. A secret is an encrypted value (AES-256-GCM with
`HANKO_SECRET_KEY`, the same key as integrations) that you reference as `${secret:NAME}`:

- in **header and variable values** of an MCP integration (`Authorization: Bearer ${secret:docs-token}`);
- in the **persona `adapter_config`** (`env` and arguments): `{"env":{"ANTHROPIC_AUTH_TOKEN":"${secret:gateway}"}}`;
- in the **model provider** (the persona `env` that points the harness at a gateway) and, once it exists, the repository credential.

The database stores only the reference; the value is read **when the run is dispatched**, goes to the agent process and joins the log
redaction list. Rotating (`hanko secret rotate NAME --value-stdin`) takes effect on the next run, without editing the persona or the integration.

| Command | What it does |
|---|---|
| `hanko secret create NAME --value-stdin` | Creates (value from stdin, outside the shell history). Also `--generate` (the server generates it and shows it once) and `--vault-ref path#field`. |
| `hanko secret ls` / `show NAME` | Metadata: provider, version, whether it was revealed, last use and **who uses it**. Never the value. |
| `hanko secret reveal NAME` | Shows the value **once per version** (admin; audited). The second call is 409 until you rotate. |
| `hanko secret rotate NAME` | Replaces the value; the version goes up and the "revealed" mark is cleared. |
| `hanko secret rm NAME [--force]` | Deletes; refused while a persona or integration uses it, unless `--force`. |
| `hanko secret --user …` | The same commands on **your** personal secrets (`${secret:user/NAME}`): only you list, reveal and use them; not even the installation owner sees them. Requires a login (does not exist in trusted mode). Today they serve actions you perform yourself; use by persona runs does not exist yet. |
| `hanko secret rekey` | After rotating `HANKO_SECRET_KEY` (see the integrations section): rewrites integrations **and** secrets. |
| `hanko secret migrate-env` | Moves the plaintext `env` of existing personas into `persona.<slug>.<VARIABLE>` secrets (installation owner; idempotent). |

**The old persona env.** Before M7 the `adapter_config` `env` sat in plaintext in the database. With `HANKO_SECRET_KEY` set, boot migrates
everything by itself (log `segredos: env das personas movido…`) and new personas are born with references. Without the key nothing changes
(plaintext keeps working) and boot warns how many personas are still that way. To check:
`SELECT slug, adapter_config FROM personas;` should show only `${secret:…}` in `env` values.

**External vault (optional).** Set `HANKO_VAULT_ADDR` and `HANKO_VAULT_TOKEN` (and, if needed, `HANKO_VAULT_MOUNT`, default `secret`, and
`HANKO_VAULT_NAMESPACE`). The provider speaks the **KV v2** API (Vault; OpenBao serves the same one). Create the secret with
`--vault-ref apps/hanko#token`: Hanko stores only the reference and queries the vault **on every dispatch** (no cache; rotating in the
vault takes effect immediately). Vault down: the persona run fails early naming the secret; an MCP integration is skipped with a warning.
Hanko's token only needs read access to the paths in use.

**Guarantees and limits.** The value never enters activity, outbound webhooks, exports, briefings, the server log, run events, the
summary/comments or OTel spans (non-leak tests cover each point). The export carries only `${secret:…}`; on import the result reports
`secret_refs_to_create` (the secrets must be recreated in the new company). A company admin who defines a persona's configuration can,
by definition, make the agent use the secret: "reveal once" is an audit trail, not a barrier.

### Instance settings

**Settings › Instance** screen, `hanko settings list|get|set|unset|audit` CLI, `/instance/settings` API (installation owner).
**Non-sensitive** toggles only: `acp.allow`, `acp.commands`, `integrations.allow_stdio`, `integrations.allow_private`,
`artifacts.max_bytes`, `artifacts.quota_bytes`, `run.reserve_usd`, `alerts.silent_factor`, `alerts.run_stuck_after` and the
`ratelimit.*` keys (general rate limit, below).
Precedence: **environment variable > database > yaml/default**. With the variable set (non-empty) the key shows as locked
("locked by HANKO_X") and the API returns 409; to change it, change the environment. Changes in the database apply without a restart and
are recorded in `instance_setting_audit` (before, after, author). Secrets and keys are never settings: environment only.

### Artifact storage (BlobStore)

Default: Postgres (`artifact_blobs`), as before. For S3-compatible storage (S3, MinIO, R2):

| Variable | Effect |
|---|---|
| `HANKO_BLOB_DRIVER` | `postgres` (default) or `s3`: where **new** writes go. |
| `HANKO_S3_ENDPOINT` · `HANKO_S3_BUCKET` | Address (`https://…`; `http` only on loopback) and bucket. |
| `HANKO_S3_ACCESS_KEY` · `HANKO_S3_SECRET_KEY` | Credentials (**environment only**). |
| `HANKO_S3_REGION` (`us-east-1`) · `HANKO_S3_PREFIX` · `HANKO_S3_PATH_STYLE` (`true`) | Signing region, key prefix and path-style addressing (`false` = virtual host). |

With `s3` active the per-file limit goes up: `HANKO_ARTIFACT_MAX_BYTES` accepts up to 64 MiB. Each artifact records which driver holds it, so
the two coexist. **Moving what already exists:**

```
hanko blobs migrate --from postgres --to s3 --dry-run     # only counts
hanko blobs migrate --from postgres --to s3                # copies, verifies (sha256 + size at the destination), switches the record and deletes the source
hanko blobs migrate --from s3 --to postgres --keep-source  # back again, keeping the content in the bucket
hanko blobs status                                         # active driver and what each one holds
```

The command uses the server's database and environment (run it on the Hanko host with the same variables). It is resumable and idempotent;
a failure on one artifact is counted and the migration goes on, and the exit code is 1 if anything failed. `--company acme` limits it to one
company. With S3, `pg_dump` no longer covers the files: back up the bucket. Orphan objects (process killed between upload and transaction,
or a deleted company) take space without showing in the quota; the key is `<prefix>/<company>/<artifact>`, so it can be checked against the database.

| Symptom | Cause | Action |
|---|---|---|
| Boot refused: `HANKO_BLOB_DRIVER=s3 exige…` | endpoint, bucket or keys missing | set `HANKO_S3_*` |
| Upload fails: `s3: … SignatureDoesNotMatch` | wrong keys/region or server clock off | check `HANKO_S3_REGION` and the time (NTP) |
| `s3: endpoint inacessível` | network/DNS/TLS | `curl` the endpoint from the Hanko host |
| Creating a secret is refused: `HANKO_SECRET_KEY não está configurada` | no encryption key | set `HANKO_SECRET_KEY` (`openssl rand -hex 32`) |
| Run fails: `segredo não resolvido: ${secret:X}` | the secret was deleted or the vault is down | recreate the secret / check the vault and token |
| Settings: 409 "definida por HANKO_X" | the environment variable locks the key | change the server environment |

## Rate limit, errors via OTel, e2e and dev labs (ADR-0058)

### General rate limit
A Postgres token bucket (`rate_limit_buckets`, one atomic statement per decision, correct with several instances) per **API key** (hash),
session **user**, **run** (valid run token) or **IP** (the same `HANKO_TRUSTED_PROXIES` as login). Classes: `write` (POST/PUT/PATCH/DELETE on the API
and UI; login, claim and invite have their own limiter), `mcp` (`/mcp`, the tool gateway, `/memory-mcp`), `webhook` (`/hooks/…`: flow triggers and channels, ADR-0046/0047; was reserved for the M19
triggers) and `browser_error` (`/telemetry/errors`). Writes also add a per-IP ceiling. Reads, SSE, `/healthz`, `/readyz`, `/metrics` and `/static/`
never consume. Over the limit: `429` with `Retry-After` (s), `RateLimit-Limit` and `RateLimit-Remaining`; a `problem+json` body on the API/MCP,
plain text on the UI. If the limiter's database is down the request **passes** (fail-open) and the metric counts `outcome=error`.

Defaults (per minute, 1-minute burst): writes 600 per identity and 1200 per IP; MCP 1500; webhooks 120 per IP; browser errors 20 per IP. Change them
under **Settings › Instance** (`ratelimit.enabled`, `ratelimit.writes_per_min`, `ratelimit.ip_per_min`, `ratelimit.mcp_per_min`,
`ratelimit.webhooks_per_min`, `ratelimit.browser_errors_per_min`), with `hanko settings set`, or with `HANKO_RATELIMIT_*` (which wins and locks).
No restart needed. Metric: `hanko.ratelimit.decisions{class,outcome}`.

### Errors in OpenTelemetry (no SaaS)
- **Server:** every unexpected 5xx and every handler panic becomes an `exception` span event (type, message, stack) with `Status=Error`, an `ERROR`
  log with `exception.stacktrace` (shipped to Loki over OTLP) and the `hanko.errors{source,exception_type}` counter.
- **Browser:** `hanko.js` sends `window.onerror` and `unhandledrejection` (up to 5 per page) to `POST /telemetry/errors` (needs a session and same
  origin; body ≤ 8 KiB; URL without query/fragment; 20/min per IP). It becomes a `browser.error` span with the `exception` event and an `ERROR` log
  with `exception.source=browser`. Nothing is stored in the database.
- **Dashboard:** `deploy/grafana/dashboards/hanko-erros.json` ("Hanko · Erros": exceptions by source and type, traces with `status=error`, error
  logs with stack, rate-limit decisions), provisioned by `deploy/compose.yaml` into `otel-lgtm` (Grafana folder *Hanko*). Outside the compose file,
  copy the provider `deploy/grafana/provisioning/dashboards/hanko.yaml` and the JSON into your Grafana (datasources `prometheus`, `tempo`, `loki`).

### e2e tests (Playwright)
`e2e/` (Playwright 1.56.1, development/CI only; the binary does not depend on Node). It needs `bin/hanko` (`make build`), a **disposable** Postgres
(the `public` schema is dropped) in `HANKO_E2E_DATABASE_URL`, and `psql`:

```bash
make build
cd e2e && npm ci
export HANKO_E2E_DATABASE_URL='postgres://postgres:postgres@localhost:5432/hanko_e2e?sslmode=disable'
PLAYWRIGHT_BROWSERS_PATH=/opt/pw-browsers npx playwright test          # or: npm test
npx playwright test --update-snapshots                                # regenerate the screenshot-diff baselines
npx playwright show-report                                            # HTML report
```
The harness starts `hanko serve` with the `fake` adapter, reads the claim link from stderr and runs the journeys. Baselines live in
`e2e/tests/__screenshots__/` (Linux/Chromium). In CI: `.github/workflows/e2e.yml` (PRs touching UI, handlers, migrations or `e2e/`, and `main`).

### Dev labs (`/design-guide`, `/dev/ux`)
Only in trusted mode or for the installation owner/admin (everyone else gets 404). `/design-guide` shows the Kubo DS in light and dark
(`?tema=ambos|claro|escuro`); `/dev/ux` lists synthetic scenarios: **long thread** (500 comments, `?n=` changes the count) and **run with 10,000
lines** (the real virtualized log over a synthetic API). Nothing is written.

## Search (ADR-0024)

Search uses Postgres full-text over `search_tsv` columns generated from `hanko_fold(text)` (case, accents, ASCII punctuation and Unicode
formatting characters). Operating notes:

- **Database locale.** The database **must not use `lc_ctype=C`/`POSIX`**: there `lower()` and the tokenizer only understand ASCII, and
  search stops finding upper-case or non-ASCII letters. Use a UTF-8 locale (`C.UTF-8`, `en_US.UTF-8`, ...). `hanko serve` reads
  `pg_database.datctype` at boot and logs a warning (`o banco está com lc_ctype=C`) when it is `C`. Changing the locale means
  recreating the database (dump/restore).
- **Changing `hanko_fold` requires a migration that recreates the columns.** The function backs stored generated columns and GIN
  indexes: `CREATE OR REPLACE FUNCTION` neither recomputes existing rows nor reindexes. Any change to the fold must drop the indexes
  and `search_tsv` columns, replace the function and rebuild them (migration 00024 does it), together with the Go table in
  `internal/search` (a parity test checks both sides).
- **Large installations.** Migrations 00015 and 00024 rewrite `tasks`, `task_comments`, `personas`, `runs` and `flows` and build the
  GIN indexes under a write lock (~25 s with 80k runs, 60k comments and 30k tasks). If the window does not fit, apply the concurrent
  variant by hand before starting the new version: add the generated column, create each index with `CREATE INDEX CONCURRENTLY`
  (outside a transaction), then mark the migration as applied. `ADD COLUMN … GENERATED … STORED` still rewrites the table; the gain is
  not holding the lock while the GIN is built.
- **Per-query ceiling.** Each search group runs in a read-only transaction with a 2 s `statement_timeout`; exceeding it returns 422
  ("search took too long; use more specific words"). The last word matches by prefix only from 2 letters (3 in comments).
- **Memory in search.** The "Memory" group appears only when the memory server lists the `memory_query` tool (read from `tools/list`,
  cached for 5 min). Without it the group disappears and the log carries one warning; consolidation (ADR-0030) does not depend on it.

## Execution environments, readiness and model catalog (ADR-0037, ADR-0055)

By default every run is a child process of this server (the implicit **local** environment). An admin can add environments in
Settings › Environments or with `hanko env create`: an explicit **local** (a named local with its own variables, useful to force
local on a persona or project when the company default is remote), **ssh** (a remote host), **sandbox** (a hardened docker
container per run) and **cloud** (a disposable cloud sandbox per run, provider Daytona). Each driver exists only if the operator
turns it on:

| Variable | Default | Effect |
|---|---|---|
| `HANKO_ENV_SSH_ALLOW_HOSTS` | empty | Hosts allowed for SSH (exact, suffix, IP, CIDR or `*`). Empty = SSH off. |
| `HANKO_ENV_SANDBOX` | empty | `docker` turns the docker sandbox on (needs the `docker` binary and daemon access). |
| `HANKO_ENV_SANDBOX_HOST_ROOT` | empty | Hanko inside a container talking to the host's docker: the host path of `HANKO_WORKSPACES_ROOT`. |
| `HANKO_ENV_CLOUD` | empty | `daytona` turns the cloud sandbox on. Requires `HANKO_ENV_CLOUD_SSH_KNOWN_HOSTS`. |
| `HANKO_ENV_CLOUD_API_URL` | `https://app.daytona.io/api` | Provider API base (operator-owned; no company picks a network destination). |
| `HANKO_ENV_CLOUD_SSH_HOST` | `ssh.app.daytona.io` | Provider SSH gateway. |
| `HANKO_ENV_CLOUD_SSH_KNOWN_HOSTS` | empty | known_hosts lines of the gateway (`ssh-keyscan`). Required: ssh never accepts an unknown host key. |
| `HANKO_READINESS_INTERVAL` | `15m` | Age after which a persona's readiness test is redone by the periodic check; `0` turns it off ("Test now" still works). |
| `HANKO_PUBLIC_URL` | — | Required and non-loopback for SSH, networked sandboxes and cloud: the remote harness reaches Hanko's MCP through it. |

**Precedence.** A run's environment is the persona's; else the project's (`hanko project env`); else the company default; else the
implicit local. A level pointing at an archived environment is skipped (a warning in the run log). The decision is made when the run
is created and stored on it (`environment_id`, `environment_source`); the run page shows where and why. Archiving an environment
moves its personas and projects.

**Cloud (Daytona).** Each run creates a sandbox at the provider, uploads the workspace through the SSH gateway with a short-lived
token, runs the harness, brings the result back and deletes the sandbox. A failed run or sync **keeps** the sandbox so the work can
be recovered; the provider stops it after 15 idle minutes and deletes it 60 minutes after it stopped. The API key is stored
encrypted (accepts `${secret:NAME}`). The client is tested only against a stub server: try `hanko env probe` against a real account
before relying on it.

**Probe and capabilities.** `hanko env probe` runs a script on the target and records the system, writable folder, binaries
(`tar`, `git`, `claude`, `codex`), whether the target reaches Hanko through the public URL, and `capabilities` (`sync`, `git`,
`claude`, `codex`, `hanko-reachable`, plus `network`, `isolated`, `persistent` from the driver).

**Readiness (persona).** Before a run fails for lack of login, Hanko tests the persona's harness in its environment: binary,
login or API key, and model (format and price). It never calls the model, so it costs no tokens. Steps are named (`environment`,
`binary`, `auth`, `model`) and stored in `persona_readiness`. It runs on "Test now" (the persona page, and right after hiring),
`hanko persona test <persona>`, and the periodic `readiness_tick` job. A failure opens the `persona_not_ready` alert and **blocks
dispatch** (tasks, chat and reviews) with the reason until a test passes; a never-tested persona is not blocked.

**Model catalog.** `hanko model list [--agent claude]` and the hire form's model picker (with an "Other…" option) merge harness
aliases, `model_prices` rows, models seen by readiness tests and models used by the company's runs in the last 90 days. A model
without its own price is flagged: its cost falls back to the fallback price (ADR-0019).
## Access and company lifecycle (ADR-0057)

- **CLI login:** `hanko --url https://hanko.example.com login [--scope full|read] [--device name]` opens `/cli-auth/<code>`; a signed-in person authorizes it and the CLI keeps the API key in `~/.config/hanko/contexts.json` (0600; `HANKO_CONFIG_DIR` changes the location). `hanko whoami` shows the account, device and scope; revoke under Settings › API keys. A `read` key only reads (the API refuses writes and MCP).
- **Archive:** `hanko -c <company> company archive` pauses personas, cancels runs, switches flows off and makes the company read-only (UI banner, 409 on writes). `company restore [--resume]` undoes it. `company delete --confirm "<Name>" -o backup.json` (owner only, archived only) keeps the export in `company_deletions`; `hanko company deletions` lists and `company download-deletion <id>` downloads.
- **Import:** `hanko import file.json --dry-run` shows the preview; `--into <company> --on-conflict skip|rename|replace --resolve personas/dev=replace` merges only the configuration into an existing company. In the UI: `/importar`.
- **Letting people in:** Settings › Members › "Generate join link" (`/pedir-acesso/<token>`); an admin approves with a role via `hanko member requests|approve|deny`. Installation accounts: `/admin/usuarios` or `hanko users`.
## Run resilience: continuation, diagnosis, steer and queue

ADR-0039 (migration 00090). When the Hanko process dies in the middle of a run, the run **goes on by itself**; when the outcome is
uncertain it **waits for your decision**; and you can **change the course** of an active run without waiting for it to end.

- **Automatic continuation.** An interrupted run (process crash or server shutdown) gets a continuation linked to it
  (`continuation_of_run_id`): it resumes the harness session (`session_ref`, stored as soon as the harness announces it) and receives
  a briefing of what was already done. **At most 2 automatic continuations** per chain; the third interruption sends the run to the
  inbox. Recorded in the activity feed (`run.diagnosed`, `run.continued`) and visible on the run page.
- **Uncertain outcome.** If a tool with an external effect (Bash, file write, a write to Hanko, a third-party MCP tool…) was in
  progress when the run died, **nothing restarts by itself**: the run shows up in the inbox (Failures tab, "Stopped runs waiting for a
  decision") with the diagnosis card (cause, attempt, next action, owner) and the tool name. Reads (Read, Grep, `hanko_*_get`…) do not
  count as effects. The heartbeat does not wake the task until the decision comes.
- **Operator actions** (UI on the run and in the inbox; API `POST /runs/{id}/actions`; CLI):

```
hanko run show <run>                         # shows the diagnosis card and the continuation chain
hanko run continue <run> --note "checked the remote"   # resumes the session with the briefing (and your note)
hanko run retry <run>                        # starts over, without the previous session
hanko run close <run>                        # closes the card; an in-progress task goes to blocked
```

  They work on an interrupted, failed or expired run that was not decided yet. Every decision restores the attempt budget.
- **Steer** (change the course of an active run): "Change course" box on the run (and on the task/conversation), `POST
  /runs/{id}/steer`, `hanko run steer <run> "message"`. The UI shows the harness mode: `claude` (stream-json stdin), `acp` (cancels the
  turn and resends in the same session) and `fake` **inject live**; `codex` **interrupts and continues** (the run is cancelled and the
  next one starts from the session when the harness can resume). The message becomes a task comment (or a conversation message) when
  the agent receives it; if the run ends first it stays queued for the next run.
- **Follow-up queue** (task and chat): what you send while the run works waits in order, with the reason for the wait (active run,
  pending steer, pending decision, persona unavailable…). Edit and discard work until delivery.

```
hanko followup list --task HNK-12            # order and reason for the wait (or --conversation <id>)
hanko followup add --task HNK-12 "then update the changelog"
hanko followup edit <id> "new text"
hanko followup discard <id>
```

  When the run ends (for any reason, cancelled included), pending follow-ups wake the persona and the next run collects them in order.
  If the wake is lost, the heartbeat notices a follow-up stuck for over 1 minute.

## Flows: variables, triggers, revisions and archiving (ADR-0047)

A flow is still "work defined once, one task per firing"; it now takes **parameters**, **triggers other than the clock** and keeps a **history**.

- **Variables.** `hanko flow create "Review PR" -p ada --var repo! --var env=staging --title 'Review {{repo}} ({{disparo}})'`. `name!` is
  required, `name=default` gives a default. The title and description accept `{{name}}` and the built-ins `{{data}}`, `{{n}}`,
  `{{semana}}` (ISO week, `2026-W43`) and `{{disparo}}` (`agenda`, `manual`, `webhook` or `evento`). A marker that is neither a built-in nor
  declared is refused on save. The value goes in as text and is **never** expanded again. A schedule requires a default on every required
  variable (cron has nobody to fill it in); `hanko flow run review-pr --var repo=acme/api` (or the "Run now" form on the flow page) asks for the required ones.
- **Optional schedule.** `--cron` may be omitted: the flow then fires only from a trigger or "Run now". `hanko flow edit <flow> --cron ''` removes the schedule.
- **Incoming webhook.** `hanko flow trigger add <flow> github --kind webhook --map repo=repository.full_name` creates the trigger,
  **generates** the HMAC secret (`${secret:hook-…}`, shown once) and returns the path `POST /hooks/flows/<id>`. The delivery carries
  `X-Hanko-Signature: t=<unix>,v1=<hex>` (HMAC-SHA256 of `<t>.<body>` with the secret, 5-minute window; the same scheme as outbound
  webhooks) and optionally `X-Hanko-Delivery` or `Idempotency-Key` (a repeated delivery does not create another task). Responses: `202`
  (task created), `200` duplicate, `401` signature, `404` trigger, `409` flow/trigger off or archived, `413` body over 256 KiB, `422`
  missing required variable, `429` trigger limit (`--rate`, default 30/min) or IP limit (`ratelimit.webhooks_per_min`).
- **Internal event.** `hanko flow trigger add <flow> on-decision --kind event --event 'gate.*' --filter data.decision=approved --map gate=subject_id`
  fires when an activity fact matches (exact verb or `x.*` prefix, optional filter on `data`), in the **same transaction** as the fact.
  `flow.*` facts fire nothing and the facts generated by the firing itself are not re-evaluated (no loops). Over-limit events are dropped
  and noted as `flow.throttled`.
- **Revisions.** Every create/edit (and trigger change) keeps a revision that never changes: `hanko flow revisions <flow>`,
  `hanko flow restore <flow> <n>` (it becomes a new edit; triggers do not come back). On the flow page, the Revisions section.
- **Archive is not disable.** `hanko flow archive <flow>` takes the flow off the list and off every firing path (schedule, "Run now",
  webhook → 409, event); tasks and old firings stay; `hanko flow ls --archived` and `hanko flow unarchive` (it comes back off).
- **Export/import** carry the variables and the archived state; **triggers and secrets do not travel** (they belong to the installation).

## External channels: Telegram and GitHub (ADR-0046)

Settings › Channels (or `hanko channel …`). Two drivers: **Telegram** (chat with the persona and decide gates) and **GitHub** (a comment on a
PR/issue wakes the persona, which returns its summary and can publish a review).

1. **Secrets.** In Secrets, create the token (a BotFather bot or a GitHub token) and a secret that authenticates the entry. The channel stores only the `${secret:NAME}` references.
2. **Register.** `hanko channel add team-bot --kind telegram -p ada --token '${secret:tg-token}' --webhook-secret '${secret:tg-hook}' --enable`.
   GitHub: `--kind github --repo acme/api --repo 'kubo/*' --mention @hanko` (a persona is required to turn it on).
3. **Connect.** `hanko channel connect team-bot` registers the webhook with Telegram (`setWebhook` with the `secret_token`; it needs an https
   `HANKO_PUBLIC_URL`). On GitHub, configure it on the repository (Settings › Webhooks): URL `<HANKO_PUBLIC_URL>/hooks/channels/<identifier>`,
   content type `application/json`, the channel secret and the events *Issue comments*, *Pull request review comments* and *Pull request reviews*.
4. **Link the person.** Each person generates their own code (`hanko channel link team-bot` or the button on the channel page; valid for 15 min)
   and proves it in the channel: Telegram `/vincular XXXX-XXXX`; GitHub, a comment `/hanko vincular XXXX-XXXX`. **Without a link the channel does nothing.**
   A link can be revoked at any time (`/desvincular`, `hanko channel unlink`, or an admin on the page).
5. **Telegram.** New gates arrive with Approve/Reject buttons for whoever can decide them (`approval`, `task_review`, `tool`, `memory`; the other
   kinds show the summary and the link). The click runs `DecideGate` **as the linked person**, with the usual role and audience. Commands:
   `/pendentes`, `/novo`, `/desvincular`, `/ajuda`. Text and files (up to the attachment quota) go to the direct conversation with the channel's persona, and its reply comes back to the chat.
6. **Quick replies.** In Settings › Decision model, turn on the *Quick reply* feature: before waking the persona, the decision model answers simple
   questions ("how many open tasks?") from a short snapshot of the company, or escalates to the persona. It never decides a gate.
7. **GitHub.** A comment that mentions the channel's handle (or starts with `/hanko`), from a **linked** person, in a listed repository, creates a task
   for the persona (the next comment on the same PR becomes a comment on it); the persona's summary is published on the PR.
   `hanko channel review github --task ACM-7 --event request_changes --body '…'` (or the MCP tool `hanko_publish_review`, for the persona)
   publishes a review; **APPROVE only by a human admin**.
8. **Delivery state.** `hanko channel messages <channel>` and the page show each message (`queued → delivered | failed`, attempts, error). Delivery is
   at-least-once, with 6 attempts and backoff; `hanko channel resend` requeues a failed one.

Audit: facts `channel.created|updated|enabled|disabled|deleted|connected`, `channel.identity_requested|linked|revoked`,
`channel.message_received|sent|failed|requeued`, `channel.reply_fast`, `channel.review_requested`. Metrics `hanko.channel.inbound`,
`hanko.channel.outbound` and `hanko.flow.fires`. New dependency: none. Driver destinations go through `netguard` (outbound traffic must reach
`api.telegram.org`/`api.github.com` directly).

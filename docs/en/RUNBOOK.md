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

**Create the owner through the CLI before exposing the server.** The web form "Create owner account" (first come, first owner) only exists when
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
| `/entrar` shows "Criar conta de dono" (Create owner account) | database has no users | `hanko auth create-owner` (§5.1) before exposing it; on a public URL the form only exists with `HANKO_AUTH_WEB_BOOTSTRAP=true` |
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

## Skills and integrations (ADR-0029)

Operator settings (a company cannot turn these on by itself):

| Variable | Default | Effect |
|---|---|---|
| `HANKO_SECRET_KEY` | empty | Key (32+ characters) that encrypts integration secrets (AES-256-GCM). Empty: Hanko starts but refuses to store integration secrets. |
| `HANKO_SECRET_KEY_PREVIOUS` | empty | The previous key, only during rotation. |
| `HANKO_INTEGRATIONS_ALLOW_STDIO` | `false` | Allows `stdio` MCP servers, which run a command **on the server machine**. |
| `HANKO_INTEGRATIONS_ALLOW_PRIVATE` | `false` | Allows webhooks and connection tests to loopback, link-local and private networks (otherwise SSRF blocking applies). |
| `HANKO_SKILL_SOURCES` | `false` | Turns on git skill sources (ADR-0035): the server clones public repositories over https with the `git` on the PATH. Internal hosts only with `HANKO_INTEGRATIONS_ALLOW_PRIVATE`. |

**Generate the key:** `openssl rand -hex 32`. Keep it with `HANKO_SECRET` and in the operator's backup; losing it loses the
stored secrets (integrations remain, without credentials; enter them again).

**Rotate the key:** set the new key in `HANKO_SECRET_KEY` and the old one in `HANKO_SECRET_KEY_PREVIOUS`, restart, run
`hanko integration rekey` (or `POST /api/v1/integrations/rekey-secrets`; installation owner only), then remove
`HANKO_SECRET_KEY_PREVIOUS` and restart.

**Webhooks.** Each delivery carries `X-Hanko-Signature: t=<unix>,v1=<hex>`, `X-Hanko-Event` and `X-Hanko-Delivery`. The receiver
computes `HMAC-SHA256(secret, "<t>.<body>")`, compares in constant time and rejects an old `t`. Failures retry up to 7
attempts (30 s, 2 min, 8 min, 32 min, ~2 h, 6 h). Delivery is at-least-once; deduplicate on `X-Hanko-Delivery`.
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

**Artifacts** (ADR-0028) are files with immutable versions: the same name on the same task creates the next version; publishing
the same content again does not create one. The `/artefatos` screen (filters by project, task, persona, type and name; upload;
a detail page with preview, versions and a text diff) and sections on the task, run and project pages. Roles: viewer reads, member
publishes, admin deletes (the whole chain, with no trash).

| Variable | Default | Effect |
|---|---|---|
| `HANKO_ARTIFACT_MAX_BYTES` | 5 MiB (1 to 16 MiB) | Maximum size of one file. |
| `HANKO_ARTIFACT_QUOTA_BYTES` | 512 MiB | Company quota, summing every version. |

- **Publishing:** the UI upload, `hanko artifact put <file|-> [--task HNK-1]` (also `list`, `get <id> [-o file] [--info]`,
  `diff <id> [--against <id>]`, `rm <id>`, `usage`), the `…/artifacts` API and the MCP tool `hanko_artifact_put` (text in `content`,
  binary in `content_base64`). A persona publishes only on its own task and run.
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

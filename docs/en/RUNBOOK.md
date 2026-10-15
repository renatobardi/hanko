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

**UI language.** The web UI speaks pt-BR (default) and English. The language comes from the `hanko_lang` cookie (set by the selector in the sidebar footer and on the sign-in screen, via `POST /idioma`) and, without it, from `Accept-Language` (`en*` is English, anything else pt-BR). User data, run logs and API errors are never translated; the CLI stays in pt-BR.

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
- Workspaces can only point inside `HANKO_WORKSPACES_ROOT` (default `$HOME/hanko-workspaces`), with an absolute, clean path.
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
| `path: must be inside the workspaces root` when creating a workspace | path outside `HANKO_WORKSPACES_ROOT` | use a path inside the root or change the root |
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
| Port 7420 in use | another instance | `HANKO_ADDR` |

Logs are JSON on stderr with `trace_id`/`span_id`: copy the `trace_id` and look it up in Tempo (Grafana) to see the full trace of the request/run.

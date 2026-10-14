# RUNBOOK — Hanko

Operação do dia a dia. Decisões em `docs/adr/` (0008 deploy, 0011 Postgres, 0013 OTel, 0015 memória).

## 1. Rodar local em 5 minutos

Pré-requisitos: Go 1.27, Docker com Compose v2.

```bash
cp .env.example .env
# edite .env: POSTGRES_PASSWORD, AI_MEMORY_IMAGE (digest fixado), AI_MEMORY_AUTH_TOKEN (= HANKO_MEMORY_TOKEN)
# ajuste HANKO_DATABASE_URL com a mesma senha

docker compose -f deploy/compose.yaml --env-file .env up -d postgres ai-memory otel-lgtm
make dev          # sobe o Hanko local em http://127.0.0.1:7420 (migrations rodam no boot)
```

- UI: <http://127.0.0.1:7420> · API/docs: `/docs` · métricas: `/metrics`
- Grafana (traces, logs, métricas): <http://127.0.0.1:3000> (LGTM; login inicial `admin`/`admin`)
- Tudo em container (inclui o Hanko): `make up`; logs: `make logs`; parar: `make down`.
- Sem ai-memory: `HANKO_MEMORY_PROVIDER=none`.
- Os CLIs `claude`/`codex` precisam estar no PATH da máquina onde o Hanko roda (ou em `HANKO_CLAUDE_BIN`/`HANKO_CODEX_BIN`).
  A imagem padrão (distroless) não os inclui; use `Dockerfile.agents` para rodar agentes dentro do container.

Testes: `export HANKO_TEST_DATABASE_URL=postgres://postgres:...@127.0.0.1:5432/hanko_test?sslmode=disable; make test`
(testes de integração fazem skip sem a variável).

## 2. Deploy em LXC (oute-server)

Dois containers: `hanko-stg` (branch `develop`) e `hanko-prd` (tag `v*`). Acesso via Tailscale (split-horizon, como no runbook do kura-dev).

1. No oute-server: `deploy/lxc/setup.sh hanko-stg` (cria o container Ubuntu 24.04 com Docker, restic e psql).
2. Copie a stack: `lxc file push -r deploy .env hanko-stg/opt/hanko/` e ajuste o dono (`chown -R hanko:hanko /opt/hanko`).
3. Preencha `/opt/hanko/.env`:
   - `HANKO_VERSION` = tag (prd) ou `develop` (stg); `POSTGRES_PASSWORD`; `HANKO_SECRET` (`openssl rand -hex 32`);
   - `AI_MEMORY_IMAGE` por digest; `AI_MEMORY_AUTH_TOKEN`; `HANKO_PUBLIC_URL=https://hanko.oute.pro` (prd);
   - `HANKO_DOMAIN`;
   - borda (prd): `HANKO_EDGE_USER` e `HANKO_EDGE_PASSWORD_HASH` (veja "Borda com basic_auth" abaixo). O compose recusa subir o profile `edge` sem eles.
4. Suba: `cd /opt/hanko && docker compose -f deploy/compose.yaml --env-file .env up -d` (prd com borda TLS: `--profile edge`).
5. Verifique: `curl -fsS http://127.0.0.1:7420/healthz` e `/readyz`; abra o Grafana por túnel (porta 3000 só em loopback).
6. Atualizar versão: ajuste `HANKO_VERSION`, `docker compose pull hanko && docker compose up -d hanko`. Migrations rodam no boot
   (`HANKO_MIGRATE_ON_BOOT=true`); para rodar à parte: `docker compose run --rm --entrypoint /hanko hanko migrate`.
7. Rollback: volte `HANKO_VERSION` e suba de novo. Migrations são append-only; rollback de schema = restore do backup.

Segredos nunca entram no git: `.env` fica só no container.

### Borda com basic_auth (até o M5)

Enquanto o modo `authenticated` não existe, o Hanko não autentica humanos: quem alcança a porta age como operador. Por isso o
`deploy/caddy/Caddyfile` exige `basic_auth` e remove o `Authorization` antes de repassar ao Hanko. Gere o hash bcrypt:

```bash
docker run --rm caddy:2 caddy hash-password --plaintext 'SENHA-FORTE'   # ou: caddy hash-password
```

No `.env`, `HANKO_EDGE_USER=operador` e `HANKO_EDGE_PASSWORD_HASH=...` com **cada `$` do hash escrito como `$$`** (o compose
interpola `$`). Troque a senha regerando o hash e executando `docker compose up -d caddy`.

### Endurecimento do modo trusted

- `HANKO_SECRET` com menos de 32 bytes é recusado no boot; vazio gera um por boot.
- O cabeçalho `Host` só vale para `localhost`, `127.0.0.1`, `[::1]`, o host de `HANKO_PUBLIC_URL` e `HANKO_ALLOWED_HOSTS`
  (csv); outros recebem `421`. Atrás de proxy/túnel com outro nome, acrescente-o em `HANKO_ALLOWED_HOSTS`.
- Agentes não herdam o ambiente do servidor: só `PATH, HOME, USER, LANG, LC_*, TERM, TMPDIR, TZ, SHELL, SSL_CERT_*`,
  proxies, prefixos `ANTHROPIC_ CLAUDE_ OPENAI_ CODEX_ OTEL_ NODE_ XDG_ GIT_ GH_` e o que `HANKO_AGENT_ENV_ALLOW` (csv, aceita
  prefixo com `*`) liberar. `HANKO_*` do servidor nunca passa.
- Saída dos agentes é varrida para segredos (run token, Bearer, JWT, chaves de API, senhas em URL de banco) antes de virar log de run.
- Run tokens só valem enquanto o run está `queued`/`running`.
- Memória: o agente fala com `<HANKO_PUBLIC_URL>/memory-mcp` usando o run token; o Hanko injeta `HANKO_MEMORY_TOKEN` e o escopo
  (empresa/workspace) do lado do servidor. O token do ai-memory nunca vai ao agente.
- Workspaces só podem apontar para dentro de `HANKO_WORKSPACES_ROOT` (padrão `$HOME/hanko-workspaces`), com caminho absoluto e limpo.
- Limites e recomendação de isolamento: `docs/adr/0017-modo-trusted-e-limites.md`.

## 3. Backup e restore

Backup diário (cron do usuário `hanko` no container): `pg_dump` + `restic` para o bucket R2.

```bash
# /opt/hanko/backup.sh  (exporte RESTIC_REPOSITORY=s3:https://<conta>.r2.cloudflarestorage.com/<bucket>,
# RESTIC_PASSWORD, AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY no ambiente do cron)
set -euo pipefail
cd /opt/hanko
docker compose -f deploy/compose.yaml exec -T postgres pg_dump -U "$POSTGRES_USER" -Fc "$POSTGRES_DB" > /tmp/hanko.dump
restic backup /tmp/hanko.dump --tag hanko-db
# ai-memory: volume com wiki em git + índice SQLite
docker run --rm -v hanko_aimemory:/data:ro -v /tmp:/out alpine tar czf /out/aimemory.tgz -C /data .
restic backup /tmp/aimemory.tgz --tag hanko-memory
rm -f /tmp/hanko.dump /tmp/aimemory.tgz
restic forget --tag hanko-db --tag hanko-memory --keep-daily 7 --keep-weekly 4 --keep-monthly 6 --prune
```

Cron: `17 3 * * * /opt/hanko/backup.sh >> /var/log/hanko-backup.log 2>&1`.

**Teste de restore (1x por mês, em `hanko-stg`):**

```bash
restic restore latest --tag hanko-db --target /tmp/restore
docker compose -f deploy/compose.yaml exec -T postgres createdb -U "$POSTGRES_USER" hanko_restore
docker compose -f deploy/compose.yaml exec -T postgres pg_restore -U "$POSTGRES_USER" -d hanko_restore --no-owner < /tmp/restore/tmp/hanko.dump
docker compose -f deploy/compose.yaml exec -T postgres psql -U "$POSTGRES_USER" -d hanko_restore -c "select count(*) from companies; select max(version_id) from goose_db_version;"
docker compose -f deploy/compose.yaml exec -T postgres dropdb -U "$POSTGRES_USER" hanko_restore
```

Registre data e resultado em `docs/SESSION.md` ou no issue de operação.

## 4. Troubleshooting

| Sintoma | Causa provável | O que fazer |
|---|---|---|
| Hanko não sobe: `HANKO_DATABASE_URL é obrigatório` | env/yaml sem URL | defina `HANKO_DATABASE_URL`; confira `HANKO_CONFIG` |
| `db: ping após 5 tentativa(s)` | Postgres fora/senha errada | `docker compose ps postgres`, healthcheck, senha no `.env` |
| Aviso `falha de exportação OpenTelemetry` | collector (LGTM) fora | **não derruba o app**: dados de telemetria ficam sem destino até voltar. `docker compose up -d otel-lgtm`; confira `OTEL_EXPORTER_OTLP_ENDPOINT`. `/metrics` segue funcionando (pull) |
| Aviso `ai-memory indisponível no boot` | ai-memory fora ou token errado | app **degrada**: runs abrem sem briefing, fatos ficam em `memory_outbox` e drenam quando voltar. `docker compose logs ai-memory`; confira `HANKO_MEMORY_URL`/`HANKO_MEMORY_TOKEN` |
| Aviso `HANKO_SECRET não definido` | secret gerado por boot | defina `HANKO_SECRET`; sem isso, run tokens de runs em andamento invalidam a cada restart |
| Runs ficam `queued` | heartbeat parado, `Workers` baixo ou CLI do agente ausente | log do hanko; `HANKO_WORKERS`; `claude`/`codex` no PATH (ou `Dockerfile.agents`) |
| `/mcp` responde 401 de fora | modo trusted só aceita sem token em loopback | normal: agentes usam run token; humanos usam a API/UI |
| `421 host não permitido` | cabeçalho `Host` fora da lista | acrescente o nome em `HANKO_ALLOWED_HOSTS` ou ajuste `HANKO_PUBLIC_URL` |
| `HANKO_SECRET tem N bytes` | secret curto | `openssl rand -hex 32` |
| `path: must be inside the workspaces root` ao criar workspace | caminho fora de `HANKO_WORKSPACES_ROOT` | use um caminho dentro da raiz ou mude a raiz |
| Agente não enxerga uma variável de ambiente | env filtrado (allowlist) | `HANKO_AGENT_ENV_ALLOW=NOME` |
| Porta 7420 ocupada | outra instância | `HANKO_ADDR` |

Logs são JSON no stderr com `trace_id`/`span_id`: copie o `trace_id` e busque no Tempo (Grafana) para ver o trace completo da requisição/run.

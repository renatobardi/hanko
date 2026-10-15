# Estado da última sessão
Data: 2026-10-14 (madrugada, ~02h) · Orquestração: Opus 5.5 · Execução: Sonnet 5.5 (13 trilhas) · Revisão independente: Fable 5.1 (3 rodadas)

## Entregue na `main`
| Milestone | O quê |
|---|---|
| M0 | Go 1.27, config `HANKO_*`/`hanko.yaml`, OpenTelemetry mandatório (OTLP + `/metrics`), pgx + otelpgx, migrations goose + river, wiring, Makefile, Dockerfiles, `deploy/compose.yaml` (hanko + pg17 + ai-memory + otel-lgtm + caddy), CI/release (GitHub Actions + goreleaser), RUNBOOK |
| M1 | domínio + schema (ADR-0016), store sqlc, service (tx por caso de uso, activity na mesma tx, broker pós-commit, idempotência por event_id, numeração e checkout atômicos), heartbeat river, adapters `claude`/`codex`/`fake`, API REST `/api/v1` + SSE + `api/openapi.json`, CLI completa |
| M2 | UI templ + htmx no Kubo DS (claro/escuro, 390 px), MCP `/mcp` (tools `hanko_*`), ai-memory ligado por padrão (briefing/handoff, `memory_outbox`, `finalize-session`, proxy `/memory-mcp`) |
| M3 | Gates (aprovação humana, `require_review`, decisão vira memória), Organograma (SVG, delegação pela árvore, sem ciclos), Caixa de entrada, Decisões |
| M4 | Custos (ledger, reportado × estimado por tabela de preços, gráfico diário), orçamentos com hard stop + reserva por run em voo, pausa/retomada automática, Fluxos (cron + fuso, "Rodar agora", catch-up, sem acúmulo) |
| M5 (parcial) | `HANKO_MODE=authenticated`: login local (argon2id) e Firebase (Google + e-mail/senha, ID token validado via JWKS sem Admin SDK), sessões, API keys `hk_…`, tela Configurações, `hanko auth …` |

Revisões independentes acharam 37 problemas reais (vários críticos: token não validado na API, segredos do servidor herdados pelo agente, estouro de orçamento ilimitado, laço de runs, wake perdido, gate em task alheia, primeiro visitante virando dono…). Todos corrigidos, cada um com teste. ADRs novas: 0017 (limites do modo trusted) e 0018 (endurecimento do authenticated).

**Testes:** suíte completa verde (unitários + integração com Postgres real e river real, `-p 1`). E2E com o binário real numa base nova (migrations 1–7): empresa → personas com organograma → task → execução → todas as 12 telas respondem 200.

## Não verificado aqui (rodar no Mac)
- `docker compose up`, imagens, Caddy, GitHub Actions, goreleaser, golangci-lint.
- `claude`/`codex` reais (testado com scripts que imitam a saída) e ai-memory real (servidor falso). Fixar digest de `AI_MEMORY_IMAGE`.
- Login real com Google/Firebase (validado com JWKS falso).
- `go mod tidy`/`go mod verify` contra o proxy oficial (aqui foi espelho local montado do GitHub).

## Teste em 5 minutos (no Mac) — modo authenticated (padrão)
```bash
cd ~/Projects/Github/Hanko
go mod download && go build -o bin/hanko ./cmd/hanko && go test ./...
docker run -d --name hanko-pg -e POSTGRES_HOST_AUTH_METHOD=trust -p 127.0.0.1:5432:5432 postgres:17
export HANKO_DATABASE_URL="postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable" HANKO_SECRET=$(openssl rand -hex 32)
HANKO_OWNER_PASSWORD='troque-esta-senha' ./bin/hanko auth create-owner --email renatobardicabral@gmail.com --name "Renato Bardi"
export HANKO_TOKEN=$(./bin/hanko auth token create --name mac | grep -oE 'hk_[A-Za-z0-9_-]+')
./bin/hanko serve &            # http://127.0.0.1:7420 → /entrar com o e-mail e a senha acima
./bin/hanko company create "Oute Lab" && export HANKO_COMPANY=oute-lab
./bin/hanko persona hire Ada --agent claude --title CTO
./bin/hanko task create "Diga olá e descreva o repositório" -p ada
./bin/hanko run logs -f $(./bin/hanko run list --json | jq -r '.[0].id')
```
(Login com Google/Firebase: `HANKO_AUTH_PROVIDER=firebase` + variáveis `HANKO_FIREBASE_*` do projeto `oute-sso` — ver RUNBOOK §5.)

## Decisões pendentes do dono
1. ~~ADR-0017~~ decidido: `authenticated` sempre (padrão da config).
2. **Firebase** (projeto `oute-sso` criado): falta a `apiKey` do app Web; criar o projeto, habilitar Google + e-mail/senha, domínios autorizados e definir `HANKO_AUTH_OWNER_EMAIL` ou listas de admissão (RUNBOOK §5).
3. ~~UI~~ decidido: CSS próprio (adendo na ADR-0005).
4. **Gates do processo:** gravar as demos de M1–M4 e criar as tags `v0.1.0-mN`.
5. ~~Autoria~~ decidido: commits no nome do Bardi.

## Próximo
Restante do M5: deploy LXC `hanko-stg`/`hanko-prd` (RUNBOOK §3), versão en, `v0.1.0`.

## T6 (alertas, nome de run, revisor)
- Feito: alertas operacionais (job `alert_tick`, Caixa e `/metrics`), nome amigável da run (`hanko run logs <nome>`), revisão automática por outro modelo antes do gate. Migration 00009.
- Bloqueado: nada. Fora do escopo: formulário na UI para configurar o revisor (só API/CLI).
- Próximo: revisar o merge com a 00008 (`runs_trigger_check` redefinido na 00009).

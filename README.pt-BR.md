[English](README.md) | **Português**

# Hanko (判子)

Control plane open-source para orquestrar times de agentes de IA: tasks, org chart, heartbeat, governança (aprovação), budget.
Single binary em Go. Postgres como única dependência de dados. Observabilidade OpenTelemetry completa, sempre ligada, apontando para o **seu** collector — nunca phone-home.

- Stack e convenções: `docs/STACK.md`
- Decisões (ADR): `docs/adr/`
- Roadmap v0.1: `docs/ROADMAP.md`
- Demos por milestone (roteiros de gravação e empresa de exemplo `examples/demo-company.json`): `docs/DEMOS.md`
- Protocolo de início de sessão (humanos e agentes): `AGENTS.md`
- Licença: Apache-2.0 (ver `LICENSE`, `CONTRIBUTING.md`)

URL: https://hanko.oute.pro

## Rodando

Requisitos: Go 1.27 e Docker (Compose v2).

```bash
cp .env.example .env                      # ajuste senhas e AI_MEMORY_IMAGE
docker compose -f deploy/compose.yaml --env-file .env up -d postgres ai-memory otel-lgtm
export HANKO_SECRET=$(openssl rand -hex 32)  # o modo padrão é authenticated: exige segredo e um dono
go run ./cmd/hanko auth create-owner --email voce@exemplo.com --name "Seu nome"   # senha em HANKO_OWNER_PASSWORD ou na stdin
make dev                                  # http://127.0.0.1:7420 → /entrar
```
Passo a passo completo (personas, tasks, runs): `docs/SESSION.md`, seção "Teste em 5 minutos".

Outros alvos: `make build`, `make test`, `make lint`, `make up` (stack completa em container).
Configuração: `hanko.example.yaml` e variáveis `HANKO_*` (ver `.env.example`); OpenTelemetry sempre ligado, exportando para `OTEL_EXPORTER_OTLP_ENDPOINT` (default `localhost:4317`).
Deploy, backup/restore e troubleshooting: `docs/RUNBOOK.md`.

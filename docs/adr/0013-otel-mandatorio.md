# ADR-0013 — Observabilidade OpenTelemetry completa e mandatória
Data: 2026-10-13 · Status: aceita (substitui "telemetria opt-in")

## Contexto
Produto que roda agentes autônomos gastando dinheiro precisa de trace por run desde o dia 1. "Opt-in" vira "ninguém liga".

## Decisão
Instrumentação OTel **sempre ligada**, não configurável para desligar: `otelhttp` (server), `otelpgx` (DB), middleware OTel no river (span por job), span por run nos adapters com atributos `company_id`/`agent_id`/`run_id`, `otelslog` bridge (logs com trace_id), métricas OTel + `/metrics` Prometheus. Exporter OTLP gRPC, default `localhost:4317`. Compose de staging/prod inclui `grafana/otel-lgtm` (1 container). Collector ausente = SDK descarta em silêncio; app nunca falha por OTel.

Distinção explícita: **observabilidade** (OTel → collector do operador) ≠ **telemetria de produto** (phone-home). A segunda não existe no Hanko.

## Consequências
Overhead pequeno e constante. Operador precisa de um backend OTel para ver algo (LGTM vem no compose). Sem Sentry/PostHog.

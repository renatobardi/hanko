# ADR-0012 — Fila e scheduler: river sobre Postgres
Data: 2026-10-13 · Status: aceita

## Decisão
`river` (riverqueue) para wakeup queue, retries, periodic jobs (routines) e unique jobs (coalescing de heartbeat). Sem Redis.

## Consequências
Fila e domínio na mesma transação pgx. Evita escrever fila própria sobre Postgres.

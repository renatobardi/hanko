# ADR-0011 — PostgreSQL 17 é a única dependência; SurrealDB rejeitado
Data: 2026-10-13 · Status: aceita

## Contexto
SurrealDB foi considerado. Rejeitado: (1) mata river, sqlc, goose, SKIP LOCKED, testcontainers, embedded-postgres; (2) enterprise regulado on-prem opera Postgres, não Surreal; (3) SDK Go é segunda classe, breaking changes entre majors, sem embedded pra Go; (4) org chart é pequeno — CTE recursiva resolve.

## Decisão
PostgreSQL 17. Sem camada de abstração "pra trocar depois".

## Consequências
Infra chata por design. Novidade vai no domínio.

## Revisão 2026-10-13 (ADR-0015)
Postgres é a única dependência **obrigatória**. ai-memory vem ligado por padrão como dependência **degradável**: indisponível, o Hanko funciona e a outbox drena depois.

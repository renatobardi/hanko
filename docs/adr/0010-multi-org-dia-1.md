# ADR-0010 — Multi-org desde M1 via `company_id`
Data: 2026-10-13 · Status: aceita

## Decisão
`company_id` em toda tabela de domínio desde a 1ª migration. Troca de org na UI só v0.2. Sem RLS e sem schema-per-tenant no v0.1.

## Consequências
Custo baixo agora; evita retrofit caro. Toda query sqlc recebe company_id.

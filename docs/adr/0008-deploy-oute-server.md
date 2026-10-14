# ADR-0008 — Deploy em LXC no oute-server
Data: 2026-10-13 · Status: aceita

## Decisão
Containers `hanko-stg` (develop) e `hanko-prd` (tag v*) no oute-server (OCI/LXD). Compose: hanko + postgres:17 + caddy. Backup pg_dump + restic → R2; restore mensal. VPS externo só com usuário externo.

## Consequências
Custo zero. Runbook do kura-dev reaproveitado. Disponibilidade = a do oute-server.

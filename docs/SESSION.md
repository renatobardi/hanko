# Backup do prd sem dump do banco · ADR-0093
Data: 2026-11-10

## Feito
- `backup.sh`/`restore.sh` com banco externo: só memória e artefatos; `--pgdump` recusado; sem cliente do Postgres (`pglib.sh` só decide o modo). `clear-schema.sql` removido; o restore interno (`hanko backup`) segue escopado ao schema.
- Correção: `backup.sh` sem nenhum arquivo não chama mais `sha256sum` sem argumentos (lia o stdin).
- RUNBOOK §3.1 e ADR-0093.

## Travado
- Nada.

## Próximo passo
- Deploy; no LXC apagar os `hanko-*.pgdump`/`pre-restore-*` antigos de `/var/backups/hanko` e a imagem `postgres:17`.

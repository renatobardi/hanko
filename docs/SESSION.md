# Estado da última sessão (documentos de artefato com versão MAJOR.MINOR, ADR-0064)
Data: 2026-10-22 · branch `claude/jolly-hamilton-wtnkxr` → `main` (depois do #40, compositores de chat)

## Feito
- ADR-0064 + migration 00201: documento de artefato com id estável, versão MAJOR.MINOR (persona declara, servidor mede o diff), restaurar, reclassificar, tags; apagar task preserva versões.
- Persona lê e altera artefatos: MCP `hanko_artifact_list|get|restore`, `artifact=` no put, seção "Artefatos" no briefing (task, chat, sala). API, CLI, UI, export/import, e2e e docs.
## Travado (só o dono)
- Deploy no hanko-prd exige `ssh oute-server` do Mac (com o #39 e o #40 também pendentes de deploy); tags v0.3.0–v0.6.0.
## Próximo passo
- `deploy/oute-server/deploy-prd.sh` na `main` (aplica a migration 00201); conferir `systemctl list-timers hanko-backup.timer`; no chat em produção, pedir a uma persona uma alteração num documento e ver a versão nova (não documento novo).

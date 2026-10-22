# Estado da última sessão (documentos de artefato com versão MAJOR.MINOR, ADR-0064)
Data: 2026-10-22 · branch `claude/jolly-hamilton-wtnkxr`

## Feito
- ADR-0064 + migration 00201: documento de artefato com id estável, versão MAJOR.MINOR (persona declara, servidor mede o diff), restaurar, reclassificar, tags; apagar task preserva versões.
- Persona lê e altera artefatos: MCP `hanko_artifact_list|get|restore`, `artifact=` no put, seção "Artefatos" no briefing (task, chat, sala). API, CLI, UI, export/import e e2e atualizados.
## Travado
- Nada no código. Deploy no hanko-prd depende do acesso ao oute-server (ver `docs/VALIDACAO-MANHA.md`).
## Próximo passo
- Dogfood: pedir a uma persona uma alteração num documento existente e conferir que vira versão nova (não documento novo); ajustar os limiares 0,10/0,40 se a disputa aparecer demais.

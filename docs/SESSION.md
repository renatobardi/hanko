# Estado da última sessão (lote v0.2 fechado)
Data: 2026-10-15 · main em `b62fd94` (migrations até 00025)

## Feito
- Oito trilhas: ACP, ilhas, busca, projetos/workspaces, chat, memória governada, artefatos/metas, skills/integrações (ADR-0023 a 0030).
- Quatro revisões independentes; todos os achados corrigidos (migrations 00023 a 00025).
- Integração completa verde em `b62fd94`.
## Pendente
- Deploy no prd (ainda não feito): janela calma, pois a 00015 reescreve 5 tabelas sob ACCESS EXCLUSIVE (~25 s no volume sintético) e a 00024 recria colunas `search_tsv`.
- Variáveis novas no prd: `HANKO_SECRET_KEY`, `HANKO_WORKSPACES_ROOT`, `HANKO_ACP_ALLOW`/`HANKO_ACP_COMMANDS`, `HANKO_INTEGRATIONS_ALLOW_STDIO` (opcional).
- Catálogo real de tools do ai-memory (`memory_search` é suposição; diff exige `HANKO_MEMORY_READ_TOOL`).
- Tag e demos ficam com o dono.

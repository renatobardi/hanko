# Estado da última sessão (lote v0.2 no prd)
Data: 2026-10-15 · prd em `b4b04c8` (migrations até 00025)

## Feito
- Oito trilhas (ACP, ilhas, busca, projetos/workspaces, chat, memória governada, artefatos/metas, skills/integrações; ADR-0023 a 0030), quatro revisões independentes com todos os achados corrigidos.
- Deploy no prd feito e validado: `HANKO_SECRET_KEY`, `HANKO_ACP_ALLOW`, `HANKO_INTEGRATIONS_ALLOW_STDIO` no `.env`; UI saudável; snapshots do LXC opt-in.
- Busca de memória na tool real `memory_query`, formato da resposta confirmado com dados do prd.
- Diff da curadoria: `HANKO_MEMORY_READ_TOOL` com padrão `memory_read_page` (tool real do ai-memory), sem ajuste manual no prd.
- Persona `claude` atua no prd: `--allowedTools mcp__hanko`, `HANKO_AGENT_URL` (loopback), comentário reabre `in_review`/`blocked`; diagnóstico de MCP e tools negadas nos eventos.
- CI em Actions Node 24 e runners `ubuntu-24.04`.
## Pendente
- `HANKO_ACP_COMMANDS` quando o binário do agente ACP for escolhido.
- Guardar `HANKO_SECRET_KEY` no Vaultwarden (dono).
- Migrar runners para `ubuntu-26.04` antes de 19/11/2026 (testar em ramo).
- Tag `v0.1.0`/`v0.2.0` e demos ficam com o dono.

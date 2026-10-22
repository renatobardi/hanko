# Estado da última sessão (card social + personas claude com integrações MCP, ADR-0059)
Data: 2026-10-22 · PR #26 (`claude/dreamy-johnson-btv0kl`) → `main`

## Feito
- Card de prévia social: `docs/brand/social-card.html` e `scripts/gen-social-card.mjs` (5 PNG, EN/PT, claro/escuro, guias). Publicar é manual (Settings → Social preview).
- Deploy do `main` (32cdc08) no prd com `HANKO_GIT_WORKSPACES=true`; adapter claude libera os servidores do gateway de tools em `--allowedTools` (`Spec.GatewayMCP`, ADR-0059).
## Travado
- `adapter_config` de persona não é editável depois da contratação (nem API nem UI).
## Próximo passo
- Deploy da correção do ADR-0059; criar a Conexão OAuth do GitHub, ligar as tools de leitura do Delivery Manager e apagar a política provisória de `WebFetch`; subir `docs/brand/social-card.png`.

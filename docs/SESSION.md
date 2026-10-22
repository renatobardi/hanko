# Estado da última sessão (personas claude usando integrações MCP, ADR-0059)
Data: 2026-10-22 · ramo `claude/eager-brahmagupta-4dj6tw` → `main`

## Feito
- Deploy do `main` (32cdc08) no prd com `HANKO_GIT_WORKSPACES=true`; workspace `kubo` com Git ligado.
- Adapter claude libera em `--allowedTools` os servidores do gateway de tools (`Spec.GatewayMCP`); antes toda tool de integração era negada no headless. ADR-0059.
## Travado
- `adapter_config` de persona não é editável depois da contratação (nem API nem UI).
## Próximo passo
- Deploy desta correção; criar a Conexão OAuth do GitHub, ligar as tools de leitura para o Delivery Manager e apagar a política provisória de `WebFetch`.

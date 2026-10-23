# Estado da última sessão (integração MCP nova nasce fechada para escrita, ADR-0065)
Data: 2026-10-23 · ramo `claude/eager-brahmagupta-4dj6tw` → `main`

## Feito
- ADR-0065: servidor MCP novo não vem mais para todas as personas; integração criada agora grava catálogo pendente e a primeira descoberta põe escrita/destrutiva/sem risco em quarentena (antigas mantêm a linha de base do ADR-0036); regra por risco exige escolha explícita.
- Em produção: GitHub ligado ao Delivery Manager por token fine-grained + perfil "leitura livre, escrita pede aprovação"; deploy com #39 feito.
## Travado (só o dono)
- Deploy da `main` com o ADR-0064 (migration 00201) e este ADR; tags v0.3.0–v0.6.0.
## Próximo passo
- Deploy; cadastrar um servidor MCP de teste e ver as tools de escrita em quarentena e na Caixa. Assistente "conectar ferramenta a uma persona" segue como issue.

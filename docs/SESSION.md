# Estado da última sessão (integração MCP nova nasce fechada para escrita, ADR-0067)
Data: 2026-10-23 · ramo `claude/eager-brahmagupta-4dj6tw` → `main`

## Feito
- ADR-0067: servidor MCP novo não vem mais para todas as personas; integração criada agora grava catálogo pendente e a primeira descoberta põe escrita/destrutiva/sem risco em quarentena (antigas mantêm a linha de base do ADR-0036); regra por risco exige escolha explícita.
- Em produção: GitHub ligado ao Delivery Manager por token fine-grained + perfil "leitura livre, escrita pede aprovação".
## Travado (só o dono)
- Deploy pelo Mac (`deploy/oute-server/deploy-prd.sh`, na `main` atualizada); tags v0.3.0–v0.6.0; SonarCloud do projeto falhando em todo PR.
## Próximo passo
- Deploy; cadastrar um servidor MCP de teste e ver as tools de escrita em quarentena e na Caixa. Assistente "conectar ferramenta a uma persona" segue na issue #45.

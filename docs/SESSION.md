# Estado da última sessão (integração MCP nova nasce fechada para escrita, ADR-0067)
Data: 2026-10-23 · ramo `claude/eager-brahmagupta-4dj6tw` → `main` (depois do #43 e #44, anexos)

## Feito
- ADR-0067: servidor MCP novo não vem mais para todas as personas; integração criada agora grava catálogo pendente e a primeira descoberta põe escrita/destrutiva/sem risco em quarentena (antigas mantêm a linha de base do ADR-0036); regra por risco exige escolha explícita.
- Em produção: GitHub ligado ao Delivery Manager por token fine-grained + perfil "leitura livre, escrita pede aprovação".
## Travado (só o dono)
- Deploy com `--edge` (nginx novo dos anexos, #43/#44): `deploy/oute-server/deploy-prd.sh --edge`. Tags v0.3.0–v0.6.0; SonarCloud falhando em todo PR.
## Próximo passo
- Deploy; cadastrar um servidor MCP de teste e ver as tools de escrita em quarentena e na Caixa; anexar uma imagem num chat e pedir à persona para descrevê-la. Assistente "conectar ferramenta a uma persona" segue na issue #45.

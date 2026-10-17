# Estado da última sessão (paridade v0.3)
Data: 2026-10-17 · PR único `feat/paridade-v03` → `main` · prd em `e22999b` (migrations até 00026)

## Feito
- 7 itens do v0.3: cards de status, sala, pipelines/cases/revisão/aprendizados, skills (versões, fontes, estúdio), tools (catálogo, perfis, gateways), ambientes SSH/sandbox, plugins. ADR-0032 a 0038; migrations 00027–00034.
- CI local completo verde (testes de integração e lint) na branch combinada.
## Travado
- Nada. Interruptores do operador desligados por padrão (`HANKO_SKILL_SOURCES`, `HANKO_ENV_SSH_ALLOW_HOSTS`, `HANKO_ENV_SANDBOX`, `HANKO_PLUGINS_DIR`).
## Próximo passo
- Deploy do `main` (`deploy/oute-server/deploy-prd.sh`; aplica 00027–00034) e conferência das telas novas.
- Teste `internal/deploy` usa bancos fixos (`hanko_rt*`): não rodar em paralelo.

# Assistente Conectar (#45) · sessão `swarm-1031-1825`
Data: 2026-11-01

## Feito
- Modelos de acesso (migration 00217), service, API (rotas do assistente e dos modelos), CLI `hanko integration connect` e tela `/c/{empresa}/conectar`, conforme ADR-0089.
- Testes com Postgres local verdes: service (connect e modelos), api, cli (`IntegrationConnect`), ui (conectar), docsite, i18n. CI do PR #176 verde e gate do SonarCloud OK no head anterior.
- OpenAPI regenerado (`make openapi`): só 11 operações novas; `sqlc` sem drift. Merge com origin/main feito de novo (2026-11-01): a main trouxe alertas v0.8 e um script de diagnóstico, sem geradores afetados.

## Travado
- Retorno do OAuth para o assistente (ADR-0089, "não decidido"): hoje volta à aba de conexões.
- CRUD de modelos de acesso na UI: só na API.
- Sem ensaio com servidor MCP real e persona lendo issues (critério `qa`).

## Próximo passo
- Decidir o retorno do OAuth e a tela de modelos; depois o ensaio `qa` com persona real.

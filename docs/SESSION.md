# Assistente Conectar (#45) · sessão `swarm-1031-1825`
Data: 2026-11-01

## Feito
- Modelos de acesso (migration 00217), service, API, CLI `hanko integration connect`, tela `/c/{empresa}/conectar`, conforme ADR-0089. OpenAPI regenerado: só 11 operações novas.
- Merge com origin/main (48fd2c9) feito; conflito só em `docs/SESSION.md`, resolvido sobrescrevendo. Geradores (openapi, sqlc, templ) sem drift.
- Teste `TestExternalPaidFollowsMonthlyHistory` com relógio ao meio-dia UTC (mesmo ajuste do PR #192, branch `fix/184-teste-virada-mes`). O defeito de código continua: #191.

## Travado
- CI do #176 no teste de custo depende do merge do PR #192 (`Refs #184`), pedido do Bardi.
- #191: peso de chamada externa em dia UTC × rateio em dia local. Falta decidir qual dia vale.
- Retorno do OAuth para o assistente (ADR-0089, "não decidido"); CRUD de modelos na UI; ensaio `qa` com persona real.

## Próximo passo
- Decidir o dia de negócio do rateio (#191). Depois, se o Bardi pedir, merge do #192 e rebase do #176 sobre a main.

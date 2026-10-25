# Fase 3 (ADRs) do decom do agent-studio · sessão `hanko-decom-agent-studio` (#66)
Data: 2026-10-25

## Feito
- ADR-0080 a ADR-0086 aceitos pelo Bardi (PRs #106, #107, #109, #111, #112, #113 e #114); com eles, a lista de ADRs da #66 fecha.
- Banco e retenção não têm ADR novo: o gate da #68 decidiu só Postgres, tudo quente e sem prazo (`docs/research/2026-10-banco-telemetria.md` §10).
- Alerta de disco do Postgres registrado na #66 como item obrigatório do contrato da v0.8 (comentário 6402851982).

## Travado
- Contrato e código da v0.8 esperam o fechamento da v0.7. A #81 (tokens do Codex) segue aberta.

## Próximo passo
- Com a v0.7 fechada: contrato da v0.8 (migrations + tipos de domínio + OpenAPI) a partir dos ADR-0075 a 0086, com gate do Bardi.

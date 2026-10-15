# ADR-0019 — Assinaturas, quatro custos e trava semanal
Data: 2026-10-15 · Status: aceita

## Contexto
Até o M4 o Hanko só sabia o custo **reportado** pelo harness (`cost_usd`) ou **estimado** por tabela de preços.
Quem usa planos de assinatura (mensalidade fixa por harness) não enxergava quanto do plano cada run consumiu,
nem tinha como impedir que uma semana de uso pesado esgotasse a cota do plano antes do fim do ciclo.

## Decisão
1. **Preço append-only.** `model_prices` só recebe INSERT (trigger recusa UPDATE/DELETE). O custo de lista de um
   run é calculado uma vez, com o preço vigente no fim do run, e gravado em `runs.list_cost_usd`. Preço novo vale
   dali em diante; nada é recalculado.
2. **Quatro custos** por run e por dia:
   - **real** — `cost_usd` reportado pelo harness;
   - **lista** — tokens × preço de tabela vigente;
   - **estimado** — o que o Hanko usa quando não há real (é o de lista);
   - **pago** — a mensalidade da assinatura rateada: mensalidade ÷ dias do mês, distribuída entre os runs do dia
     proporcionalmente ao custo de lista de cada um. Dia sem uso aparece como **sem uso** (`idle_usd`), de modo que
     pago + ocioso fecha a mensalidade de um mês completo. Mês elegível: do mês de criação ao de desativação.
3. **Assinatura é entidade** (`subscriptions`): harness, mensalidade, prefixos de modelo cobertos, teto semanal opcional
   e `weekly_guard_pct`. A persona aponta opcionalmente para uma; o run fixa a assinatura que o cobre no checkout.
4. **Trava semanal.** Uso estimado da semana (segunda a segunda, no fuso da empresa) ≥ `weekly_guard_pct` do teto →
   novos runs daquela assinatura são recusados com o mesmo mecanismo do hard stop de orçamento
   (`ErrSubscriptionGuard`, transação confirmada, persona não é pausada) e o primeiro bloqueio da semana vira item
   na Caixa de entrada (`subscription.guard_blocked`).
5. **Origem do modelo** gravada no run (`model_source`): task (manual) > persona > padrão do harness.

## Consequências
- O pago histórico usa a mensalidade atual da assinatura (não há histórico de mensalidade).
- A trava conta runs finalizados; runs em voo não reservam cota, então a semana pode passar um pouco do teto.
- Inserção de preço novo continua por migration/seed (sem API) até haver conferência automática de preços.

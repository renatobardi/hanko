# Teste do pago no dia 1 · sessão `hanko-184-teste-pago-dia-1`
Data: 2026-11-01

## Feito
- #184 reproduzido com o relógio real em 1º/nov (pago = 0, esperado 600/31) e a causa achada: o teste datava a assinatura (criada "hoje") depois da chamada ("ontem"); fora de `subscription_periods` não há cobrança (ADR-0082 item 6), então no dia 1 o mês da chamada não accrua. O cálculo segue o ADR; o defeito era só do teste.
- `TestExternalPaidFollowsMonthlyHistory` reescrito com relógio fixo (`svc.Now`): assinatura nasce antes da chamada, dois casos — leitura no dia 1 do mês seguinte e no meio do mês. PR #188 aberto, checks verdes; branch atualizado com a `origin/main` (#182) a pedido do dispatcher.

## Travado
- Nada.

## Próximo passo
- Bardi revisa/mergeia o PR #188; release e deploy da v0.8.3 ficam com ele (fora de escopo).

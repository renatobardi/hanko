# Teste do pago no dia 1 · sessão `hanko-184-teste-pago-dia-1`
Data: 2026-11-01

## Feito
- #184 reproduzido com o relógio real em 1º/nov (pago = 0, esperado 600/31) e a causa achada: o teste datava a assinatura (criada "hoje") depois da chamada ("ontem"); fora de `subscription_periods` não há cobrança (ADR-0082 item 6), então no dia 1 o mês da chamada não accrua. O cálculo segue o ADR; o defeito era só do teste.
- `TestExternalPaidFollowsMonthlyHistory` reescrito com relógio fixo (`svc.Now`): assinatura nasce antes da chamada, dois casos — leitura no dia 1 do mês seguinte e no meio do mês. PR #188 aberto; branch acompanhando a `origin/main` (#182, #189 e #176 — o conflito do #176 manteve o relógio fixo, a leitura ao meio-dia UTC e a nota da janela 21h–24h locais, que é o defeito aberto do #191).

## Travado
- Nada.

## Próximo passo
- Bardi decide entre o PR #188 (relógio fixo, dois casos) e o PR #192 (meio-dia UTC sobre o teste antigo) para o #184; o não escolhido deve ser fechado. Release e deploy da v0.8.3 ficam com ele.

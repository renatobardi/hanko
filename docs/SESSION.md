# Aviso de alcance na aprovação do gate · sessão `95-aviso-gate` (issue #95)
Data: 2026-10-25 · rodada swarm-1024-0625 · fase `build`

## Feito
- Aprovar um gate `hire` com ficha devolve os avisos de alcance da persona criada: `GateView.HirePersonaID` (service) → campo `warnings` na resposta do decide (API) → `hanko gate approve` imprime `aviso: …` (ADR-0068 §2.5). Nunca o valor.
- Testes: integração da API (aviso presente, valor não vaza, sem pendência e rejeição sem aviso), service (`HirePersonaID` = persona criada) e CLI (imprime e `--json`). Prova contra a `main`: resposta da aprovação sem `warnings` (falha confirmada). openapi.json regenerado; fragmento em `changelog.d/cofre-aviso-gate-95.md`.

## Travado
- Nada.

## Próximo passo
- Merge do PR → o #95 fecha com todos os critérios de build e o qa já visto em produção.

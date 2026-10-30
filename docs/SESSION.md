# Fonte externa e ingestão (#125, fatia 1) · sessão `153-junta-contadores`
Data: 2026-10-30

## Feito
- Juntar agente e host soma as nove colunas contadoras de `calls_daily` do juntado nas linhas do que fica com a mesma chave; repetir a junção não soma de novo (#153).
- Juntar host escolhe a lista de `external_outbox_snapshot` de hora mais nova; em empate fica a do que fica (ADR-0081 item 5).
- Testes: nove colunas, outra empresa, reversão e snapshot sem lista. `go test ./...` com Postgres local: exit 0. Lint: 0 issues.
- Gate do Sonar: OK no head anterior (e652643); o head desta sessão ainda não foi analisado.

## Travado
- Retirada do pendente que a lista mais nova não tem (ADR-0081 item 5) não está implementada. Quando estiver, um pendente vindo do juntado pode sumir (UNCERTAIN 7 do PR #154). Decisão do Bardi.

## Próximo passo
- Conferir `oute-sonar pr 154 --json` no head novo; avisar o head só com gate OK. Sem merge.

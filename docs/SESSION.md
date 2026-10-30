# Fonte externa e ingestão (#125, fatia 1) · sessão `153-junta-contadores`
Data: 2026-10-30

## Feito
- Juntar agente e host soma `calls_daily` do juntado nas linhas do que fica com a mesma chave; repetir a junção não soma de novo (#153).
- Juntar host escolhe a lista de `external_outbox_snapshot` de hora mais nova; em empate fica a do que fica (ADR-0081 item 5).
- Testes: colisão de chave (agente e host), linha sem colisão, idempotência, e os quatro casos da lista. `go test ./...` com Postgres local: 68 pacotes ok. Lint: 0 issues.

## Travado
- Nada da #153. A decisão de adiar a junção de `calls_daily` e do snapshot foi aceita pelo Bardi com esta condição.

## Próximo passo
- Alerta `host_no_data` (ADR-0077 item 4); antes de qualquer fatia que passe a gravar nessas tabelas, já há a soma.

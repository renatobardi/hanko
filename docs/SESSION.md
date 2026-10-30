# Fonte externa e ingestão (#125, fatia 1) · sessão `153-junta-contadores`
Data: 2026-10-30

## Feito
- Juntar agente e host soma `calls_daily` do juntado nas linhas do que fica com a mesma chave; repetir a junção não soma de novo (#153).
- Juntar host escolhe a lista de `external_outbox_snapshot` de hora mais nova; em empate fica a do que fica (ADR-0081 item 5).
- Gate do Sonar do head 5b555d1 era ERROR (duplicação 10,9%, S107, S3776 em 4 pontos). Corrigido em 848ded0: helpers compartilhados e testes sem repetição. Sem mudança de comportamento.
- `go test ./...` com Postgres local: 68 pacotes ok. Lint: 0 issues.

## Travado
- Gate do Sonar do novo head: ainda não conferido (aguarda o CI).

## Próximo passo
- Conferir `oute-sonar pr 154 --json`; só avisar o head com gate OK. Sem merge.

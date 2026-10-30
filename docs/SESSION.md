# Fonte externa e ingestão (#125, fatia 1) · sessão `153-junta-contadores`
Data: 2026-10-30

## Feito
- Juntar agente e host soma `calls_daily` do juntado nas linhas do que fica com a mesma chave; repetir a junção não soma de novo (#153).
- Juntar host escolhe a lista de `external_outbox_snapshot` de hora mais nova; em empate fica a do que fica (ADR-0081 item 5).
- Gate do Sonar: S107 e S3776 corrigidos (848ded0). Duplicação em código novo era 3,9% (limite 3): as duas consultas de soma viraram `MergeCallsDaily` (ver o commit seguinte à 848ded0).
- `go test ./...` com Postgres local: exit 0. Lint: 0 issues.

## Travado
- Gate do Sonar do novo head: aguarda o CI e a análise.

## Próximo passo
- Conferir `oute-sonar pr 154 --json` no head novo; só avisar com gate OK. Sem merge.

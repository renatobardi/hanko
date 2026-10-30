# Fonte externa e ingestão (#125, fatia 1) · sessão `filtro-segredo`
Data: 2026-10-30

## Feito
- Filtro de segredo no decision model (ADR-0079 item 6): texto com padrão de segredo não sai do Hanko em triage, phase_classifier e name_summary. Ledger e evento guardam só o tipo do padrão.
- `go test ./...` com Postgres local: exit 0. Lint: 0 issues.

## Travado
- Classificação de conversa (ADR-0079 itens 1 a 3, 5 e 7): não começada. Depende de conversa externa (ADR-0078), não das palavras do gerador.
- Gerador de nome (ADR-0084): aguarda as 192 + 192 palavras do Bardi.

## Próximo passo
- Conferir `oute-sonar pr <n> --json` no head; avisar o head só com gate OK. Sem merge.

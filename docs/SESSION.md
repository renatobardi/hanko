# Fonte externa e ingestão (#125, fatia 1) · sessão `alerta-host`
Data: 2026-10-30

## Feito
- Alerta `host_no_data` (ADR-0077 item 4; ADR-0083 itens 2 e 5): host externo sempre ligado, não arquivado, sem fato há mais do limite da empresa abre o alerta; o fato que chega fecha.
- `go test ./...` com Postgres local: exit 0. Lint: 0 issues.

## Travado
- Classificação de conversa (ADR-0079 itens 1 a 3, 5 e 7): não começada.
- Gerador de nome (ADR-0084): aguarda as 192 + 192 palavras do Bardi.
- SHOULD-FIX da auditoria do #155 (entrada longa no secretscan, teste do phase_classifier): pendente.

## Próximo passo
- Conferir `oute-sonar pr <n> --json` no head; avisar só com gate OK. Sem merge.

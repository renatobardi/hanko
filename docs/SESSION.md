# Fonte externa e ingestão (#125, fatia 1) · sessão `alerta-host`
Data: 2026-10-30

## Feito
- Alerta `host_no_data` (ADR-0077 item 4; ADR-0083 itens 2 e 5), com testes de host sem fato (prazo da criação), outra empresa e passada repetida sem duplicar (auditoria do #156).
- `go test ./...` com Postgres local: exit 0. Lint: 0 issues.
- Sonar do head 8cf38c7: OK, com um MINOR (S8193) no teste; corrigido em 4cb21b7.
- Head 6b56caa: CI verde, mas o check SonarCloud não apareceu; novo commit de docs para republicar a análise.

## Travado
- UNCERTAIN 2 do #156: se o `last_seen_at` deve usar o relógio do fato ou o do recebimento. Pergunta ao Bardi; a ingestão não foi alterada.
- Classificação de conversa (ADR-0079 itens 1 a 3, 5 e 7): não começada. Gerador de nome (ADR-0084): aguarda as palavras do Bardi.

## Próximo passo
- Conferir `oute-sonar pr 156 --json` no head novo; avisar só com gate OK. Sem merge.

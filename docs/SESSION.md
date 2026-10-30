# Fonte externa e ingestão (#125, fatia 1) · sessão `classificacao`
Data: 2026-10-30

## Feito
- Auditoria do #158: janela da classificação (migração 00214, `classified_last_at`, relê só sessões com fato novo), trava própria da passada, testes das regras, outra empresa, reclassificação, conversa sem fato, papel e label entre sessões.
- `go test ./...` com Postgres local: exit 0. Lint: 0 issues.

## Travado
- Conversas externas não são criadas pela ingestão ainda: o `external_conversation` depende do gerador de nome (ADR-0084), que aguarda as palavras do Bardi. Por isso a classificação não atinge telemetria real até lá.
- `full_command` depende de OTEL_LOG_TOOL_DETAILS (docker/compose.yaml:43@845764f, fonte externa, oute-agent); o nome do atributo no bucket não foi verificado (docs/adr/0004-observabilidade.md:210@845764f).
- Jev (ADR-0079 itens 4 e 5) não é chamado. Fixture de paridade com o Python não feita.
- Emparelhamento com troca de workspace (ADR-0075 item 5): decisão do Bardi sobre calls_daily.

## Próximo passo
- Conferir o check do SonarCloud no head novo; avisar só com check verde. Sem merge.

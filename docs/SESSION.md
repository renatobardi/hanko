# Fonte externa e ingestão (#125, fatia 1) · sessão `classificacao`
Data: 2026-10-30

## Feito
- Classificação de conversa externa pela telemetria (ADR-0079 itens 1 a 3 e 7): pacote `internal/aidlc` (regra do phase.py), passada no tick de alertas, marca `regression`, troca manual vale sobre tudo.
- Verificação: `telemetry_spans.attributes` guarda o mapa completo de atributos (internal/ingest/rows.go:349, :365); o collector não remove full_command, file_path nem tool_name (config/otel/collector.yaml:81-91).
- `go test ./...` com Postgres local: exit 0. Lint: 0 issues.

## Travado
- `full_command` depende de OTEL_LOG_TOOL_DETAILS (docker/compose.yaml:43@845764f); o nome do atributo no bucket real não foi verificado (docs/adr/0004-observabilidade.md:210@845764f). Sem ele, as regras de ação não casam.
- O Jev (decision model, ADR-0079 itens 4 e 5) não é chamado nesta fatia.
- Emparelhamento com troca de workspace (ADR-0075 item 5): depende de decisão do Bardi sobre calls_daily.
- Gerador de nome (ADR-0084): aguarda as palavras do Bardi.

## Próximo passo
- Conferir o gate do SonarCloud no head; avisar só com o check verde. Sem merge.

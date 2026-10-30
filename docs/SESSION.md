# Fonte externa e ingestão (#125, fatia 1) · sessão `classificacao`
Data: 2026-10-30

## Feito
- Reauditoria do #158: cursor da classificação pelo recebimento da sessão (`external_session.updated_at`, migração 00214 com `classified_session_at`), não pelo relógio do fato.
- `go test ./...` com Postgres local: exit 0. Lint: 0 issues.

## Travado
- Conversa sem vínculo com a sessão (`external_conversation.external_session_id`) não é relida por fato novo: nenhum código grava o vínculo. A criação da conversa espera o gerador de nome (ADR-0084). Registrado na #125.
- `full_command` depende de OTEL_LOG_TOOL_DETAILS (docker/compose.yaml:43@845764f, oute-agent, fonte externa); o nome do atributo no bucket não foi verificado.
- Jev (ADR-0079 itens 4 e 5) não é chamado. Fixture de paridade com o phase.py não feita.
- Emparelhamento com troca de workspace (ADR-0075 item 5): decisão do Bardi sobre calls_daily.

## Próximo passo
- Conferir o check do SonarCloud no head novo; avisar só com check verde. Sem merge.

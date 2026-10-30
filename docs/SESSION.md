# Fonte externa e ingestão (#125, fatia 1) · sessão `testes-secretscan`
Data: 2026-10-30

## Feito
- SHOULD-FIX do #155: teste de entrada longa no secretscan e teste de recusa do phase_classifier (cfc136a).
- `go test ./...` com Postgres local: exit 0. Lint: 0 issues.

## Travado
- Emparelhamento, troca de workspace com passado (ADR-0075 item 5): `calls_daily` não guarda o repositório, então o custo do repositório não se separa só somando linhas; é preciso recalcular a partir de `model_calls`. Decisão do Bardi (ver o PR).
- Classificação de conversa (ADR-0079 itens 1 a 3, 5 e 7): exige os atributos de ferramenta do `phase.py` (`full_command`, `file_path`, `tool_name`) em `telemetry_spans`; não verificado se o Hanko os guarda. Não começada.
- Gerador de nome (ADR-0084): aguarda as palavras do Bardi.

## Próximo passo
- Conferir os atributos de `telemetry_spans` para a classificação; decidir o recálculo de `calls_daily` para o emparelhamento. Avisar o head só com o SonarCloud verde. Sem merge.

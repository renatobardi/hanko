# Fonte externa e ingestão (#125, fatia 1) · sessão `125-caixa-externa`
Data: 2026-10-30

## Feito
- Caixa lista agentes e hosts externos criados nos últimos 30 dias como itens informativos (fora de Count e do selo); lido e arquivado seguem o de todo item (ADR-0075 item 2; ADR-0077).
- Enum da API de Caixa e OpenAPI ganham `external_agent` e `external_host`.
- Teste de corrida das sessões com barreira de partida (SHOULD-FIX A do #149).
- Testes: `TestInboxListsNewExternalAgentsAndHostsAsInformational`, `TestInboxLeavesOutOldExternalAgents`, `TestInboxExternalItemFollowsReadState`. `go test ./...` com Postgres local: 68 pacotes ok. Lint: 0 issues.

## Travado
- Seção visual dos itens na tela da Caixa (views e i18n): próximo PR.
- Janela de 30 dias é escolha do autor; registrar com o Bardi.
- Gerador de nome (ADR-0084): as 192 + 192 palavras do Bardi.
- Caixa "emparelhamento a conferir" (só existe com chave por URL), renomear, arquivar e juntar agente e host (ADR-0075 item 2).

## Próximo passo
- Seção da Caixa para agente e host externos novos, com i18n; depois renomear, arquivar e juntar.

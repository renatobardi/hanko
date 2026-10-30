# Fonte externa e ingestão (#125, fatia 1) · sessão `125-entidades-externas`
Data: 2026-10-30

## Feito
- Agentes e hosts externos: listar, ver, renomear, arquivar ou voltar, marcar sempre ligado (host) e juntar (ADR-0075 item 2; ADR-0077 itens 3 e 4). As oito rotas deixam o 501.
- Renomear guarda o nome antigo como apelido do mesmo agente ou host. Juntar move apelidos, sessões e conversas para o que fica, arquiva o juntado e leva o nome dele como apelido.
- Testes: serviço (renomear com apelido, conflito, arquivar e desarquivar, visualizador recusado, juntar agente, juntar host com sempre ligado e hora, junção consigo mesmo) e API (rotas ligadas, contrato de 501 com 10 operações restantes). `go test ./...` com Postgres local: 68 pacotes ok. Lint: 0 issues.

## Travado
- Repositório: trocar o workspace de um emparelhamento com o passado junto (ADR-0075 item 5) e a rota de emparelhamentos (`repo-pairings`) seguem em 501.
- Alerta `host_no_data` (ADR-0077 item 4) e a parada de alertas de host parado (item 5): dependem dos alertas, ainda não ligados.
- Telas da fonte externa e classificação de conversa (ADR-0079).
- Gerador de nome (ADR-0084): as 192 + 192 palavras do Bardi.
- Janela de 30 dias da Caixa é escolha do autor, a confirmar.

## Próximo passo
- Alerta `host_no_data` e o emparelhamento com a troca de workspace; depois a classificação, quando o gerador de nome tiver as palavras.

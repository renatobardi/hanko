# Fonte externa e ingestão (#125, fatia 1) · sessão `125-entidades-externas`
Data: 2026-10-30

## Feito
- Agentes e hosts externos: listar, ver, renomear, arquivar ou voltar, sempre ligado (host) e juntar. As oito rotas deixam o 501 (ADR-0075 item 2; ADR-0077 itens 3 e 4).
- Juntar trava as duas linhas (ordem de id), recusa `into` arquivado (409), preserva o horário de arquivamento do juntado e move sessões, conversas, chamadas do modelo e aprovações. Renomear para apelido de outra entidade dá 409.
- Testes: serviço (conversa e host movidos, recusa sem efeito, horário preservado, idempotência, outra empresa, junção cruzada concorrente, apelido em conflito) e API (409, 403, 404, 422 e rotas ligadas). Guard: chave `read` barrada em PATCH e merge; chave `ingest` barrada em rotas públicas.
- Testes da junção: chamada do modelo e aprovação passam ao que fica; conflito de apelido de host (409); evento por junção feita. `go test ./...` com Postgres local: 68 pacotes ok. Lint: 0 issues.

## Travado
- `calls_daily` e `external_outbox_snapshot` não são movidos na junção: issue #153.
- Troca de workspace de emparelhamento com o passado junto e `repo-pairings` seguem em 501 (ADR-0075 item 5).
- Alerta `host_no_data` e parada de alertas de host parado (ADR-0077 itens 4 e 5).
- Janela de 30 dias da Caixa e arquivar com ingestão mantida: decisões do autor, para o Bardi confirmar.

## Próximo passo
- Issue #153 (somar `calls_daily`, decidir o snapshot); depois o alerta `host_no_data`.

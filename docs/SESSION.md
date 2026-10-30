# Fonte externa e ingestão (#125, fatia 1) · sessão `125-emparelhamento-repo`
Data: 2026-10-30

## Feito
- Ingestão deriva, na transação do lote, o agente externo, o host externo e o emparelhamento do repositório (ADR-0075 itens 2 a 6, ADR-0077 item 2), com `activity_event`.
- Ajustes da auditoria do #147: `no-repo` reservado (colisão tratada), texto fora do CHECK não derruba o lote, corrida de criação tratada, eventos só de quem inseriu.
- Testes: 10 `TestDerive*` (incluem colisão, entrada inválida e corrida). `go test ./...` com Postgres local: 68 pacotes ok. Lint: 0 issues.
- Sem migration nova: as tabelas e a coluna `is_system` já vieram na 00208.

## Travado
- Caixa "emparelhamento a conferir" (ADR-0075 item 4.4, caso de URL em conflito), corrigir emparelhamento com passado (item 5), renomear/arquivar/juntar agente e host, telas.
- Chave de repositório por URL (item 4.1): o fato de hoje traz só o nome.
- Repositório com slug inválido, reservado ou fora do limite vai para `no-repo`, ou fica sem workspace se o `no-repo` não puder existir (decisão deste PR, não do ADR).

## Próximo passo
- Sessão e conversa (ADR-0078) e a conversa com o nome do gerador (ADR-0084); depois classificação (ADR-0079) e leitura.

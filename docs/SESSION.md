# Fonte externa e ingestão (#125, fatia 1) · sessão `125-emparelhamento-repo`
Data: 2026-10-30

## Feito
- Ingestão deriva, na transação do lote, o agente externo, o host externo e o emparelhamento do repositório (ADR-0075 itens 2 a 6, ADR-0077 item 2), com `activity_event`.
- Testes: `TestDerive*` (agente por nome sem caixa, host com hora do fato mais novo, emparelhamento único, workspace existente por slug, `no-repo`, empresa arquivada). `go test ./...` com Postgres local: 68 pacotes ok. Lint: 0 issues.
- Sem migration nova: as tabelas `repo_pairing`, `external_agent_alias` e `external_host_alias` e a flag `is_system` já vieram na 00208.

## Travado
- Caixa "emparelhamento a conferir" (ADR-0075 item 4.4, só para URL em conflito), corrigir emparelhamento com passado (item 5), renomear/arquivar/juntar agente e host, telas.
- Chave de repositório por URL (item 4.1): o fato de hoje traz só o nome do repositório.
- Slug inválido de repositório vai para `no-repo` (decisão deste PR, não do ADR).

## Próximo passo
- Sessão e conversa (ADR-0078) e a conversa com o nome do gerador (ADR-0084); depois classificação (ADR-0079) e leitura.

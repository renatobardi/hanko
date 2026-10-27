# Contrato da v0.8 · sessão `feat-124-contrato-rodada-aprovacao`
Data: 2026-10-27

## Feito
- Migration 00210: rodada, ciclo, participante, etapa (só a revisão vigente; texto no fato), ação, marca, aprovação, versões do script, lista da outbox, busca em conversa e sessão externas, alerta `db_disk_low` e itens da Caixa. Testada do zero e sobre a 00209.
- Domínio e 8 operações novas em 501 (rodada, ciclo, etapa, aprovação, script). Limites de disco em `AlertLimitSpecs`.
- Sem drift: `sqlc generate`, `templ generate`, `make openapi`, `make islands`. `golangci-lint`: 0 issues. `go test ./...`: verde.

## Travado
- Padrões do disco (aviso 5 GiB, crítico 0,5 GiB) e alerta por empresa: recomendação do autor, a confirmar com o Bardi.

## Próximo passo
- Bardi revisa e decide o merge do PR. Depois: fatia 2 (#126), operações de custo com domínio; fatia 3 (#127), ingestão de rodada e aprovação.

# Contrato da v0.8 · sessão `feat-124-contrato-rodada-aprovacao`
Data: 2026-10-28

## Feito
- Migration 00210 (rodada, ciclo, participante, etapa, ação, marca, aprovação, versões do script, lista da outbox, busca de conversa e sessão externas, `db_disk_low`, itens da Caixa), testada do zero e sobre a 00209.
- Domínio e 8 operações novas em 501 (rodada, ciclo, etapa, aprovação, script). Limites de disco em `AlertLimitSpecs`.
- Corpo do PR #138 com os itens do `## Falta`; branch atualizado com a `origin/main` (#137 entrou).

## Travado
- Padrões do disco (aviso 5 GiB, crítico 0,5 GiB) e alerta por empresa: recomendação do autor, a confirmar com o Bardi.
- Gate do SonarCloud do #138 sem resultado (`NONE`); CI não acompanhado.

## Próximo passo
- Bardi revisa o #138 e decide o merge. Depois: fatia 2 (#126), custo e alertas externos; fatia 3 (#127), rodada e aprovação, com a severidade do disco; fatia 4 (#128), busca e export.

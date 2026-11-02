# Release v0.8.5: log das chamadas de memória encaminhadas (#207) · sessão `hanko-ai-memory-update`
Data: 2026-11-02

## Feito
- v0.8.5 cortada com o log `memory-mcp: chamada encaminhada` (#208). Tag e deploy são pedidos do canal.
- Em prd (v0.8.4) até agora: 0 runs e 0 propostas de memória desde o deploy; 0 `chamada fora do contrato`.

## Travado
- `GHSA-jjc6-c2mx-9fq7` segue em rascunho (publicar é decisão do Bardi). Falta a primeira run com workspace em prd para testar o proxy de ponta a ponta.

## Próximo passo
- Depois da primeira run com memória: reler o log (`chamada encaminhada` e `fora do contrato`). Fase 2: #205.

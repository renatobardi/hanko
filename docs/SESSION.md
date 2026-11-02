# Log das chamadas de memória encaminhadas (#207) · sessão `hanko-ai-memory-update`
Data: 2026-11-02

## Feito
- Proxy `/memory-mcp` registra `memory-mcp: chamada encaminhada` (tool, run, workspace, project; sem argumentos). Teste e RUNBOOK (pt-BR e en) atualizados.
- Leitura em prd (v0.8.4): 0 runs e 0 propostas de memória desde o deploy; log com 0 `chamada fora do contrato`. O proxy novo ainda não recebeu tráfego.

## Travado
- `GHSA-jjc6-c2mx-9fq7` segue em rascunho (publicar é decisão do Bardi). Falta a primeira run com workspace em prd para testar o proxy de ponta a ponta.

## Próximo passo
- Depois da primeira run com memória: reler o log (`chamada encaminhada` e `fora do contrato`). Fase 2: #205.

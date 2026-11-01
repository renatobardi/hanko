# ai-memory 3.3.1 e contrato do proxy (#200, #202) · sessão `hanko-ai-memory-update`
Data: 2026-11-01

## Feito
- `hanko-prd` no ai-memory 3.3.1. Proxy `/memory-mcp` com lista de permitidos e escopo sempre nos argumentos (#203). v0.8.4 cortada; tag e deploy são pedidos do canal.

## Travado
- `GHSA-jjc6-c2mx-9fq7` segue em rascunho até a release sair. Não medido: dados no escopo padrão do ai-memory ou no workspace errado em prd.

## Próximo passo
- Publicar o advisory; ler `memory-mcp: chamada fora do contrato` no log para decidir concessões; abrir a issue da fase 2 (credencial por empresa).

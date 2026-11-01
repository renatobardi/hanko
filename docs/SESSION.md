# Contrato do proxy de memória (#202) · sessão `hanko-ai-memory-update`
Data: 2026-11-01

## Feito
- Proxy `/memory-mcp`: lista de permitidos (`internal/memory/args.go`), escopo sempre nos argumentos, leitura estrita do corpo, escopo igual ao da run, recusas no log. `TestMemoryProxyContraServidorReal` testa token de run, proxy e ai-memory real (só com `HANKO_AI_MEMORY_CONTRACT_URL`).

## Travado
- `GHSA-jjc6-c2mx-9fq7` segue em rascunho: publicar depois da release. Não medido: dados no escopo padrão do ai-memory ou no workspace errado em prd.

## Próximo passo
- Merge, release e deploy; depois olhar `memory-mcp: chamada fora do contrato` no log para decidir concessões. Fase 2: credencial por empresa (issue própria).

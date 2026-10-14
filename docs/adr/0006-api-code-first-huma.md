# ADR-0006 — API code-first com huma; OpenAPI gerado e versionado
Data: 2026-10-13 · Status: aceita

## Contexto
Spec-first (oapi-codegen + kin-openapi) solo = yaml + interface gerada + implementação = 3 lugares pra drift por endpoint.

## Decisão
`huma` v2 sobre `net/http` (`humago`): structs Go → `api/openapi.json` + validação + `/docs`. `make openapi` gera; CI falha se diff não commitado.

## Consequências
1 dependência de framework leve. OpenAPI continua contrato pra CLI, MCP, agentes externos e ilhas.

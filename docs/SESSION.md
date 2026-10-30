# Fonte externa e ingestão · sessão `feat-125-fonte-externa-ingestao`
Data: 2026-10-29

## Feito
- Gerador único de nome (ADR-0084): listas 64 + 64 em `internal/domain/names/`, derivação por hash com tentativas e sufixo; testes de formato e de nunca repetir numa empresa.
- Escopo `ingest` (ADR-0076 item 3): chave presa à empresa; a API pública e o `/mcp` a recusam; listener `HANKO_INGEST_ADDR` com `POST /v1/traces` (protobuf, gzip).
- Span gravado uma vez pela chave de dedupe; o fato novo cria agente, host e conversa externos, com `activity_event`.

## Travado
- Ficou de fora: logs, métricas, replay e prontidão (501); contadores e custo (fatia 2, #126); emparelhamento e workspace `no-repo`; classificação; Caixa; telas; vhost nginx na tailnet (ação de host).
- As 192 + 192 palavras novas do ADR-0084 aguardam a revisão do Bardi.

## Próximo passo
- Revisão do PR e demo qa (Bardi). Depois: logs e métricas OTLP, e o custo (#126).

# Fonte externa e ingestão (#125, fatia 1) · PR #142 (`feat/125-ingestao-nucleo`)
Data: 2026-10-30

## Feito
- Auditoria do #142: `registerIngestKeys` virou métodos do `server`, uma função curta por rota (BLOCKING 1).
- `HANKO_INGEST_ADDR` recusa endereço curinga (vazio, `0.0.0.0`, `::`); listener de ingestão com `ReadTimeout` de 2 min.
- `git merge origin/main` (c3e6159, #140, migration 00212): conflito em `internal/api/api.go` resolvido; `make openapi`, `make generate` e `make islands` rodados, sem gerado à mão.
- Testes: `go test ./...` com Postgres local e lint (0 issues) passaram no branch.

## Travado
- Sonar do PR: gate ainda não verificado.
- SHOULD-FIX não tratados: enum de `scope` em `internal/api/access.go` (justificado no PR: o CLI nunca emite chave `ingest`); concorrência já vem antes de ler o corpo (justificado no PR).

## Próximo passo
- Conferir o gate do Sonar (`oute-sonar pr 142 --json`) e tratar os achados de código novo; avisar o head novo para o dispatcher.

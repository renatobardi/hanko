# Fonte externa e ingestão (#125, fatia 1) · PR #142 (`feat/125-ingestao-nucleo`)
Data: 2026-10-30

## Feito
- Auditoria do #142: `registerIngestKeys` virou métodos do `server`, uma função curta por rota (BLOCKING 1).
- `HANKO_INGEST_ADDR` recusa endereço curinga (vazio, `0.0.0.0`, `::`); listener de ingestão com `ReadTimeout` de 2 min.
- `git merge origin/main` em c3e6159 (#140, migration 00212) e em 02227e0 (#144): conflitos resolvidos; `make openapi`, `make generate` e `make islands` rodados, sem gerado à mão.
- `go test ./...` com Postgres local passou (68 pacotes ok) e `golangci-lint run ./...` deu 0 issues.

## Travado
- Sonar do PR: gate do head novo ainda não verificado; achados em código novo listados no PR para decisão do Bardi.
- SHOULD-FIX não tratados: enum de `scope` em `internal/api/access.go` (justificado no PR); concorrência já vem antes de ler o corpo (justificado no PR).

## Próximo passo
- Conferir o gate do Sonar depois do CI (`oute-sonar pr 142 --json`); avisar o head novo para o dispatcher.

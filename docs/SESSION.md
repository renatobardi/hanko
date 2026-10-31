# Gerador de nome (#125, ADR-0084) · sessão `feat-125-gerador-de-nome`
Data: 2026-10-30

## Feito
- `origin/main` (908ff9a, #146) incorporada por merge, sem conflito. Commit de merge com sign-off.
- `make openapi`, `make generate` e `make islands` rodados depois do merge: nenhum arquivo gerado mudou.
- `go build ./...` e `go test ./internal/domain ./internal/i18n ./internal/ui` passam.

## Travado
- Nada local. O CI do #160 precisa rodar de novo no head novo.

## Próximo passo
- Confirmar CI e SonarCloud no head novo do #160. O merge é do dispatcher e do Bardi.

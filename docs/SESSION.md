# Estado da última sessão (CI: uma rodada de testes só)
Data: 2026-10-23 · branch `chore/51-ci-rodada-unica` → `main` · issue #51 (plano no comentário de 2026-10-23)

## Feito
- PR 1 da #51: `ci.yml` roda os testes uma vez só, com `-tags integration` (antes, a suíte base rodava duas vezes). Prova: `go test -list .` sem a tag = 2034 testes, com a tag = 2207, nenhum só sem a tag.
- Passo novo falha se algum `_test.go` tiver `//go:build !integration` (ele sairia do CI).
## Travado (só o dono)
- Merge do PR 1; medir o `check` (esperado ~38 min, antes ~65).
## Próximo passo
- PR 2 da #51: jobs `estatico`/`gerados`/`test` (matriz `service` | `resto`) e `check` agregador com `if: always()`.

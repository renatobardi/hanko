# Estado da última sessão (CI em jobs paralelos, PR 2 da #51)
Data: 2026-10-24 · branch `chore/51-ci-jobs-paralelos` → `main` · issue #51 (plano no comentário de 2026-10-23)

## Feito
- PR 1 (#53) na main: uma rodada de testes só. `check` de ~65 para ~27–41 min.
- PR 2: `ci.yml` em jobs `estatico`, `gerados`, `test` (shards `service` | `resto`, Postgres próprio) e `check` agregador (`if: always()`, união dos shards, perfil mesclado, piso). Docs do CI atualizadas.
## Travado (só o dono)
- Merge do PR 2; medir o `check` em 3 PRs típicos (meta ≤ ~25 min).
## Próximo passo
- Se o `internal/service` (~18 min) continuar sendo o gargalo, abrir issue para acelerar esse pacote.

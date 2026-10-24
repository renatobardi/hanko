# Estado da última sessão (testes do service mais rápidos, #71)
Data: 2026-10-24 · branch `chore/71-service-testes-rapidos` → `main` · issue #71

## Feito
- `testdb.New` clona um template migrado uma vez por processo, em vez de recriar o schema e rodar as 70 migrations a cada teste (ADR-0071).
- `internal/service`: 1036 s → 130 s localmente; mesmos 654 testes e cobertura (77,9 %). Suíte inteira verde local (623 s).
## Travado (só o dono)
- Nada: merge autorizado pelo dono na conversa.
## Próximo passo
- Medir `test (service)` em 3 execuções do CI (meta ≤ ~8 min). O caminho crítico do `check` passa a ser o shard `resto`.

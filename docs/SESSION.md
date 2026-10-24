# Estado da última sessão (shard `resto` do CI, #75)
Data: 2026-10-24 · branch `chore/75-resto-compila-paralelo` · issue #75

## Feito
- Job `test` do CI: um passo compila os testes em paralelo antes da rodada com `-p 1`, e `GORACE=atexit_sleep_ms=0` tira a espera de 1 s na saída de cada binário com `-race`. Nenhum teste nem gate muda.
- Medido localmente no `resto`: compilação a frio 226 s → 102 s; rodada completa 343 s → 268 s.
## Travado
- Merge do PR da #75 (só o dono). Seguem abertas as decisões da sessão anterior (#57, cofre): nome `reauth.minutes` e fato por empresa.
## Próximo passo
- Medir `test (resto)` em 3 execuções do CI (meta ≤ ~8 min). Se ainda for o caminho crítico: shard próprio para o `internal/heartbeat` (104 s locais), o que muda a decisão de dois shards fixos da #51.

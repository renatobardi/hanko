# Estado da última sessão (cofre, fatia 1, #56)
Data: 2026-10-24 · branch `feat/56-cofre-campos` · issue #56 (parte de #54)

## Feito
- Campos de segredo (N por item, visível ou secreto), histórico por campo com retenção (5 versões ou 90 dias) e restaurar como versão nova, coleções com concessões, migração 00204.
- API (campos e coleções), CLI `hanko secret field|collection`, UI de campos e coleções, job `secret_history_tick`.
- Testes: service (critérios de build), API de integração, migração, UI e CLI.

## Travado
- Revelar campo, histórico, restaurar e conceder ou revogar concessão exigem reautenticação (ADR-0070, #57): seguem em 501 na API.

## Próximo passo
- Fatia 2 (#57): fechar a reautenticação e ligar essas 7 operações da API ao service já pronto.

# Estado da última sessão (shard `resto` do CI, #75)
Data: 2026-10-24 · branch `chore/75-resto-compila-paralelo` · issue #75

## Feito
- Job `test` do CI: um passo compila os testes em paralelo antes da rodada com `-p 1`, e `GORACE=atexit_sleep_ms=0` tira a espera de 1 s na saída de cada binário com `-race`. Nenhum teste nem gate muda.
- Medido localmente no `resto`: compilação a frio 226 s → 102 s; rodada completa 343 s → 268 s.
## Travado
- Merge do PR da #75 (só o dono). Seguem abertas as decisões da sessão anterior (#57, cofre): nome `reauth.minutes` e fato por empresa.
## Próximo passo
- Medir `test (resto)` em 3 execuções do CI (meta ≤ ~8 min). Se ainda for o caminho crítico: shard próprio para o `internal/heartbeat` (104 s locais), o que muda a decisão de dois shards fixos da #51.
>
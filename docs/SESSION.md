# Corte da v0.7.2 · sessão `release-v0-7-2` (sem issue, #825)
Data: 2026-10-25 · rodada swarm-1024-0625

## Feito
- `scripts/release-notes.sh cut 0.7.2` no ramo `release/v0.7.2`: seção 0.7.2 no `CHANGELOG.md` (entradas dos PRs #99, #100 e #108); os 3 fragmentos de `changelog.d/` saíram. `cofre-contrato.md` já tinha saído na v0.7.0.
- README/ROADMAP não citavam a v0.7.1: sem mudança. `scripts/test-release-notes.sh` verde.

## Travado
- Nada. Tag e deploy ficam com o Bardi (docs/RELEASING.md, passos 4–6).

## Próximo passo
- PR do corte mergeado → Bardi tagueia `v0.7.2` no commit mergeado e faz o deploy no `hanko-prd`.

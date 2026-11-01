# Corte da v0.8.3 · sessão `swarm-1029-2126` (dispatcher)
Data: 2026-11-01

## Feito
- `scripts/release-notes.sh cut 0.8.3`: funde os 6 fragmentos de `changelog.d/` em `## [0.8.3]` do `CHANGELOG.md` (alertas de preço e de custo, alertas operacionais, "vence em", roteiro da demo da #125, menus do organograma, `scripts/hanko-diag.sh`).

## Travado
- Nada. O PR espera o merge do Bardi; a tag é passo humano (`docs/RELEASING.md`, passo 4).

## Próximo passo
- Merge deste PR, tag `v0.8.3` na `main` (pedido pelo canal) e deploy no `hanko-prd` com `deploy-prd.sh --build-local`.

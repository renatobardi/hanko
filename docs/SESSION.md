# Corte da versão v0.7.1 · branch `release/v0.7.1`
Data: 2026-10-25 · sessão `release-v0-7-1` (sem issue, #825; rodada swarm-1024-0625)

## Feito
- Corte da v0.7.1 (`docs/RELEASING.md` passos 1–3): main verde antes do corte (`ci` — jobs `check` e `release-dry-run` success — e `security` no head `804ae43`), seção `## [0.7.1] — 2026-10-25` no `CHANGELOG.md` com as 6 entregas desde a v0.7.0 (#86–#91; fatias 3–7 e a ligação da fatia 1 à reautenticação), `[Unreleased]` vazio, os 6 fragmentos de `changelog.d/` removidos; `scripts/release-notes.sh notes 0.7.1` imprime a seção (46 linhas, 29 marcadores).
- Commit assinado `release: v0.7.1` (`a841b84`); PR do ramo `release/v0.7.1` para a `main`. Sem tag, sem merge, sem deploy (passos 4–6, do Bardi).

## Travado
- Nada.

## Próximo passo
- Bardi: fazer merge do PR, taguear `v0.7.1` no commit mergeado e deployar no `hanko-prd` (canal de aprovação).

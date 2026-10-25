# Cofre, fatia 1: ações sensíveis ligadas à reautenticação (#56)
Data: 2026-10-24 · branch `feat/56-cofre-ligar-501` · sessão `56-cofre-ligar-501` (rodada swarm-1024-0625)

## Feito
- 12 operações saíram do 501 (revelar campo, histórico listar/revelar/restaurar, apagar campo, em `secrets` e `user-secrets`; criar/revogar concessão). Reautenticação exigida no service (`requireFieldSensitive`, `requireGrantAction`); teste do gate no service e da API no teste de integração de campos.
- CI do PR é a validação: o container não tem Go.

## Próximo passo
- Merge pelo dispatcher depois do CI verde.

# Estado da última sessão (corte da v0.7.0)
Data: 2026-10-24 · branch `release/v0.7.0` · sessão `release-v0-7-0` (sem issue, rodada swarm-1024-0625)

## Feito
- Corte da v0.7.0 (docs/RELEASING.md passos 1–3): seção `## [0.7.0] — 2026-10-25` no CHANGELOG.md com as 8 fatias (#55–#62), `[Unreleased]` vazio, fragmentos de `changelog.d/` removidos; `scripts/release-notes.sh notes 0.7.0` imprime a seção.
- Commit assinado `release: v0.7.0`; PR do ramo `release/v0.7.0` para a main. Main verde antes do corte (ci e security em 4c3d86b). Sem tag, sem merge, sem deploy (passos 4–6, do Bardi).

## Travado
- Nada.

## Próximo passo
- Bardi: fazer merge, taguear `v0.7.0` no commit mergeado e deployar no `hanko-prd` (canal de aprovação).

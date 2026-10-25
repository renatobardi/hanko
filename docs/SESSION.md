# Estado da última sessão (corte da v0.7.0)
Data: 2026-10-24 · branch `release/v0.7.0` · sessão `release-v0-7-0` (sem issue, rodada swarm-1024-0625)

## Feito
- Corte da v0.7.0 (docs/RELEASING.md passos 1–3): seção `## [0.7.0] — 2026-10-25` no CHANGELOG.md com as 8 fatias (#55–#62), `[Unreleased]` vazio, fragmentos de `changelog.d/` removidos; `scripts/release-notes.sh notes 0.7.0` imprime a seção.
- Commit assinado `release: v0.7.0`; PR do ramo `release/v0.7.0` para a main. Main verde antes do corte (ci e security em 4c3d86b). Sem tag, sem merge, sem deploy (passos 4–6, do Bardi).

## Travado
- Nada.

## Próximo passo
- Bardi: fazer merge, taguear `v0.7.0` no commit mergeado e deployar no `hanko-prd` (canal de aprovação).

## Cofre, fatia 4 (#59), sessão `hanko-59-cofre-envios-tool`
- Feito (código sem compilar no container; CI é a verificação): envio de arquivo (criar, subir conteúdo, blob em `secret_send_blobs`/BlobStore), listar, ver e apagar por papel, tool `hanko_send_create` (política `ask`), 4 operações saindo do 501 no OpenAPI e no piso de `vault_test.go`.
- Travado: nada. Falta documentada no PR.
- Próximo passo: entregar a abertura de envio de arquivo pelo link (`content_url`, uso único em 60 s) e a demo qa (persona cria envio e o link expira no navegador).

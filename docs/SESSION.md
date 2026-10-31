# Release v0.8.0 · sessão `estudo-issues`
Data: 2026-10-31

## Feito
- `CHANGELOG.md`: seção `[0.8.0]` montada com `scripts/release-notes.sh cut 0.8.0` (24 fragmentos de `changelog.d/`, agora removidos).
- Entram: cofre na tela (campo arquivo com histórico, delegação, env da persona), tags de persona, contrato da v0.8 e o back-end da fonte externa (ingestão, classificação, gerador de nome).

## Travado
- Nada. Tag `v0.8.0` e deploy no `hanko-prd` são passos do Bardi (`docs/RELEASING.md`).

## Próximo passo
- Bardi: merge deste PR, tag assinada `v0.8.0`, deploy; depois as demos `qa` da #117 e da #125 (a da #125 ainda depende de conversa externa, emparelhamento com histórico e telas).

# Release v0.8.1 · sessão `estudo-issues`
Data: 2026-10-31

## Feito
- `CHANGELOG.md`: seção `[0.8.1]` com `scripts/release-notes.sh cut 0.8.1` (4 fragmentos: conversa externa, leitura da fonte externa, emparelhamento de repositório e `--build-local`).
- A produção já roda este código: `hanko-prd` na `74948c7c`, deploy com `--build-local` (smoke ok, `doctor` 8 ok e 2 avisos, migrations até 00215). O commit de release só muda `CHANGELOG.md`, fragmentos e este arquivo.

## Travado
- Nada. Tag `v0.8.1` é passo do Bardi (`docs/RELEASING.md`); o deploy já foi feito.

## Próximo passo
- Bardi: merge deste PR e tag `v0.8.1`; depois a demo `qa` da #117 em produção.

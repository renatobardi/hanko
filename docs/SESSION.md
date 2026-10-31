# Release v0.8.1 · sessão `estudo-issues`
Data: 2026-10-31

## Feito
- `CHANGELOG.md`: seção `[0.8.1]` com `scripts/release-notes.sh cut 0.8.1` (5 fragmentos: conversa externa, leitura, emparelhamento, custo de lista e `--build-local`).
- A produção roda a `74948c7c`, que tem 4 dos 5 itens. O custo de lista (#168, migration 00216) entrou na `main` depois do deploy: a `v0.8.1` o inclui, então precisa de um deploy novo com `--build-local`.

## Travado
- Nada. Tag `v0.8.1` e o deploy são passos do Bardi (pedidos no canal).

## Próximo passo
- Bardi: aprovar a tag `v0.8.1` e o deploy no `hanko-prd`; depois a demo `qa` da #117.

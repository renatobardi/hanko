# Release v0.8.2 · sessão `estudo-issues`
Data: 2026-10-31

## Feito
- `CHANGELOG.md`: seção `[0.8.2]` com `scripts/release-notes.sh cut 0.8.2` (4 fragmentos do #126: histórico de mensalidade, pago em rateio único, ajustes de alerta e conferência de preços).
- A produção já roda este código: `hanko-prd` na `2ec645dc` desde 20:30 UTC, deploy com `--build-local`. O commit de release só muda `CHANGELOG.md`, fragmentos e este arquivo; o ADR-0089 (#171) também já está na `main`.

## Travado
- Nada. A tag `v0.8.2` é passo do Bardi (pedido no canal). Não há deploy novo.

## Próximo passo
- Bardi: aprovar a tag `v0.8.2`; a demo `qa` da #117; o contrato da #45 (ADR-0089) com o gate dele.

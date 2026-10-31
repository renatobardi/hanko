# Menus de filtro do organograma · sessão `estudo-issues`
Data: 2026-10-31

## Feito
- Bug achado na QA das tags (#118) em produção: os menus Tags e Níveis do filtro do organograma não fechavam depois de abertos.
- `FilterBar.tsx`: fecham ao clicar fora, no Esc e ao abrir o outro menu. Escolher uma tag não fecha (o filtro aceita várias).
- Teste novo em `org.test.tsx` (falha sem a correção); `npm test` 64 ok; bundle `org-chart.js` regenerado.

## Travado
- Nada. Não vi o menu rodando num navegador: só jsdom.

## Próximo passo
- Bardi: merge do PR e deploy com `--build-local`; depois repetir o passo D da QA das tags.

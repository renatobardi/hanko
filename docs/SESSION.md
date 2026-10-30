# Tags de persona, fatia 1 sem as telas · sessão `118-tags-contrato`
Data: 2026-10-30

## Feito
- Service e API das tags (ADR-0087): cadastro, arquivar, deletar com contagem, aplicar, criar e aplicar, tirar, `GET /org-chart/personas`; papéis no service; `activity_event` e `personas.tag_names` na mesma transação.
- CLI `hanko persona tag`, MCP só leitura (`hanko_persona_list` com filtro, `hanko_whoami`), export e import com reaproveitamento pelo nome.

## Travado
- Nada. As telas (aba Tags, chips, seletor, filtros na ilha com URL e cookie `hanko_org`) ficaram para o PR seguinte.

## Próximo passo
- Com o merge deste PR: PR das telas da fatia 1; depois a fatia 2 (edição em lote).

# Cofre, fatia 1: pendências do #72 e do #86 · branch `sessao/95-cofre-pendencias-fatia1`
Issue #95 · rodada swarm-1024-0625 · sessão retomada depois de cota esgotada na sessão anterior

## Feito
- Aviso de alcance na contratação: API (`warnings` da persona), CLI e página da persona (`PersonaReachWarnings`, testes de service, API e UI).
- Uso no despacho: `${secret:…}` do adapter_config e do env MCP grava `secret_access_log` (via `dispatch`), sem o valor (teste de service).
- Coleção na criação: `hanko secret create --collection`, campo `collection` da API e select no formulário da UI (testes).
- 404 em `DeleteCollection` e `RemoveSecretFromCollection` quando não há o que remover (teste).
- openapi.json, templ e fragmento `changelog.d/cofre-pendencias-95.md`. Sem migration (nenhuma tabela nova).

## Travado
- Contratação por gate: o aviso aparece na página da persona (live), mas a API e a CLI não o devolvem na aprovação, que devolve o gate.

## Próximo passo
- PR com `Refs #95` (o gate fica de fora). Demo em produção do aviso (qa) com o Bardi.

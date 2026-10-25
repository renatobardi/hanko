### Cofre: pendências do #72 e do #86

- Aviso na contratação: quando o `adapter_config` da persona referencia um `${secret:…}` que ela não alcança, a API (campo `warnings` da persona), a CLI e a página da persona avisam. É aviso, não bloqueia: a concessão pode vir depois, e a run só falha no despacho.
- Uso no despacho: a resolução de `${secret:…}` do `adapter_config` e do env dos servidores MCP grava o uso do segredo da empresa em `secret_access_log` (via `dispatch`), com a run e a persona. O valor não entra no registro.
- Coleção na criação: `hanko secret create --collection`, o campo `collection` da API e a escolha no formulário da UI. Sem escolha, o segredo entra na coleção padrão.
- Remover o que não existe responde "não encontrado" (404), em vez de sucesso: remover um segredo de uma coleção que não o tem, ou apagar uma coleção que já não existe.

- English summary: vault leftovers from #72 and #86: a warning on hire when the persona's adapter_config references a secret it cannot reach (API `warnings`, CLI and persona page; not blocking); dispatch-time use of company secrets (adapter_config and MCP env) recorded in secret_access_log (via dispatch, no value); collection choice on secret creation (`hanko secret create --collection`, API `collection`, UI form; default when omitted); 404 instead of success when removing a secret from a collection that does not hold it or deleting an already-deleted collection.

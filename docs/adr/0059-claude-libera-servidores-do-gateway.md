# ADR-0059 — O adapter claude libera os servidores MCP do gateway de tools
Data: 2026-10-22 · Status: aceita · Complementa: ADR-0036, ADR-0053

## Contexto
O claude roda headless (`-p`) e, nesse modo, nega toda tool MCP que não esteja em `--allowedTools`: não há humano para
aprovar. O adapter liberava só `mcp__hanko`. As integrações MCP http (ADR-0029), inclusive as criadas por uma conexão OAuth
(ADR-0043), chegam à run como servidores apontados para o gateway de tools (`/mcp/run/{integração}` ou `hanko-tools`,
ADR-0036/0053) e eram negadas pelo próprio harness antes de o gateway decidir. O hook `PreToolUse` da política ignora
`mcp__*` de propósito (o gateway já decide), então nada as liberava. Na prática uma persona `claude` não conseguia usar
integração nenhuma sem `extra_args` gravado na contratação, e o caminho que restava era liberar tools nativas largas
(Bash, WebFetch) para falar com o serviço por fora do gateway, sem perfil, gate por tool nem auditoria.

## Decisão
1. `adapter.Spec.GatewayMCP` lista os nomes, em `MCPConfig`, dos servidores que o executor montou apontando para o gateway.
   Só eles: servidores stdio da empresa (rodam direto, sem gateway) e o de memória não entram. Um nome tomado por outro
   servidor na mescla do executor também não entra.
2. O adapter claude acrescenta `mcp__<servidor>` (nome normalizado como o claude nomeia as tools) ao `--allowedTools`, ao
   lado de `mcp__hanko`. Liberar no harness não abre nada: perfil de ferramentas, política off/ask/allow, quarentena,
   gate `tool` e auditoria continuam valendo no servidor, a cada chamada.
3. Revisão (`ReadOnly`) segue sem liberação nenhuma (e já não recebe token nem gateway). Na fase de plano (`PlanOnly`,
   ADR-0041) só o Hanko é liberado: uma tool de integração pode escrever fora do workspace.
4. Outros harnesses não mudam (codex e ACP têm seus próprios mecanismos de permissão).

## Alternativas descartadas
- `extra_args` por persona: exige saber, na contratação, o nome de cada integração futura; integração nova quebraria
  personas antigas.
- `permission_mode: bypassPermissions`: libera tudo, inclusive Bash e escrita.
- Fazer o hook `PreToolUse` responder às tools `mcp__*`: duplicaria no harness a decisão que o gateway já toma, com uma
  chamada extra por tool.

## Consequências
- Uma integração ligada para a persona passa a funcionar em runs de task e chat sem configuração no harness; o controle
  fica onde já estava, no catálogo e na política de tools.
- Sem migration, sem API nova, sem dependência nova.

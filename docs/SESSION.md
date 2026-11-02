# Correções do diagnóstico da produção · sessão `bold-ptolemy`
Data: 2026-11-02

## Feito
- #183 (gateway `Mcp-Param-*` + script de diagnóstico) e #193 (recusa de Bash explicada na run) mesclados; #183 em produção, #193 aguarda deploy.
- Item 3: o gauge `hanko_alerts_open` loga a própria falha de leitura (WARN a cada 5 min, resto debug) e devolve nil ao SDK; o erro de banco deixa de sair como "falha de exportação OpenTelemetry" (`internal/app/alertmetrics.go`; teste cobre o log e a ausência de ponto). Linha no troubleshooting do RUNBOOK e fragmento em `changelog.d/`.
- Operação pelo Bardi: catálogo do GitHub atualizado, política `allow Bash` para `delivery-manager`, API keys expostas revogadas.

## Travado
- Nada.

## Próximo passo
- (4) readiness falhar com `${secret:}` inexistente e alias de modelo fora do catálogo (`internal/readiness/readiness.go`, `internal/service/readiness.go`); (5) chat usar o workspace do projeto quando a persona não tem (`internal/service/conversations.go:562`); (6) decidir se o Hanko passa a fazer push da branch do worktree (revisar ADR-0054) ou documentar o caminho via GitHub MCP (`create_branch`/`push_files`/`create_pull_request`).

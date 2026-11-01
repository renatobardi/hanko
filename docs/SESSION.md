# Correções do diagnóstico da produção · sessão `bold-ptolemy`
Data: 2026-11-01

## Feito
- #183 mesclado e em produção (`48fd2c99`): gateway repete `x-mcp-header` como `Mcp-Param-*`; catálogo do GitHub atualizado pelo Bardi; política nativa `allow Bash` gravada para `delivery-manager`.
- Item 2: run `claude` sem política nativa explica a recusa do modo headless do claude no evento "tools negadas por permissão", com o comando `hanko tool-policy set allow --persona <slug> --native <tool>` (`internal/heartbeat/tools.go`: `nativeDenialHint`, `denialHintSink`; teste unitário). Linha no troubleshooting do RUNBOOK e fragmento em `changelog.d/`.
- Achado novo na persona: ADR-0054 §12 deixa push/PR fora do escopo; a branch `ote-N` vive no espelho e `gh` não está na imagem. Caminho hoje: tools `create_branch`/`push_files`/`create_pull_request` do GitHub MCP pelo gateway (conferir quarentena pós-refresh, ADR-0067).

## Travado
- Nada.

## Próximo passo
- (3) rótulo do WARN "falha de exportação OpenTelemetry" que carrega erro de banco (`internal/telemetry/telemetry.go:221-238`, callback `count alerts` em `internal/service/alerts.go:411`); (4) readiness falhar com `${secret:}` inexistente e alias de modelo fora do catálogo; (5) chat usar o workspace do projeto quando a persona não tem (`conversations.go:562`); (6) decidir se o Hanko passa a fazer push da branch do worktree (revisar ADR-0054) ou documentar o caminho via GitHub MCP.

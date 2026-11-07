# Correções do diagnóstico da produção · sessão `bold-ptolemy`
Data: 2026-11-07

## Feito
- #183, #193 e #206 mesclados e em produção (`62211e1a`).
- Item 4: readiness ganha o passo `secrets` (toda `${secret:}` resolve; `${secretfile:}` existe, está ao alcance da persona e cabe em arquivo) e o passo `model` recusa nome de família de preço (`claude-haiku`); o catálogo deixa de oferecer as famílias. Enum da API regenerado, rótulos na UI e no catálogo i18n, linhas no RUNBOOK, fragmento em `changelog.d/`, testes em `readiness`, `domain`, `service` e `api`.

## Travado
- Nada.

## Próximo passo
- (5) chat usar o workspace do projeto quando a persona não tem (`internal/service/conversations.go:562`; precedência task > projeto > persona de `internal/service/runworkspace.go`); (6) decidir se o Hanko passa a fazer push da branch do worktree (revisar ADR-0054 → ADR novo) ou documentar o caminho via GitHub MCP (`create_branch`/`push_files`/`create_pull_request`).

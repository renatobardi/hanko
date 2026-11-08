# Correções do diagnóstico da produção · sessão `bold-ptolemy`
Data: 2026-11-08

## Feito
- #183, #193, #206 e #213 (item 4: readiness confere segredos e recusa família de modelo) mesclados; #213 aguarda deploy.
- Item 5 (ADR-0090): conversa (chat e sala) com projeto opcional; as runs usam o workspace e o ambiente do projeto na precedência das tasks (projeto > persona > temporário). Migration 00218, `project`/`project_id` na API, `--project` na CLI, seletor na UI, RUNBOOK, changelog, testes em `service` e `api`.

## Travado
- Nada.

## Próximo passo
- (6) decidir se o Hanko passa a fazer push da branch do worktree (revisar ADR-0054 → ADR novo) ou documentar o caminho via GitHub MCP (`create_branch`/`push_files`/`create_pull_request`); apresentar recomendação ao Bardi antes de codar.
- Deploy do #213 e deste PR em `hanko-prd`; depois, abrir os chats da `oute` com o projeto `kubo` para a persona ver o repositório.

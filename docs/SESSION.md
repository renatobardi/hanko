# Correções do diagnóstico da produção · sessão `bold-ptolemy`
Data: 2026-11-08

## Feito
- #183, #193, #206, #213 e #214 (item 5, conversa com projeto, ADR-0090) mesclados; #214 aguarda deploy.
- Item 6 (ADR-0091): o Hanko envia a branch do worktree ao remoto por ação explícita. `PushTaskWorktree` (botão "Enviar branch", `POST /tasks/{task}/git/push`, `hanko task git push`) e `GitPushForRun` atrás da tool `hanko_git_push`, que passa pela política de tools (sem política, gate). `gitrepo.PushBranch` sem força, com a credencial do workspace; commit de segurança do que está solto; `task_worktrees.pushed_sha/pushed_at` (migration 00219). Briefing da run aponta a tool. RUNBOOK, changelog, testes em `gitrepo`, `service`, `ui`, `heartbeat`.

## Travado
- Nada.

## Próximo passo
- Mesclar este PR; deploy em `hanko-prd`; na `oute`, liberar `hanko tool-policy set allow --native hanko_git_push --persona delivery-manager` (ou deixar o gate perguntar) e conferir que a credencial do workspace `kubo` tem escrita no repositório.
- Depois do deploy, abrir os chats com o projeto `kubo` e rodar uma task de ponta a ponta: branch → `hanko_git_push` → PR via integração do GitHub.

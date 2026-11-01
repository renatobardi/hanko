# Diagnóstico da produção (hanko-prd) · sessão `bold-ptolemy`
Data: 2026-11-01

## Feito
- `scripts/hanko-diag.sh` rodou no LXC `hanko-prd` (v791fe025, authenticated+firebase, `HANKO_GIT_WORKSPACES=true`). Servidor saudável: doctor 8 OK, 13 WARN em 400 linhas, todos de boot/restart; nenhum ruído recorrente.
- Achados: persona `demo-arquivo` referencia `${secret:demo-kube}` inexistente (WARN a cada boot, run falha no início); as 7 pastas de workspace da empresa `oute` estão vazias (criadas à mão em 18/10) e `.hanko/` existe; WARN "falha de exportação OpenTelemetry" carrega erro de banco (`count alerts`), rótulo enganoso; sem login do `codex`; `pg_dump` ausente e nenhum backup.
- Correção do script: `docker compose exec` não herdava `HANKO_TOKEN`/`HANKO_COMPANY` (seção 6 dava 401 com chave); alcance da API agora pelo próprio CLI; seção 9 lista espelhos/worktrees e `workspace git show`.

## Travado
- Seção 6 e 9 (workspaces com repo_url, espelhos, readiness, runs com falha) ainda não saíram: dependem da nova rodada com API key.

## Próximo passo
- Bardi: revogar a API key exposta no chat, criar outra e rodar o script atualizado no LXC. Depois priorizar: segredo `demo-kube`; rótulo do WARN de OTel; falha explícita no fallback para diretório temporário; validação de existência/permissão no cadastro do workspace; `doctor` com raiz/permissões/outbox.

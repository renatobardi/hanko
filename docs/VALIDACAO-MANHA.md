# Validação da manhã

Roteiro para o dono validar o que entrou durante a execução autônoma do plano de evolução. Uma seção por marco, preenchida ao fechar cada fatia.

## Ambiente da execução (limites encontrados)
- **Deploy no hanko-prd não executado daqui.** O `deploy/oute-server/deploy-prd.sh` roda no Mac via `ssh oute-server`; o ambiente da execução não tem `ssh` nem Tailscale. Nenhum caminho alternativo foi inventado. Para cada onda mergeada, no Mac, na `main` atualizada:
  1. `deploy/oute-server/backup.sh`
  2. `deploy/oute-server/deploy-prd.sh`
  3. `deploy/oute-server/smoke.sh` + conferir `/healthz`, `/readyz`, migrations aplicadas e um run de persona de ponta a ponta (RUNBOOK).
- **Tags de release bloqueadas.** `git push` de tag devolve HTTP 403 neste ambiente. `v0.2.0` já existia no remoto (aponta para `34978d2`). Pendente no Mac: `git tag -a v0.1.0 9a5d13e -m "Hanko v0.1.0" && git push origin v0.1.0`, e depois `v0.3.0` … `v0.6.0` nos commits de fechamento de cada onda (o workflow `release` publica a partir da tag).
- `gh` sem autenticação válida; PRs e merges feitos pela API do GitHub via ferramentas MCP.
- Testes de integração rodam contra Postgres 16 local (o CI usa 17).

## Pulado / Falhou / Deploy pendente
- (preencher a cada marco)

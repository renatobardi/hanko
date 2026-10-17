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
- **GitHub Actions sem runners para runs novos (a partir de ~06:26Z de 2026-10-17).** Os jobs de `ci`, `e2e` e `security` passaram a falhar em 2 s com `runner_id: 0`, sem passos executados e sem log (HTTP 404), em labels diferentes (`ubuntu-26.04` e `ubuntu-24.04`). O padrão aponta para cota de minutos ou limite de gasto da conta, e não para o código: os gates de drift, build, vet, gofmt e os testes dos pacotes tocados passam localmente nos mesmos commits. `rerun_failed_jobs` devolve 403 neste ambiente. Runs que já tinham runner antes disso terminaram normalmente. **Ação do dono:** conferir Settings › Billing › Actions (minutos e limite de gasto) e re-executar o CI dos PRs abertos. Sem CI verde no head não mergeei: a regra do dono é merge só com CI verde, e não a contornei.
- **PRs com merge pendente por CI:** #11 (M13), #12 (M16b), #14 (M9), #15 (trilha-o1), #16 (M12), #17 (M10), #18 (M16a), #19 (M11). Em cada um o ramo já recebeu a main (M8) e passa nos gates locais; falta só o CI rodar.
- (demais itens: preencher a cada marco)

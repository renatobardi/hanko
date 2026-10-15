# Estado da última sessão
Data: 2026-10-15 (madrugada, ~23h→07h) · Orquestração: Opus 5.5 · Execução: Sonnet 5.5 (trilhas paralelas em worktrees) · Revisão independente: Fable 5.1 (4 rodadas) · Ensaio do deploy local

## Entregue na `main` (GitHub `renatobardi/hanko`, CI verde: lint, govulncheck, `-race`, integração, diffs, dry-run de release)
- **Fechamento do v0.1:** smoke de produção, backup/restore do prd (timer no LXC + restic do lab, restore com rollback), backup pré-deploy, CHANGELOG, docs em inglês, UI e CLI bilíngues.
- **Custos (ADR-0019):** assinaturas, quatro custos (real/lista/estimado/pago), preço append-only, trava semanal, origem do modelo.
- **Operação (ADR-0020):** alertas (persona silenciosa, run travada, orçamento ≥80%), nome amigável de run, revisão automática por outro modelo antes do gate (sem credencial, somente leitura).
- **v0.2 adiantado:** multi-humano com papéis e convites (ADR-0021), troca de empresa, controles por papel, formulário do revisor, export/import de empresa (ADR-0022), PWA instalável; índices e consultas quentes otimizados (`docs/PERF.md`, heartbeat 1,3 s → 1,5 ms em 5M eventos).
- **Revisões:** 4 rodadas independentes, achados corrigidos com teste (ver CHANGELOG). Roteiros de demo em `docs/DEMOS.md` + `examples/demo-company.json`.

## Para colocar em produção (no Mac) — ensaiado localmente: migrations em ~64 ms, dados íntegros, smoke ok, restore e down ok
1. `cd ~/Projects/Github/Hanko && git pull` (a main local já está contida na remota).
2. `deploy/oute-server/deploy-prd.sh` — snapshot `pre-deploy-<ts>` do LXC, troca o código, backup consistente (`backup pré-deploy ok`), sobe (migrations 00008–00013), `readyz ok`, `ai-memory ok`, `==> smoke ok`. Qualquer `FALHA` no smoke: pare e volte.
3. Conferir: `docker compose logs hanko | grep "migration aplicada"` (8 a 13), login Google, tasks/runs/custos, e `company_members` com você como `owner`.
4. Voltar, se preciso (RUNBOOK, "Voltar um deploy que trouxe migrations"): só o binário (`git reset --hard 9858b84` em `/opt/app` + `HANKO_VERSION` + `docker compose up -d`), restore completo (`restore.sh --apply`) ou `lxc restore hanko-prd pre-deploy-<ts>`.
5. lab: mergear PR #281 (backup do hanko-prd) e rodar `tools/deploy-backup.sh --dry-run` e depois sem a flag.

## Travado
- Adapter ACP: a leitura da especificação pediu permissão (WebFetch) que não foi respondida; nada implementado.

## Próximo
Tags `v0.1.0` (dono), demos; ACP; ilhas (log virtualizado); consolidação de memória.

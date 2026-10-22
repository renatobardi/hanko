# Estado da última sessão (deploy-prd.sh: binário errado esvaziava as units de backup)
Data: 2026-10-22 · ramo `claude/eager-brahmagupta-4dj6tw` → `main`

## Feito
- #30 no `main` (ADR-0059): personas claude usam integrações MCP pelo gateway.
- `deploy-prd.sh` chama `hanko` do PATH (a imagem do prd não tem `/hanko`) e gera as units do systemd por `.tmp` + `mv`; teste cobre falha, saída vazia e sucesso.
## Travado
- Um deploy com o script anterior pode ter deixado `hanko-backup.service`/`.timer` vazios no LXC: o próximo deploy os regrava.
## Próximo passo
- Merge e `deploy/oute-server/deploy-prd.sh`; conferir `systemctl list-timers hanko-backup.timer`. Depois: Conexão OAuth do GitHub para o Delivery Manager e apagar a política provisória de `WebFetch`.
- Segue pendente da sessão de fechamento: tags v0.3.0–v0.6.0 e o checklist de `docs/VALIDACAO-MANHA.md`.

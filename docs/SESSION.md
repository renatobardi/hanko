# Estado da última sessão (correção dos compositores de chat)
Data: 2026-10-22 · branch `fix/compositor-chat` → `main` (PR #40)

## Feito
- Texto/anexo sumindo: o autosave do rascunho disparava a limpeza do form (hx-on::after-request). Limpeza no hanko.js, só no envio 2xx do form[data-composer] e só do que foi enviado; autosave com `hx-sync="closest form:queue last"`.
- Envio que "não aparecia": poll suspenso/cancelado durante o envio (livePoll); thread da task com polling (`/tasks/{task}/conversa`); erros 5xx/rede no #flash; botão desabilitado no envio.
- Antes, no `main`: `deploy-prd.sh` chama `hanko` do PATH e gera as units por `.tmp` + `mv` (#39).
## Travado (só o dono)
- Deploy no hanko-prd exige `ssh oute-server` do Mac. Unidades de backup podem estar vazias no LXC até o próximo deploy.
## Próximo passo
- `deploy/oute-server/deploy-prd.sh` na `main`; conferir `systemctl list-timers hanko-backup.timer` e digitar/anexar no chat em produção.
- Pendentes: tags v0.3.0–v0.6.0, checklist de `docs/VALIDACAO-MANHA.md`, OAuth do GitHub para o Delivery Manager.

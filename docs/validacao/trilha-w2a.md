# Validação — trilha contínua, Onda 2 parte A (#64, #65, #47)

ADR-0062 (doctor, update, service, backup, tela e fluxo de sistema) e ADR-0063 (Postgres embutido). Migration 00180.

## O que entrou
| Item | Entrega | Estado |
|---|---|---|
| #64 | `hanko doctor`, `hanko update --channel`, `hanko service install/uninstall/status/restart/print`; `deploy-prd.sh` usa `service print` (units), `doctor` e `update --check` | feito |
| #65 | `hanko backup create/list/verify/restore/schedule`; Configurações › Backups; agendamento por fluxo de sistema (`system_flows` + job `system_flow_tick`); API `/instance/backups` e `/instance/backup-schedule` | feito (com a ressalva do contêiner abaixo) |
| #47 | `hanko serve --embedded-pg` usando initdb/pg_ctl locais | feito |

Tudo que toca o mundo externo (systemd, launchd, GitHub, pg_dump/pg_restore/psql, initdb/pg_ctl, S3, OTel, ai-memory) tem interface e
teste contra fake ou `httptest`. Nenhum teste chama serviço real. Nenhum deploy foi executado.

## Como validar no hanko-prd
O prd roda a imagem distroless (sem `pg_dump`/`psql`), então:
1. Depois do próximo deploy: `docker compose -f /opt/app/docker-compose.yml exec -T hanko hanko doctor` (o deploy já roda e mostra).
   Esperado: postgres e migrations OK; `pg_dump` com AVISO (a imagem não tem o cliente) e harnesses/OTel conforme o ambiente.
2. `systemctl list-timers hanko-backup.timer` e `cat /etc/systemd/system/hanko-backup.service`: as units agora vêm de
   `hanko service print timer`; devem ser equivalentes às antigas (01:30 UTC, `backup.sh`, timeout 30 min). Existe também
   `hanko-stack.service` (habilitada, sem `--now`): sobe/para a stack compose com o LXC.
3. Configurações › Backups (dono da instalação): aparece o aviso "não consegue fazer backups" até a imagem ter o cliente do
   PostgreSQL; o `backup.sh` + restic continuam sendo o backup do prd.
4. Em um host/dev com PostgreSQL instalado (binário): `hanko backup create`, `list`, `verify latest`; Configurações › Backups
   liga o agendamento (cron de 5 campos, fuso IANA); o histórico mostra o disparo agendado. `hanko backup restore latest --yes`
   com o servidor parado; depois `hanko doctor`.
5. `hanko service install --dry-run` mostra unit/plist e comandos sem gravar; `hanko update --check` consulta o GitHub
   (repositório privado precisa de `HANKO_UPDATE_TOKEN`).
6. `hanko serve --embedded-pg` (usuário comum, com PostgreSQL instalado): sobe cluster em `~/.hanko/pg`, imprime o link do primeiro dono.

Smoke real feito nesta máquina (Postgres 16 local, pg_dump/pg_restore/psql reais): migrate → `backup create` → `list` → `verify` →
mutações no banco → `backup restore latest --yes` → tabela criada depois do backup sumiu, a do backup voltou e `doctor` ficou verde.

## Migrations
- `00180_backups_e_fluxos_de_sistema.sql`: tabelas novas `instance_backups` e `system_flows` (sem CHECK de tabela compartilhada
  recriada; nada de `gates_kind_check` etc.). Instance-level, sem `company_id`, como `instance_settings`.

## Env e flags novas
`HANKO_HOME`, `HANKO_BACKUP_DIR`, `HANKO_BACKUP_KEEP` (7), `HANKO_PG_DUMP_BIN`, `HANKO_PG_RESTORE_BIN`, `HANKO_PSQL_BIN` (todas na
`Config.Ops`, também no yaml `ops:`); só da CLI: `HANKO_UPDATE_CHANNEL`, `HANKO_UPDATE_REPO`, `HANKO_UPDATE_TOKEN` (ou `GITHUB_TOKEN`),
`HANKO_IN_CONTAINER=1`. Flags: `serve --embedded-pg --pg-dir --pg-bin-dir --pg-port`; `update --channel --check --skip-backup --force
--restart --rollback`; `service install --user --run-as --env-file --workdir --exec --dry-run --no-enable`; `backup restore --yes --force --skip-blobs`.

## Riscos e decisões a conhecer
- **Imagem de contêiner sem pg_dump**: no prd o backup pela tela fica desligado (explicado na tela e no log) e `update` recusa
  contêiner. Fechar isso exige trocar a base distroless por uma com `postgresql-client` (decisão de imagem, fora desta fatia).
  O `deploy-prd.sh` por isso mantém o `backup.sh` pré-deploy e usa `doctor`/`update --check`/`service print`.
- O restore **substitui** o schema `public` (DROP + recriar em uma transação via psql). Extensões e objetos fora de `public` não são tocados.
  O script SQL intermediário tem o tamanho do banco sem compressão (precisa de disco temporário em `HANKO_BACKUP_DIR`).
- Backup agendado roda dentro do processo do servidor (job com teto de 2 h); um backup interrompido por restart fica "em andamento"
  até 2 h e então é marcado como falho ao próximo disparo (a vaga é liberada por índice parcial).
- Os textos de detalhe das checagens do doctor (pacote `ops`) saem em pt-BR também com `HANKO_LANG=en`; rótulos, comandos e telas são traduzidos.
- A auditoria do agendamento usa `instance_setting_audit` (chave `system_flow.backup`), não `activity_event` (a escrita não pertence a empresa).

## Pulado/Falhou
Nada revertido. Não feitos, fora do escopo pedido: `install.sh`/tap Homebrew (#54), `hanko mcp` (#52), evals (#58); download automático
do Postgres embutido (descartado no ADR-0063).

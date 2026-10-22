### Operação da instância: `hanko doctor`, `update`, `service` e `backup` (ADR-0062, #64 #65)
- `hanko doctor` diagnostica Postgres, migrations (pendentes por conjunto, não por ordem), OTel, ai-memory, harnesses, segredos (sem imprimir valores), disco, pg_dump e idade do último backup; sai com 1 só se algo falhou.
- `hanko update --channel stable|beta` troca o binário pela release do GitHub com backup antes (aborta se o backup falhar), sha256 obrigatório, troca atômica e `--rollback`; recusa contêiner e binário de desenvolvimento.
- `hanko service install|uninstall|status|restart` instala unit do systemd ou plist do launchd (sistema ou `--user`); `hanko service print compose|timer` gera as units usadas pelo `deploy-prd.sh`, que agora também roda `doctor` e `update --check`.
- `hanko backup create|list|verify|restore|schedule`: tar com `pg_dump -Fc`, conteúdo de artefatos em S3 e manifest sha256; restore substitui o banco em uma transação, recusa servidor no ar e schema mais novo que o binário. Retenção `HANKO_BACKUP_KEEP`.
- Configurações › Backups: histórico, "Fazer backup" em segundo plano e agendamento por fluxo de sistema (`system_flow_tick` no heartbeat). API: `GET/POST /instance/backups`, `GET/PUT /instance/backup-schedule`. Migration 00180.
- Env novas: `HANKO_HOME`, `HANKO_BACKUP_DIR`, `HANKO_BACKUP_KEEP`, `HANKO_PG_DUMP_BIN`, `HANKO_PG_RESTORE_BIN`, `HANKO_PSQL_BIN`; da CLI: `HANKO_UPDATE_CHANNEL`, `HANKO_UPDATE_REPO`, `HANKO_UPDATE_TOKEN`.
- English summary: new `hanko doctor`, `update` (backup first, sha256, atomic swap, rollback), `service` (systemd/launchd) and `backup` (create/list/verify/restore/schedule) commands, plus a Settings › Backups screen with history and a system-flow schedule.

### `hanko serve --embedded-pg` para desenvolvimento e trial (ADR-0063, #47)
- Sobe um Postgres local descartável com `initdb`/`pg_ctl` de uma instalação do PostgreSQL (nada é baixado), senha por arquivo, só em 127.0.0.1, e roda o servidor em cima dele. Recusa `HANKO_DATABASE_URL` e root. Produção continua com Postgres externo.
- English summary: `hanko serve --embedded-pg` starts a disposable local Postgres from locally installed binaries (nothing downloaded), for dev/trial only; production keeps an external Postgres.

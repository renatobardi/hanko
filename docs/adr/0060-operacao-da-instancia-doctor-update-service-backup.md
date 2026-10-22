# ADR-0060 — Operação da instância: doctor, update, service e backup
Data: 2026-10-22 · Status: aceita · Complementa: ADR-0050, ADR-0051, ADR-0058 · Itens do plano: #64, #65

## Contexto
Operar uma instalação do Hanko dependia de saber o que olhar (Postgres, migrations, OTel, memória, harnesses, segredos,
disco), de trocar o binário à mão, de escrever a unit do systemd por conta própria e de scripts de host
(`deploy/oute-server/backup.sh`, restic) para backup. O dono quer comandos do próprio produto, testáveis e iguais em
qualquer instalação, e uma tela para o histórico e o agendamento dos backups. O backup do host (restic) e o drill de
restore no CI continuam existindo.

## Decisão
### A. Comandos locais (rodam no servidor, sem a API, funcionam com o servidor parado)
1. `hanko doctor` (`internal/ops`): checagens de config, Postgres (ping e versão), migrations (pendentes por conjunto,
   não por ordem numérica, pois as trilhas chegam fora de ordem; banco mais novo que o binário vira aviso), OTel (TCP no
   coletor), ai-memory (`/health`), harnesses de CLI no PATH, segredos (`HANKO_SECRET`, `HANKO_SECRET_KEY`, par do cofre;
   nunca imprime valores), disco (workspaces e backups: aviso < 5 GiB, falha < 512 MiB), pg_dump/pg_restore e idade do
   último backup. Saída 1 só com falha; aviso não conta. `--json` para máquinas.
2. `hanko update --channel stable|beta` (`internal/ops/update.go`): consulta releases do GitHub (stable = tag sem sufixo;
   beta = inclui pré-releases; `edge` é imagem de contêiner e não tem binário de release), **faz backup antes e aborta se
   o backup falhar**, baixa o tar.gz do goreleaser, confere o sha256 contra `checksums.txt` (sem checksums não instala),
   troca o executável com rename atômico e guarda o anterior em `<exe>.prev` (`--rollback`). `--check` só consulta.
   Recusa binário de desenvolvimento (sem `--force`) e contêiner (a imagem é que se atualiza). As migrations rodam no
   próximo start (`HANKO_MIGRATE_ON_BOOT`). Token opcional para repositório privado: `HANKO_UPDATE_TOKEN`/`GITHUB_TOKEN`;
   repositório: `HANKO_UPDATE_REPO` (padrão `renatobardi/hanko`).
3. `hanko service install|uninstall|status|restart` (`internal/ops/service.go`): gera e instala a unit do systemd
   (sistema ou `--user`) ou o plist do launchd (LaunchDaemon ou LaunchAgent) que roda `hanko serve`, com arquivo de
   ambiente 0600 criado só se não existir (segredos nunca vão para a unit, e `uninstall` não apaga o arquivo). Entradas
   validadas contra injeção de linha. `hanko service print compose|timer` só imprime units (stack docker compose e
   timer): o deploy em contêiner as grava no host sem ter o `hanko` instalado nele.
4. `hanko backup create|list|verify|restore|schedule` (`internal/backup`). Detalhe em B.

Todo o mundo externo entra por interfaces: `syscmd.Runner` (pg_dump, pg_restore, systemctl, launchctl), `ops.Source`
(releases), `backup.Blobs` (conteúdo em S3), `ops.DBProbe`. Os testes usam fakes e `httptest`; nenhum teste chama systemd,
GitHub nem pg_dump de verdade.

### B. Backup
- Formato: um tar `hanko-<UTC>.tar` em `HANKO_BACKUP_DIR` (padrão `<HANKO_HOME>/backups`, 0700) com `db.pgdump`
  (`pg_dump -Fc`, que já leva `artifact_blobs` do driver postgres), `blobs/<empresa>/<artefato>` para o que está em drivers
  externos (S3) e `manifest.json` por último (versão, schema aplicado, sha256 e tamanho de cada membro). Um `.json` ao lado
  duplica o manifest para o `list` não ler o tar inteiro. Escrita em diretório temporário e rename: nada incompleto aparece.
- Conteúdo em driver não configurado neste servidor faz o backup **falhar** (melhor que um backup que parece completo).
- A senha do banco vai por variáveis `PG*`, nunca em argv.
- `verify` confere cada membro; `restore` verifica antes de tocar no banco, recusa schema do backup mais novo que o
  binário (`--force`), exige `--yes`, recusa com outras conexões ao banco (servidor no ar; `--force`), roda
  `pg_restore --clean --if-exists --single-transaction` e devolve o conteúdo externo. Backup mais antigo pede `hanko migrate`.
- Retenção: `HANKO_BACKUP_KEEP` (padrão 7) poda só os automáticos (agendado, pré-atualização, pré-deploy); manuais ficam.
- A imagem de contêiner é distroless e não tem `pg_dump`: nela o backup pela tela fica desligado (o servidor loga o motivo e a
  tela explica) e vale o backup do host (`backup.sh` + restic). Instalações em binário/host usam tudo.

### C. Tela e agendamento (instalação, não empresa)
- Tabelas sem `company_id`, como `instance_settings`: `instance_backups` (histórico, quem pediu, erro, arquivo) e
  `system_flows` (fluxo de sistema). Só o dono da instalação lê e dispara; mudanças de agendamento vão para
  `instance_setting_audit` (chave `system_flow.backup`). Isso substitui o `activity_event` por empresa, que não faz sentido
  para uma escrita que não pertence a nenhuma.
- **Fluxo de sistema**: tarefa periódica da própria instalação, sem persona nem task, com a mesma semântica de cron dos
  fluxos de empresa (`ParseCron`/`NextCron`, fuso IANA, catch-up dispara só a ocorrência mais recente, a agenda avança
  antes de executar, então no máximo uma vez por ocorrência). Um índice parcial garante um único backup em andamento e
  `scheduled_for` único evita duplicata entre nós; um "em andamento" com mais de 2 h é de um processo morto e é liberado.
- O disparo é o job `system_flow_tick` do heartbeat, separado do `flow_tick` porque um backup dura minutos e o job tem
  teto de 2 h (o padrão do River é 1 min). Backup manual pela tela roda em segundo plano e responde 202.
- Tela Configurações › Backups; API `GET/POST /instance/backups`, `GET/PUT /instance/backup-schedule`.

## Alternativas descartadas
- Reaproveitar `flows` com persona nula: mistura cron de empresa com tarefa da instância, exige `company_id` e task.
- Backup em Go puro (sem pg_dump): reimplementaria o formato custom; o pg_dump é a ferramenta certa e o drill do CI o usa.
- `update` trocando o binário dentro do contêiner: some no próximo `up`; a imagem é a unidade de atualização.
- `edge` como canal do `update`: não publica binário de release, só imagem e artefato de CI.

## Consequências
Duas migrations aditivas (00180). Env novas: `HANKO_HOME`, `HANKO_BACKUP_DIR`, `HANKO_BACKUP_KEEP`, `HANKO_PG_DUMP_BIN`,
`HANKO_PG_RESTORE_BIN`; `HANKO_UPDATE_CHANNEL/REPO/TOKEN` (só a CLI). Sem dependência Go nova. O `deploy-prd.sh` passa a
gerar as units com `service print`, rodar `doctor` e `update --check` no contêiner; o `backup.sh` e o restic seguem.

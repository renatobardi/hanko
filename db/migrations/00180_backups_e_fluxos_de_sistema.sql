-- +goose Up
-- Backups da instalação e fluxos de sistema (ADR-0060). São da INSTÂNCIA, não de uma empresa (como instance_settings e
-- users): um backup leva o banco inteiro. Quem pode ler e mudar é só o dono da instalação; a trilha das mudanças de
-- agendamento vai em instance_setting_audit (chave system_flow.backup) e cada backup guarda quem o pediu.

-- Histórico: uma linha por backup feito pelo servidor (manual, agendado, antes de atualizar). O arquivo em si mora
-- em HANKO_BACKUP_DIR; `hanko backup list` lê os arquivos, não esta tabela (funciona com o servidor parado).
CREATE TABLE instance_backups (
    id             uuid PRIMARY KEY,
    kind           text NOT NULL CHECK (kind IN ('manual', 'scheduled', 'pre_update', 'pre_deploy')),
    status         text NOT NULL CHECK (status IN ('running', 'ok', 'failed')),
    label          text NOT NULL DEFAULT '',
    file_name      text NOT NULL DEFAULT '',
    size_bytes     bigint NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
    schema_version bigint NOT NULL DEFAULT 0,
    blobs          integer NOT NULL DEFAULT 0 CHECK (blobs >= 0),
    error          text NOT NULL DEFAULT '',
    actor_type     text NOT NULL CHECK (actor_type IN ('human', 'persona', 'system')),
    actor_id       uuid,
    -- ocorrência do agendamento que originou o backup; único = o mesmo disparo nunca gera dois arquivos
    scheduled_for  timestamptz,
    started_at     timestamptz NOT NULL DEFAULT now(),
    finished_at    timestamptz
);
CREATE UNIQUE INDEX instance_backups_scheduled_uq ON instance_backups (scheduled_for) WHERE scheduled_for IS NOT NULL;
-- no máximo um backup em andamento por instalação
CREATE UNIQUE INDEX instance_backups_one_running_uq ON instance_backups ((true)) WHERE status = 'running';
CREATE INDEX instance_backups_started_idx ON instance_backups (started_at DESC, id);

-- Fluxos de sistema: tarefas periódicas da própria instalação (sem persona nem task). Mesma semântica de cron dos
-- fluxos de empresa (fuso IANA, catch-up só da ocorrência mais recente, agenda sempre avança). Hoje há um: backup.
CREATE TABLE system_flows (
    key           text PRIMARY KEY CHECK (key IN ('backup')),
    cron          text NOT NULL,
    timezone      text NOT NULL,
    enabled       boolean NOT NULL DEFAULT false,
    last_fired_at timestamptz,
    next_fire_at  timestamptz,
    updated_by    uuid,
    updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX system_flows_due_idx ON system_flows (next_fire_at) WHERE enabled;

-- +goose Down
DROP TABLE system_flows;
DROP TABLE instance_backups;

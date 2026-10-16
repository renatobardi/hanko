-- +goose Up
-- Plugins de terceiros (ADR-0038). Um plugin é instalado na INSTALAÇÃO (pelo dono) e ligado por EMPRESA (pelo
-- admin dela). O processo do plugin nunca acessa o banco: tudo passa por RPC do host, checado contra as
-- capacidades concedidas.
CREATE TABLE plugins (
    id                   uuid PRIMARY KEY,
    plugin_key           text NOT NULL UNIQUE CHECK (plugin_key ~ '^[a-z][a-z0-9-]{1,62}$'),
    version              text NOT NULL CHECK (version <> ''),
    api_version          integer NOT NULL,
    manifest             jsonb NOT NULL,
    -- realpath do diretório do plugin, dentro de HANKO_PLUGINS_DIR
    dir                  text NOT NULL,
    status               text NOT NULL CHECK (status IN ('installed','ready','disabled','error','upgrade_pending','uninstalled')),
    granted_capabilities text[] NOT NULL DEFAULT '{}',
    -- upgrade que pede capacidade a mais: o manifesto novo e as capacidades que ele pede esperam o dono aprovar
    pending_capabilities text[] NOT NULL DEFAULT '{}',
    pending_manifest     jsonb,
    -- eventos de atividade assinados (cópia de manifest.events, para o filtro da entrega)
    events               text[] NOT NULL DEFAULT '{}',
    last_error           text NOT NULL DEFAULT '',
    installed_by         uuid REFERENCES users(id) ON DELETE SET NULL,
    installed_at         timestamptz NOT NULL,
    updated_at           timestamptz NOT NULL
);

CREATE TABLE plugin_company_settings (
    plugin_id   uuid NOT NULL REFERENCES plugins(id) ON DELETE CASCADE,
    company_id  uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    enabled     boolean NOT NULL DEFAULT false,
    -- só os campos que não são segredo; os secretos ficam em secret_enc (AES-GCM, AAD = plugin + empresa)
    config      jsonb NOT NULL DEFAULT '{}'::jsonb,
    secret_enc  text NOT NULL DEFAULT '',
    updated_by  uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL,
    PRIMARY KEY (plugin_id, company_id)
);
CREATE INDEX plugin_company_settings_company_idx ON plugin_company_settings (company_id) WHERE enabled;

CREATE TABLE plugin_state (
    plugin_id   uuid NOT NULL REFERENCES plugins(id) ON DELETE CASCADE,
    company_id  uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    key         text NOT NULL CHECK (key <> '' AND length(key) <= 200),
    value       jsonb NOT NULL,
    updated_at  timestamptz NOT NULL,
    PRIMARY KEY (plugin_id, company_id, key)
);

CREATE TABLE plugin_logs (
    id          uuid PRIMARY KEY,
    plugin_id   uuid NOT NULL REFERENCES plugins(id) ON DELETE CASCADE,
    company_id  uuid REFERENCES companies(id) ON DELETE CASCADE,
    level       text NOT NULL CHECK (level IN ('debug','info','warn','error','stderr','host')),
    message     text NOT NULL CHECK (length(message) <= 4096),
    created_at  timestamptz NOT NULL
);
CREATE INDEX plugin_logs_plugin_idx ON plugin_logs (plugin_id, created_at DESC, id DESC);

CREATE TABLE plugin_job_runs (
    id          uuid PRIMARY KEY,
    plugin_id   uuid NOT NULL REFERENCES plugins(id) ON DELETE CASCADE,
    company_id  uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    job_key     text NOT NULL,
    status      text NOT NULL CHECK (status IN ('running','done','failed')),
    started_at  timestamptz NOT NULL,
    finished_at timestamptz,
    error       text NOT NULL DEFAULT ''
);
CREATE INDEX plugin_job_runs_last_idx ON plugin_job_runs (plugin_id, company_id, job_key, started_at DESC);
-- sem sobreposição: no máximo uma execução em andamento por (plugin, empresa, job)
CREATE UNIQUE INDEX plugin_job_runs_running_uq ON plugin_job_runs (plugin_id, company_id, job_key) WHERE status = 'running';

-- +goose Down
DROP TABLE plugin_job_runs;
DROP TABLE plugin_logs;
DROP TABLE plugin_state;
DROP TABLE plugin_company_settings;
DROP TABLE plugins;

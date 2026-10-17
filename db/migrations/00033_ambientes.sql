-- +goose Up
-- Ambientes de execução remotos: SSH e sandbox (ADR-0037). O ambiente local é implícito (sem linha) e é o
-- padrão quando a empresa não marca outro.

-- config guarda só o que não é segredo (host, porta, usuário, pasta remota, known_hosts; imagem e limites do
-- sandbox; nomes das variáveis). secret_enc é o JSON da chave privada e dos valores das variáveis, cifrado
-- (AES-GCM, HANKO_SECRET_KEY, AAD = id) e nunca devolvido.
CREATE TABLE environments (
    id                uuid PRIMARY KEY,
    company_id        uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    slug              text NOT NULL,
    name              text NOT NULL,
    description       text NOT NULL DEFAULT '',
    driver            text NOT NULL CHECK (driver IN ('local','ssh','sandbox')),
    status            text NOT NULL DEFAULT 'active' CHECK (status IN ('active','archived')),
    config            jsonb NOT NULL DEFAULT '{}',
    secret_enc        text NOT NULL DEFAULT '',
    is_default        boolean NOT NULL DEFAULT false,
    last_probe_at     timestamptz,
    last_probe_ok     boolean,
    last_probe_report jsonb,
    created_by_type   text NOT NULL CHECK (created_by_type IN ('human','persona','system')),
    created_by_id     uuid,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (company_id, slug),
    CHECK (NOT is_default OR status = 'active')
);
-- No máximo um ambiente padrão por empresa.
CREATE UNIQUE INDEX environments_one_default ON environments (company_id) WHERE is_default;
CREATE INDEX environments_company_idx ON environments (company_id, status, name);

-- Onde as runs da persona rodam; nulo = o padrão da empresa (ou o local).
ALTER TABLE personas ADD COLUMN environment_id uuid REFERENCES environments(id) ON DELETE SET NULL;
CREATE INDEX personas_environment_idx ON personas (environment_id) WHERE environment_id IS NOT NULL;

-- Uso de um ambiente por uma run: o que está em uso agora (tela, limpeza) e o histórico recente.
CREATE TABLE environment_leases (
    id             uuid PRIMARY KEY,
    company_id     uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    environment_id uuid NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    run_id         uuid NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    status         text NOT NULL DEFAULT 'active' CHECK (status IN ('active','released','failed')),
    remote_dir     text NOT NULL DEFAULT '',
    acquired_at    timestamptz NOT NULL DEFAULT now(),
    released_at    timestamptz,
    failure_reason text NOT NULL DEFAULT '',
    UNIQUE (run_id)
);
CREATE INDEX environment_leases_env_idx ON environment_leases (environment_id, acquired_at DESC, id DESC);
CREATE INDEX environment_leases_active_idx ON environment_leases (company_id) WHERE status = 'active';

-- +goose Down
DROP TABLE environment_leases;
DROP INDEX personas_environment_idx;
ALTER TABLE personas DROP COLUMN environment_id;
DROP TABLE environments;

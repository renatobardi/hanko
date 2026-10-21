-- +goose Up
-- M11 (ADR-0055): ambientes explícitos e cloud, ambiente por projeto com origem gravada na run, readiness da
-- persona e catálogo de modelos. Aditiva e retrocompatível: nada do que existe é reescrito.

-- 1. Drivers: o local passa a poder ser uma linha explícita (para forçar o local numa persona/projeto quando o
--    padrão da empresa é remoto) e entra o sandbox cloud ("cloud"). "sandbox" segue sendo o contêiner docker.
ALTER TABLE environments DROP CONSTRAINT environments_driver_check;
ALTER TABLE environments ADD CONSTRAINT environments_driver_check CHECK (driver IN ('local','ssh','sandbox','cloud'));

-- 2. Ambiente do projeto. Precedência: persona > projeto > padrão da empresa > local.
ALTER TABLE projects ADD COLUMN environment_id uuid REFERENCES environments(id) ON DELETE SET NULL;
CREATE INDEX projects_environment_idx ON projects (environment_id) WHERE environment_id IS NOT NULL;

-- 3. A run grava onde vai rodar e por quê (decidido na criação da run). Vazio = run anterior ao M11 (o executor
--    resolve na hora). environment_id nulo com source 'local' = o local implícito.
ALTER TABLE runs ADD COLUMN environment_id uuid REFERENCES environments(id) ON DELETE SET NULL;
ALTER TABLE runs ADD COLUMN environment_source text NOT NULL DEFAULT ''
    CHECK (environment_source IN ('', 'persona', 'project', 'default', 'local'));

-- 4. Readiness da persona: o último resultado do probe do harness (binário, auth, modelo) com os passos
--    nomeados. Uma linha por persona (sobrescrita a cada teste); not_ready bloqueia o despacho com o motivo.
CREATE TABLE persona_readiness (
    persona_id         uuid PRIMARY KEY REFERENCES personas(id) ON DELETE CASCADE,
    company_id         uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    status             text NOT NULL CHECK (status IN ('ready','not_ready')),
    agent              text NOT NULL,
    model              text NOT NULL DEFAULT '',
    environment_id     uuid REFERENCES environments(id) ON DELETE SET NULL,
    environment_source text NOT NULL DEFAULT 'local' CHECK (environment_source IN ('persona','project','default','local')),
    steps              jsonb NOT NULL DEFAULT '[]',
    reason             text NOT NULL DEFAULT '',
    trigger            text NOT NULL CHECK (trigger IN ('manual','periodic','hire','update')),
    duration_ms        integer NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
    checked_at         timestamptz NOT NULL,
    failing_since      timestamptz
);
CREATE INDEX persona_readiness_company_idx ON persona_readiness (company_id, status, checked_at);

-- 5. Modelos que um probe viu (catálogo híbrido junto com model_prices): por empresa e harness.
CREATE TABLE model_reports (
    company_id   uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    agent        text NOT NULL,
    model        text NOT NULL,
    source       text NOT NULL CHECK (source IN ('probe','run')),
    last_seen_at timestamptz NOT NULL,
    PRIMARY KEY (company_id, agent, model)
);

-- 6. Alerta de persona sem condição de rodar.
ALTER TABLE alerts DROP CONSTRAINT alerts_kind_check;
ALTER TABLE alerts ADD CONSTRAINT alerts_kind_check CHECK (kind IN ('persona_silent','run_stuck','budget_near_limit','persona_not_ready'));

-- +goose Down
ALTER TABLE alerts DROP CONSTRAINT alerts_kind_check;
DELETE FROM alerts WHERE kind = 'persona_not_ready';
ALTER TABLE alerts ADD CONSTRAINT alerts_kind_check CHECK (kind IN ('persona_silent','run_stuck','budget_near_limit'));
DROP TABLE model_reports;
DROP TABLE persona_readiness;
ALTER TABLE runs DROP COLUMN environment_source;
ALTER TABLE runs DROP COLUMN environment_id;
DROP INDEX projects_environment_idx;
ALTER TABLE projects DROP COLUMN environment_id;
UPDATE environments SET status = 'archived', is_default = false WHERE driver IN ('local','cloud');
UPDATE personas SET environment_id = NULL WHERE environment_id IN (SELECT id FROM environments WHERE driver IN ('local','cloud'));
DELETE FROM environments WHERE driver IN ('local','cloud');
ALTER TABLE environments DROP CONSTRAINT environments_driver_check;
ALTER TABLE environments ADD CONSTRAINT environments_driver_check CHECK (driver IN ('local','ssh','sandbox'));

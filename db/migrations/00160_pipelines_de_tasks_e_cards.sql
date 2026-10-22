-- +goose Up
-- Pipelines sobre tasks e cards de status com decision model, cron e resultado em artefato (ADR-0048, M20).
-- Tudo aditivo: nenhuma tabela existente perde coluna; as constraints recriadas só ganham valores.

-- ---------- #45: etapas por projeto ----------

-- Etapas do projeto, em ordem. A etapa mapeia um status de task (todo, in_progress ou in_review) e, opcionalmente, uma
-- persona; exit_gate diz o que para o avanço quando a etapa termina: nada, uma aprovação humana ou a revisão da task
-- (task_review, com a revisão automática da política vigente).
CREATE TABLE project_stages (
    id           uuid PRIMARY KEY,
    company_id   uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    project_id   uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    key          text NOT NULL CHECK (key ~ '^[a-z][a-z0-9_-]{0,39}$'),
    name         text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    position     integer NOT NULL CHECK (position >= 1),
    status       text NOT NULL CHECK (status IN ('todo','in_progress','in_review')),
    persona_id   uuid REFERENCES personas(id) ON DELETE SET NULL,
    exit_gate    text NOT NULL DEFAULT 'none' CHECK (exit_gate IN ('none','approval','review')),
    instructions text NOT NULL DEFAULT '' CHECK (char_length(instructions) <= 8192),
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (project_id, key),
    UNIQUE (project_id, position) DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX project_stages_company_idx ON project_stages (company_id, project_id, position);

-- Onde cada task está no pipeline do projeto. working = a persona da etapa trabalha; completing = a etapa terminou e um
-- gate decide o avanço; finished = atravessou a última etapa (ou saiu do pipeline).
CREATE TABLE task_stages (
    task_id      uuid PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
    company_id   uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    project_id   uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    stage_id     uuid NOT NULL REFERENCES project_stages(id) ON DELETE CASCADE,
    state        text NOT NULL DEFAULT 'working' CHECK (state IN ('working','completing','finished')),
    entered_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX task_stages_stage_idx ON task_stages (stage_id) WHERE state <> 'finished';
CREATE INDEX task_stages_company_idx ON task_stages (company_id, project_id);

-- Histórico imutável do pipeline de cada task. As decisões de gate (kind gate_decided, com a nota) são os aprendizados
-- da etapa: voltam no briefing da persona e viram fato de memória.
CREATE TABLE task_stage_events (
    id             uuid PRIMARY KEY,
    company_id     uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    project_id     uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    task_id        uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    kind           text NOT NULL CHECK (kind IN ('enrolled','completed','gate_requested','gate_decided','advanced','returned','finished','left')),
    stage_key      text NOT NULL DEFAULT '',
    to_stage_key   text NOT NULL DEFAULT '',
    actor_type     text NOT NULL CHECK (actor_type IN ('human','persona','system')),
    actor_id       uuid,
    gate_id        uuid REFERENCES gates(id) ON DELETE SET NULL,
    decision       text NOT NULL DEFAULT '',
    note           text NOT NULL DEFAULT '' CHECK (char_length(note) <= 100000),
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX task_stage_events_task_idx ON task_stage_events (task_id, created_at);
CREATE INDEX task_stage_events_learning_idx ON task_stage_events (project_id, stage_key, created_at DESC) WHERE kind = 'gate_decided';
CREATE TRIGGER task_stage_events_immutable BEFORE UPDATE ON task_stage_events
    FOR EACH ROW EXECUTE FUNCTION hanko_revision_immutable();

-- ---------- #46: cards de status ----------

-- engine: quem escreve o resumo. persona = a resumidora (ADR-0032); decision = o decision model da empresa, numa chamada
-- só, sobre o retrato das tasks (feature card_summary). artifact_name/artifact_id: o resultado também vira um artefato
-- versionado (uma versão por atualização); on_dashboard mostra o card no Painel.
ALTER TABLE status_cards ADD COLUMN engine        text    NOT NULL DEFAULT 'persona' CHECK (engine IN ('persona','decision'));
ALTER TABLE status_cards ADD COLUMN artifact_name text    NOT NULL DEFAULT '' CHECK (char_length(artifact_name) <= 200);
ALTER TABLE status_cards ADD COLUMN artifact_id   uuid    REFERENCES artifacts(id) ON DELETE SET NULL;
ALTER TABLE status_cards ADD COLUMN on_dashboard  boolean NOT NULL DEFAULT false;
CREATE INDEX status_cards_dashboard_idx ON status_cards (company_id) WHERE on_dashboard AND archived_at IS NULL;

-- Gatilho da rodada: o cron é um gatilho novo.
ALTER TABLE status_card_updates DROP CONSTRAINT status_card_updates_trigger_check;
ALTER TABLE status_card_updates ADD CONSTRAINT status_card_updates_trigger_check
    CHECK (trigger IN ('manual','interval','reactive','restore','query','cron'));

-- Feature nova do decision model (a lista COMPLETA vigente, 00080, mais card_summary).
ALTER TABLE decision_features DROP CONSTRAINT decision_features_feature_check;
ALTER TABLE decision_features ADD CONSTRAINT decision_features_feature_check
    CHECK (feature IN ('triage','phase_classifier','name_summary','card_summary'));
ALTER TABLE decision_invocations DROP CONSTRAINT decision_invocations_feature_check;
ALTER TABLE decision_invocations ADD CONSTRAINT decision_invocations_feature_check
    CHECK (feature IN ('triage','phase_classifier','name_summary','card_summary'));

-- +goose Down
ALTER TABLE decision_invocations DROP CONSTRAINT decision_invocations_feature_check;
ALTER TABLE decision_invocations ADD CONSTRAINT decision_invocations_feature_check
    CHECK (feature IN ('triage','phase_classifier','name_summary'));
ALTER TABLE decision_features DROP CONSTRAINT decision_features_feature_check;
ALTER TABLE decision_features ADD CONSTRAINT decision_features_feature_check
    CHECK (feature IN ('triage','phase_classifier','name_summary'));
ALTER TABLE status_card_updates DROP CONSTRAINT status_card_updates_trigger_check;
ALTER TABLE status_card_updates ADD CONSTRAINT status_card_updates_trigger_check
    CHECK (trigger IN ('manual','interval','reactive','restore','query'));
DROP INDEX status_cards_dashboard_idx;
ALTER TABLE status_cards DROP COLUMN on_dashboard;
ALTER TABLE status_cards DROP COLUMN artifact_id;
ALTER TABLE status_cards DROP COLUMN artifact_name;
ALTER TABLE status_cards DROP COLUMN engine;
DROP TABLE task_stage_events;
DROP TABLE task_stages;
DROP TABLE project_stages;

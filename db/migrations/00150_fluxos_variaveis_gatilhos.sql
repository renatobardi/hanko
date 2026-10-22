-- +goose Up
-- Variáveis, gatilhos, revisões e arquivar de Fluxos (ADR-0047, M19 · itens #22 e #23). Aditiva.

-- Variáveis declaradas: [{"name","label","required","default"}]. Revisão vigente e arquivamento (≠ desativado).
-- O cron passa a ser opcional por convenção: cron = '' é "sem agenda" (next_fire_at nulo); fluxos existentes
-- continuam com o cron preenchido.
ALTER TABLE flows ADD COLUMN variables jsonb NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE flows ADD COLUMN revision integer NOT NULL DEFAULT 1 CHECK (revision >= 1);
ALTER TABLE flows ADD COLUMN archived_at timestamptz;
CREATE INDEX flows_archived_idx ON flows (company_id) WHERE archived_at IS NOT NULL;

-- Gatilhos de um fluxo: webhook de entrada (HMAC) e evento interno (verbo de atividade). A agenda continua em flows.cron.
CREATE TABLE flow_triggers (
    id            uuid PRIMARY KEY,
    company_id    uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    flow_id       uuid NOT NULL REFERENCES flows(id) ON DELETE CASCADE,
    kind          text NOT NULL CHECK (kind IN ('webhook', 'event')),
    name          text NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
    enabled       boolean NOT NULL DEFAULT true,
    -- ${secret:NOME} do HMAC (webhook); o valor mora em secrets (ADR-0050).
    secret_ref    text NOT NULL DEFAULT '',
    rate_per_min  integer NOT NULL DEFAULT 30 CHECK (rate_per_min BETWEEN 1 AND 600),
    -- evento: verbo exato ("gate.decided") ou prefixo ("gate.*") e filtro {chave: texto} sobre data do fato.
    event_verb    text NOT NULL DEFAULT '',
    event_filter  jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- {variável: "caminho.pontuado"} da carga (webhook) ou do fato (evento).
    mapping       jsonb NOT NULL DEFAULT '{}'::jsonb,
    last_fired_at timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (flow_id, name),
    CHECK ((kind = 'webhook' AND event_verb = '') OR (kind = 'event' AND event_verb <> '' AND secret_ref = ''))
);
CREATE INDEX flow_triggers_flow_idx ON flow_triggers (flow_id);
CREATE INDEX flow_triggers_event_idx ON flow_triggers (company_id) WHERE kind = 'event' AND enabled;

-- Origem de cada disparo. A chave única da agenda e do "Rodar agora" continua por (fluxo, instante, manual) mas só entre
-- eles; webhook e evento deduplicam por dedupe_key (id da entrega / id da atividade).
ALTER TABLE flow_fires ADD COLUMN source text NOT NULL DEFAULT 'schedule' CHECK (source IN ('schedule', 'manual', 'webhook', 'event'));
UPDATE flow_fires SET source = 'manual' WHERE manual;
ALTER TABLE flow_fires ADD COLUMN trigger_id uuid REFERENCES flow_triggers(id) ON DELETE SET NULL;
ALTER TABLE flow_fires ADD COLUMN dedupe_key text NOT NULL DEFAULT '';
ALTER TABLE flow_fires ADD COLUMN variables jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE flow_fires DROP CONSTRAINT flow_fires_flow_sched_manual_key;
CREATE UNIQUE INDEX flow_fires_flow_sched_manual_key ON flow_fires (flow_id, scheduled_for, manual) WHERE source IN ('schedule', 'manual');
CREATE UNIQUE INDEX flow_fires_dedupe_key ON flow_fires (flow_id, dedupe_key) WHERE dedupe_key <> '';

-- Revisões append-only da definição (mesmo gatilho do ADR-0052: recusa UPDATE).
CREATE TABLE flow_revisions (
    id            uuid PRIMARY KEY,
    company_id    uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    flow_id       uuid NOT NULL REFERENCES flows(id) ON DELETE CASCADE,
    revision      integer NOT NULL CHECK (revision >= 1),
    snapshot      jsonb NOT NULL,
    summary       text NOT NULL DEFAULT '' CHECK (length(summary) <= 500),
    actor_type    text NOT NULL CHECK (actor_type IN ('human', 'persona', 'system')),
    actor_id      uuid,
    origin        text NOT NULL CHECK (origin IN ('backfill', 'create', 'edit', 'restore', 'import')),
    restored_from integer,
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (flow_id, revision)
);
CREATE INDEX flow_revisions_company_idx ON flow_revisions (company_id);
CREATE TRIGGER flow_revisions_immutable BEFORE UPDATE ON flow_revisions
    FOR EACH ROW EXECUTE FUNCTION hanko_revision_immutable();

-- Os fluxos existentes ganham a revisão 1 (a definição de hoje).
INSERT INTO flow_revisions (id, company_id, flow_id, revision, snapshot, summary, actor_type, origin, created_at)
SELECT gen_random_uuid(), f.company_id, f.id, 1,
       jsonb_build_object('name', f.name, 'description', f.description, 'cron', f.cron, 'timezone', f.timezone,
                          'persona_id', f.persona_id, 'project_id', f.project_id, 'priority', f.priority,
                          'task_title', f.task_title, 'task_description', f.task_description,
                          'variables', f.variables, 'triggers', '[]'::jsonb),
       '', 'system', 'backfill', f.updated_at
FROM flows f;

-- +goose Down
DROP TABLE flow_revisions;
DROP INDEX flow_fires_dedupe_key;
DROP INDEX flow_fires_flow_sched_manual_key;
DELETE FROM flow_fires WHERE source IN ('webhook', 'event');
ALTER TABLE flow_fires ADD CONSTRAINT flow_fires_flow_sched_manual_key UNIQUE (flow_id, scheduled_for, manual);
ALTER TABLE flow_fires DROP COLUMN variables, DROP COLUMN dedupe_key, DROP COLUMN trigger_id, DROP COLUMN source;
DROP TABLE flow_triggers;
DELETE FROM flows WHERE cron = '';
ALTER TABLE flows DROP COLUMN archived_at, DROP COLUMN revision, DROP COLUMN variables;

-- +goose Up
-- Alertas operacionais, nome amigável de execução e revisão por outro modelo antes do gate.
-- Vocabulário: docs/GLOSSARIO.md (ADR-0016).

-- Alertas operacionais (Caixa de entrada e /metrics). Um alerta aberto por condição:
-- (empresa, tipo, sujeito). O job alert_tick abre quando a condição aparece, atualiza enquanto
-- ela dura e fecha sozinho quando ela some (closed_at). Critérios em docs/RUNBOOK.md.
CREATE TABLE alerts (
    id            uuid PRIMARY KEY,
    company_id    uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    kind          text NOT NULL CHECK (kind IN ('persona_silent','run_stuck','budget_near_limit')),
    subject_type  text NOT NULL CHECK (subject_type IN ('persona','run','company')),
    subject_id    uuid NOT NULL,
    severity      text NOT NULL CHECK (severity IN ('warning','critical')),
    message       text NOT NULL,
    data          jsonb NOT NULL DEFAULT '{}'::jsonb,
    opened_at     timestamptz NOT NULL,
    last_seen_at  timestamptz NOT NULL,
    closed_at     timestamptz
);
CREATE UNIQUE INDEX alerts_open_uq ON alerts (company_id, kind, subject_id) WHERE closed_at IS NULL;
CREATE INDEX alerts_open_company_idx ON alerts (company_id, opened_at DESC) WHERE closed_at IS NULL;

-- Nome amigável da execução (Adjetivo_Substantivo, derivado do id; docs/GLOSSARIO.md). Único por empresa
-- quando preenchido; runs antigas ficam com '' até o backfill do boot nomeá-las.
ALTER TABLE runs ADD COLUMN name text NOT NULL DEFAULT '';
CREATE UNIQUE INDEX runs_company_name_uq ON runs (company_id, name) WHERE name <> '';

-- Revisão por outro modelo antes do gate humano (task_review). A persona autora aponta um revisor:
-- ou outra persona (reviewer_persona_id, com as próprias instruções, harness e modelo) ou o mesmo
-- papel rodando em outro harness/modelo (reviewer_agent + reviewer_model, ex.: autora claude, revisor codex).
-- Sem nenhum dos dois o comportamento é o de sempre: o gate abre direto.
ALTER TABLE personas ADD COLUMN reviewer_persona_id uuid REFERENCES personas(id) ON DELETE SET NULL;
ALTER TABLE personas ADD COLUMN reviewer_agent text NOT NULL DEFAULT '' CHECK (reviewer_agent IN ('', 'claude', 'codex', 'fake'));
ALTER TABLE personas ADD COLUMN reviewer_model text NOT NULL DEFAULT '';
ALTER TABLE personas ADD CONSTRAINT personas_reviewer_exclusive CHECK (reviewer_persona_id IS NULL OR reviewer_agent = '');
ALTER TABLE personas ADD CONSTRAINT personas_reviewer_not_self CHECK (reviewer_persona_id IS NULL OR reviewer_persona_id <> id);

-- Nova origem de run: a revisão headless que antecede o gate.
ALTER TABLE runs DROP CONSTRAINT runs_trigger_check;
ALTER TABLE runs ADD CONSTRAINT runs_trigger_check CHECK (trigger IN ('assign','comment','manual','heartbeat','gate','flow','review'));

-- Uma revisão por pedido de fechamento. Fica pending até a run da autora terminar (a task só tem uma
-- run ativa), running enquanto o revisor trabalha, e termina em done (veredito), failed (sem veredito
-- válido) ou cancelled (a task saiu de in_review). O gate só nasce ao fim, carregando o veredito.
CREATE TABLE reviews (
    id                    uuid PRIMARY KEY,
    company_id            uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    task_id               uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    author_persona_id     uuid NOT NULL REFERENCES personas(id) ON DELETE CASCADE,
    author_run_id         uuid REFERENCES runs(id) ON DELETE SET NULL,
    reviewer_persona_id   uuid REFERENCES personas(id) ON DELETE SET NULL,
    agent                 text NOT NULL,
    model                 text NOT NULL DEFAULT '',
    requested_status      text NOT NULL CHECK (requested_status IN ('done','cancelled')),
    status                text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','done','failed','cancelled')),
    run_id                uuid REFERENCES runs(id) ON DELETE SET NULL,
    artifact              text NOT NULL DEFAULT '',
    artifact_sha256       text NOT NULL DEFAULT '',
    verdict               text CHECK (verdict IS NULL OR verdict IN ('approved','rejected')),
    justification         text NOT NULL DEFAULT '',
    gate_id               uuid REFERENCES gates(id) ON DELETE SET NULL,
    created_at            timestamptz NOT NULL,
    started_at            timestamptz,
    finished_at           timestamptz
);
CREATE UNIQUE INDEX reviews_one_open_per_task ON reviews (task_id) WHERE status IN ('pending','running');
CREATE INDEX reviews_run_idx ON reviews (run_id) WHERE run_id IS NOT NULL;
CREATE INDEX reviews_open_idx ON reviews (created_at) WHERE status IN ('pending','running');

-- O veredito vai para o gate (denormalizado: o gate se lê sozinho). review_reviewer vazio = gate sem revisão.
-- Com revisão mas sem review_verdict, a revisão não concluiu e review_justification diz por quê.
ALTER TABLE gates ADD COLUMN review_reviewer text NOT NULL DEFAULT '';
ALTER TABLE gates ADD COLUMN review_verdict text CHECK (review_verdict IS NULL OR review_verdict IN ('approved','rejected'));
ALTER TABLE gates ADD COLUMN review_justification text NOT NULL DEFAULT '';
ALTER TABLE gates ADD COLUMN review_artifact_sha256 text NOT NULL DEFAULT '';
ALTER TABLE gates ADD COLUMN review_run_id uuid REFERENCES runs(id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE gates DROP COLUMN review_run_id;
ALTER TABLE gates DROP COLUMN review_artifact_sha256;
ALTER TABLE gates DROP COLUMN review_justification;
ALTER TABLE gates DROP COLUMN review_verdict;
ALTER TABLE gates DROP COLUMN review_reviewer;
DROP TABLE reviews;
UPDATE runs SET trigger = 'manual' WHERE trigger = 'review';
ALTER TABLE runs DROP CONSTRAINT runs_trigger_check;
ALTER TABLE runs ADD CONSTRAINT runs_trigger_check CHECK (trigger IN ('assign','comment','manual','heartbeat','gate','flow'));
ALTER TABLE personas DROP CONSTRAINT personas_reviewer_not_self;
ALTER TABLE personas DROP CONSTRAINT personas_reviewer_exclusive;
ALTER TABLE personas DROP COLUMN reviewer_model;
ALTER TABLE personas DROP COLUMN reviewer_agent;
ALTER TABLE personas DROP COLUMN reviewer_persona_id;
DROP INDEX runs_company_name_uq;
ALTER TABLE runs DROP COLUMN name;
DROP TABLE alerts;

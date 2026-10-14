-- +goose Up
-- Organograma + Gates (M3): revisão obrigatória por persona, gates com trilha de auditoria
-- e o gatilho `gate` das execuções. Vocabulário: docs/GLOSSARIO.md (ADR-0016).

ALTER TABLE personas ADD COLUMN require_review boolean NOT NULL DEFAULT false;

CREATE TABLE gates (
    id                uuid PRIMARY KEY,
    company_id        uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    kind              text NOT NULL CHECK (kind IN ('approval','task_review','hire')),
    status            text NOT NULL DEFAULT 'pending'
                      CHECK (status IN ('pending','approved','rejected','changes_requested','expired')),
    title             text NOT NULL CHECK (length(title) BETWEEN 1 AND 300),
    body              text NOT NULL DEFAULT '',
    task_id           uuid REFERENCES tasks(id) ON DELETE SET NULL,
    run_id            uuid REFERENCES runs(id) ON DELETE SET NULL,
    persona_id        uuid REFERENCES personas(id) ON DELETE SET NULL,
    requested_by_type text NOT NULL CHECK (requested_by_type IN ('human','persona','system')),
    requested_by_id   uuid,
    decided_by_type   text CHECK (decided_by_type IN ('human','persona','system')),
    decided_by_id     uuid,
    decision_note     text NOT NULL DEFAULT '',
    event_id          text,
    created_at        timestamptz NOT NULL DEFAULT now(),
    decided_at        timestamptz,
    CHECK ((status = 'pending') = (decided_at IS NULL))
);
CREATE INDEX gates_company_status_idx ON gates (company_id, status, created_at DESC);
CREATE INDEX gates_task_idx ON gates (task_id) WHERE task_id IS NOT NULL;
CREATE INDEX gates_persona_pending_idx ON gates (persona_id) WHERE status = 'pending' AND persona_id IS NOT NULL;
CREATE UNIQUE INDEX gates_event_uq ON gates (company_id, event_id) WHERE event_id IS NOT NULL;

ALTER TABLE runs DROP CONSTRAINT runs_trigger_check;
ALTER TABLE runs ADD CONSTRAINT runs_trigger_check CHECK (trigger IN ('assign','comment','manual','heartbeat','gate'));

-- +goose Down
UPDATE runs SET trigger = 'manual' WHERE trigger = 'gate';
ALTER TABLE runs DROP CONSTRAINT runs_trigger_check;
ALTER TABLE runs ADD CONSTRAINT runs_trigger_check CHECK (trigger IN ('assign','comment','manual','heartbeat'));
DROP TABLE gates;
ALTER TABLE personas DROP COLUMN require_review;

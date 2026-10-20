-- +goose Up
-- Resiliência de run (ADR-0039): continuação limitada de run interrompido, diagnóstico com decisão humana,
-- steer por capability e fila de follow-ups unificada (chat + task).

-- #12/#13: a cadeia de continuações e o estado de decisão vivem na própria run.
--   continuation_of_run_id / continuation_attempt / continuation_reason: de qual run esta é continuação (0 = original).
--   resume_pending / resume_reason: marca na run ANTIGA que a próxima run da task deve ser a continuação dela
--     (consumida no checkout; a rede de segurança do heartbeat também enxerga esta marca).
--   diagnosis: causa, tentativa, próxima ação, dono e o desfecho incerto (tool de efeito externo em andamento).
--   decision_pending / decision*: a run espera decisão humana (esgotou as tentativas ou o desfecho é incerto).
ALTER TABLE runs
    ADD COLUMN continuation_of_run_id uuid REFERENCES runs(id) ON DELETE SET NULL,
    ADD COLUMN continuation_attempt   integer NOT NULL DEFAULT 0 CHECK (continuation_attempt >= 0),
    ADD COLUMN continuation_reason    text NOT NULL DEFAULT ''
        CHECK (continuation_reason IN ('', 'recovery', 'steer', 'retry', 'continue')),
    ADD COLUMN resume_pending         boolean NOT NULL DEFAULT false,
    ADD COLUMN resume_reason          text NOT NULL DEFAULT ''
        CHECK (resume_reason IN ('', 'recovery', 'steer', 'retry', 'continue')),
    ADD COLUMN diagnosis              jsonb,
    ADD COLUMN decision_pending       boolean NOT NULL DEFAULT false,
    ADD COLUMN decision               text NOT NULL DEFAULT '' CHECK (decision IN ('', 'retry', 'continue', 'close')),
    ADD COLUMN decision_note          text NOT NULL DEFAULT '',
    ADD COLUMN decided_by_type        text CHECK (decided_by_type IS NULL OR decided_by_type IN ('human', 'persona', 'system')),
    ADD COLUMN decided_by_id          uuid,
    ADD COLUMN decided_at             timestamptz;
-- uma run tem no máximo uma continuação (idempotência do checkout e das ações do operador)
CREATE UNIQUE INDEX runs_one_continuation_uq ON runs (continuation_of_run_id) WHERE continuation_of_run_id IS NOT NULL;
CREATE INDEX runs_resume_pending_idx ON runs (task_id) WHERE resume_pending AND task_id IS NOT NULL;
CREATE INDEX runs_decision_pending_idx ON runs (company_id, created_at DESC) WHERE decision_pending;

-- #15: follow-ups de task (instruções que esperam a run ativa). Os do chat continuam em conversation_messages
-- (run_id NULL = na fila); o service unifica as duas filas.
CREATE TABLE task_followups (
    id              uuid PRIMARY KEY,
    company_id      uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    task_id         uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    author_type     text NOT NULL CHECK (author_type IN ('human', 'persona', 'system')),
    author_id       uuid,
    body            text NOT NULL CHECK (length(body) BETWEEN 1 AND 20000),
    status          text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'consumed', 'discarded')),
    -- queue: espera a run terminar; steer: injetar na run ativa; restart: interromper a run ativa e continuar
    delivery        text NOT NULL DEFAULT 'queue' CHECK (delivery IN ('queue', 'steer', 'restart')),
    -- a run ativa quando o follow-up entrou (o que ele espera)
    waiting_run_id  uuid REFERENCES runs(id) ON DELETE SET NULL,
    consumed_run_id uuid REFERENCES runs(id) ON DELETE SET NULL,
    consumed_at     timestamptz,
    comment_id      uuid,
    edited_at       timestamptz,
    discarded_at    timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX task_followups_pending_idx ON task_followups (task_id, created_at, id) WHERE status = 'pending';
CREATE INDEX task_followups_company_idx ON task_followups (company_id, created_at DESC);

ALTER TABLE conversation_messages
    ADD COLUMN edited_at timestamptz,
    ADD COLUMN delivery  text NOT NULL DEFAULT 'queue' CHECK (delivery IN ('queue', 'steer', 'restart'));

-- +goose Down
ALTER TABLE conversation_messages DROP COLUMN delivery, DROP COLUMN edited_at;
DROP TABLE task_followups;
DROP INDEX runs_decision_pending_idx;
DROP INDEX runs_resume_pending_idx;
DROP INDEX runs_one_continuation_uq;
ALTER TABLE runs
    DROP COLUMN decided_at, DROP COLUMN decided_by_id, DROP COLUMN decided_by_type, DROP COLUMN decision_note,
    DROP COLUMN decision, DROP COLUMN decision_pending, DROP COLUMN diagnosis, DROP COLUMN resume_reason,
    DROP COLUMN resume_pending, DROP COLUMN continuation_reason, DROP COLUMN continuation_attempt,
    DROP COLUMN continuation_of_run_id;

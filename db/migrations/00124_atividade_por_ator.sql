-- +goose Up
-- Atividade filtrável por ator (ADR-0042 §C) e a participação "responsável" das visões Minhas: quem atribuiu a task.
CREATE INDEX activity_events_actor_idx ON activity_events (company_id, actor_type, actor_id, occurred_at DESC, id DESC);
CREATE INDEX activity_events_assigned_idx ON activity_events (company_id, actor_id, subject_id) WHERE verb = 'task.assigned' AND actor_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS activity_events_assigned_idx;
DROP INDEX IF EXISTS activity_events_actor_idx;

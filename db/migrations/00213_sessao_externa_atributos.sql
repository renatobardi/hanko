-- +goose Up
-- Sessão externa (ADR-0078 item 2). Cada atributo da sessão guarda a hora do fato que o trouxe, como a conversa
-- (00208, item 7): o fato mais antigo troca o valor, um mais novo não. A sessão é o `oute.task.id` da fonte.
ALTER TABLE external_session ADD COLUMN oute_repo_at bigint CHECK (oute_repo_at >= 0);
ALTER TABLE external_session ADD COLUMN agent_at bigint CHECK (agent_at >= 0);
ALTER TABLE external_session ADD COLUMN round_at bigint CHECK (round_at >= 0);

-- +goose Down
ALTER TABLE external_session DROP COLUMN round_at, DROP COLUMN agent_at, DROP COLUMN oute_repo_at;

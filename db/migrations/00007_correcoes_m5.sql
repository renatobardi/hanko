-- +goose Up
-- Correções de M5: rede de segurança do heartbeat sem laço, expiração absoluta de sessão,
-- tempestade de wakes por reserva de orçamento e seq de eventos de run à prova de corrida.

-- Última mudança "não vista" feita por ator não-persona (status, atribuição, criação atribuída).
-- Só ela (mais comentário humano) alimenta a rede de segurança; edição de título/descrição/prioridade
-- e qualquer escrita de persona não contam.
ALTER TABLE tasks ADD COLUMN last_human_change_at timestamptz;
UPDATE tasks SET last_human_change_at = updated_at WHERE persona_id IS NOT NULL;

-- Quando a última recusa de checkout por reserva de orçamento aconteceu (a persona sai da rede de segurança por um tempo).
ALTER TABLE personas ADD COLUMN budget_refused_at timestamptz;

-- Teto absoluto da sessão (90 dias), além da validade deslizante.
ALTER TABLE sessions ADD COLUMN absolute_expires_at timestamptz;
UPDATE sessions SET absolute_expires_at = created_at + interval '90 days';
ALTER TABLE sessions ALTER COLUMN absolute_expires_at SET NOT NULL;

-- Próximo seq de run_events; alocado com UPDATE ... RETURNING, que serializa os escritores na linha da run.
ALTER TABLE runs ADD COLUMN next_event_seq bigint NOT NULL DEFAULT 1;
UPDATE runs r SET next_event_seq = COALESCE((SELECT max(e.seq) FROM run_events e WHERE e.run_id = r.id), 0) + 1;

-- +goose Down
ALTER TABLE runs DROP COLUMN next_event_seq;
ALTER TABLE sessions DROP COLUMN absolute_expires_at;
ALTER TABLE personas DROP COLUMN budget_refused_at;
ALTER TABLE tasks DROP COLUMN last_human_change_at;

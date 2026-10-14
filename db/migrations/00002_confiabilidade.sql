-- +goose Up
-- Confiabilidade: finalização pendente de runs, saúde da persona e backoff do outbox de memória.

-- Resultado de uma run guardado antes de FinishRun; permite finalizar sem reexecutar o harness.
ALTER TABLE runs ADD COLUMN pending_result jsonb;

-- Última falha da persona e falhas seguidas (error só após o limite; zera em sucesso).
ALTER TABLE personas ADD COLUMN last_error text;
ALTER TABLE personas ADD COLUMN last_error_at timestamptz;
ALTER TABLE personas ADD COLUMN consecutive_failures integer NOT NULL DEFAULT 0 CHECK (consecutive_failures >= 0);

-- Erro de rede mantém o fato pendente com próxima tentativa; 'failed' só para erro permanente.
ALTER TABLE memory_outbox ADD COLUMN next_attempt_at timestamptz;
UPDATE memory_outbox SET status = 'pending', next_attempt_at = now() WHERE status = 'failed' AND last_error NOT LIKE 'payload inválido%';

-- +goose Down
ALTER TABLE memory_outbox DROP COLUMN next_attempt_at;
ALTER TABLE personas DROP COLUMN consecutive_failures;
ALTER TABLE personas DROP COLUMN last_error_at;
ALTER TABLE personas DROP COLUMN last_error;
ALTER TABLE runs DROP COLUMN pending_result;

-- +goose Up
-- Correções da revisão (ADR-0030): a aplicação de uma proposta marca o início da tentativa antes de chamar o
-- provedor de memória, para que uma repetição depois de uma chamada bem-sucedida não seja cega.
ALTER TABLE memory_proposals ADD COLUMN attempt_started_at timestamptz;

-- +goose Down
ALTER TABLE memory_proposals DROP COLUMN attempt_started_at;

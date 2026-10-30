-- +goose Up
-- Janela da classificação de conversa externa (ADR-0079; auditoria do #158): a hora de recebimento da sessão
-- (external_session.updated_at, gravada a cada lote de fatos) que a conversa tinha quando foi classificada. NULL = nunca.
-- Não usa o relógio do fato: um fato com relógio atrasado também reabre a conversa.
ALTER TABLE external_conversation ADD COLUMN classified_session_at timestamptz;

-- +goose Down
ALTER TABLE external_conversation DROP COLUMN classified_session_at;

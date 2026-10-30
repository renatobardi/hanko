-- +goose Up
-- Janela da classificação de conversa externa (ADR-0079; auditoria do #158): a última hora de fato da sessão que a
-- conversa tinha quando foi classificada. A passada só relê conversas cuja sessão recebeu fato depois dessa hora.
ALTER TABLE external_conversation ADD COLUMN classified_last_at bigint NOT NULL DEFAULT 0 CHECK (classified_last_at >= 0);

-- +goose Down
ALTER TABLE external_conversation DROP COLUMN classified_last_at;

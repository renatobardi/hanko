-- +goose Up
-- /new no chat (ADR-0052): zera o contexto sem apagar nada. Uma mensagem de sistema com marker = 'context_reset' marca o
-- ponto: o histórico do prompt começa depois dele e nenhuma run anterior ao marcador retoma a sessão do harness.
ALTER TABLE conversation_messages ADD COLUMN marker text;
ALTER TABLE conversation_messages ADD CONSTRAINT conversation_messages_marker_check
    CHECK (marker IS NULL OR (marker = 'context_reset' AND author_type = 'system'));

-- +goose Down
ALTER TABLE conversation_messages DROP CONSTRAINT conversation_messages_marker_check;
ALTER TABLE conversation_messages DROP COLUMN marker;

-- +goose Up
-- Auditoria das operações administrativas da instalação (hanko auth user ...): quem operou, em quem, o quê e o
-- resultado. Sem FKs de propósito: o registro precisa sobreviver à remoção da conta-alvo (e do operador).
-- Nunca guarda senha nem token.
CREATE TABLE admin_audit (
    id           uuid PRIMARY KEY,
    at           timestamptz NOT NULL,
    actor_id     uuid,
    actor_email  text NOT NULL DEFAULT '',
    action       text NOT NULL CHECK (action <> ''),
    target_id    uuid,
    target_email text NOT NULL DEFAULT '',
    result       text NOT NULL CHECK (result <> '')
);
CREATE INDEX admin_audit_at_idx ON admin_audit (at DESC, id DESC);

-- +goose Down
DROP TABLE admin_audit;

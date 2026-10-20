-- +goose Up
-- Claim do primeiro dono (ADR-0058 §A): link de uso único impresso no boot quando não há usuário. O token nunca é
-- guardado, só o SHA-256. Sem company_id: é da instância, anterior a qualquer empresa (como users e admin_audit).
CREATE TABLE owner_claims (
    id         uuid PRIMARY KEY,
    token_hash text NOT NULL UNIQUE CHECK (length(token_hash) = 64),
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL CHECK (expires_at > created_at),
    used_at    timestamptz,
    used_by    uuid
);
CREATE INDEX owner_claims_open_idx ON owner_claims (expires_at) WHERE used_at IS NULL;

-- +goose Down
DROP TABLE owner_claims;

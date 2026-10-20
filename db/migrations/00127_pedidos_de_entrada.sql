-- +goose Up
-- Pedidos de entrada na empresa (ADR-0057): um link público por empresa e os pedidos de quem o abre.

CREATE TABLE company_join_links (
    company_id uuid PRIMARY KEY REFERENCES companies(id) ON DELETE CASCADE,
    token_hash text NOT NULL UNIQUE,
    enabled    boolean NOT NULL DEFAULT true,
    created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE company_join_requests (
    id         uuid PRIMARY KEY,
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    message    text NOT NULL DEFAULT '' CHECK (char_length(message) <= 500),
    status     text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'denied')),
    role       text CHECK (role IS NULL OR role IN ('owner', 'admin', 'member', 'viewer')),
    decided_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    decided_at timestamptz
);
-- Um pedido aberto por empresa e usuário.
CREATE UNIQUE INDEX company_join_requests_open_uq ON company_join_requests (company_id, user_id) WHERE status = 'pending';
CREATE INDEX company_join_requests_company_idx ON company_join_requests (company_id, created_at DESC);

-- +goose Down
DROP TABLE company_join_requests;
DROP TABLE company_join_links;

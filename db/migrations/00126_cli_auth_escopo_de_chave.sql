-- +goose Up
-- Login da CLI pelo navegador (ADR-0057): escopo e dispositivo na API key e os pedidos de autorização.

ALTER TABLE api_keys ADD COLUMN scope text NOT NULL DEFAULT 'full' CHECK (scope IN ('full', 'read'));
ALTER TABLE api_keys ADD COLUMN device text NOT NULL DEFAULT '' CHECK (char_length(device) <= 120);

-- code_hash: o código curto da URL /cli-auth/<código>; poll_hash: o segredo que só a CLI conhece. Só os hashes ficam.
CREATE TABLE cli_auth_requests (
    id         uuid PRIMARY KEY,
    code_hash  text NOT NULL UNIQUE,
    poll_hash  text NOT NULL UNIQUE,
    device     text NOT NULL CHECK (device <> '' AND char_length(device) <= 120),
    scope      text NOT NULL CHECK (scope IN ('full', 'read')),
    status     text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'denied', 'consumed')),
    user_id    uuid REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    decided_at timestamptz
);
CREATE INDEX cli_auth_requests_expires_idx ON cli_auth_requests (expires_at);

-- +goose Down
DROP TABLE cli_auth_requests;
ALTER TABLE api_keys DROP COLUMN device;
ALTER TABLE api_keys DROP COLUMN scope;

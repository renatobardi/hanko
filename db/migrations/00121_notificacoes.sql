-- +goose Up
-- Notificações por pessoa (ADR-0042): chaves da instância, assinaturas de web push e preferências por tipo.

-- Segredos da instalação gerados em runtime (hoje, o par de chaves VAPID do web push). O valor é cifrado com o
-- secretbox (HANKO_SECRET_KEY, AAD "instance_secret/<nome>"); sem chave no servidor o web push fica desligado.
CREATE TABLE instance_secrets (
    name       text        PRIMARY KEY CHECK (name ~ '^[a-z][a-z0-9_.-]{0,63}$'),
    value_enc  text        NOT NULL CHECK (value_enc <> ''),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

-- Assinatura de web push de um navegador/aparelho. É da pessoa (vale para todas as empresas dela), como a sessão:
-- por isso não tem company_id. O endpoint é único: o mesmo navegador reassinando por outro usuário muda de dono.
CREATE TABLE push_subscriptions (
    id              uuid        PRIMARY KEY,
    user_id         uuid        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    endpoint        text        NOT NULL CHECK (length(endpoint) BETWEEN 10 AND 2048),
    p256dh          text        NOT NULL CHECK (length(p256dh) BETWEEN 1 AND 200),
    auth            text        NOT NULL CHECK (length(auth) BETWEEN 1 AND 100),
    user_agent      text        NOT NULL DEFAULT '' CHECK (length(user_agent) <= 300),
    created_at      timestamptz NOT NULL,
    last_success_at timestamptz,
    failures        integer     NOT NULL DEFAULT 0 CHECK (failures >= 0)
);
CREATE UNIQUE INDEX push_subscriptions_endpoint_uq ON push_subscriptions (endpoint);
CREATE INDEX push_subscriptions_user_idx ON push_subscriptions (user_id);

-- Preferências por empresa e tipo de aviso. Só guarda o que a pessoa mudou; o resto vale o padrão do código.
CREATE TABLE notification_prefs (
    company_id uuid        NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    user_id    uuid        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind       text        NOT NULL CHECK (kind IN ('mention','gate','run_failed','alert')),
    toast      boolean     NOT NULL,
    push       boolean     NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (company_id, user_id, kind)
);

-- +goose Down
DROP TABLE IF EXISTS notification_prefs;
DROP TABLE IF EXISTS push_subscriptions;
DROP TABLE IF EXISTS instance_secrets;

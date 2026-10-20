-- +goose Up
-- Token bucket do rate limit geral (ADR-0058 §B). UNLOGGED de propósito: é estado efêmero, e perdê-lo num crash só
-- afrouxa o limite por alguns segundos. Não é tabela de domínio (sem company_id): a chave carrega a identidade
-- (key:/user:/run:/ip: + classe).
CREATE UNLOGGED TABLE rate_limit_buckets (
    key          text PRIMARY KEY CHECK (length(key) BETWEEN 1 AND 200),
    tokens       double precision NOT NULL CHECK (tokens >= 0),
    updated_at   timestamptz NOT NULL,
    last_allowed boolean NOT NULL
);
CREATE INDEX rate_limit_buckets_updated_idx ON rate_limit_buckets (updated_at);

-- +goose Down
DROP TABLE rate_limit_buckets;

-- +goose Up
-- Settings da instalação (ADR-0050): toggles NÃO sensíveis, com trilha. Não têm company_id: são da instância (como users).
CREATE TABLE instance_settings (
    key        text PRIMARY KEY CHECK (length(key) BETWEEN 1 AND 100),
    value      jsonb NOT NULL,
    updated_by uuid,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Auditoria de cada mudança (valor anterior e novo; nunca há segredo aqui). new_value nulo = voltou ao padrão.
CREATE TABLE instance_setting_audit (
    id          uuid PRIMARY KEY,
    key         text NOT NULL,
    old_value   jsonb,
    new_value   jsonb,
    actor_type  text NOT NULL CHECK (actor_type IN ('human','persona','system')),
    actor_id    uuid,
    occurred_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX instance_setting_audit_idx ON instance_setting_audit (occurred_at DESC, id DESC);
CREATE INDEX instance_setting_audit_key_idx ON instance_setting_audit (key, occurred_at DESC);

-- +goose Down
DROP TABLE instance_setting_audit;
DROP TABLE instance_settings;

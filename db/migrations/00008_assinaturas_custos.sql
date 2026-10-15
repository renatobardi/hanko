-- +goose Up
-- Assinaturas (plano de um harness), custo de lista por run, trava semanal e origem da escolha de modelo.

-- Histórico de preços é append-only: um preço novo vale dali em diante e o custo já gravado nunca é recalculado.
-- +goose StatementBegin
CREATE FUNCTION model_prices_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'model_prices é append-only: insira um preço novo com outra vigência' USING ERRCODE = 'integrity_constraint_violation';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER model_prices_append_only BEFORE UPDATE OR DELETE ON model_prices
    FOR EACH ROW EXECUTE FUNCTION model_prices_append_only();

-- Assinatura: mensalidade em USD de um plano (harness/provedor), prefixos de modelo que ela cobre e
-- trava semanal. weekly_limit_usd é o teto semanal configurável (nulo = sem trava); a trava dispara
-- quando o uso estimado da semana chega a weekly_guard_pct desse teto.
CREATE TABLE subscriptions (
    id               uuid PRIMARY KEY,
    company_id       uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    name             text NOT NULL,
    agent            text NOT NULL,
    monthly_usd      numeric(14,2) NOT NULL CHECK (monthly_usd >= 0),
    model_prefixes   text[] NOT NULL DEFAULT '{}',
    weekly_limit_usd numeric(14,2) CHECK (weekly_limit_usd IS NULL OR weekly_limit_usd > 0),
    weekly_guard_pct integer NOT NULL DEFAULT 90 CHECK (weekly_guard_pct BETWEEN 1 AND 100),
    active           boolean NOT NULL DEFAULT true,
    ended_at         timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (company_id, name)
);

-- Trava semanal já avisada: no máximo um aviso por (assinatura, semana).
CREATE TABLE subscription_guard_alerts (
    subscription_id uuid NOT NULL REFERENCES subscriptions(id) ON DELETE CASCADE,
    week            text NOT NULL CHECK (week ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}$'),
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (subscription_id, week)
);

-- Persona: vínculo opcional com uma assinatura.
ALTER TABLE personas ADD COLUMN subscription_id uuid REFERENCES subscriptions(id) ON DELETE SET NULL;

-- Task: modelo escolhido à mão (vence o da persona). Vazio = sem escolha manual.
ALTER TABLE tasks ADD COLUMN model text NOT NULL DEFAULT '';

-- Run: origem da escolha de modelo, assinatura que a cobre (fixada no checkout) e custo de lista
-- (tokens x preço de tabela vigente no fim da run; gravado uma vez, nunca recalculado).
ALTER TABLE runs ADD COLUMN model_source text NOT NULL DEFAULT 'harness_default' CHECK (model_source IN ('task','persona','harness_default'));
ALTER TABLE runs ADD COLUMN subscription_id uuid REFERENCES subscriptions(id) ON DELETE SET NULL;
ALTER TABLE runs ADD COLUMN list_cost_usd numeric(14,6) CHECK (list_cost_usd IS NULL OR list_cost_usd >= 0);
UPDATE runs SET model_source = 'persona' WHERE model <> '';
UPDATE runs SET list_cost_usd = cost_usd WHERE cost_source = 'estimated';
CREATE INDEX runs_subscription_created_idx ON runs (subscription_id, created_at) WHERE subscription_id IS NOT NULL;

-- +goose Down
DROP INDEX runs_subscription_created_idx;
ALTER TABLE runs DROP COLUMN list_cost_usd;
ALTER TABLE runs DROP COLUMN subscription_id;
ALTER TABLE runs DROP COLUMN model_source;
ALTER TABLE tasks DROP COLUMN model;
ALTER TABLE personas DROP COLUMN subscription_id;
DROP TABLE subscription_guard_alerts;
DROP TABLE subscriptions;
DROP TRIGGER model_prices_append_only ON model_prices;
DROP FUNCTION model_prices_append_only();

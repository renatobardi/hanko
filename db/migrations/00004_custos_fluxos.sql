-- +goose Up
-- M4: custos e orçamentos (ledger, preços, alertas) e Fluxos (cron -> tasks).

-- Orçamento mensal da empresa em centavos de USD (nulo = sem teto). O da persona já existe.
ALTER TABLE companies ADD COLUMN budget_monthly_cents bigint CHECK (budget_monthly_cents IS NULL OR budget_monthly_cents >= 0);

-- Motivo de uma pausa automática (orçamento); limpo quando a persona sai de paused.
ALTER TABLE personas ADD COLUMN pause_reason text;

-- Origem do custo da run: informado pelo harness, estimado por tabela de preços, ou desconhecido.
ALTER TABLE runs ADD COLUMN cost_source text NOT NULL DEFAULT 'none' CHECK (cost_source IN ('reported','estimated','none'));
UPDATE runs SET cost_source = 'reported' WHERE cost_usd IS NOT NULL;
CREATE INDEX runs_company_persona_created_idx ON runs (company_id, persona_id, created_at);

-- Novo gatilho de run: Fluxo.
ALTER TABLE runs DROP CONSTRAINT runs_trigger_check;
ALTER TABLE runs ADD CONSTRAINT runs_trigger_check CHECK (trigger IN ('assign','comment','manual','heartbeat','gate','flow'));

-- Preços por 1M de tokens (USD). 'model' é um prefixo; vale o prefixo mais longo com
-- effective_at <= instante do fato. 'fixed' é um valor fixo por run (USD).
CREATE TABLE model_prices (
    id                  uuid PRIMARY KEY,
    model               text NOT NULL,
    input_per_mtok      numeric(14,6) NOT NULL DEFAULT 0 CHECK (input_per_mtok >= 0),
    output_per_mtok     numeric(14,6) NOT NULL DEFAULT 0 CHECK (output_per_mtok >= 0),
    cache_read_per_mtok numeric(14,6) NOT NULL DEFAULT 0 CHECK (cache_read_per_mtok >= 0),
    cache_creation_per_mtok numeric(14,6) NOT NULL DEFAULT 0 CHECK (cache_creation_per_mtok >= 0),
    fixed               numeric(14,6) NOT NULL DEFAULT 0 CHECK (fixed >= 0),
    effective_at        timestamptz NOT NULL DEFAULT now(),
    created_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (model, effective_at)
);
-- Valores conservadores (tendem a superestimar): melhor travar cedo do que gastar sem ver.
INSERT INTO model_prices (id, model, input_per_mtok, output_per_mtok, cache_read_per_mtok, cache_creation_per_mtok, effective_at) VALUES
    ('0192f000-0000-7000-8000-000000000001', 'claude-opus',   15.000000, 75.000000, 1.500000, 18.750000, '2020-01-01T00:00:00Z'),
    ('0192f000-0000-7000-8000-000000000002', 'claude-sonnet',  3.000000, 15.000000, 0.300000,  3.750000, '2020-01-01T00:00:00Z'),
    ('0192f000-0000-7000-8000-000000000003', 'claude-haiku',   1.000000,  5.000000, 0.100000,  1.250000, '2020-01-01T00:00:00Z'),
    ('0192f000-0000-7000-8000-000000000004', 'gpt-',           5.000000, 20.000000, 0.500000,  0.000000, '2020-01-01T00:00:00Z');

-- Alertas de orçamento já emitidos: no máximo um por (alvo, mês, nível).
CREATE TABLE budget_alerts (
    id          uuid PRIMARY KEY,
    company_id  uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    scope       text NOT NULL CHECK (scope IN ('company','persona')),
    target_id   uuid NOT NULL,
    month       text NOT NULL CHECK (month ~ '^[0-9]{4}-[0-9]{2}$'),
    level       text NOT NULL CHECK (level IN ('warning','exceeded')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (target_id, month, level)
);

-- Fluxos: definição + gatilho cron -> task atribuída a uma persona.
CREATE TABLE flows (
    id               uuid PRIMARY KEY,
    company_id       uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    slug             text NOT NULL,
    name             text NOT NULL,
    description      text NOT NULL DEFAULT '',
    cron             text NOT NULL,
    timezone         text NOT NULL,
    enabled          boolean NOT NULL DEFAULT true,
    persona_id       uuid NOT NULL REFERENCES personas(id) ON DELETE CASCADE,
    project_id       uuid REFERENCES projects(id) ON DELETE SET NULL,
    priority         text NOT NULL DEFAULT 'medium' CHECK (priority IN ('critical','high','medium','low')),
    task_title       text NOT NULL DEFAULT '',
    task_description text NOT NULL DEFAULT '',
    last_fired_at    timestamptz,
    next_fire_at     timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (company_id, slug)
);
CREATE INDEX flows_due_idx ON flows (next_fire_at) WHERE enabled;

-- Cada disparo de um Fluxo; (flow_id, scheduled_for) único garante idempotência.
CREATE TABLE flow_fires (
    id             uuid PRIMARY KEY,
    flow_id        uuid NOT NULL REFERENCES flows(id) ON DELETE CASCADE,
    company_id     uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    scheduled_for  timestamptz NOT NULL,
    task_id        uuid REFERENCES tasks(id) ON DELETE SET NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (flow_id, scheduled_for)
);
CREATE INDEX flow_fires_flow_idx ON flow_fires (flow_id, scheduled_for DESC);

-- +goose Down
DROP TABLE flow_fires;
DROP TABLE flows;
DROP TABLE budget_alerts;
DROP TABLE model_prices;
UPDATE runs SET trigger = 'heartbeat' WHERE trigger = 'flow';
ALTER TABLE runs DROP CONSTRAINT runs_trigger_check;
ALTER TABLE runs ADD CONSTRAINT runs_trigger_check CHECK (trigger IN ('assign','comment','manual','heartbeat','gate'));
DROP INDEX runs_company_persona_created_idx;
ALTER TABLE runs DROP COLUMN cost_source;
ALTER TABLE personas DROP COLUMN pause_reason;
ALTER TABLE companies DROP COLUMN budget_monthly_cents;

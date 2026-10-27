-- +goose Up
-- Custo elevado e alertas de agente externo (ADR-0082 e 0083; grupo 2 do contrato da v0.8, #124). Só o contrato, com uma
-- exceção: cada assinatura que já existe ganha a linha inicial do histórico de mensalidade (o valor atual, com início
-- na criação), para que nenhum "pago" de hoje mude (ADR-0082 item 6).
--
-- Desenho (decidido pelo Bardi em 2026-10-27): os runs do Hanko não mudam no banco; a classe de custo deles é calculada
-- por `RunCostClass` a partir de `cost_source` e da assinatura (ADR-0082 item 2). O sujeito de um alerta que não é um id
-- (modelo, fonte de preço) usa um UUID derivado das dimensões da ocorrência, com o nome legível em `data`. Os limites
-- e os prazos de cada empresa são sobrescritas de um padrão que vive no código, como em `notification_prefs`.

-- 1. Preço de modelo: origem da linha e trava manual (ADR-0082 item 7). A trava vale para a linha; a fonte nunca a troca.
ALTER TABLE model_prices ADD COLUMN origin text NOT NULL DEFAULT 'config' CHECK (origin IN ('config', 'sources', 'migrated'));
ALTER TABLE model_prices ADD COLUMN locked boolean NOT NULL DEFAULT false;

-- 2. Assinatura: chave externa opcional, que liga a assinatura às chamadas externas que a declaram (ADR-0082 item 4).
ALTER TABLE subscriptions ADD COLUMN external_key text CHECK (external_key ~ '^[a-z][a-z0-9_-]{0,31}$');
CREATE UNIQUE INDEX subscriptions_external_key_uq ON subscriptions (company_id, external_key) WHERE external_key IS NOT NULL;

-- 3. Histórico de mensalidade (ADR-0082 item 6). Só recebe linhas novas: o valor de um dia é o da linha com o maior
-- início menor ou igual a ele, e a registrada por último desempata. `subscriptions.monthly_usd` continua como o valor
-- vigente hoje, atualizado na mesma transação em que se acrescenta uma linha.
CREATE TABLE subscription_monthly_history (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id      uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    subscription_id uuid NOT NULL REFERENCES subscriptions(id) ON DELETE CASCADE,
    monthly_usd     numeric(14,2) NOT NULL CHECK (monthly_usd >= 0),
    starts_on       date NOT NULL,
    recorded_at     timestamptz NOT NULL DEFAULT now(),
    origin          text NOT NULL DEFAULT 'manual' CHECK (origin IN ('created', 'manual', 'migrated')),
    actor_type      text NOT NULL DEFAULT 'system' CHECK (actor_type IN ('human', 'system')),
    actor_id        uuid
);
CREATE INDEX subscription_monthly_history_idx ON subscription_monthly_history (subscription_id, starts_on DESC, recorded_at DESC);
INSERT INTO subscription_monthly_history (company_id, subscription_id, monthly_usd, starts_on, recorded_at, origin)
SELECT company_id, id, monthly_usd, (created_at AT TIME ZONE 'UTC')::date, created_at, 'created' FROM subscriptions;

-- 4. Conferência diária de preços (ADR-0082 item 7). Global, como `model_prices`: a conferência mantém a tabela, não a
-- empresa. Cada conferência grava o resultado por fonte e por modelo; a falha guarda só o código, nunca o corpo.
CREATE TABLE price_check_run (
    id          uuid PRIMARY KEY,
    reason      text NOT NULL CHECK (reason IN ('boot', 'daily', 'manual', 'migrated')),
    started_at  timestamptz NOT NULL,
    finished_at timestamptz,
    CHECK (finished_at IS NULL OR finished_at >= started_at)
);
CREATE INDEX price_check_run_started_idx ON price_check_run (started_at DESC);

CREATE TABLE price_check_source (
    run_id      uuid NOT NULL REFERENCES price_check_run(id) ON DELETE CASCADE,
    source      text NOT NULL CHECK (length(source) BETWEEN 1 AND 100),
    status      text NOT NULL CHECK (status IN ('ok', 'down')),
    error_code  text NOT NULL DEFAULT '' CHECK (length(error_code) <= 100),
    PRIMARY KEY (run_id, source),
    CHECK (status = 'down' OR error_code = '')
);

CREATE TABLE price_check_result (
    run_id          uuid NOT NULL REFERENCES price_check_run(id) ON DELETE CASCADE,
    model           text NOT NULL CHECK (length(model) BETWEEN 1 AND 200),
    source          text NOT NULL CHECK (length(source) BETWEEN 1 AND 100),
    status          text NOT NULL CHECK (status IN ('equal', 'changed', 'new', 'diverges', 'locked_differs', 'no_source', 'source_down')),
    input_per_mtok          numeric(14,6) CHECK (input_per_mtok IS NULL OR input_per_mtok >= 0),
    output_per_mtok         numeric(14,6) CHECK (output_per_mtok IS NULL OR output_per_mtok >= 0),
    cache_read_per_mtok     numeric(14,6) CHECK (cache_read_per_mtok IS NULL OR cache_read_per_mtok >= 0),
    cache_creation_per_mtok numeric(14,6) CHECK (cache_creation_per_mtok IS NULL OR cache_creation_per_mtok >= 0),
    price_id        uuid REFERENCES model_prices(id) ON DELETE SET NULL,
    PRIMARY KEY (run_id, model, source)
);
CREATE INDEX price_check_result_model_idx ON price_check_result (model, run_id);

-- 5. Alertas de agente externo (ADR-0083 itens 1 e 5). Os 17 tipos entram em `alerts`; `price_fixed_differs` do
-- agent-studio vira `price_locked_differs`. O sujeito que não é um id (modelo, fonte de preço) usa um UUID derivado das
-- dimensões da ocorrência (decisão do Bardi de 2026-10-27); o nome legível fica em `data`. O índice de alerta aberto
-- único continua como está.
ALTER TABLE alerts DROP CONSTRAINT alerts_kind_check;
ALTER TABLE alerts ADD CONSTRAINT alerts_kind_check CHECK (kind IN (
    'persona_silent', 'run_stuck', 'budget_near_limit', 'persona_not_ready', 'secret_rotation_due',
    'queue', 'destination_refusing', 'host_no_data', 'spool', 'quota', 'round_stalled', 'round_old', 'llm_proxy_down',
    'memory_capture', 'memory_hooks_untrusted',
    'price_changed', 'price_sources_diverge', 'price_source_down', 'price_model_unpriced', 'price_locked_differs',
    'cost_claude_diff', 'cost_subscription_unpriced'));
ALTER TABLE alerts DROP CONSTRAINT alerts_subject_type_check;
ALTER TABLE alerts ADD CONSTRAINT alerts_subject_type_check CHECK (subject_type IN (
    'persona', 'run', 'company', 'secret', 'external_host', 'external_agent', 'external_round', 'model', 'price_source'));

-- 6. Limites e prazos por empresa (ADR-0083 itens 3 e 6). Só guarda o que a empresa mudou; o padrão vale pelo código,
-- com os valores da Oute. Chaves `limit.<nome>` (a unidade é do limite) e `due.<tipo-de-alerta>` (horas; 0 = sem prazo,
-- até 720 = 30 dias). A lista de chaves válidas fica no domínio.
CREATE TABLE company_alert_settings (
    company_id  uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    key         text NOT NULL CHECK (key ~ '^(limit|due)\.[a-z][a-z0-9_]{0,63}$'),
    value       numeric(18,6) NOT NULL CHECK (value >= 0),
    updated_by_type text NOT NULL CHECK (updated_by_type IN ('human', 'system')),
    updated_by_id   uuid,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (company_id, key),
    CHECK (key NOT LIKE 'due.%' OR value <= 720)
);

-- 7. Caixa (ADR-0083 itens 6 e 7): a marca migrada das `ack_marks` do agent-studio. O prazo do tipo conta desde
-- `updated_at`, a hora da marca. Os itens continuam derivados do estado; aqui só entra o sinal da migração.
ALTER TABLE inbox_state ADD COLUMN migrated boolean NOT NULL DEFAULT false;

-- +goose Down
-- Os alertas de agente externo, o histórico de mensalidade, a conferência de preços e os ajustes das empresas somem
-- junto com as tabelas e as linhas; os runs e as assinaturas não mudam.
ALTER TABLE inbox_state DROP COLUMN migrated;
DROP TABLE company_alert_settings;
DELETE FROM alerts WHERE kind NOT IN ('persona_silent', 'run_stuck', 'budget_near_limit', 'persona_not_ready', 'secret_rotation_due')
    OR subject_type NOT IN ('persona', 'run', 'company', 'secret');
ALTER TABLE alerts DROP CONSTRAINT alerts_subject_type_check;
ALTER TABLE alerts DROP CONSTRAINT alerts_kind_check;
ALTER TABLE alerts ADD CONSTRAINT alerts_kind_check CHECK (kind IN ('persona_silent', 'run_stuck', 'budget_near_limit', 'persona_not_ready', 'secret_rotation_due'));
ALTER TABLE alerts ADD CONSTRAINT alerts_subject_type_check CHECK (subject_type IN ('persona', 'run', 'company', 'secret'));
DROP TABLE price_check_result;
DROP TABLE price_check_source;
DROP TABLE price_check_run;
DROP TABLE subscription_monthly_history;
DROP INDEX subscriptions_external_key_uq;
ALTER TABLE subscriptions DROP COLUMN external_key;
ALTER TABLE model_prices DROP COLUMN locked;
ALTER TABLE model_prices DROP COLUMN origin;

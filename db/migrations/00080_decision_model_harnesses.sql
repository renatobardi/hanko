-- +goose Up
-- M12 (ADR-0056): decision model por empresa e novos harnesses (devin, pi, cursor, http).

-- Harnesses novos em personas.agent. Só entram aqui; reviewer_agent não muda (ADR-0023 item 10: harness alternativo
-- de revisão não carrega adapter_config).
ALTER TABLE personas DROP CONSTRAINT personas_agent_check;
ALTER TABLE personas ADD CONSTRAINT personas_agent_check CHECK (agent IN ('claude', 'codex', 'acp', 'devin', 'pi', 'cursor', 'http', 'fake'));

-- Configuração do decision model: no máximo uma por empresa. A chave NUNCA é guardada aqui: api_key_ref é uma
-- referência ${secret:NOME} (ADR-0050), resolvida a cada invocação.
CREATE TABLE decision_models (
    company_id           uuid PRIMARY KEY REFERENCES companies(id) ON DELETE CASCADE,
    provider             text NOT NULL DEFAULT 'openrouter' CHECK (provider IN ('openrouter')),
    base_url             text NOT NULL CHECK (length(base_url) BETWEEN 8 AND 500),
    api_key_ref          text NOT NULL DEFAULT '' CHECK (length(api_key_ref) <= 200),
    model                text NOT NULL CHECK (length(model) BETWEEN 1 AND 200),
    -- preço por token (USD) do catálogo no momento da escolha; estima o custo quando a resposta não o traz
    prompt_usd_token     numeric(20,12) NOT NULL DEFAULT 0 CHECK (prompt_usd_token >= 0),
    completion_usd_token numeric(20,12) NOT NULL DEFAULT 0 CHECK (completion_usd_token >= 0),
    timeout_sec          integer NOT NULL DEFAULT 30 CHECK (timeout_sec BETWEEN 1 AND 300),
    enabled              boolean NOT NULL DEFAULT true,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now()
);

-- Features registradas em código (triage, phase_classifier, name_summary) ligadas/desligadas por empresa. Sem linha =
-- desligada (nada chama o modelo sem o admin ligar). model vazio = usa o modelo da empresa.
CREATE TABLE decision_features (
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    feature    text NOT NULL CHECK (feature IN ('triage', 'phase_classifier', 'name_summary')),
    enabled    boolean NOT NULL DEFAULT false,
    model      text NOT NULL DEFAULT '' CHECK (length(model) <= 200),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (company_id, feature)
);

-- Livro de invocações do decision model: cada chamada é uma linha, com ator system (não é uma run de persona).
CREATE TABLE decision_invocations (
    id             uuid PRIMARY KEY,
    company_id     uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    feature        text NOT NULL CHECK (feature IN ('triage', 'phase_classifier', 'name_summary')),
    actor_type     text NOT NULL DEFAULT 'system' CHECK (actor_type = 'system'),
    task_id        uuid REFERENCES tasks(id) ON DELETE SET NULL,
    model          text NOT NULL,
    status         text NOT NULL CHECK (status IN ('ok', 'error')),
    error          text NOT NULL DEFAULT '',
    input_tokens   bigint NOT NULL DEFAULT 0,
    output_tokens  bigint NOT NULL DEFAULT 0,
    cost_usd       numeric(14,6) NOT NULL DEFAULT 0,
    cost_source    text NOT NULL DEFAULT 'estimated' CHECK (cost_source IN ('reported', 'estimated')),
    latency_ms     integer NOT NULL DEFAULT 0,
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX decision_invocations_company_idx ON decision_invocations (company_id, created_at DESC);

-- +goose Down
DROP TABLE decision_invocations;
DROP TABLE decision_features;
DROP TABLE decision_models;
ALTER TABLE personas DROP CONSTRAINT personas_agent_check;
ALTER TABLE personas ADD CONSTRAINT personas_agent_check CHECK (agent IN ('claude', 'codex', 'acp', 'fake'));

-- +goose Up
-- Fonte externa e ingestão (ADR-0075 a 0079, 0084 e 0086; fatia 0 da v0.8, #124). Só o contrato: tabelas e colunas
-- novas, sem mover dado. A ingestão, a resolução de nomes e a migração do agent-studio vêm nas fatias seguintes.
--
-- Desenho (decidido pelo Bardi em 2026-10-26): o dado bruto guarda só o texto que chegou, sem id; as tabelas de apelido
-- ligam o texto à entidade; as tabelas derivadas guardam o id já resolvido. Juntar dois agentes ou corrigir um
-- emparelhamento mexe só nas derivadas e nos apelidos, nunca nas partições brutas.

-- 1. Chave de ingestão (ADR-0076 item 3): escopo `ingest`, uma chave por host, sempre presa a uma empresa. Só grava
-- telemetria; não lê nada. `user_id` continua sendo quem a criou (owner ou admin).
ALTER TABLE api_keys DROP CONSTRAINT api_keys_scope_check;
ALTER TABLE api_keys ADD COLUMN company_id uuid REFERENCES companies(id) ON DELETE CASCADE;
ALTER TABLE api_keys ADD CONSTRAINT api_keys_scope_check CHECK (scope IN ('full', 'read', 'ingest'));
ALTER TABLE api_keys ADD CONSTRAINT api_keys_ingest_company_check CHECK ((scope = 'ingest') = (company_id IS NOT NULL));

-- 2. Workspace de sistema `no-repo` (ADR-0075 item 6): recebe todo fato sem repositório; não se exclui nem se renomeia.
ALTER TABLE workspaces ADD COLUMN is_system boolean NOT NULL DEFAULT false;
CREATE UNIQUE INDEX workspaces_one_system_uq ON workspaces (company_id) WHERE is_system;

-- 3. Agente externo (ADR-0075 itens 1 e 2). Nasce na ingestão. `name` é o nome de exibição, que owner e admin renomeiam.
CREATE TABLE external_agent (
    id          uuid PRIMARY KEY,
    company_id  uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    source_kind text NOT NULL DEFAULT 'external' CHECK (source_kind IN ('external')),
    name        text NOT NULL CHECK (name ~ '^[^[:space:]].{0,98}[^[:space:]]$|^[^[:space:]]$'),
    archived_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX external_agent_name_uq ON external_agent (company_id, lower(name));

-- Apelido: cada texto de agente que já chegou aponta para um agente externo. Juntar dois agentes move os apelidos.
CREATE TABLE external_agent_alias (
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    alias      text NOT NULL CHECK (length(alias) BETWEEN 1 AND 200),
    agent_id   uuid NOT NULL REFERENCES external_agent(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (company_id, alias)
);
CREATE INDEX external_agent_alias_agent_idx ON external_agent_alias (agent_id);

-- 4. Host externo (ADR-0077). `always_on` liga o alerta `host_no_data`; `last_seen_at` é a hora do último fato recebido.
CREATE TABLE external_host (
    id           uuid PRIMARY KEY,
    company_id   uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    source_kind  text NOT NULL DEFAULT 'external' CHECK (source_kind IN ('external')),
    name         text NOT NULL CHECK (name ~ '^[^[:space:]].{0,98}[^[:space:]]$|^[^[:space:]]$'),
    always_on    boolean NOT NULL DEFAULT false,
    last_seen_at timestamptz,
    archived_at  timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX external_host_name_uq ON external_host (company_id, lower(name));

CREATE TABLE external_host_alias (
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    alias      text NOT NULL CHECK (length(alias) BETWEEN 1 AND 200),
    host_id    uuid NOT NULL REFERENCES external_host(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (company_id, alias)
);
CREATE INDEX external_host_alias_host_idx ON external_host_alias (host_id);

-- 5. Emparelhamento (ADR-0075 itens 3 a 5): a chave do repositório (URL normalizada do remoto, ou o nome quando o fato
-- não traz URL) aponta para um workspace da empresa. Trocar o workspace leva o passado junto, com activity_event.
CREATE TABLE repo_pairing (
    id           uuid PRIMARY KEY,
    company_id   uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    repo_key     text NOT NULL CHECK (length(repo_key) BETWEEN 1 AND 500),
    key_kind     text NOT NULL CHECK (key_kind IN ('url', 'name')),
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE RESTRICT,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (company_id, repo_key)
);
CREATE INDEX repo_pairing_workspace_idx ON repo_pairing (workspace_id);

-- 6. Sessão externa (ADR-0078 item 2): agrupamento de conversas pelo `oute.task.id` que a fonte declara.
CREATE TABLE external_session (
    id           uuid PRIMARY KEY,
    company_id   uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    source_kind  text NOT NULL DEFAULT 'external' CHECK (source_kind IN ('external')),
    task_id      text NOT NULL CHECK (length(task_id) BETWEEN 1 AND 200),
    oute_repo    text NOT NULL DEFAULT '',
    workspace_id uuid REFERENCES workspaces(id) ON DELETE SET NULL,
    slug         text NOT NULL DEFAULT '',
    agent_id     uuid REFERENCES external_agent(id) ON DELETE SET NULL,
    state        text NOT NULL DEFAULT '',
    round        text NOT NULL DEFAULT '',
    worker       text NOT NULL DEFAULT '',
    first_at     bigint,
    last_at      bigint,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (company_id, task_id)
);

-- 7. Conversa externa (ADR-0078 itens 1 e 6; classificação do ADR-0079 item 1). Mantida na ingestão, na mesma transação
-- do fato. Cada atributo guarda o valor e a hora do fato que o trouxe (`*_at`, em ns): um fato mais antigo troca o valor,
-- um mais novo não. Os contadores só somam o que entrou de novo; p95 e contexto são refeitos a partir de model_calls.
CREATE TABLE external_conversation (
    id                  uuid PRIMARY KEY,
    company_id          uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    source_kind         text NOT NULL DEFAULT 'external' CHECK (source_kind IN ('external')),
    session_id          text NOT NULL CHECK (length(session_id) BETWEEN 1 AND 200),
    name                text NOT NULL CHECK (name ~ '^[A-Z][a-z]+_[A-Z][a-z]+(_[1-9][0-9]{2})?$'),
    external_session_id uuid REFERENCES external_session(id) ON DELETE SET NULL,
    host_id             uuid REFERENCES external_host(id) ON DELETE RESTRICT,
    host_at             bigint,
    agent_id            uuid REFERENCES external_agent(id) ON DELETE RESTRICT,
    agent_at            bigint,
    oute_instance       text NOT NULL DEFAULT '',
    oute_instance_at    bigint,
    service_name        text NOT NULL DEFAULT '',
    service_name_at     bigint,
    subscription_key    text NOT NULL DEFAULT '',
    subscription_key_at bigint,
    oute_repo           text NOT NULL DEFAULT '',
    oute_repo_at        bigint,
    workspace_id        uuid REFERENCES workspaces(id) ON DELETE SET NULL,
    inferred_attribution boolean NOT NULL DEFAULT false,
    first_at            bigint,
    last_at             bigint,
    calls               bigint NOT NULL DEFAULT 0 CHECK (calls >= 0),
    errors              bigint NOT NULL DEFAULT 0 CHECK (errors >= 0),
    input_tokens        bigint NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens       bigint NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    cache_read_tokens   bigint NOT NULL DEFAULT 0 CHECK (cache_read_tokens >= 0),
    cache_creation_tokens bigint NOT NULL DEFAULT 0 CHECK (cache_creation_tokens >= 0),
    cost_real_usd       numeric(18,6) NOT NULL DEFAULT 0 CHECK (cost_real_usd >= 0),
    cost_list_usd       numeric(18,6) NOT NULL DEFAULT 0 CHECK (cost_list_usd >= 0),
    unpriced_calls      bigint NOT NULL DEFAULT 0 CHECK (unpriced_calls >= 0),
    p95_ms              integer,
    context_avg         bigint,
    context_max         bigint,
    -- classificação atual (refeita pelo classificador; a troca manual e a resposta do decision model ficam em
    -- conversation_class_input, que só recebe linhas novas)
    class               text NOT NULL DEFAULT '' CHECK (length(class) <= 40),
    class_origin        text NOT NULL DEFAULT '' CHECK (class_origin IN ('', 'opening', 'label', 'skill', 'role', 'action', 'decision_model', 'manual')),
    class_confidence    text NOT NULL DEFAULT 'low' CHECK (class_confidence IN ('high', 'low')),
    classifier          text NOT NULL DEFAULT '' CHECK (length(classifier) <= 40),
    tags                text[] NOT NULL DEFAULT '{}',
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (company_id, session_id),
    UNIQUE (company_id, name)
);
CREATE INDEX external_conversation_last_idx ON external_conversation (company_id, last_at DESC);
CREATE INDEX external_conversation_session_idx ON external_conversation (external_session_id) WHERE external_session_id IS NOT NULL;
CREATE INDEX external_conversation_workspace_idx ON external_conversation (company_id, workspace_id);

-- 8. Entradas da classificação que não se refazem (ADR-0079 itens 1, 4 e 8). Só acréscimo: a classe atual é a troca
-- manual mais recente, se houver, ou então o que o classificador calculou.
CREATE TABLE conversation_class_input (
    id                     uuid PRIMARY KEY,
    company_id             uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    conversation_id        uuid NOT NULL REFERENCES external_conversation(id) ON DELETE CASCADE,
    kind                   text NOT NULL CHECK (kind IN ('decision_model', 'manual')),
    class                  text NOT NULL CHECK (length(class) BETWEEN 1 AND 40),
    confidence             text NOT NULL CHECK (confidence IN ('high', 'low')),
    decision_invocation_id uuid REFERENCES decision_invocations(id) ON DELETE SET NULL,
    actor_type             text NOT NULL CHECK (actor_type IN ('human', 'system')),
    actor_id               uuid,
    migrated               boolean NOT NULL DEFAULT false,
    created_at             timestamptz NOT NULL DEFAULT now(),
    CHECK (kind = 'decision_model' OR actor_type = 'human' OR migrated)
);
CREATE INDEX conversation_class_input_idx ON conversation_class_input (conversation_id, created_at DESC);

-- 9. Telemetria bruta (ADR-0076; decisão do Bardi de 2026-10-26): uma tabela por sinal, particionada por mês pela hora
-- do fato. Colunas do agent-studio (`store.py:26-140@845764f`), mais a empresa e a chave que entregou. Texto como chegou,
-- sem id. A chave de dedupe é a do agent-studio; com a empresa e a hora, ela é a chave primária (exigência do
-- particionamento).
CREATE TABLE telemetry_spans (
    company_id            uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    dedupe_key            text NOT NULL,
    time_unix_nano        bigint NOT NULL CHECK (time_unix_nano >= 0),
    end_unix_nano         bigint,
    duration_ns           bigint,
    host_name             text,
    oute_instance         text,
    oute_agent            text,
    service_name          text,
    session_id            text,
    oute_task_id          text,
    oute_swarm_round      text,
    oute_repo             text,
    oute_subscription     text,
    trace_id              text NOT NULL,
    span_id               text NOT NULL,
    parent_span_id        text,
    name                  text,
    kind                  integer,
    status_code           integer,
    status_message        text,
    model                 text,
    input_tokens          bigint,
    output_tokens         bigint,
    cache_read_tokens     bigint,
    cache_creation_tokens bigint,
    cost_usd              double precision,
    scope_name            text,
    resource_attributes   jsonb,
    attributes            jsonb,
    events                jsonb,
    links                 jsonb,
    received_unix_nano    bigint NOT NULL,
    ingested_by_key_id    uuid,
    PRIMARY KEY (company_id, dedupe_key, time_unix_nano)
) PARTITION BY RANGE (time_unix_nano);
CREATE INDEX telemetry_spans_time_idx ON telemetry_spans (company_id, time_unix_nano);
CREATE INDEX telemetry_spans_session_idx ON telemetry_spans (company_id, session_id, time_unix_nano);
CREATE INDEX telemetry_spans_name_idx ON telemetry_spans (company_id, name, time_unix_nano);
CREATE INDEX telemetry_spans_trace_idx ON telemetry_spans (company_id, trace_id, span_id);

CREATE TABLE telemetry_logs (
    company_id          uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    dedupe_key          text NOT NULL,
    time_unix_nano      bigint NOT NULL CHECK (time_unix_nano >= 0),
    observed_unix_nano  bigint,
    host_name           text,
    oute_instance       text,
    oute_agent          text,
    service_name        text,
    session_id          text,
    oute_task_id        text,
    oute_swarm_round    text,
    oute_repo           text,
    oute_subscription   text,
    event_name          text,
    oute_event_id       text,
    severity_number     integer,
    severity_text       text,
    body                text,
    trace_id            text,
    span_id             text,
    scope_name          text,
    resource_attributes jsonb,
    attributes          jsonb,
    received_unix_nano  bigint NOT NULL,
    ingested_by_key_id  uuid,
    PRIMARY KEY (company_id, dedupe_key, time_unix_nano)
) PARTITION BY RANGE (time_unix_nano);
CREATE INDEX telemetry_logs_time_idx ON telemetry_logs (company_id, time_unix_nano);
CREATE INDEX telemetry_logs_session_idx ON telemetry_logs (company_id, session_id, time_unix_nano);
CREATE INDEX telemetry_logs_event_idx ON telemetry_logs (company_id, event_name, time_unix_nano);

CREATE TABLE telemetry_metrics (
    company_id              uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    dedupe_key              text NOT NULL,
    time_unix_nano          bigint NOT NULL CHECK (time_unix_nano >= 0),
    start_unix_nano         bigint,
    host_name               text,
    oute_instance           text,
    oute_agent              text,
    service_name            text,
    session_id              text,
    oute_task_id            text,
    oute_swarm_round        text,
    oute_repo               text,
    oute_subscription       text,
    metric_name             text NOT NULL,
    metric_type             text NOT NULL CHECK (metric_type IN ('gauge', 'sum', 'histogram', 'exponential_histogram', 'summary')),
    unit                    text,
    value                   double precision,
    count                   bigint,
    is_monotonic            boolean,
    aggregation_temporality integer,
    scope_name              text,
    resource_attributes     jsonb,
    attributes              jsonb,
    point                   jsonb,
    received_unix_nano      bigint NOT NULL,
    ingested_by_key_id      uuid,
    PRIMARY KEY (company_id, dedupe_key, time_unix_nano)
) PARTITION BY RANGE (time_unix_nano);
CREATE INDEX telemetry_metrics_time_idx ON telemetry_metrics (company_id, time_unix_nano);
CREATE INDEX telemetry_metrics_name_idx ON telemetry_metrics (company_id, metric_name, time_unix_nano);

-- Partições mensais (UTC): do mês anterior até três meses à frente, mais a partição `DEFAULT` que impede a recusa de um
-- fato se o job de criação falhar. O job do Hanko cria o mês seguinte com dois meses de antecedência, e a migração do
-- agent-studio (fatia 6) cria os meses antigos antes de carregar, porque o Postgres não deixa criar uma partição com
-- faixa que já tem linhas na `DEFAULT`.
-- +goose StatementBegin
DO $$
DECLARE
    t text;
    m date;
BEGIN
    FOREACH t IN ARRAY ARRAY['telemetry_spans', 'telemetry_logs', 'telemetry_metrics'] LOOP
        EXECUTE format('CREATE TABLE %I PARTITION OF %I DEFAULT', t || '_default', t);
        FOR m IN SELECT generate_series(date_trunc('month', now() AT TIME ZONE 'UTC')::date - interval '1 month',
                                        date_trunc('month', now() AT TIME ZONE 'UTC')::date + interval '3 months',
                                        interval '1 month')::date LOOP
            EXECUTE format('CREATE TABLE %I PARTITION OF %I FOR VALUES FROM (%s) TO (%s)',
                           t || '_' || to_char(m, 'YYYY_MM'), t,
                           (extract(epoch FROM m::timestamp)::bigint * 1000000000)::text,
                           (extract(epoch FROM (m + interval '1 month')::timestamp)::bigint * 1000000000)::text);
        END LOOP;
    END LOOP;
END $$;
-- +goose StatementEnd

-- 10. Chamadas ao modelo (decisão do Bardi de 2026-10-26; ADR-0078 item 3, ADR-0082 itens 1 e 2). Derivada, gravada na
-- ingestão na mesma transação do fato, só quando o fato é novo. Guarda uso e custo de lista; nunca o "pago", que depende
-- do histórico de mensalidade e é calculado na leitura. O custo de lista é calculado uma vez, com o preço da hora do fato.
CREATE TABLE model_calls (
    company_id        uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    dedupe_key        text NOT NULL,
    time_unix_nano    bigint NOT NULL CHECK (time_unix_nano >= 0),
    conversation_id   uuid REFERENCES external_conversation(id) ON DELETE CASCADE,
    host_id           uuid NOT NULL REFERENCES external_host(id) ON DELETE RESTRICT,
    agent_id          uuid NOT NULL REFERENCES external_agent(id) ON DELETE RESTRICT,
    workspace_id      uuid NOT NULL REFERENCES workspaces(id) ON DELETE RESTRICT,
    role              text NOT NULL CHECK (role IN ('subagent', 'reviewer', 'worker', 'dispatcher', 'standalone')),
    model             text NOT NULL DEFAULT '',
    subscription_key  text NOT NULL DEFAULT '',
    input_tokens      bigint NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens     bigint NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    cache_read_tokens bigint NOT NULL DEFAULT 0 CHECK (cache_read_tokens >= 0),
    cache_creation_tokens bigint NOT NULL DEFAULT 0 CHECK (cache_creation_tokens >= 0),
    duration_ns       bigint,
    is_error          boolean NOT NULL DEFAULT false,
    cost_class        text NOT NULL CHECK (cost_class IN ('real', 'listed', 'estimated', 'unpriced')),
    cost_real_usd     numeric(18,6) CHECK (cost_real_usd IS NULL OR cost_real_usd >= 0),
    cost_list_usd     numeric(18,6) CHECK (cost_list_usd IS NULL OR cost_list_usd >= 0),
    price_id          uuid REFERENCES model_prices(id) ON DELETE RESTRICT,
    origin            text NOT NULL DEFAULT 'ingested' CHECK (origin IN ('ingested', 'migrated')),
    PRIMARY KEY (company_id, dedupe_key),
    CHECK (cost_class <> 'unpriced' OR (cost_real_usd IS NULL AND cost_list_usd IS NULL))
);
CREATE INDEX model_calls_time_idx ON model_calls (company_id, time_unix_nano);
CREATE INDEX model_calls_conversation_idx ON model_calls (company_id, conversation_id, time_unix_nano);

-- 11. Agregado diário (mesma decisão). Dia × host × agente × modelo × workspace × assinatura. Soma só o que entrou de novo.
CREATE TABLE calls_daily (
    company_id        uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    day               date NOT NULL,
    host_id           uuid NOT NULL REFERENCES external_host(id) ON DELETE RESTRICT,
    agent_id          uuid NOT NULL REFERENCES external_agent(id) ON DELETE RESTRICT,
    workspace_id      uuid NOT NULL REFERENCES workspaces(id) ON DELETE RESTRICT,
    model             text NOT NULL DEFAULT '',
    subscription_key  text NOT NULL DEFAULT '',
    calls             bigint NOT NULL DEFAULT 0 CHECK (calls >= 0),
    errors            bigint NOT NULL DEFAULT 0 CHECK (errors >= 0),
    input_tokens      bigint NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens     bigint NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    cache_read_tokens bigint NOT NULL DEFAULT 0 CHECK (cache_read_tokens >= 0),
    cache_creation_tokens bigint NOT NULL DEFAULT 0 CHECK (cache_creation_tokens >= 0),
    cost_real_usd     numeric(18,6) NOT NULL DEFAULT 0 CHECK (cost_real_usd >= 0),
    cost_list_usd     numeric(18,6) NOT NULL DEFAULT 0 CHECK (cost_list_usd >= 0),
    unpriced_calls    bigint NOT NULL DEFAULT 0 CHECK (unpriced_calls >= 0),
    PRIMARY KEY (company_id, day, host_id, agent_id, workspace_id, model, subscription_key)
);
CREATE INDEX calls_daily_day_idx ON calls_daily (company_id, day);

-- 12. Caixa (ADR-0075 item 2, ADR-0077 item 3, ADR-0075 item 4.4): três tipos de item novos. Os itens continuam
-- derivados do estado; aqui só entram os tipos que `inbox_state` aceita.
ALTER TABLE inbox_state DROP CONSTRAINT inbox_state_item_kind_check;
ALTER TABLE inbox_state ADD CONSTRAINT inbox_state_item_kind_check CHECK (item_kind IN ('gate','task_blocked','task_held','run_failed','persona_error','alert','subscription','mention','goal_achieved','external_agent','external_host','repo_pairing'));

-- +goose Down
-- Os fatos brutos e as derivadas somem junto com as tabelas; a chave `ingest` só pode ser desfeita se não houver nenhuma.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM api_keys WHERE scope = 'ingest') THEN
        RAISE EXCEPTION 'há chaves de escopo ingest: revogue e apague antes de desfazer a 00208';
    END IF;
END $$;
-- +goose StatementEnd
DELETE FROM inbox_state WHERE item_kind IN ('external_agent','external_host','repo_pairing');
ALTER TABLE inbox_state DROP CONSTRAINT inbox_state_item_kind_check;
ALTER TABLE inbox_state ADD CONSTRAINT inbox_state_item_kind_check CHECK (item_kind IN ('gate','task_blocked','task_held','run_failed','persona_error','alert','subscription','mention','goal_achieved'));
DROP TABLE calls_daily;
DROP TABLE model_calls;
DROP TABLE telemetry_metrics;
DROP TABLE telemetry_logs;
DROP TABLE telemetry_spans;
DROP TABLE conversation_class_input;
DROP TABLE external_conversation;
DROP TABLE external_session;
DROP TABLE repo_pairing;
DROP TABLE external_host_alias;
DROP TABLE external_host;
DROP TABLE external_agent_alias;
DROP TABLE external_agent;
DROP INDEX workspaces_one_system_uq;
ALTER TABLE workspaces DROP COLUMN is_system;
ALTER TABLE api_keys DROP CONSTRAINT api_keys_ingest_company_check;
ALTER TABLE api_keys DROP CONSTRAINT api_keys_scope_check;
ALTER TABLE api_keys DROP COLUMN company_id;
ALTER TABLE api_keys ADD CONSTRAINT api_keys_scope_check CHECK (scope IN ('full', 'read'));

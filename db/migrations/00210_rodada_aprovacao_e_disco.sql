-- +goose Up
-- Rodada e aprovação externas (ADR-0080 e 0081), busca das conversas e sessões externas (ADR-0078), alerta de disco do
-- Postgres (comentário da #66, decisão do Bardi de 2026-10-25). Grupo 3 do contrato da v0.8 (#124): tabelas, colunas e
-- constraints novas, sem mover dado. A ingestão dos eventos `oute.swarm.*` e `oute.canal.*` vem na fatia 3 (#127).
--
-- Desenho (ADR-0080 item 3; ADR-0081 itens 2 e 3): o texto de uma etapa e o script de um pedido ficam no fato da ingestão
-- (`telemetry_logs`, pela chave de dedupe e pela hora), nunca copiados para o estado. A etapa guarda a revisão vigente, o
-- sha256 e o ponteiro para o fato; o pedido guarda o sha256 que o host mostra e a decisão. Quem lê o script passa pela
-- API, que confere o papel e grava `activity_event`.

-- 1. Ciclo (ADR-0080 item 7): as rodadas de uma mesma issue `<dono>/<repo>#<n>`. A origem é a da revisão vigente da triagem.
CREATE TABLE round_cycle (
    id          uuid PRIMARY KEY,
    company_id  uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    source_kind text NOT NULL DEFAULT 'external' CHECK (source_kind IN ('external')),
    issue_ref   text NOT NULL CHECK (length(issue_ref) <= 200 AND issue_ref ~ '^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+#[0-9]{1,9}$'),
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (company_id, issue_ref)
);

-- 2. Rodada externa (ADR-0080 itens 1, 2 e 7). `round_key` é o id da rodada no agent-studio (texto do fato); `name` é o
-- nome amigável do gerador único (ADR-0084), com o mesmo formato da conversa. Os atributos seguem a regra da 00208: cada um
-- guarda a hora do fato que o trouxe, e um fato mais antigo não troca o valor.
CREATE TABLE external_round (
    id           uuid PRIMARY KEY,
    company_id   uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    source_kind  text NOT NULL DEFAULT 'external' CHECK (source_kind IN ('external')),
    round_key    text NOT NULL CHECK (length(round_key) BETWEEN 1 AND 200),
    name         text NOT NULL CHECK (name ~ '^[A-Z][a-z]+_[A-Z][a-z]+(_[1-9][0-9]{2})?$'),
    cycle_id     uuid REFERENCES round_cycle(id) ON DELETE SET NULL,
    cycle_at     bigint,
    repo         text NOT NULL DEFAULT '',
    repo_at      bigint,
    workspace_id uuid REFERENCES workspaces(id) ON DELETE SET NULL,
    max_sessions integer CHECK (max_sessions >= 0),
    max_at       bigint,
    opened_at    bigint,
    closed_at    bigint,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (company_id, round_key),
    UNIQUE (company_id, name)
);
CREATE INDEX external_round_cycle_idx ON external_round (company_id, cycle_id) WHERE cycle_id IS NOT NULL;
CREATE INDEX external_round_open_idx ON external_round (company_id, opened_at DESC) WHERE closed_at IS NULL;

-- 3. Participante (ADR-0080 itens 1 e 2): um worker da rodada. Liga-se à sessão externa quando ela já chegou.
CREATE TABLE round_participant (
    id                  uuid PRIMARY KEY,
    company_id          uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    source_kind         text NOT NULL DEFAULT 'external' CHECK (source_kind IN ('external')),
    round_id            uuid NOT NULL REFERENCES external_round(id) ON DELETE CASCADE,
    slug                text NOT NULL CHECK (length(slug) BETWEEN 1 AND 200),
    issue_ref           text NOT NULL DEFAULT '' CHECK (length(issue_ref) <= 200),
    external_session_id uuid REFERENCES external_session(id) ON DELETE SET NULL,
    spawned_at          bigint,
    closed_at           bigint,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (round_id, slug)
);
CREATE INDEX round_participant_session_idx ON round_participant (external_session_id) WHERE external_session_id IS NOT NULL;

-- 4. Etapa da rodada (ADR-0080 itens 1 e 3). Uma linha por etapa lógica (rodada, tipo, chave); a revisão mais alta vence
-- em qualquer ordem de chegada. O resumo do ciclo (`cycle_summary`) pertence ao ciclo, não a uma rodada. O texto não entra
-- aqui: `fact_dedupe_key` e `fact_time_unix_nano` apontam para o fato em `telemetry_logs` (a tabela é particionada, por isso
-- sem chave estrangeira).
CREATE TABLE round_step (
    id                   uuid PRIMARY KEY,
    company_id           uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    source_kind          text NOT NULL DEFAULT 'external' CHECK (source_kind IN ('external')),
    round_id             uuid REFERENCES external_round(id) ON DELETE CASCADE,
    cycle_id             uuid REFERENCES round_cycle(id) ON DELETE CASCADE,
    kind                 text NOT NULL CHECK (kind IN ('triage', 'merge', 'kaizen', 'closing', 'cycle_summary')),
    step_key             text NOT NULL DEFAULT '' CHECK (step_key = '' OR step_key ~ '^[1-9][0-9]{0,8}$'),
    revision             bigint NOT NULL CHECK (revision >= 0),
    sha256               text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    review               text NOT NULL CHECK (review IN ('approved', 'rejected', 'no_reviewer')),
    reference_check      text NOT NULL CHECK (reference_check IN ('ok', 'failed', 'missing')),
    fact_dedupe_key      text NOT NULL CHECK (length(fact_dedupe_key) BETWEEN 1 AND 500),
    fact_time_unix_nano  bigint NOT NULL CHECK (fact_time_unix_nano >= 0),
    published_at         bigint NOT NULL CHECK (published_at >= 0),
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    CHECK ((kind = 'merge') = (step_key <> '')),
    CHECK ((kind = 'cycle_summary') = (cycle_id IS NOT NULL AND round_id IS NULL)),
    CHECK ((kind <> 'cycle_summary') = (round_id IS NOT NULL AND cycle_id IS NULL))
);
CREATE UNIQUE INDEX round_step_round_uq ON round_step (round_id, kind, step_key) WHERE round_id IS NOT NULL;
CREATE UNIQUE INDEX round_step_cycle_uq ON round_step (cycle_id, kind, step_key) WHERE cycle_id IS NOT NULL;

-- 5. Ação de uma etapa (ADR-0080 item 4). Cada revisão substitui as ações da anterior; a marca não depende desta linha
-- (ela aponta para a etapa e o id da ação), para que uma revisão nova não apague o que o Bardi marcou. `approval_key` é o
-- `pedido:<id>` da ação, o id do canal; quando há, o estado da ação é o do pedido (ADR-0080 item 4).
CREATE TABLE step_action (
    id           uuid PRIMARY KEY,
    company_id   uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    source_kind  text NOT NULL DEFAULT 'external' CHECK (source_kind IN ('external')),
    step_id      uuid NOT NULL REFERENCES round_step(id) ON DELETE CASCADE,
    action_id    text NOT NULL CHECK (action_id ~ '^[a-z][a-z0-9]{0,15}$'),
    approval_key text NOT NULL DEFAULT '' CHECK (approval_key = '' OR approval_key ~ '^[0-9]{8}-[0-9]{6}-[a-z0-9-]+$'),
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (step_id, action_id)
);

-- 6. Marca do Bardi numa ação (ADR-0080 item 4). Só se acrescenta: a marca mais nova (`marked_unix_nano`) vence. A migração
-- do agent-studio entra com `migrated` = true e o autor que a migração escolher.
CREATE TABLE action_mark (
    id              uuid PRIMARY KEY,
    company_id      uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    step_id         uuid NOT NULL REFERENCES round_step(id) ON DELETE CASCADE,
    action_id       text NOT NULL CHECK (action_id ~ '^[a-z][a-z0-9]{0,15}$'),
    state           text NOT NULL CHECK (state IN ('done', 'pending')),
    marked_by_type  text NOT NULL CHECK (marked_by_type IN ('human', 'system')),
    marked_by_id    uuid,
    marked_unix_nano bigint NOT NULL CHECK (marked_unix_nano >= 0),
    migrated        boolean NOT NULL DEFAULT false,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CHECK (marked_by_type = 'system' OR marked_by_id IS NOT NULL)
);
CREATE INDEX action_mark_latest_idx ON action_mark (step_id, action_id, marked_unix_nano DESC);

-- 7. Aprovação externa (ADR-0081 itens 1 a 4). `canal_id` é o `oute.canal.id` (o id do pedido). O estado é `pending`
-- ou `decided`; a decisão e o aprovador entram juntos. `script_sha256` é o da versão vigente; o script em si fica no fato
-- (`fact_dedupe_key`), e cada versão diferente que chega fica em `external_approval_version`.
CREATE TABLE external_approval (
    id                   uuid PRIMARY KEY,
    company_id           uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    source_kind          text NOT NULL DEFAULT 'external' CHECK (source_kind IN ('external')),
    canal_id             text NOT NULL CHECK (canal_id ~ '^[0-9]{8}-[0-9]{6}-[a-z0-9-]+$'),
    title                text NOT NULL DEFAULT '' CHECK (length(title) <= 300),
    requested_as         text NOT NULL DEFAULT '' CHECK (length(requested_as) <= 200),
    origin               text NOT NULL DEFAULT '' CHECK (length(origin) <= 200),
    host_id              uuid REFERENCES external_host(id) ON DELETE RESTRICT,
    host_name            text NOT NULL DEFAULT '' CHECK (length(host_name) <= 200),
    agent_id             uuid REFERENCES external_agent(id) ON DELETE SET NULL,
    external_session_id  uuid REFERENCES external_session(id) ON DELETE SET NULL,
    state                text NOT NULL CHECK (state IN ('pending', 'decided')),
    decision             text CHECK (decision IN ('rejected', 'executed')),
    approver             text CHECK (approver IN ('human', 'human_dark')),
    rc                   integer,
    duration_ms          integer CHECK (duration_ms >= 0),
    output_bytes         integer CHECK (output_bytes >= 0),
    decided_sha256       text CHECK (decided_sha256 ~ '^[0-9a-f]{64}$'),
    script_sha256        text NOT NULL DEFAULT '' CHECK (script_sha256 = '' OR script_sha256 ~ '^[0-9a-f]{64}$'),
    fact_dedupe_key      text NOT NULL DEFAULT '' CHECK (length(fact_dedupe_key) <= 500),
    fact_time_unix_nano  bigint,
    requested_at         bigint,
    decided_at           bigint,
    listed_at            bigint,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    UNIQUE (company_id, canal_id),
    CHECK ((state = 'decided') = (decision IS NOT NULL AND approver IS NOT NULL AND decided_sha256 IS NOT NULL))
);
CREATE INDEX external_approval_state_idx ON external_approval (company_id, state, requested_at DESC);

-- 8. Versões de um pedido (ADR-0081 item 4): uma linha por sha256 diferente que chegou. Com mais de uma, a página avisa.
CREATE TABLE external_approval_version (
    approval_id         uuid NOT NULL REFERENCES external_approval(id) ON DELETE CASCADE,
    sha256              text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    fact_dedupe_key     text NOT NULL CHECK (length(fact_dedupe_key) BETWEEN 1 AND 500),
    fact_time_unix_nano bigint NOT NULL CHECK (fact_time_unix_nano >= 0),
    seen_at             bigint NOT NULL CHECK (seen_at >= 0),
    PRIMARY KEY (approval_id, sha256)
);

-- 9. Lista da outbox de cada host (ADR-0081 item 5): a última lista que o host mandou, a de hora mais nova vence. Um pedido
-- que não está nela, depois da folga de 60 s, sai das telas sem ser apagado (`listed_at` guarda a última vez que esteve).
CREATE TABLE external_outbox_snapshot (
    host_id     uuid PRIMARY KEY REFERENCES external_host(id) ON DELETE CASCADE,
    company_id  uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    snapshot_at bigint NOT NULL CHECK (snapshot_at >= 0),
    canal_ids   text[] NOT NULL DEFAULT '{}' CHECK (cardinality(canal_ids) <= 1000),
    received_at bigint NOT NULL CHECK (received_at >= 0)
);

-- 10. Busca das conversas e sessões externas (ADR-0078, "Consequências"; ADR-0088). Mesmo padrão de `runs` e `personas`: uma
-- coluna gerada com `hanko_fold` e um índice GIN. A tag da conversa fica de fora: `array_to_string` não é imutável, e a
-- coluna gerada exige função imutável.
ALTER TABLE external_conversation ADD COLUMN search_tsv tsvector GENERATED ALWAYS AS (
    setweight(to_tsvector('simple', hanko_fold(left(name, 1000))), 'A') ||
    setweight(to_tsvector('simple', hanko_fold(left(class, 1000) || ' ' || left(oute_repo, 1000))), 'B')
) STORED;
CREATE INDEX external_conversation_search_idx ON external_conversation USING gin (search_tsv);

ALTER TABLE external_session ADD COLUMN search_tsv tsvector GENERATED ALWAYS AS (
    setweight(to_tsvector('simple', hanko_fold(left(task_id, 1000))), 'A') ||
    setweight(to_tsvector('simple', hanko_fold(left(oute_repo, 1000) || ' ' || left(slug, 1000))), 'B')
) STORED;
CREATE INDEX external_session_search_idx ON external_session USING gin (search_tsv);

-- 11. Alerta de disco do Postgres (comentário da #66; ADR-0062 `hanko doctor`). Sujeito: a empresa, como os demais alertas
-- (`alerts.company_id` é obrigatório). Os limites seguem `company_alert_settings` (`limit.db_disk_*`), com o padrão no código.
-- Esta constraint é a única mudança de alertas: os 17 tipos da 00209 ficam como estão.
-- +goose StatementBegin
DO $$
DECLARE
    kinds text[] := ARRAY['persona_silent', 'run_stuck', 'budget_near_limit', 'persona_not_ready', 'secret_rotation_due',
        'queue', 'destination_refusing', 'host_no_data', 'spool', 'quota', 'round_stalled', 'round_old', 'llm_proxy_down',
        'memory_capture', 'memory_hooks_untrusted', 'price_changed', 'price_sources_diverge', 'price_source_down',
        'price_model_unpriced', 'price_locked_differs', 'cost_claude_diff', 'cost_subscription_unpriced', 'db_disk_low'];
BEGIN
    ALTER TABLE alerts DROP CONSTRAINT alerts_kind_check;
    EXECUTE format('ALTER TABLE alerts ADD CONSTRAINT alerts_kind_check CHECK (kind = ANY (%L::text[]))', kinds);
END $$;
-- +goose StatementEnd

-- 12. Caixa (ADR-0080 item 6; ADR-0081 item 6): a pergunta da rodada e a aprovação externa pendente são itens próprios.
-- Os itens continuam derivados do estado; aqui só entram os tipos que `inbox_state` aceita.
ALTER TABLE inbox_state DROP CONSTRAINT inbox_state_item_kind_check;
ALTER TABLE inbox_state ADD CONSTRAINT inbox_state_item_kind_check CHECK (item_kind IN ('gate','task_blocked','task_held','run_failed','persona_error','alert','subscription','mention','goal_achieved','external_agent','external_host','repo_pairing','round_question','external_approval'));

-- +goose Down
-- Rodadas, etapas, marcas, aprovações e listas da outbox somem com as tabelas; a busca, o alerta de disco e os itens da
-- Caixa voltam ao schema da 00209. Nenhum dado de fora da rodada e da aprovação muda.
DELETE FROM inbox_state WHERE item_kind IN ('round_question', 'external_approval');
ALTER TABLE inbox_state DROP CONSTRAINT inbox_state_item_kind_check;
ALTER TABLE inbox_state ADD CONSTRAINT inbox_state_item_kind_check CHECK (item_kind IN ('gate','task_blocked','task_held','run_failed','persona_error','alert','subscription','mention','goal_achieved','external_agent','external_host','repo_pairing'));

-- +goose StatementBegin
DO $$
DECLARE
    kinds text[] := ARRAY['persona_silent', 'run_stuck', 'budget_near_limit', 'persona_not_ready', 'secret_rotation_due',
        'queue', 'destination_refusing', 'host_no_data', 'spool', 'quota', 'round_stalled', 'round_old', 'llm_proxy_down',
        'memory_capture', 'memory_hooks_untrusted', 'price_changed', 'price_sources_diverge', 'price_source_down',
        'price_model_unpriced', 'price_locked_differs', 'cost_claude_diff', 'cost_subscription_unpriced'];
BEGIN
    DELETE FROM alerts WHERE kind = 'db_disk_low';
    ALTER TABLE alerts DROP CONSTRAINT alerts_kind_check;
    EXECUTE format('ALTER TABLE alerts ADD CONSTRAINT alerts_kind_check CHECK (kind = ANY (%L::text[]))', kinds);
END $$;
-- +goose StatementEnd

DROP INDEX external_session_search_idx;
ALTER TABLE external_session DROP COLUMN search_tsv;
DROP INDEX external_conversation_search_idx;
ALTER TABLE external_conversation DROP COLUMN search_tsv;
DROP TABLE external_outbox_snapshot;
DROP TABLE external_approval_version;
DROP TABLE external_approval;
DROP TABLE action_mark;
DROP TABLE step_action;
DROP INDEX round_step_cycle_uq;
DROP INDEX round_step_round_uq;
DROP TABLE round_step;
DROP TABLE round_participant;
DROP TABLE external_round;
DROP TABLE round_cycle;

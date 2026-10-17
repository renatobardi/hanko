-- +goose Up
-- Cards de status (ADR-0032): um resumo vivo de um conjunto de tasks, mantido por uma persona resumidora.
-- O humano escreve o interesse em linguagem natural; a resumidora o transforma em filtros de task (montagem) e
-- mantém um resumo em markdown que é refeito quando as tasks observadas mudam. Os gatilhos de run (status_card)
-- já existem desde a 00027.

CREATE TABLE status_cards (
    id                    uuid PRIMARY KEY,
    company_id            uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    title                 text NOT NULL DEFAULT '' CHECK (char_length(title) <= 200),
    title_pinned          boolean NOT NULL DEFAULT false,
    interest              text NOT NULL CHECK (char_length(interest) BETWEEN 1 AND 20000),
    -- hash do interesse da última montagem pedida: chave de idempotência da task de montagem
    interest_hash         text NOT NULL DEFAULT '',
    summarizer_persona_id uuid REFERENCES personas(id) ON DELETE SET NULL,
    -- lista de 1 a 10 filtros de task (domain.StatusCardQuery), gravada pela resumidora na montagem
    queries               jsonb NOT NULL DEFAULT '[]'::jsonb,
    query_version         integer NOT NULL DEFAULT 0,
    query_compiled_at     timestamptz,
    refresh_policy        jsonb NOT NULL DEFAULT '{}'::jsonb,
    state                 text NOT NULL DEFAULT 'compiling'
                          CHECK (state IN ('compiling','active','error','paused_budget','paused_hours')),
    pending_change_count  integer NOT NULL DEFAULT 0,
    pending_change_hash   text NOT NULL DEFAULT '',
    last_change_at        timestamptz,
    -- task_id -> {key, title, status, priority, persona, updated_at, último comentário humano} da última versão
    fingerprint           jsonb NOT NULL DEFAULT '{}'::jsonb,
    fingerprint_at        timestamptz,
    mentioned_task_ids    uuid[] NOT NULL DEFAULT '{}',
    summary               text NOT NULL DEFAULT '' CHECK (octet_length(summary) <= 65536),
    summary_revision      integer NOT NULL DEFAULT 0,
    -- versão dos filtros que o resumo atual reflete e incrementais desde o último full
    summary_query_version integer NOT NULL DEFAULT 0,
    incrementals_since_full integer NOT NULL DEFAULT 0,
    last_update_kind      text CHECK (last_update_kind IS NULL OR last_update_kind IN ('full','incremental')),
    last_generated_at     timestamptz,
    last_model            text NOT NULL DEFAULT '',
    generating_task_id    uuid REFERENCES tasks(id) ON DELETE SET NULL,
    generating_since      timestamptz,
    failure_reason        text NOT NULL DEFAULT '',
    next_eval_at          timestamptz,
    archived_at           timestamptz,
    created_by_type       text NOT NULL CHECK (created_by_type IN ('human','persona','system')),
    created_by_id         uuid,
    created_at            timestamptz NOT NULL,
    updated_at            timestamptz NOT NULL
);
CREATE INDEX status_cards_company_idx ON status_cards (company_id, archived_at);
CREATE INDEX status_cards_due_idx ON status_cards (next_eval_at) WHERE archived_at IS NULL AND next_eval_at IS NOT NULL;
CREATE INDEX status_cards_persona_idx ON status_cards (company_id, created_by_id) WHERE created_by_type = 'persona';
CREATE INDEX status_cards_generating_idx ON status_cards (generating_task_id) WHERE generating_task_id IS NOT NULL;

-- Histórico imutável do resumo (no máximo 50 por card; o service apaga as mais antigas).
CREATE TABLE status_card_revisions (
    id             uuid PRIMARY KEY,
    company_id     uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    card_id        uuid NOT NULL REFERENCES status_cards(id) ON DELETE CASCADE,
    revision       integer NOT NULL CHECK (revision >= 1),
    markdown       text NOT NULL CHECK (octet_length(markdown) <= 65536),
    change_summary text NOT NULL DEFAULT '',
    kind           text NOT NULL CHECK (kind IN ('full','incremental')),
    run_id         uuid REFERENCES runs(id) ON DELETE SET NULL,
    model          text NOT NULL DEFAULT '',
    mentioned_task_ids uuid[] NOT NULL DEFAULT '{}',
    created_at     timestamptz NOT NULL,
    UNIQUE (card_id, revision)
);

-- Rodadas: montagem (compile) e gerações (full/incremental), com as mudanças que as motivaram e o custo da run
-- (no máximo 200 por card).
CREATE TABLE status_card_updates (
    id             uuid PRIMARY KEY,
    company_id     uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    card_id        uuid NOT NULL REFERENCES status_cards(id) ON DELETE CASCADE,
    kind           text NOT NULL CHECK (kind IN ('compile','full','incremental')),
    trigger        text NOT NULL CHECK (trigger IN ('manual','interval','reactive','restore','query')),
    task_id        uuid REFERENCES tasks(id) ON DELETE SET NULL,
    run_id         uuid REFERENCES runs(id) ON DELETE SET NULL,
    changes        jsonb NOT NULL DEFAULT '[]'::jsonb,
    -- retrato das tasks no momento da geração: vira o fingerprint do card quando o resumo é gravado
    fingerprint    jsonb NOT NULL DEFAULT '{}'::jsonb,
    input_tokens   bigint NOT NULL DEFAULT 0,
    output_tokens  bigint NOT NULL DEFAULT 0,
    cost_usd       numeric(14,6),
    model          text NOT NULL DEFAULT '',
    query_version  integer NOT NULL DEFAULT 0,
    change_summary text NOT NULL DEFAULT '',
    started_at     timestamptz NOT NULL,
    finished_at    timestamptz,
    status         text NOT NULL DEFAULT 'running' CHECK (status IN ('running','ok','failed')),
    error          text NOT NULL DEFAULT ''
);
CREATE INDEX status_card_updates_card_idx ON status_card_updates (card_id, started_at DESC);
CREATE UNIQUE INDEX status_card_updates_task_idx ON status_card_updates (task_id) WHERE task_id IS NOT NULL;

-- +goose Down
DROP TABLE status_card_updates;
DROP TABLE status_card_revisions;
DROP TABLE status_cards;

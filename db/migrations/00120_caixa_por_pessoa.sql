-- +goose Up
-- Caixa de entrada por pessoa (ADR-0042). Os itens da Caixa continuam derivados do estado da empresa (gates, tasks
-- bloqueadas, execuções que falharam...); aqui fica só o que é de cada usuário: lido e arquivado, mais as menções
-- feitas em comentários. Tudo aditivo.

-- Estado de um item para um usuário. item_id aponta para a linha de origem (gate, task, run, persona, alerta,
-- assinatura, menção, meta) sem FK: a origem vive em tabelas diferentes e o item some da Caixa quando a condição
-- acaba. A impressão digital (fingerprint) é a "versão" do item no momento em que a pessoa o marcou: se o item
-- mudou (nova falha da persona, novo bloqueio da task), a impressão atual difere e o item reaparece como novo.
CREATE TABLE inbox_state (
    company_id  uuid        NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    user_id     uuid        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    item_kind   text        NOT NULL CHECK (item_kind IN ('gate','task_blocked','task_held','run_failed','persona_error','alert','subscription','mention','goal_achieved')),
    item_id     uuid        NOT NULL,
    fingerprint text        NOT NULL DEFAULT '' CHECK (length(fingerprint) <= 200),
    read_at     timestamptz,
    archived_at timestamptz,
    updated_at  timestamptz NOT NULL,
    PRIMARY KEY (company_id, user_id, item_kind, item_id)
);
CREATE INDEX inbox_state_user_idx ON inbox_state (company_id, user_id, updated_at DESC);

-- Menções (@usuario, @persona) em comentários de task. Humano citado vai para a Caixa dele; persona citada é acordada
-- (só quando o autor do comentário é humano). Uma linha por (comentário, alvo).
CREATE TABLE comment_mentions (
    id          uuid        PRIMARY KEY,
    company_id  uuid        NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    comment_id  uuid        NOT NULL REFERENCES task_comments(id) ON DELETE CASCADE,
    task_id     uuid        NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    target_type text        NOT NULL CHECK (target_type IN ('user','persona')),
    user_id     uuid        REFERENCES users(id) ON DELETE CASCADE,
    persona_id  uuid        REFERENCES personas(id) ON DELETE CASCADE,
    author_type text        NOT NULL CHECK (author_type IN ('human','persona','system')),
    author_id   uuid,
    created_at  timestamptz NOT NULL,
    CHECK ((target_type = 'user' AND user_id IS NOT NULL AND persona_id IS NULL)
        OR (target_type = 'persona' AND persona_id IS NOT NULL AND user_id IS NULL))
);
CREATE UNIQUE INDEX comment_mentions_target_uq ON comment_mentions (comment_id, target_type, COALESCE(user_id, persona_id));
CREATE INDEX comment_mentions_user_idx ON comment_mentions (company_id, user_id, created_at DESC) WHERE user_id IS NOT NULL;
CREATE INDEX comment_mentions_task_idx ON comment_mentions (task_id);

-- +goose Down
DROP TABLE IF EXISTS comment_mentions;
DROP TABLE IF EXISTS inbox_state;

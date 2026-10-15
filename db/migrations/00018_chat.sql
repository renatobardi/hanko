-- +goose Up
-- Chat (ADR-0027): conversa direta de um humano com uma persona, fora de uma task.
-- Cada mensagem humana dispara uma run da persona com trigger 'chat', ligada à conversa (runs.conversation_id)
-- e sem task (runs.task_id passa a aceitar NULL, com CHECK de coerência).

CREATE TABLE conversations (
    id              uuid PRIMARY KEY,
    company_id      uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    persona_id      uuid NOT NULL REFERENCES personas(id) ON DELETE CASCADE,
    title           text NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
    status          text NOT NULL DEFAULT 'open' CHECK (status IN ('open','archived')),
    created_by_type text NOT NULL CHECK (created_by_type IN ('human','persona','system')),
    created_by_id   uuid,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    last_message_at timestamptz NOT NULL DEFAULT now()
);
-- lista da empresa (abertas/arquivadas, mais recente primeiro) e lista da página da persona
CREATE INDEX conversations_company_idx ON conversations (company_id, status, last_message_at DESC, id DESC);
CREATE INDEX conversations_persona_idx ON conversations (persona_id, last_message_at DESC, id DESC);

CREATE TABLE conversation_messages (
    id              uuid PRIMARY KEY,
    company_id      uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    author_type     text NOT NULL CHECK (author_type IN ('human','persona','system')),
    author_id       uuid,
    body            text NOT NULL CHECK (length(body) BETWEEN 1 AND 100000),
    -- mensagem da persona/aviso: a run que a produziu; mensagem humana: a run que a recolheu (NULL = ainda na fila)
    run_id          uuid REFERENCES runs(id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX conversation_messages_thread_idx ON conversation_messages (conversation_id, created_at, id);
-- mensagens humanas ainda não recolhidas por uma run (a próxima run coalesce todas)
CREATE INDEX conversation_messages_pending_idx ON conversation_messages (conversation_id, created_at, id)
    WHERE author_type = 'human' AND run_id IS NULL;
CREATE INDEX conversation_messages_run_idx ON conversation_messages (run_id) WHERE run_id IS NOT NULL;

ALTER TABLE runs ALTER COLUMN task_id DROP NOT NULL;
ALTER TABLE runs ADD COLUMN conversation_id uuid REFERENCES conversations(id) ON DELETE CASCADE;
ALTER TABLE runs DROP CONSTRAINT runs_trigger_check;
ALTER TABLE runs ADD CONSTRAINT runs_trigger_check CHECK (trigger IN ('assign','comment','manual','heartbeat','gate','flow','review','chat'));
-- Run de chat: conversa e sem task. Qualquer outra: task e sem conversa.
ALTER TABLE runs ADD CONSTRAINT runs_chat_shape_check CHECK (
    (trigger = 'chat' AND task_id IS NULL AND conversation_id IS NOT NULL)
    OR (trigger <> 'chat' AND task_id IS NOT NULL AND conversation_id IS NULL));
-- no máximo uma run ativa por conversa (as mensagens enquanto ela roda entram na próxima)
CREATE UNIQUE INDEX runs_one_active_per_conversation ON runs (conversation_id)
    WHERE conversation_id IS NOT NULL AND status IN ('queued','running');
CREATE INDEX runs_conversation_created_idx ON runs (conversation_id, created_at DESC) WHERE conversation_id IS NOT NULL;

-- +goose Down
DELETE FROM runs WHERE trigger = 'chat';
DROP INDEX runs_conversation_created_idx;
DROP INDEX runs_one_active_per_conversation;
ALTER TABLE runs DROP CONSTRAINT runs_chat_shape_check;
ALTER TABLE runs DROP CONSTRAINT runs_trigger_check;
ALTER TABLE runs ADD CONSTRAINT runs_trigger_check CHECK (trigger IN ('assign','comment','manual','heartbeat','gate','flow','review'));
ALTER TABLE runs DROP COLUMN conversation_id;
ALTER TABLE runs ALTER COLUMN task_id SET NOT NULL;
DROP TABLE conversation_messages;
DROP TABLE conversations;

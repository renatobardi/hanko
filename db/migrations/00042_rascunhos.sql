-- +goose Up
-- Rascunhos no servidor (ADR-0052): o texto que alguém começou a escrever num comentário de task ou numa mensagem de
-- conversa, por usuário + empresa + alvo. Sobrevive à troca de dispositivo e some quando o texto é enviado. user_key é
-- o id do usuário (sessão ou API key) ou "local" no modo trusted, sem usuário. O alvo é task OU conversa, cada um com
-- a sua chave estrangeira (apagar a task ou a conversa apaga os rascunhos).
CREATE TABLE drafts (
    id              uuid PRIMARY KEY,
    company_id      uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    user_key        text NOT NULL CHECK (length(user_key) BETWEEN 1 AND 100),
    target_type     text NOT NULL CHECK (target_type IN ('task','conversation')),
    task_id         uuid REFERENCES tasks(id) ON DELETE CASCADE,
    conversation_id uuid REFERENCES conversations(id) ON DELETE CASCADE,
    body            text NOT NULL CHECK (length(body) BETWEEN 1 AND 100000),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CHECK ((target_type = 'task' AND task_id IS NOT NULL AND conversation_id IS NULL)
        OR (target_type = 'conversation' AND conversation_id IS NOT NULL AND task_id IS NULL))
);
CREATE UNIQUE INDEX drafts_task_uq ON drafts (company_id, user_key, task_id) WHERE task_id IS NOT NULL;
CREATE UNIQUE INDEX drafts_conversation_uq ON drafts (company_id, user_key, conversation_id) WHERE conversation_id IS NOT NULL;
CREATE INDEX drafts_updated_idx ON drafts (updated_at);

-- +goose Down
DROP TABLE drafts;

-- +goose Up
-- Anexos (ADR-0052): um anexo é um artefato com kind = 'attachment', ligado a uma task (e opcionalmente a um comentário)
-- ou a uma conversa do chat (e opcionalmente a uma mensagem). Mesmo conteúdo (artifact_blobs/BlobStore), mesma cota,
-- mesma entrega segura. Um anexo é sempre a versão 1: não forma cadeia de versões.
ALTER TABLE artifacts ADD COLUMN kind text NOT NULL DEFAULT 'artifact' CHECK (kind IN ('artifact','attachment'));
ALTER TABLE artifacts ADD COLUMN comment_id uuid REFERENCES task_comments(id) ON DELETE CASCADE;
ALTER TABLE artifacts ADD COLUMN conversation_id uuid REFERENCES conversations(id) ON DELETE CASCADE;
ALTER TABLE artifacts ADD COLUMN message_id uuid REFERENCES conversation_messages(id) ON DELETE CASCADE;

-- Só anexo tem ligação a comentário/conversa/mensagem; todo anexo tem task ou conversa (não os dois).
ALTER TABLE artifacts ADD CONSTRAINT artifacts_kind_shape_check CHECK (
    (kind = 'artifact' AND comment_id IS NULL AND conversation_id IS NULL AND message_id IS NULL)
    OR (kind = 'attachment' AND (task_id IS NOT NULL) <> (conversation_id IS NOT NULL)
        AND (comment_id IS NULL OR task_id IS NOT NULL) AND (message_id IS NULL OR conversation_id IS NOT NULL)));

-- A cadeia de versões passa a incluir o kind e a conversa: um anexo e um artefato de mesmo nome não se misturam, e
-- anexos de conversas diferentes (sem task) também não.
DROP INDEX artifacts_chain_version_uq;
CREATE UNIQUE INDEX artifacts_chain_version_uq
    ON artifacts (company_id, kind, COALESCE(task_id, '00000000-0000-0000-0000-000000000000'::uuid),
                  COALESCE(conversation_id, '00000000-0000-0000-0000-000000000000'::uuid), name, version);
CREATE INDEX artifacts_comment_idx ON artifacts (comment_id) WHERE comment_id IS NOT NULL;
CREATE INDEX artifacts_conversation_idx ON artifacts (conversation_id, created_at) WHERE conversation_id IS NOT NULL;
CREATE INDEX artifacts_message_idx ON artifacts (message_id) WHERE message_id IS NOT NULL;

-- +goose Down
DROP INDEX artifacts_message_idx;
DROP INDEX artifacts_conversation_idx;
DROP INDEX artifacts_comment_idx;
DROP INDEX artifacts_chain_version_uq;
DELETE FROM artifacts WHERE kind = 'attachment';
CREATE UNIQUE INDEX artifacts_chain_version_uq
    ON artifacts (company_id, COALESCE(task_id, '00000000-0000-0000-0000-000000000000'::uuid), name, version);
ALTER TABLE artifacts DROP CONSTRAINT artifacts_kind_shape_check;
ALTER TABLE artifacts DROP COLUMN message_id;
ALTER TABLE artifacts DROP COLUMN conversation_id;
ALTER TABLE artifacts DROP COLUMN comment_id;
ALTER TABLE artifacts DROP COLUMN kind;

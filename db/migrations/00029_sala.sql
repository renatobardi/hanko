-- +goose Up
-- Sala (ADR-0033): uma conversa de um ou mais humanos com várias personas ao mesmo tempo. Estende o chat
-- (ADR-0027): a sala é uma conversa com kind = 'room', sem persona própria (persona_id NULL) e com as personas
-- em conversation_participants. Cada mensagem humana pode ser recolhida por várias runs (uma por persona), o
-- que fica em conversation_message_runs; conversation_messages.run_id continua só para o chat direto (na sala,
-- ele marca apenas a run que produziu a resposta ou o aviso).

ALTER TABLE conversations ADD COLUMN kind text NOT NULL DEFAULT 'direct' CHECK (kind IN ('direct','room'));
ALTER TABLE conversations ALTER COLUMN persona_id DROP NOT NULL;
-- conversa direta tem a sua persona; a sala não tem (as personas estão em conversation_participants)
ALTER TABLE conversations ADD CONSTRAINT conversations_kind_persona_check CHECK (
    (kind = 'direct' AND persona_id IS NOT NULL) OR (kind = 'room' AND persona_id IS NULL));
-- configuração da sala: mode (mentions|all), moderator_persona_id, max_hops, max_runs_per_message (0 = participantes)
ALTER TABLE conversations ADD COLUMN room_settings jsonb NOT NULL DEFAULT '{}'::jsonb;
CREATE INDEX conversations_company_kind_idx ON conversations (company_id, kind, status, last_message_at DESC, id DESC);

CREATE TABLE conversation_participants (
    company_id      uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    persona_id      uuid NOT NULL REFERENCES personas(id) ON DELETE CASCADE,
    added_at        timestamptz NOT NULL DEFAULT now(),
    added_by_type   text NOT NULL CHECK (added_by_type IN ('human','persona','system')),
    added_by_id     uuid,
    -- NULL = está na sala; uma persona que sai e volta reaproveita a linha (removed_at volta a NULL)
    removed_at      timestamptz,
    PRIMARY KEY (conversation_id, persona_id)
);
-- "Salas em que participa" (página da persona)
CREATE INDEX conversation_participants_persona_idx ON conversation_participants (persona_id, conversation_id) WHERE removed_at IS NULL;

-- quem falou (resposta ou aviso sobre uma persona da sala), a que mensagem responde e quem foi mencionado
ALTER TABLE conversation_messages ADD COLUMN persona_id uuid REFERENCES personas(id) ON DELETE SET NULL;
ALTER TABLE conversation_messages ADD COLUMN reply_to_id uuid REFERENCES conversation_messages(id) ON DELETE SET NULL;
ALTER TABLE conversation_messages ADD COLUMN mentions uuid[] NOT NULL DEFAULT '{}';

-- Entregas de uma mensagem da sala a uma persona: a mensagem (humana, ou de outra persona que a mencionou) espera
-- (run_id NULL) até uma run daquela persona recolhê-la. origin_message_id é a mensagem humana que abriu a cadeia
-- e hop o salto (0 = a própria mensagem humana; 1 = resposta de uma persona que acordou outra; ...).
CREATE TABLE conversation_message_runs (
    company_id        uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    conversation_id   uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    message_id        uuid NOT NULL REFERENCES conversation_messages(id) ON DELETE CASCADE,
    persona_id        uuid NOT NULL REFERENCES personas(id) ON DELETE CASCADE,
    run_id            uuid REFERENCES runs(id) ON DELETE CASCADE,
    origin_message_id uuid NOT NULL REFERENCES conversation_messages(id) ON DELETE CASCADE,
    hop               integer NOT NULL DEFAULT 0 CHECK (hop BETWEEN 0 AND 5),
    created_at        timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (message_id, persona_id)
);
-- fila por (sala, persona): o que a próxima run da persona recolhe
CREATE INDEX conversation_message_runs_pending_idx ON conversation_message_runs (conversation_id, persona_id, created_at)
    WHERE run_id IS NULL;
CREATE INDEX conversation_message_runs_run_idx ON conversation_message_runs (run_id) WHERE run_id IS NOT NULL;
CREATE INDEX conversation_message_runs_origin_idx ON conversation_message_runs (origin_message_id);

-- Uma run ativa por (conversa, persona) em vez de por conversa: na sala cada persona tem a sua; o chat direto
-- só tem uma persona, então continua com uma só.
DROP INDEX runs_one_active_per_conversation;
CREATE UNIQUE INDEX runs_one_active_per_conversation_persona ON runs (conversation_id, persona_id)
    WHERE conversation_id IS NOT NULL AND status IN ('queued','running');

-- +goose Down
-- as salas (e as runs delas, por cascata) não cabem no modelo anterior
DELETE FROM conversations WHERE kind = 'room';
DROP INDEX runs_one_active_per_conversation_persona;
CREATE UNIQUE INDEX runs_one_active_per_conversation ON runs (conversation_id)
    WHERE conversation_id IS NOT NULL AND status IN ('queued','running');
DROP TABLE conversation_message_runs;
ALTER TABLE conversation_messages DROP COLUMN mentions;
ALTER TABLE conversation_messages DROP COLUMN reply_to_id;
ALTER TABLE conversation_messages DROP COLUMN persona_id;
DROP TABLE conversation_participants;
DROP INDEX conversations_company_kind_idx;
ALTER TABLE conversations DROP COLUMN room_settings;
ALTER TABLE conversations DROP CONSTRAINT conversations_kind_persona_check;
ALTER TABLE conversations ALTER COLUMN persona_id SET NOT NULL;
ALTER TABLE conversations DROP COLUMN kind;

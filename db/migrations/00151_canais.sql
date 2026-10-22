-- +goose Up
-- Canais externos: Telegram e GitHub (ADR-0046, M19 · item #42). Aditiva.

CREATE TABLE channels (
    id                  uuid PRIMARY KEY,
    company_id          uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    kind                text NOT NULL CHECK (kind IN ('telegram', 'github')),
    name                text NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
    enabled             boolean NOT NULL DEFAULT false,
    -- persona que conversa (Telegram) ou é acordada (GitHub); nula = canal sem persona (só decide gates).
    persona_id          uuid REFERENCES personas(id) ON DELETE SET NULL,
    project_id          uuid REFERENCES projects(id) ON DELETE SET NULL,
    -- referências ${secret:NOME} (ADR-0050); o valor nunca mora aqui.
    token_ref           text NOT NULL DEFAULT '',
    webhook_secret_ref  text NOT NULL DEFAULT '',
    config              jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- identificador opaco da URL de entrada; não é o segredo de autenticação.
    inbound_token       text NOT NULL UNIQUE,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (company_id, name)
);
CREATE INDEX channels_company_idx ON channels (company_id);

-- Vínculo entre a pessoa do canal (external_id) e um usuário do Hanko. Nasce pending com o código (hash) e o external_id
-- nulo; a prova no canal preenche external_id e passa a linked.
CREATE TABLE channel_identities (
    id              uuid PRIMARY KEY,
    company_id      uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    channel_id      uuid NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    external_id     text,
    external_label  text NOT NULL DEFAULT '',
    status          text NOT NULL CHECK (status IN ('pending', 'linked', 'revoked')),
    code_hash       text NOT NULL DEFAULT '',
    code_expires_at timestamptz,
    linked_at       timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (channel_id, external_id)
);
CREATE UNIQUE INDEX channel_identities_user_uq ON channel_identities (channel_id, user_id) WHERE status <> 'revoked';
CREATE UNIQUE INDEX channel_identities_code_uq ON channel_identities (code_hash) WHERE code_hash <> '';
CREATE INDEX channel_identities_company_idx ON channel_identities (company_id);

-- Interlocutor → conversa (Telegram) ou task (GitHub).
CREATE TABLE channel_threads (
    id              uuid PRIMARY KEY,
    company_id      uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    channel_id      uuid NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    peer            text NOT NULL,
    identity_id     uuid REFERENCES channel_identities(id) ON DELETE SET NULL,
    conversation_id uuid REFERENCES conversations(id) ON DELETE SET NULL,
    task_id         uuid REFERENCES tasks(id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (channel_id, peer)
);
CREATE INDEX channel_threads_conversation_idx ON channel_threads (conversation_id) WHERE conversation_id IS NOT NULL;
CREATE INDEX channel_threads_task_idx ON channel_threads (task_id) WHERE task_id IS NOT NULL;

-- Mensagens que entram e saem, com o estado de entrega. A entrada é idempotente por (canal, direção, external_id).
CREATE TABLE channel_messages (
    id               uuid PRIMARY KEY,
    company_id       uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    channel_id       uuid NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    direction        text NOT NULL CHECK (direction IN ('in', 'out')),
    kind             text NOT NULL DEFAULT 'text' CHECK (kind IN ('text', 'command', 'action', 'gate', 'notice', 'reply', 'review', 'event')),
    peer             text NOT NULL DEFAULT '',
    identity_id      uuid REFERENCES channel_identities(id) ON DELETE SET NULL,
    external_id      text,
    body             text NOT NULL DEFAULT '',
    -- entrada: bruto normalizado (anexos a baixar, ação do botão); saída: {buttons, artifact_ids, review}.
    payload          jsonb NOT NULL DEFAULT '{}'::jsonb,
    subject_type     text NOT NULL DEFAULT '',
    subject_id       uuid,
    -- entrada: received → processed | ignored | failed; saída: pending → sent | failed.
    status           text NOT NULL CHECK (status IN ('received', 'processed', 'ignored', 'pending', 'sent', 'failed')),
    status_reason    text NOT NULL DEFAULT '' CHECK (length(status_reason) <= 500),
    attempts         integer NOT NULL DEFAULT 0,
    last_error       text NOT NULL DEFAULT '' CHECK (length(last_error) <= 500),
    provider_msg_id  text NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    sent_at          timestamptz
);
CREATE UNIQUE INDEX channel_messages_in_uq ON channel_messages (channel_id, direction, external_id) WHERE external_id IS NOT NULL;
CREATE INDEX channel_messages_channel_idx ON channel_messages (channel_id, created_at DESC);
CREATE INDEX channel_messages_pending_idx ON channel_messages (created_at) WHERE direction = 'out' AND status = 'pending';

-- Nova feature do decision model: resposta rápida aos canais (ADR-0046 §D.5).
ALTER TABLE decision_features DROP CONSTRAINT decision_features_feature_check;
ALTER TABLE decision_features ADD CONSTRAINT decision_features_feature_check
    CHECK (feature IN ('triage', 'phase_classifier', 'name_summary', 'fast_response'));
ALTER TABLE decision_invocations DROP CONSTRAINT decision_invocations_feature_check;
ALTER TABLE decision_invocations ADD CONSTRAINT decision_invocations_feature_check
    CHECK (feature IN ('triage', 'phase_classifier', 'name_summary', 'fast_response'));

-- +goose Down
ALTER TABLE decision_invocations DROP CONSTRAINT decision_invocations_feature_check;
DELETE FROM decision_invocations WHERE feature = 'fast_response';
ALTER TABLE decision_invocations ADD CONSTRAINT decision_invocations_feature_check
    CHECK (feature IN ('triage', 'phase_classifier', 'name_summary'));
ALTER TABLE decision_features DROP CONSTRAINT decision_features_feature_check;
DELETE FROM decision_features WHERE feature = 'fast_response';
ALTER TABLE decision_features ADD CONSTRAINT decision_features_feature_check
    CHECK (feature IN ('triage', 'phase_classifier', 'name_summary'));
DROP TABLE channel_messages;
DROP TABLE channel_threads;
DROP TABLE channel_identities;
DROP TABLE channels;

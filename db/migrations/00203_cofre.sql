-- +goose Up
-- Cofre para agentes (ADR-0068), envio (ADR-0069) e reautenticação com passkey (ADR-0070). Só o contrato: tabelas e
-- colunas novas, sem mover dado. A cópia de secrets.value_enc para secret_fields e a coleção `padrão` vêm na migration
-- da fatia 1, junto com o código que lê campos (ADR-0068 §10).

-- 1. Campos do segredo. Todo campo é cifrado (secretbox), inclusive o visível; `sensitive` decide quem lê.
CREATE TABLE secret_fields (
    id            uuid PRIMARY KEY,
    company_id    uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    secret_id     uuid NOT NULL REFERENCES secrets(id) ON DELETE CASCADE,
    name          text NOT NULL CHECK (name ~ '^[A-Za-z][A-Za-z0-9_-]{0,63}$'),
    kind          text NOT NULL DEFAULT 'text' CHECK (kind IN ('text','totp','file')),
    sensitive     boolean NOT NULL DEFAULT true,
    provider      text NOT NULL DEFAULT 'local' CHECK (provider IN ('local','vault')),
    value_enc     text NOT NULL DEFAULT '',
    external_ref  text NOT NULL DEFAULT '' CHECK (length(external_ref) <= 500),
    -- campo arquivo: blob cifrado no BlobStore (ADR-0051); nome, tipo e tamanho são metadados visíveis
    blob_key      text NOT NULL DEFAULT '',
    file_name     text NOT NULL DEFAULT '' CHECK (length(file_name) <= 255),
    content_type  text NOT NULL DEFAULT '' CHECK (length(content_type) <= 255),
    size_bytes    integer NOT NULL DEFAULT 0 CHECK (size_bytes >= 0 AND size_bytes <= 1048576),
    -- HMAC do valor (chave derivada de HANKO_SECRET_KEY) para achar repetidos sem decifrar (ADR-0068 §7)
    value_hmac    text NOT NULL DEFAULT '',
    -- 0 = AAD legado `secret/<secret_id>` (campo migrado do ADR-0050); 1 = `secret/<secret_id>/field/<field_id>`
    aad_version   smallint NOT NULL DEFAULT 1 CHECK (aad_version IN (0,1)),
    position      integer NOT NULL DEFAULT 0,
    version       integer NOT NULL DEFAULT 1 CHECK (version >= 1),
    revealed_at   timestamptz,
    last_used_at  timestamptz,
    rotated_at    timestamptz NOT NULL DEFAULT now(),
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (secret_id, name),
    CHECK (kind = 'text' OR sensitive),
    CHECK (kind <> 'file' OR provider = 'local'),
    CHECK ((provider = 'vault' AND external_ref <> '' AND value_enc = '' AND blob_key = '')
        OR (provider = 'local' AND external_ref = '' AND kind = 'file' AND blob_key <> '' AND value_enc = '')
        OR (provider = 'local' AND external_ref = '' AND kind <> 'file' AND value_enc <> '' AND blob_key = ''))
);
CREATE INDEX secret_fields_company_idx ON secret_fields (company_id, secret_id, position);
CREATE INDEX secret_fields_hmac_idx ON secret_fields (company_id, value_hmac) WHERE value_hmac <> '';

-- 2. Histórico: o valor anterior de cada rotação, cifrado com AAD `.../field/<field_id>/v/<version>`.
CREATE TABLE secret_field_versions (
    id              uuid PRIMARY KEY,
    company_id      uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    field_id        uuid NOT NULL REFERENCES secret_fields(id) ON DELETE CASCADE,
    version         integer NOT NULL CHECK (version >= 1),
    provider        text NOT NULL CHECK (provider IN ('local','vault')),
    value_enc       text NOT NULL DEFAULT '',
    external_ref    text NOT NULL DEFAULT '',
    blob_key        text NOT NULL DEFAULT '',
    file_name       text NOT NULL DEFAULT '',
    content_type    text NOT NULL DEFAULT '',
    size_bytes      integer NOT NULL DEFAULT 0,
    aad_version     smallint NOT NULL DEFAULT 1 CHECK (aad_version IN (0,1)),
    revealed_at     timestamptz,
    replaced_by_type text NOT NULL CHECK (replaced_by_type IN ('human','persona','system')),
    replaced_by_id  uuid,
    created_at      timestamptz NOT NULL DEFAULT now(),
    replaced_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (field_id, version)
);
CREATE INDEX secret_field_versions_prune_idx ON secret_field_versions (company_id, replaced_at);

-- 3. Prazo de rotação (relatório de saúde e alerta, ADR-0068 §7).
ALTER TABLE secrets ADD COLUMN rotate_after_days integer CHECK (rotate_after_days IS NULL OR rotate_after_days BETWEEN 1 AND 3650);

-- 4. Coleções e concessões. Só segredos de escopo company entram em coleção (o service confere).
CREATE TABLE secret_collections (
    id                uuid PRIMARY KEY,
    company_id        uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    -- nome livre (aparece na UI, nunca em referência ${secret:}): 1 a 64 caracteres, sem controle nem espaço nas pontas
    name              text NOT NULL CHECK (name ~ '^[^[:cntrl:]]{1,64}$' AND name = btrim(name)),
    description       text NOT NULL DEFAULT '' CHECK (length(description) <= 300),
    is_default        boolean NOT NULL DEFAULT false,
    rotate_after_days integer CHECK (rotate_after_days IS NULL OR rotate_after_days BETWEEN 1 AND 3650),
    created_by_type   text NOT NULL CHECK (created_by_type IN ('human','persona','system')),
    created_by_id     uuid,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (company_id, name)
);
CREATE UNIQUE INDEX secret_collections_default_uq ON secret_collections (company_id) WHERE is_default;

CREATE TABLE secret_collection_items (
    company_id    uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    collection_id uuid NOT NULL REFERENCES secret_collections(id) ON DELETE CASCADE,
    secret_id     uuid NOT NULL REFERENCES secrets(id) ON DELETE CASCADE,
    added_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (collection_id, secret_id)
);
CREATE INDEX secret_collection_items_secret_idx ON secret_collection_items (company_id, secret_id);

CREATE TABLE secret_collection_grants (
    id              uuid PRIMARY KEY,
    company_id      uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    collection_id   uuid NOT NULL REFERENCES secret_collections(id) ON DELETE CASCADE,
    subject_type    text NOT NULL CHECK (subject_type IN ('persona','persona_tree','all_personas','user')),
    subject_id      uuid,
    permission      text NOT NULL CHECK (permission IN ('use','view','manage')),
    created_by_type text NOT NULL CHECK (created_by_type IN ('human','persona','system')),
    created_by_id   uuid,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CHECK ((subject_type = 'all_personas') = (subject_id IS NULL)),
    CHECK ((subject_type = 'user') = (permission IN ('view','manage')))
);
CREATE UNIQUE INDEX secret_collection_grants_uq ON secret_collection_grants
    (collection_id, subject_type, COALESCE(subject_id, '00000000-0000-0000-0000-000000000000'::uuid), permission);
CREATE INDEX secret_collection_grants_subject_idx ON secret_collection_grants (company_id, subject_type, subject_id);

-- 5. Delegação de segredo pessoal às runs de um alvo que o dono pediu (ADR-0068 §6).
CREATE TABLE secret_delegations (
    id            uuid PRIMARY KEY,
    company_id    uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    owner_user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    secret_id     uuid NOT NULL REFERENCES secrets(id) ON DELETE CASCADE,
    target_type   text NOT NULL CHECK (target_type IN ('task','conversation','run')),
    target_id     uuid NOT NULL,
    expires_at    timestamptz NOT NULL,
    revoked_at    timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    CHECK (expires_at > created_at)
);
CREATE INDEX secret_delegations_target_idx ON secret_delegations (company_id, target_type, target_id);
CREATE INDEX secret_delegations_owner_idx ON secret_delegations (company_id, owner_user_id, created_at DESC);

-- 6. Registro de uso: despacho, tool, arquivo e envio. Só ids e campo, nunca valor.
CREATE TABLE secret_access_log (
    id            uuid PRIMARY KEY,
    company_id    uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    secret_id     uuid NOT NULL REFERENCES secrets(id) ON DELETE CASCADE,
    field_name    text NOT NULL,
    via           text NOT NULL CHECK (via IN ('dispatch','tool','file','send')),
    run_id        uuid,
    persona_id    uuid,
    delegation_id uuid REFERENCES secret_delegations(id) ON DELETE SET NULL,
    accessed_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX secret_access_log_secret_idx ON secret_access_log (company_id, secret_id, accessed_at DESC);
CREATE INDEX secret_access_log_prune_idx ON secret_access_log (accessed_at);

-- 7. Envio (ADR-0069). O servidor guarda só o texto cifrado; a chave fica no fragmento do link.
CREATE TABLE secret_sends (
    id                uuid PRIMARY KEY,
    company_id        uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    public_id         text NOT NULL UNIQUE CHECK (public_id ~ '^[A-Za-z0-9_-]{22}$'),
    kind              text NOT NULL CHECK (kind IN ('text','file')),
    ciphertext        text NOT NULL DEFAULT '',
    blob_key          text NOT NULL DEFAULT '',
    size_bytes        integer NOT NULL DEFAULT 0 CHECK (size_bytes >= 0 AND size_bytes <= 26214400),
    label             text NOT NULL DEFAULT '' CHECK (length(label) <= 120),
    kdf_salt          text NOT NULL DEFAULT '',
    password_verifier text NOT NULL DEFAULT '',
    failed_attempts   integer NOT NULL DEFAULT 0,
    locked_until      timestamptz,
    max_accesses      integer CHECK (max_accesses IS NULL OR max_accesses BETWEEN 1 AND 1000),
    access_count      integer NOT NULL DEFAULT 0 CHECK (access_count >= 0),
    expires_at        timestamptz NOT NULL,
    last_accessed_at  timestamptz,
    purged_at         timestamptz,
    source_secret_id  uuid REFERENCES secrets(id) ON DELETE SET NULL,
    source_field      text NOT NULL DEFAULT '',
    run_id            uuid,
    created_by_type   text NOT NULL CHECK (created_by_type IN ('human','persona','system')),
    created_by_id     uuid,
    created_at        timestamptz NOT NULL DEFAULT now(),
    CHECK (expires_at > created_at AND expires_at <= created_at + interval '30 days'),
    CHECK ((kdf_salt = '') = (password_verifier = '')),
    CHECK (purged_at IS NOT NULL OR (kind = 'text' AND ciphertext <> '') OR (kind = 'file' AND blob_key <> ''))
);
CREATE INDEX secret_sends_company_idx ON secret_sends (company_id, created_at DESC);
CREATE INDEX secret_sends_expiry_idx ON secret_sends (expires_at) WHERE purged_at IS NULL;

-- 8. Passkeys e reautenticação recente (ADR-0070).
CREATE TABLE user_passkeys (
    id               uuid PRIMARY KEY,
    user_id          uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    credential_id    bytea NOT NULL UNIQUE,
    public_key       bytea NOT NULL,
    sign_count       bigint NOT NULL DEFAULT 0 CHECK (sign_count >= 0),
    aaguid           uuid,
    transports       text[] NOT NULL DEFAULT '{}',
    backup_eligible  boolean NOT NULL DEFAULT false,
    backup_state     boolean NOT NULL DEFAULT false,
    name             text NOT NULL DEFAULT '' CHECK (length(name) <= 80),
    created_at       timestamptz NOT NULL DEFAULT now(),
    last_used_at     timestamptz
);
CREATE INDEX user_passkeys_user_idx ON user_passkeys (user_id);

CREATE TABLE webauthn_challenges (
    id         uuid PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    session_id uuid REFERENCES sessions(id) ON DELETE CASCADE,
    purpose    text NOT NULL CHECK (purpose IN ('register','reauth')),
    challenge  bytea NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX webauthn_challenges_expiry_idx ON webauthn_challenges (expires_at);

ALTER TABLE sessions ADD COLUMN reauth_at timestamptz;
ALTER TABLE sessions ADD COLUMN reauth_method text CHECK (reauth_method IS NULL OR reauth_method IN ('passkey','password','firebase'));

-- 9. Alerta de rotação vencida (ADR-0068 §7.3).
ALTER TABLE alerts DROP CONSTRAINT alerts_kind_check;
ALTER TABLE alerts ADD CONSTRAINT alerts_kind_check CHECK (kind IN ('persona_silent','run_stuck','budget_near_limit','persona_not_ready','secret_rotation_due'));
ALTER TABLE alerts DROP CONSTRAINT alerts_subject_type_check;
ALTER TABLE alerts ADD CONSTRAINT alerts_subject_type_check CHECK (subject_type IN ('persona','run','company','secret'));

-- +goose Down
ALTER TABLE alerts DROP CONSTRAINT alerts_subject_type_check;
ALTER TABLE alerts DROP CONSTRAINT alerts_kind_check;
DELETE FROM alerts WHERE kind = 'secret_rotation_due' OR subject_type = 'secret';
ALTER TABLE alerts ADD CONSTRAINT alerts_kind_check CHECK (kind IN ('persona_silent','run_stuck','budget_near_limit','persona_not_ready'));
ALTER TABLE alerts ADD CONSTRAINT alerts_subject_type_check CHECK (subject_type IN ('persona','run','company'));
ALTER TABLE sessions DROP COLUMN reauth_method;
ALTER TABLE sessions DROP COLUMN reauth_at;
DROP TABLE webauthn_challenges;
DROP TABLE user_passkeys;
DROP TABLE secret_sends;
DROP TABLE secret_access_log;
DROP TABLE secret_delegations;
DROP TABLE secret_collection_grants;
DROP TABLE secret_collection_items;
DROP TABLE secret_collections;
ALTER TABLE secrets DROP COLUMN rotate_after_days;
DROP TABLE secret_field_versions;
DROP TABLE secret_fields;

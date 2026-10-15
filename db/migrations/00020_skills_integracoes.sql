-- +goose Up
-- Skills (habilidades reutilizáveis por persona) e Integrações (MCP da empresa e webhooks de saída). ADR-0029.

-- Uma skill é um SKILL.md (frontmatter name/description + corpo markdown). version sobe a cada mudança de
-- nome, descrição ou corpo.
CREATE TABLE skills (
    id              uuid PRIMARY KEY,
    company_id      uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    slug            text NOT NULL,
    name            text NOT NULL,
    description     text NOT NULL DEFAULT '',
    body            text NOT NULL DEFAULT '',
    version         integer NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_by_type text NOT NULL CHECK (created_by_type IN ('human','persona','system')),
    created_by_id   uuid,
    updated_by_type text NOT NULL CHECK (updated_by_type IN ('human','persona','system')),
    updated_by_id   uuid,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (company_id, slug)
);

-- Quais skills cada persona usa. A linha existe quando a skill já foi ligada alguma vez; enabled diz se vale.
CREATE TABLE persona_skills (
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    persona_id uuid NOT NULL REFERENCES personas(id) ON DELETE CASCADE,
    skill_id   uuid NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    enabled    boolean NOT NULL DEFAULT true,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (persona_id, skill_id)
);
CREATE INDEX persona_skills_skill_idx ON persona_skills (skill_id);
CREATE INDEX persona_skills_company_idx ON persona_skills (company_id);

-- Integrações da empresa. config guarda só o que não é segredo; secret_enc é o JSON dos segredos cifrado
-- (AES-GCM, HANKO_SECRET_KEY) e nunca sai pela API. secret_hint é um prefixo inofensivo para a UI reconhecer
-- o segredo gerado pelo servidor.
CREATE TABLE integrations (
    id              uuid PRIMARY KEY,
    company_id      uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    kind            text NOT NULL CHECK (kind IN ('mcp','webhook')),
    slug            text NOT NULL,
    name            text NOT NULL,
    enabled         boolean NOT NULL DEFAULT true,
    all_personas    boolean NOT NULL DEFAULT false,
    config          jsonb NOT NULL DEFAULT '{}',
    events          text[] NOT NULL DEFAULT '{}',
    secret_enc      text NOT NULL DEFAULT '',
    secret_hint     text NOT NULL DEFAULT '',
    created_by_type text NOT NULL CHECK (created_by_type IN ('human','persona','system')),
    created_by_id   uuid,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (company_id, slug)
);
CREATE INDEX integrations_company_kind_idx ON integrations (company_id, kind, enabled);

-- Personas que usam um servidor MCP quando ele não vale para todas.
CREATE TABLE integration_personas (
    company_id     uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    integration_id uuid NOT NULL REFERENCES integrations(id) ON DELETE CASCADE,
    persona_id     uuid NOT NULL REFERENCES personas(id) ON DELETE CASCADE,
    PRIMARY KEY (integration_id, persona_id)
);
CREATE INDEX integration_personas_persona_idx ON integration_personas (persona_id);

-- Entregas de webhook (outbox): uma linha por (integração, fato). A linha guarda o corpo exato enviado e o
-- resultado da última tentativa; a fila (river) faz o retry. Só as últimas 100 por integração são mantidas.
CREATE TABLE webhook_deliveries (
    id              uuid PRIMARY KEY,
    company_id      uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    integration_id  uuid NOT NULL REFERENCES integrations(id) ON DELETE CASCADE,
    event           text NOT NULL,
    activity_id     uuid,
    test            boolean NOT NULL DEFAULT false,
    payload         text NOT NULL,
    status          text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','retrying','success','failed')),
    attempts        integer NOT NULL DEFAULT 0,
    http_status     integer,
    latency_ms      integer,
    error           text NOT NULL DEFAULT '',
    next_attempt_at timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    delivered_at    timestamptz
);
CREATE INDEX webhook_deliveries_integration_idx ON webhook_deliveries (integration_id, created_at DESC, id DESC);
CREATE UNIQUE INDEX webhook_deliveries_activity_uq ON webhook_deliveries (integration_id, activity_id) WHERE activity_id IS NOT NULL;

-- +goose Down
DROP TABLE webhook_deliveries;
DROP TABLE integration_personas;
DROP TABLE integrations;
DROP TABLE persona_skills;
DROP TABLE skills;

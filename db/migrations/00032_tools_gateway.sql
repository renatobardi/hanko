-- +goose Up
-- Apps, gateway de tools e perfis de ferramentas (ADR-0036). Um "app" é a integração MCP que já existe
-- (integrations kind mcp); aqui entram o catálogo das tools que ela expõe, os perfis (regras include/exclude
-- com modo allow/ask), os vínculos do perfil a empresa/projeto/persona, os gateways nomeados com tokens para
-- clientes de fora, a aprovação de uma chamada (gate tool) e a auditoria das chamadas. O gate do tipo tool e
-- o gatilho de run já existem (migration 00027).

-- Estado da descoberta do catálogo de uma integração MCP http: quando rodou, o erro da última tentativa e se
-- a primeira descoberta (a linha de base) já foi feita. Tools da linha de base entram ativas; as que surgem
-- depois e escrevem (ou não declaram que só leem) entram em quarentena.
CREATE TABLE integration_tool_catalogs (
    integration_id uuid PRIMARY KEY REFERENCES integrations(id) ON DELETE CASCADE,
    company_id     uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    synced_at      timestamptz,
    last_error     text NOT NULL DEFAULT '',
    tools_count    integer NOT NULL DEFAULT 0,
    updated_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX integration_tool_catalogs_company_idx ON integration_tool_catalogs (company_id);

-- Uma tool do catálogo. schema_sha256 cobre nome, descrição, schema de entrada e anotações: mudou, a tool
-- "mudou de contrato" e, se não for só leitura, volta para a quarentena.
CREATE TABLE integration_tools (
    id               uuid PRIMARY KEY,
    company_id       uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    integration_id   uuid NOT NULL REFERENCES integrations(id) ON DELETE CASCADE,
    name             text NOT NULL CHECK (length(name) BETWEEN 1 AND 128),
    title            text NOT NULL DEFAULT '',
    description      text NOT NULL DEFAULT '',
    input_schema     jsonb NOT NULL DEFAULT '{"type":"object"}',
    annotations      jsonb NOT NULL DEFAULT '{}',
    schema_sha256    text NOT NULL,
    risk             text NOT NULL CHECK (risk IN ('read','write','destructive','unknown')),
    status           text NOT NULL CHECK (status IN ('active','quarantined','disabled','removed')),
    first_seen_at    timestamptz NOT NULL,
    last_seen_at     timestamptz NOT NULL,
    reviewed_by_type text CHECK (reviewed_by_type IN ('human','persona','system')),
    reviewed_by_id   uuid,
    reviewed_at      timestamptz,
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (integration_id, name)
);
CREATE INDEX integration_tools_company_idx ON integration_tools (company_id, status);

-- Perfil de ferramentas: regras nomeadas com ação padrão (deny por padrão) e teto de chamadas por minuto por
-- (persona ou gateway, integração).
CREATE TABLE tool_profiles (
    id                 uuid PRIMARY KEY,
    company_id         uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    slug               text NOT NULL,
    name               text NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    description        text NOT NULL DEFAULT '',
    default_action     text NOT NULL DEFAULT 'deny' CHECK (default_action IN ('deny','allow')),
    rate_limit_per_min integer NOT NULL DEFAULT 120 CHECK (rate_limit_per_min BETWEEN 1 AND 10000),
    created_by_type    text NOT NULL CHECK (created_by_type IN ('human','persona','system')),
    created_by_id      uuid,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (company_id, slug)
);

-- Regra de um perfil: alvo (integração inteira, uma tool, ou um nível de risco — este opcionalmente restrito
-- a uma integração), efeito include/exclude e, no include, o modo allow (pode) ou ask (pergunta antes).
CREATE TABLE tool_profile_rules (
    id             uuid PRIMARY KEY,
    company_id     uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    profile_id     uuid NOT NULL REFERENCES tool_profiles(id) ON DELETE CASCADE,
    target         text NOT NULL CHECK (target IN ('integration','tool','risk')),
    integration_id uuid REFERENCES integrations(id) ON DELETE CASCADE,
    tool_name      text NOT NULL DEFAULT '',
    risk           text NOT NULL DEFAULT '',
    effect         text NOT NULL CHECK (effect IN ('include','exclude')),
    mode           text NOT NULL DEFAULT 'allow' CHECK (mode IN ('allow','ask')),
    created_at     timestamptz NOT NULL DEFAULT now(),
    CHECK (
        (target = 'integration' AND integration_id IS NOT NULL AND tool_name = '' AND risk = '') OR
        (target = 'tool' AND integration_id IS NOT NULL AND tool_name <> '' AND risk = '') OR
        (target = 'risk' AND tool_name = '' AND risk IN ('read','write','destructive','unknown'))
    )
);
CREATE UNIQUE INDEX tool_profile_rules_uq ON tool_profile_rules
    (profile_id, target, COALESCE(integration_id, '00000000-0000-0000-0000-000000000000'::uuid), tool_name, risk);
CREATE INDEX tool_profile_rules_integration_idx ON tool_profile_rules (integration_id) WHERE integration_id IS NOT NULL;

-- Vínculo de um perfil a um escopo. O escopo mais estreito vence (persona > projeto > empresa); no mesmo escopo,
-- a maior prioridade e, empatando, o vínculo mais novo.
CREATE TABLE tool_profile_bindings (
    id         uuid PRIMARY KEY,
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    profile_id uuid NOT NULL REFERENCES tool_profiles(id) ON DELETE CASCADE,
    scope      text NOT NULL CHECK (scope IN ('company','project','persona')),
    project_id uuid REFERENCES projects(id) ON DELETE CASCADE,
    persona_id uuid REFERENCES personas(id) ON DELETE CASCADE,
    priority   integer NOT NULL DEFAULT 0 CHECK (priority BETWEEN -1000 AND 1000),
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (
        (scope = 'company' AND project_id IS NULL AND persona_id IS NULL) OR
        (scope = 'project' AND project_id IS NOT NULL AND persona_id IS NULL) OR
        (scope = 'persona' AND persona_id IS NOT NULL AND project_id IS NULL)
    )
);
CREATE UNIQUE INDEX tool_profile_bindings_uq ON tool_profile_bindings (profile_id, scope,
    COALESCE(project_id, '00000000-0000-0000-0000-000000000000'::uuid),
    COALESCE(persona_id, '00000000-0000-0000-0000-000000000000'::uuid));
CREATE INDEX tool_profile_bindings_company_idx ON tool_profile_bindings (company_id, scope);

-- Gateway nomeado: endpoint MCP para clientes de fora da instalação (ex.: uma IDE), sempre com um perfil.
CREATE TABLE tool_gateways (
    id              uuid PRIMARY KEY,
    company_id      uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    slug            text NOT NULL,
    name            text NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    profile_id      uuid NOT NULL REFERENCES tool_profiles(id) ON DELETE RESTRICT,
    enabled         boolean NOT NULL DEFAULT true,
    created_by_type text NOT NULL CHECK (created_by_type IN ('human','persona','system')),
    created_by_id   uuid,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (company_id, slug)
);
CREATE INDEX tool_gateways_profile_idx ON tool_gateways (profile_id);

-- Token de um gateway: só o hash (sha256) fica guardado; o prefixo visível (hkgw_ + 6) serve para reconhecê-lo.
-- Expira sempre (padrão de 90 dias) e só vale para as ações listadas.
CREATE TABLE tool_gateway_tokens (
    id              uuid PRIMARY KEY,
    company_id      uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    gateway_id      uuid NOT NULL REFERENCES tool_gateways(id) ON DELETE CASCADE,
    label           text NOT NULL DEFAULT '',
    prefix          text NOT NULL,
    token_hash      text NOT NULL UNIQUE,
    actions         text[] NOT NULL,
    expires_at      timestamptz NOT NULL,
    last_used_at    timestamptz,
    revoked_at      timestamptz,
    created_by_type text NOT NULL CHECK (created_by_type IN ('human','persona','system')),
    created_by_id   uuid,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CHECK (actions <@ ARRAY['tools/list','tools/call']::text[] AND cardinality(actions) >= 1)
);
CREATE INDEX tool_gateway_tokens_gateway_idx ON tool_gateway_tokens (gateway_id, created_at DESC);

-- Pedido de aprovação de uma chamada de tool (gate tool). A chave é (integração, tool, sha256 de tool+args) e
-- quem chamou (persona ou gateway): aprovado, a repetição com os mesmos argumentos passa UMA vez (consumed_at).
CREATE TABLE tool_call_approvals (
    id             uuid PRIMARY KEY,
    company_id     uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    gate_id        uuid NOT NULL UNIQUE REFERENCES gates(id) ON DELETE CASCADE,
    integration_id uuid NOT NULL REFERENCES integrations(id) ON DELETE CASCADE,
    tool           text NOT NULL,
    args_sha256    text NOT NULL,
    run_id         uuid REFERENCES runs(id) ON DELETE SET NULL,
    task_id        uuid REFERENCES tasks(id) ON DELETE SET NULL,
    persona_id     uuid REFERENCES personas(id) ON DELETE CASCADE,
    gateway_id     uuid REFERENCES tool_gateways(id) ON DELETE CASCADE,
    consumed_at    timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    CHECK ((persona_id IS NULL) <> (gateway_id IS NULL))
);
CREATE INDEX tool_call_approvals_key_idx ON tool_call_approvals (company_id, integration_id, tool, args_sha256, created_at DESC);

-- Auditoria das chamadas que passam pelo gateway. Os argumentos nunca são gravados: só o sha256 e o tamanho.
-- Guardadas por 30 dias e no máximo 10 mil por empresa (limpeza periódica).
CREATE TABLE tool_invocations (
    id               uuid PRIMARY KEY,
    company_id       uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    run_id           uuid REFERENCES runs(id) ON DELETE SET NULL,
    gateway_id       uuid REFERENCES tool_gateways(id) ON DELETE SET NULL,
    persona_id       uuid REFERENCES personas(id) ON DELETE SET NULL,
    integration_id   uuid REFERENCES integrations(id) ON DELETE SET NULL,
    integration_slug text NOT NULL DEFAULT '',
    tool             text NOT NULL,
    args_sha256      text NOT NULL DEFAULT '',
    args_bytes       integer NOT NULL DEFAULT 0,
    decision         text NOT NULL CHECK (decision IN ('allow','deny','ask','quarantined','error')),
    gate_id          uuid REFERENCES gates(id) ON DELETE SET NULL,
    status           text NOT NULL CHECK (status IN ('ok','failed','blocked','pending')),
    latency_ms       integer NOT NULL DEFAULT 0,
    error            text NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX tool_invocations_company_idx ON tool_invocations (company_id, created_at DESC, id DESC);
CREATE INDEX tool_invocations_rate_persona_idx ON tool_invocations (persona_id, integration_id, created_at)
    WHERE decision = 'allow' AND persona_id IS NOT NULL;
CREATE INDEX tool_invocations_rate_gateway_idx ON tool_invocations (gateway_id, integration_id, created_at)
    WHERE decision = 'allow' AND gateway_id IS NOT NULL;

-- +goose Down
DROP TABLE tool_invocations;
DROP TABLE tool_call_approvals;
DROP TABLE tool_gateway_tokens;
DROP TABLE tool_gateways;
DROP TABLE tool_profile_bindings;
DROP TABLE tool_profile_rules;
DROP TABLE tool_profiles;
DROP TABLE integration_tools;
DROP TABLE integration_tool_catalogs;

-- +goose Up
-- Motor de política de tools (ADR-0053). O ADR-0036 já trouxe catálogo, quarentena, perfis, vínculos, gateways nomeados,
-- o gate "tool" e a auditoria. Aqui entram só as diferenças: a política off/ask/allow por persona x tool (com padrão
-- por empresa), a aprovação de chamadas nativas do harness, o prazo do gate e os campos extras da auditoria
-- (origem, ator, duração já existente, resultado redigido, de onde veio a decisão).
-- O gate continua kind = 'tool' (gates_kind_check inalterada: approval, task_review, hire, memory, tool).

-- Política: uma linha diz "neste escopo, para este alvo, o modo é off, ask ou allow".
--   escopo company  = padrão da empresa; persona = exceção de uma persona (vence a da empresa).
--   alvo   all = qualquer tool de integração e do harness; integration = a integração inteira; tool = uma tool de
--          integração; native = uma tool nativa do harness (Bash, Edit...) ou de plugin, pelo nome.
-- Sem linha que alcance a chamada, vale o perfil de ferramentas (ADR-0036) e, para tool nativa, o que o harness já fazia.
CREATE TABLE tool_policies (
    id              uuid PRIMARY KEY,
    company_id      uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    scope           text NOT NULL CHECK (scope IN ('company','persona')),
    persona_id      uuid REFERENCES personas(id) ON DELETE CASCADE,
    target          text NOT NULL CHECK (target IN ('all','integration','tool','native')),
    integration_id  uuid REFERENCES integrations(id) ON DELETE CASCADE,
    tool_name       text NOT NULL DEFAULT '' CHECK (length(tool_name) <= 128),
    mode            text NOT NULL CHECK (mode IN ('off','ask','allow')),
    origin          text NOT NULL DEFAULT 'manual' CHECK (origin IN ('manual','always')),
    created_by_type text NOT NULL CHECK (created_by_type IN ('human','persona','system')),
    created_by_id   uuid,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CHECK (
        (scope = 'company' AND persona_id IS NULL) OR
        (scope = 'persona' AND persona_id IS NOT NULL)
    ),
    CHECK (
        (target = 'all' AND integration_id IS NULL AND tool_name = '') OR
        (target = 'integration' AND integration_id IS NOT NULL AND tool_name = '') OR
        (target = 'tool' AND integration_id IS NOT NULL AND tool_name <> '') OR
        (target = 'native' AND integration_id IS NULL AND tool_name <> '')
    )
);
CREATE UNIQUE INDEX tool_policies_uq ON tool_policies (company_id, scope,
    COALESCE(persona_id, '00000000-0000-0000-0000-000000000000'::uuid), target,
    COALESCE(integration_id, '00000000-0000-0000-0000-000000000000'::uuid), tool_name);
CREATE INDEX tool_policies_company_idx ON tool_policies (company_id, scope);
CREATE INDEX tool_policies_persona_idx ON tool_policies (persona_id) WHERE persona_id IS NOT NULL;

-- Aprovação de chamada nativa do harness: não há integração, a chave é o nome da tool nativa.
ALTER TABLE tool_call_approvals ALTER COLUMN integration_id DROP NOT NULL;
ALTER TABLE tool_call_approvals ADD COLUMN native_tool text NOT NULL DEFAULT '';
ALTER TABLE tool_call_approvals ADD CONSTRAINT tool_call_approvals_target_check
    CHECK ((integration_id IS NOT NULL AND native_tool = '') OR (integration_id IS NULL AND native_tool <> ''));
CREATE INDEX tool_call_approvals_native_idx ON tool_call_approvals (company_id, persona_id, native_tool, args_sha256, created_at DESC)
    WHERE native_tool <> '';

-- Prazo do gate: passado o prazo, o gate pendente vira expired (varredura periódica). Só os gates tool o usam por ora.
ALTER TABLE gates ADD COLUMN expires_at timestamptz;
CREATE INDEX gates_expiry_idx ON gates (expires_at) WHERE status = 'pending' AND expires_at IS NOT NULL;

-- Auditoria: de onde veio a chamada, quem a fez, um trecho redigido do resultado e de onde veio a decisão.
ALTER TABLE tool_invocations ADD COLUMN source text NOT NULL DEFAULT 'run'
    CHECK (source IN ('run','gateway','test','hook','acp','plugin'));
ALTER TABLE tool_invocations ADD COLUMN actor_type text CHECK (actor_type IN ('human','persona','system'));
ALTER TABLE tool_invocations ADD COLUMN actor_id uuid;
ALTER TABLE tool_invocations ADD COLUMN result_preview text NOT NULL DEFAULT '';
ALTER TABLE tool_invocations ADD COLUMN policy_source text NOT NULL DEFAULT '';
UPDATE tool_invocations SET source = 'gateway', actor_type = 'system' WHERE gateway_id IS NOT NULL;
UPDATE tool_invocations SET actor_type = 'persona', actor_id = persona_id WHERE persona_id IS NOT NULL AND gateway_id IS NULL;

-- +goose Down
ALTER TABLE tool_invocations DROP COLUMN policy_source;
ALTER TABLE tool_invocations DROP COLUMN result_preview;
ALTER TABLE tool_invocations DROP COLUMN actor_id;
ALTER TABLE tool_invocations DROP COLUMN actor_type;
ALTER TABLE tool_invocations DROP COLUMN source;
DROP INDEX gates_expiry_idx;
ALTER TABLE gates DROP COLUMN expires_at;
DROP INDEX tool_call_approvals_native_idx;
ALTER TABLE tool_call_approvals DROP CONSTRAINT tool_call_approvals_target_check;
DELETE FROM tool_call_approvals WHERE integration_id IS NULL;
ALTER TABLE tool_call_approvals DROP COLUMN native_tool;
ALTER TABLE tool_call_approvals ALTER COLUMN integration_id SET NOT NULL;
DROP TABLE tool_policies;

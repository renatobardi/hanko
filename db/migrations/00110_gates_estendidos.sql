-- +goose Up
-- Gates estendidos (ADR-0041, M15): pergunta (question), plano (plan), reenvio com revisões, audiência nomeada,
-- contratação com spec, persona principal, personas de sistema e incidentes de orçamento. Tudo aditivo.

-- gates.kind: a lista COMPLETA vigente (00040) mais 'question' e 'plan'.
ALTER TABLE gates DROP CONSTRAINT gates_kind_check;
ALTER TABLE gates ADD CONSTRAINT gates_kind_check
    CHECK (kind IN ('approval','task_review','hire','memory','tool','instruction','question','plan'));

-- 'dismissed': a pergunta dispensada por um humano (reabrir volta a pending). Tem decided_at como as demais decididas.
ALTER TABLE gates DROP CONSTRAINT gates_status_check;
ALTER TABLE gates ADD CONSTRAINT gates_status_check
    CHECK (status IN ('pending','approved','rejected','changes_requested','expired','dismissed'));

-- payload: question = {"fields":[...]}; hire = {"spec":{...}}; plan = {"artifact_id":...}. answer: a resposta estruturada
-- de uma question ({"values":{"chave":["a"]}}). audience: {"users":[uuid],"roles":["admin"]} (vazio = qualquer decisor).
-- revision: 1 na criação; cada reenvio soma 1 (gate_revisions guarda as versões anteriores).
ALTER TABLE gates ADD COLUMN payload  jsonb   NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE gates ADD COLUMN answer   jsonb;
ALTER TABLE gates ADD COLUMN audience jsonb   NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE gates ADD COLUMN revision integer NOT NULL DEFAULT 1 CHECK (revision >= 1);

-- Versões anteriores de um gate reenviado (append-only: o texto que o humano já viu nunca muda).
CREATE TABLE gate_revisions (
    id          uuid PRIMARY KEY,
    company_id  uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    gate_id     uuid NOT NULL REFERENCES gates(id) ON DELETE CASCADE,
    revision    integer NOT NULL CHECK (revision >= 1),
    title       text NOT NULL,
    body        text NOT NULL,
    payload     jsonb NOT NULL DEFAULT '{}'::jsonb,
    decision_note text NOT NULL DEFAULT '',  -- o que o humano pediu quando esta versão foi devolvida
    sha256      text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (gate_id, revision)
);
CREATE INDEX gate_revisions_gate_idx ON gate_revisions (gate_id, revision DESC);

-- Imutável como as revisões de instruções (00040): o UPDATE falha; apagar só em cascata, com o gate.
CREATE TRIGGER gate_revisions_immutable BEFORE UPDATE ON gate_revisions
    FOR EACH ROW EXECUTE FUNCTION hanko_revision_immutable();

-- Plan-review opt-in por task, projeto ou persona (precedência task > projeto > persona; sem linha = herda).
CREATE TABLE plan_policies (
    company_id   uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    scope        text NOT NULL CHECK (scope IN ('task','project','persona')),
    target_id    uuid NOT NULL,
    require_plan boolean NOT NULL,
    updated_by_type text NOT NULL CHECK (updated_by_type IN ('human','persona','system')),
    updated_by_id   uuid,
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (scope, target_id)
);
CREATE INDEX plan_policies_company_idx ON plan_policies (company_id);

-- Políticas da empresa (hoje: contratação por gate também para humanos).
CREATE TABLE company_policies (
    company_id        uuid PRIMARY KEY REFERENCES companies(id) ON DELETE CASCADE,
    require_hire_gate boolean NOT NULL DEFAULT false,
    updated_at        timestamptz NOT NULL DEFAULT now()
);

-- Persona principal de cada pessoa (padrão de task e de chat) e personas de sistema (curadoria, revisor).
CREATE TABLE member_primary_persona (
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    persona_id uuid NOT NULL REFERENCES personas(id) ON DELETE CASCADE,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (company_id, user_id)
);
CREATE INDEX member_primary_persona_persona_idx ON member_primary_persona (persona_id);

ALTER TABLE personas ADD COLUMN system boolean NOT NULL DEFAULT false;

-- Incidentes de orçamento: um por (alvo, mês) que estourou o teto; a Caixa os mostra até alguém resolver.
CREATE TABLE budget_incidents (
    id            uuid PRIMARY KEY,
    company_id    uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    scope         text NOT NULL CHECK (scope IN ('company','project','persona')),
    target_id     uuid NOT NULL,
    target_name   text NOT NULL DEFAULT '',
    month         text NOT NULL CHECK (month ~ '^[0-9]{4}-[0-9]{2}$'),
    status        text NOT NULL DEFAULT 'open' CHECK (status IN ('open','resolved')),
    budget_cents  bigint NOT NULL,
    spent_usd     double precision NOT NULL DEFAULT 0,
    -- execuções avulsas liberadas por "liberar só este run" e ainda não consumidas
    release_runs  integer NOT NULL DEFAULT 0 CHECK (release_runs >= 0),
    resolution    text CHECK (resolution IN ('ceiling_raised','kept_paused','released_once','auto')),
    note          text NOT NULL DEFAULT '',
    resolved_by_type text CHECK (resolved_by_type IN ('human','persona','system')),
    resolved_by_id   uuid,
    opened_at     timestamptz NOT NULL DEFAULT now(),
    resolved_at   timestamptz,
    UNIQUE (scope, target_id, month),
    CHECK ((status = 'open') = (resolved_at IS NULL))
);
CREATE INDEX budget_incidents_company_idx ON budget_incidents (company_id, status, opened_at DESC);

-- +goose Down
DROP TABLE budget_incidents;
ALTER TABLE personas DROP COLUMN system;
DROP TABLE member_primary_persona;
DROP TABLE company_policies;
DROP TABLE plan_policies;
DROP TABLE gate_revisions;
ALTER TABLE gates DROP COLUMN revision;
ALTER TABLE gates DROP COLUMN audience;
ALTER TABLE gates DROP COLUMN answer;
ALTER TABLE gates DROP COLUMN payload;
ALTER TABLE gates DROP CONSTRAINT gates_status_check;
UPDATE gates SET status = 'rejected' WHERE status = 'dismissed';
ALTER TABLE gates ADD CONSTRAINT gates_status_check CHECK (status IN ('pending','approved','rejected','changes_requested','expired'));
ALTER TABLE gates DROP CONSTRAINT gates_kind_check;
DELETE FROM gates WHERE kind IN ('question','plan');
ALTER TABLE gates ADD CONSTRAINT gates_kind_check CHECK (kind IN ('approval','task_review','hire','memory','tool','instruction'));

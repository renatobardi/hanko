-- +goose Up
-- Consolidação de memória governada pelo Hanko (ADR-0030): curadoria periódica por uma persona, propostas
-- retidas para decisão humana (gate memory) e permissões de memória por workspace.

-- Novo gatilho de run: a curadoria de memória (run de sistema da persona curadora). Mantém todos os existentes.
ALTER TABLE runs DROP CONSTRAINT runs_trigger_check;
ALTER TABLE runs ADD CONSTRAINT runs_trigger_check CHECK (trigger IN ('assign','comment','manual','heartbeat','gate','flow','review','memory'));

-- Novo tipo de gate: proposta de curadoria que remove, funde ou reescreve conteúdo.
ALTER TABLE gates DROP CONSTRAINT gates_kind_check;
ALTER TABLE gates ADD CONSTRAINT gates_kind_check CHECK (kind IN ('approval','task_review','hire','memory'));

-- Configuração da consolidação por empresa. Sem linha = desligada. O fluxo periódico é o tick do river
-- sobre next_fire_at (como os Fluxos); o curador é uma persona da empresa.
CREATE TABLE memory_settings (
    company_id         uuid PRIMARY KEY REFERENCES companies(id) ON DELETE CASCADE,
    enabled            boolean NOT NULL DEFAULT false,
    interval_hours     integer NOT NULL DEFAULT 24 CHECK (interval_hours BETWEEN 1 AND 720),
    curator_persona_id uuid REFERENCES personas(id) ON DELETE SET NULL,
    last_fired_at      timestamptz,
    next_fire_at       timestamptz,
    updated_at         timestamptz NOT NULL
);
CREATE INDEX memory_settings_due_idx ON memory_settings (next_fire_at) WHERE enabled;

-- Uma rodada de curadoria de um workspace: a task do sistema, a run e o desfecho. workspace_slug fica gravado
-- porque é o "projeto" no ai-memory e sobrevive à remoção do workspace.
CREATE TABLE memory_consolidations (
    id             uuid PRIMARY KEY,
    company_id     uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    workspace_id   uuid REFERENCES workspaces(id) ON DELETE SET NULL,
    workspace_slug text NOT NULL,
    task_id        uuid REFERENCES tasks(id) ON DELETE SET NULL,
    run_id         uuid REFERENCES runs(id) ON DELETE SET NULL,
    origin         text NOT NULL CHECK (origin IN ('scheduled','manual')),
    scheduled_for  timestamptz NOT NULL,
    status         text NOT NULL DEFAULT 'open' CHECK (status IN ('open','done','failed','cancelled','expired')),
    summary        text NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL,
    finished_at    timestamptz,
    UNIQUE (company_id, workspace_slug, origin, scheduled_for)
);
-- No máximo uma consolidação aberta por workspace: não empilha curadoria em cima de curadoria.
CREATE UNIQUE INDEX memory_consolidations_one_open ON memory_consolidations (company_id, workspace_slug) WHERE status = 'open';
CREATE INDEX memory_consolidations_company_idx ON memory_consolidations (company_id, created_at DESC);
CREATE INDEX memory_consolidations_task_idx ON memory_consolidations (task_id) WHERE task_id IS NOT NULL;
CREATE INDEX memory_consolidations_open_idx ON memory_consolidations (created_at) WHERE status = 'open';

-- Escritas da curadoria retidas para decisão humana. tool + arguments são a chamada exata que o Hanko repete no
-- ai-memory quando a proposta é aprovada; sha256 cobre essa chamada (é o que o gate mostra e o que foi aprovado).
CREATE TABLE memory_proposals (
    id              uuid PRIMARY KEY,
    company_id      uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    workspace_id    uuid REFERENCES workspaces(id) ON DELETE SET NULL,
    workspace_slug  text NOT NULL,
    run_id          uuid REFERENCES runs(id) ON DELETE SET NULL,
    gate_id         uuid REFERENCES gates(id) ON DELETE SET NULL,
    kind            text NOT NULL CHECK (kind IN ('merge','delete','rewrite')),
    status          text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected','applied','failed')),
    tool            text NOT NULL CHECK (tool <> ''),
    arguments       jsonb NOT NULL DEFAULT '{}'::jsonb,
    targets         text[] NOT NULL DEFAULT '{}',
    proposed_content text NOT NULL DEFAULT '',
    current_content text NOT NULL DEFAULT '',
    diff            text NOT NULL DEFAULT '',
    sha256          text NOT NULL CHECK (length(sha256) = 64),
    attempts        integer NOT NULL DEFAULT 0,
    last_error      text NOT NULL DEFAULT '',
    next_attempt_at timestamptz,
    decided_by_type text CHECK (decided_by_type IS NULL OR decided_by_type IN ('human','persona','system')),
    decided_by_id   uuid,
    decision_note   text NOT NULL DEFAULT '',
    decided_at      timestamptz,
    applied_at      timestamptz,
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL
);
CREATE INDEX memory_proposals_company_idx ON memory_proposals (company_id, status, created_at DESC);
CREATE INDEX memory_proposals_gate_idx ON memory_proposals (gate_id) WHERE gate_id IS NOT NULL;
CREATE INDEX memory_proposals_run_idx ON memory_proposals (run_id) WHERE run_id IS NOT NULL;
-- Varredura de aplicação: aprovadas e falhas com nova tentativa devida.
CREATE INDEX memory_proposals_due_idx ON memory_proposals (next_attempt_at) WHERE status IN ('approved','failed') AND next_attempt_at IS NOT NULL;
-- A mesma chamada na mesma run vira uma proposta só.
CREATE UNIQUE INDEX memory_proposals_run_sha_uq ON memory_proposals (run_id, sha256) WHERE run_id IS NOT NULL;

-- Permissões de memória por workspace: além do escopo da própria run (workspace da task, senão o da persona),
-- uma persona só alcança a memória de outro workspace da empresa se houver uma concessão aqui.
CREATE TABLE memory_grants (
    company_id   uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    persona_id   uuid NOT NULL REFERENCES personas(id) ON DELETE CASCADE,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    access       text NOT NULL CHECK (access IN ('read','write')),
    created_at   timestamptz NOT NULL,
    PRIMARY KEY (persona_id, workspace_id)
);
CREATE INDEX memory_grants_company_idx ON memory_grants (company_id);
CREATE INDEX memory_grants_workspace_idx ON memory_grants (workspace_id);

-- +goose Down
DROP TABLE memory_grants;
DROP TABLE memory_proposals;
DROP TABLE memory_consolidations;
DROP TABLE memory_settings;
DELETE FROM gates WHERE kind = 'memory';
ALTER TABLE gates DROP CONSTRAINT gates_kind_check;
ALTER TABLE gates ADD CONSTRAINT gates_kind_check CHECK (kind IN ('approval','task_review','hire'));
UPDATE runs SET trigger = 'manual' WHERE trigger = 'memory';
ALTER TABLE runs DROP CONSTRAINT runs_trigger_check;
ALTER TABLE runs ADD CONSTRAINT runs_trigger_check CHECK (trigger IN ('assign','comment','manual','heartbeat','gate','flow','review'));

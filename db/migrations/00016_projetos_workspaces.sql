-- +goose Up
-- Projetos e workspaces de primeira classe (ADR-0025). As tabelas existem desde a 00001; aqui entram as colunas
-- do produto e os índices das consultas novas.

-- Workspace: branch padrão opcional (metadado; o Hanko não clona nem troca de branch).
ALTER TABLE workspaces ADD COLUMN default_branch text NOT NULL DEFAULT '';

-- Projeto: status (ativo/arquivado) e orçamento mensal próprio, em centavos de USD (nulo = sem teto).
ALTER TABLE projects ADD COLUMN status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','archived'));
ALTER TABLE projects ADD COLUMN budget_monthly_cents bigint CHECK (budget_monthly_cents IS NULL OR budget_monthly_cents >= 0);

-- Run: de onde veio o diretório de trabalho (task | project | persona | none) e qual workspace foi usado.
-- Runs antigas ficam com '' (origem desconhecida).
ALTER TABLE runs ADD COLUMN workspace_id uuid REFERENCES workspaces(id) ON DELETE SET NULL;
ALTER TABLE runs ADD COLUMN workspace_source text NOT NULL DEFAULT '' CHECK (workspace_source IN ('', 'task', 'project', 'persona', 'none'));

-- Alertas de orçamento também valem para projetos.
ALTER TABLE budget_alerts DROP CONSTRAINT budget_alerts_scope_check;
ALTER TABLE budget_alerts ADD CONSTRAINT budget_alerts_scope_check CHECK (scope IN ('company','persona','project'));

-- ... e o alerta operacional de orçamento (ADR-0020) pode ter um projeto como sujeito.
ALTER TABLE alerts DROP CONSTRAINT alerts_subject_type_check;
ALTER TABLE alerts ADD CONSTRAINT alerts_subject_type_check CHECK (subject_type IN ('persona','run','company','project'));

-- Antes desta migração a task copiava o workspace do projeto/persona na criação, o que congelava a herança.
-- Agora a task só guarda workspace quando escolhido de propósito; a herança é resolvida na hora da run.
-- Limpa as cópias (valor igual ao do projeto ou da persona atuais): o diretório efetivo continua o mesmo.
UPDATE tasks t SET workspace_id = NULL
WHERE t.workspace_id IS NOT NULL
  AND (t.workspace_id = (SELECT pr.workspace_id FROM projects pr WHERE pr.id = t.project_id)
       OR t.workspace_id = (SELECT pe.workspace_id FROM personas pe WHERE pe.id = t.persona_id));

-- Tasks de um projeto por status (página do projeto, filtro do board, contagem de progresso).
CREATE INDEX tasks_project_status_idx ON tasks (company_id, project_id, status) WHERE project_id IS NOT NULL;
-- Chaves estrangeiras de workspace: "quem usa este workspace" e o ON DELETE SET NULL sem varredura sequencial.
CREATE INDEX tasks_workspace_idx ON tasks (workspace_id) WHERE workspace_id IS NOT NULL;
CREATE INDEX personas_workspace_idx ON personas (workspace_id) WHERE workspace_id IS NOT NULL;
CREATE INDEX projects_workspace_idx ON projects (workspace_id) WHERE workspace_id IS NOT NULL;
CREATE INDEX runs_workspace_idx ON runs (workspace_id) WHERE workspace_id IS NOT NULL;

-- +goose Down
-- O UPDATE que limpou as cópias de workspace das tasks não é revertido (o valor antigo era derivável do projeto/persona).
DROP INDEX runs_workspace_idx;
DROP INDEX projects_workspace_idx;
DROP INDEX personas_workspace_idx;
DROP INDEX tasks_workspace_idx;
DROP INDEX tasks_project_status_idx;
DELETE FROM alerts WHERE subject_type = 'project';
ALTER TABLE alerts DROP CONSTRAINT alerts_subject_type_check;
ALTER TABLE alerts ADD CONSTRAINT alerts_subject_type_check CHECK (subject_type IN ('persona','run','company'));
DELETE FROM budget_alerts WHERE scope = 'project';
ALTER TABLE budget_alerts DROP CONSTRAINT budget_alerts_scope_check;
ALTER TABLE budget_alerts ADD CONSTRAINT budget_alerts_scope_check CHECK (scope IN ('company','persona'));
ALTER TABLE runs DROP COLUMN workspace_source;
ALTER TABLE runs DROP COLUMN workspace_id;
ALTER TABLE projects DROP COLUMN budget_monthly_cents;
ALTER TABLE projects DROP COLUMN status;
ALTER TABLE workspaces DROP COLUMN default_branch;

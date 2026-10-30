-- Derivação da ingestão (ADR-0075 itens 2 a 6, ADR-0077 item 2; fatia 1 da v0.8, #125). Cada fato bruto guarda só o
-- texto que chegou; estas consultas ligam o texto à entidade (apelido) e criam a entidade quando ela não existe. São
-- usadas na mesma transação do lote, pelo serviço de ingestão.

-- name: ExternalAgentIDByAlias :one
SELECT agent_id FROM external_agent_alias WHERE company_id = @company_id AND alias = @alias;

-- name: ExternalAgentIDByName :one
SELECT id FROM external_agent WHERE company_id = @company_id AND lower(name) = lower(@name);

-- name: InsertExternalAgent :one
INSERT INTO external_agent (id, company_id, name, created_at, updated_at)
VALUES (@id, @company_id, @name, @now, @now)
ON CONFLICT (company_id, lower(name)) DO NOTHING
RETURNING id;

-- name: InsertExternalAgentAlias :exec
INSERT INTO external_agent_alias (company_id, alias, agent_id, created_at)
VALUES (@company_id, @alias, @agent_id, @now)
ON CONFLICT (company_id, alias) DO NOTHING;

-- name: ExternalHostIDByAlias :one
SELECT host_id FROM external_host_alias WHERE company_id = @company_id AND alias = @alias;

-- name: ExternalHostIDByName :one
SELECT id FROM external_host WHERE company_id = @company_id AND lower(name) = lower(@name);

-- name: InsertExternalHost :one
INSERT INTO external_host (id, company_id, name, created_at, updated_at)
VALUES (@id, @company_id, @name, @now, @now)
ON CONFLICT (company_id, lower(name)) DO NOTHING
RETURNING id;

-- name: InsertExternalHostAlias :exec
INSERT INTO external_host_alias (company_id, alias, host_id, created_at)
VALUES (@company_id, @alias, @host_id, @now)
ON CONFLICT (company_id, alias) DO NOTHING;

-- name: TouchExternalHost :exec
-- A hora do último fato recebido só sobe: um lote com fato antigo não a faz voltar.
UPDATE external_host
SET last_seen_at = GREATEST(COALESCE(last_seen_at, @seen_at), @seen_at)
WHERE id = @id;

-- name: RepoPairingWorkspaceID :one
SELECT workspace_id FROM repo_pairing WHERE company_id = @company_id AND repo_key = @repo_key;

-- name: WorkspaceIDBySlug :one
SELECT id FROM workspaces WHERE company_id = @company_id AND slug = @slug AND NOT is_system;

-- name: InsertRepoWorkspace :one
-- Workspace só de metadado, sem caminho em disco (ADR-0075 item 4.3, ADR-0025 item 1). Sem linha de retorno quando
-- outra transação criou o mesmo slug primeiro (corrida): quem chamou relê.
INSERT INTO workspaces (id, company_id, slug, name, path, repo_url, created_at, updated_at)
VALUES (@id, @company_id, @slug, @name, '', '', @now, @now)
ON CONFLICT (company_id, slug) DO NOTHING
RETURNING id;

-- name: InsertRepoPairing :one
-- Sem linha de retorno quando o repositório já estava emparelhado (outra transação): não se gera evento de novo.
INSERT INTO repo_pairing (id, company_id, repo_key, key_kind, workspace_id, created_at, updated_at)
VALUES (@id, @company_id, @repo_key, 'name', @workspace_id, @now, @now)
ON CONFLICT (company_id, repo_key) DO NOTHING
RETURNING id;

-- name: SystemWorkspaceID :one
SELECT id FROM workspaces WHERE company_id = @company_id AND is_system;

-- name: InsertSystemWorkspace :one
-- Workspace `no-repo` (ADR-0075 item 6): sem caminho e sem repositório; não se exclui nem se renomeia. Sem linha de
-- retorno quando já existia (outra transação criou antes).
INSERT INTO workspaces (id, company_id, slug, name, path, repo_url, is_system, created_at, updated_at)
VALUES (@id, @company_id, 'no-repo', 'Sem repositório', '', '', true, @now, @now)
ON CONFLICT DO NOTHING
RETURNING id;

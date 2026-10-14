-- name: CreateWorkspace :one
INSERT INTO workspaces (id, company_id, slug, name, path, repo_url, created_at, updated_at)
VALUES (@id, @company_id, @slug, @name, @path, @repo_url, @now, @now)
RETURNING *;

-- name: GetWorkspaceByID :one
SELECT * FROM workspaces WHERE company_id = @company_id AND id = @id;

-- name: GetWorkspaceBySlug :one
SELECT * FROM workspaces WHERE company_id = @company_id AND slug = @slug;

-- name: ListWorkspaces :many
SELECT * FROM workspaces WHERE company_id = @company_id ORDER BY created_at, id;

-- name: CreateProject :one
INSERT INTO projects (id, company_id, slug, name, description, workspace_id, created_at, updated_at)
VALUES (@id, @company_id, @slug, @name, @description, @workspace_id, @now, @now)
RETURNING *;

-- name: GetProjectByID :one
SELECT * FROM projects WHERE company_id = @company_id AND id = @id;

-- name: GetProjectBySlug :one
SELECT * FROM projects WHERE company_id = @company_id AND slug = @slug;

-- name: ListProjects :many
SELECT * FROM projects WHERE company_id = @company_id ORDER BY created_at, id;

-- +goose Up
-- Push da branch do worktree (ADR-0091): o Hanko passa a enviar a branch da task ao repositório remoto, por ação
-- explícita (botão/CLI de um membro ou a tool hanko_git_push da persona, sob a política de tools). Guardamos o último
-- commit enviado e quando: a aba Git mostra se o remoto está em dia com a branch.
ALTER TABLE task_worktrees
    ADD COLUMN pushed_sha text CHECK (pushed_sha IS NULL OR pushed_sha ~ '^[0-9a-f]{40}$'),
    ADD COLUMN pushed_at  timestamptz;

-- +goose Down
ALTER TABLE task_worktrees DROP COLUMN pushed_at, DROP COLUMN pushed_sha;

-- +goose Up
-- Correção da revisão do ADR-0028: apagar a task apaga as versões dos artefatos dela. Com ON DELETE SET NULL as
-- versões viravam "sem task" e se fundiam à cadeia (empresa, sem task, nome) que já existisse, colidindo no índice
-- único de (empresa, task, nome, versão). A run, o projeto e a persona continuam SET NULL (não definem a cadeia).
ALTER TABLE artifacts DROP CONSTRAINT artifacts_task_id_fkey;
ALTER TABLE artifacts ADD CONSTRAINT artifacts_task_id_fkey FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE artifacts DROP CONSTRAINT artifacts_task_id_fkey;
ALTER TABLE artifacts ADD CONSTRAINT artifacts_task_id_fkey FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE SET NULL;

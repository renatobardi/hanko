-- +goose Up
-- Conversa (chat direto ou sala) com projeto (ADR-0090). Opcional; escolhido ao abrir a conversa. As runs da conversa
-- herdam o workspace e o ambiente do projeto quando a persona não tem os seus (mesma precedência das tasks, ADR-0025 e
-- ADR-0055). Apagar o projeto solta a conversa (SET NULL), que segue valendo sem ele.
ALTER TABLE conversations ADD COLUMN project_id uuid REFERENCES projects(id) ON DELETE SET NULL;
CREATE INDEX conversations_project_idx ON conversations (company_id, project_id) WHERE project_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS conversations_project_idx;
ALTER TABLE conversations DROP COLUMN project_id;

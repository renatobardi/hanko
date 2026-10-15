-- +goose Up
-- Comentário ancorado a um trecho (ADR-0026): citação + offsets + hash do documento-alvo (descrição da task ou
-- resumo de uma run). A coluna é opcional: comentário comum fica com NULL. O formato do JSON é validado no service
-- (domain.Anchor); aqui só se garante que é um objeto.
ALTER TABLE task_comments ADD COLUMN anchor jsonb CHECK (anchor IS NULL OR jsonb_typeof(anchor) = 'object');

-- +goose Down
ALTER TABLE task_comments DROP COLUMN anchor;

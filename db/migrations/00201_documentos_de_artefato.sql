-- +goose Up
-- Documento de artefato com identidade estável e versão MAJOR.MINOR (ADR-0064). Cada linha de artifacts continua
-- sendo uma versão; o documento é o que se altera. Projeto, workspace e tags identificam e filtram, mas não são
-- identidade: mudar qualquer um deles não cria documento novo.
CREATE TABLE artifact_documents (
    id           uuid        PRIMARY KEY,
    company_id   uuid        NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    kind         text        NOT NULL CHECK (kind IN ('artifact','attachment')),
    name         text        NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    project_id   uuid        REFERENCES projects(id) ON DELETE SET NULL,
    workspace_id uuid        REFERENCES workspaces(id) ON DELETE SET NULL,
    tags         text[]      NOT NULL DEFAULT '{}' CHECK (cardinality(tags) <= 20),
    -- detached: alguma task do documento foi apagada (as versões dela ficaram sem task). Um documento assim não entra na
    -- busca por nome do escopo "sem task", senão uma publicação nova sem task viraria versão de um documento órfão.
    detached     boolean     NOT NULL DEFAULT false,
    created_at   timestamptz NOT NULL,
    updated_at   timestamptz NOT NULL
);
CREATE INDEX artifact_documents_company_idx ON artifact_documents (company_id, kind, updated_at DESC, id DESC);
CREATE INDEX artifact_documents_project_idx ON artifact_documents (company_id, project_id) WHERE project_id IS NOT NULL;
CREATE INDEX artifact_documents_workspace_idx ON artifact_documents (company_id, workspace_id) WHERE workspace_id IS NOT NULL;
CREATE INDEX artifact_documents_tags_idx ON artifact_documents USING gin (tags);

-- Versão: ordinal (version, já existente), MAJOR.MINOR e a classificação da mudança. change_declared é o que quem
-- publicou declarou (nulo: o servidor decidiu); change_ratio é a medida do diff por linha (nulo: não medido);
-- change_disputed marca declaração contrariada pela medida. restored_from aponta a versão restaurada.
ALTER TABLE artifacts ADD COLUMN document_id uuid REFERENCES artifact_documents(id) ON DELETE CASCADE;
ALTER TABLE artifacts ADD COLUMN version_major integer;
ALTER TABLE artifacts ADD COLUMN version_minor integer;
ALTER TABLE artifacts ADD COLUMN change_kind text;
ALTER TABLE artifacts ADD COLUMN change_declared text CHECK (change_declared IN ('major','minor'));
ALTER TABLE artifacts ADD COLUMN change_reason text NOT NULL DEFAULT '' CHECK (char_length(change_reason) <= 280);
ALTER TABLE artifacts ADD COLUMN change_ratio double precision CHECK (change_ratio >= 0 AND change_ratio <= 1);
ALTER TABLE artifacts ADD COLUMN change_disputed boolean NOT NULL DEFAULT false;
ALTER TABLE artifacts ADD COLUMN restored_from uuid REFERENCES artifacts(id) ON DELETE SET NULL;

-- Migração: cada cadeia antiga (empresa, kind, task, conversa, nome) vira um documento; vN vira N.0 (a natureza das
-- mudanças antigas é desconhecida, então todas contam como major). Projeto do documento: o da versão mais nova;
-- workspace: o da task, senão o do projeto.
CREATE TEMP TABLE artifact_chain_map ON COMMIT DROP AS
SELECT DISTINCT ON (a.company_id, a.kind, COALESCE(a.task_id, '00000000-0000-0000-0000-000000000000'::uuid),
                    COALESCE(a.conversation_id, '00000000-0000-0000-0000-000000000000'::uuid), a.name)
       gen_random_uuid() AS document_id, a.company_id, a.kind, a.task_id, a.conversation_id, a.name, a.project_id,
       COALESCE(t.workspace_id, pr.workspace_id) AS workspace_id
FROM artifacts a
LEFT JOIN tasks t ON t.id = a.task_id
LEFT JOIN projects pr ON pr.id = COALESCE(a.project_id, t.project_id)
ORDER BY a.company_id, a.kind, COALESCE(a.task_id, '00000000-0000-0000-0000-000000000000'::uuid),
         COALESCE(a.conversation_id, '00000000-0000-0000-0000-000000000000'::uuid), a.name, a.version DESC;

INSERT INTO artifact_documents (id, company_id, kind, name, project_id, workspace_id, created_at, updated_at)
SELECT m.document_id, m.company_id, m.kind, m.name, m.project_id, m.workspace_id,
       (SELECT min(a.created_at) FROM artifacts a
         WHERE a.company_id = m.company_id AND a.kind = m.kind AND a.name = m.name
           AND a.task_id IS NOT DISTINCT FROM m.task_id AND a.conversation_id IS NOT DISTINCT FROM m.conversation_id),
       (SELECT max(a.created_at) FROM artifacts a
         WHERE a.company_id = m.company_id AND a.kind = m.kind AND a.name = m.name
           AND a.task_id IS NOT DISTINCT FROM m.task_id AND a.conversation_id IS NOT DISTINCT FROM m.conversation_id)
FROM artifact_chain_map m;

UPDATE artifacts a SET document_id = m.document_id, version_major = a.version, version_minor = 0, change_kind = 'major'
FROM artifact_chain_map m
WHERE a.company_id = m.company_id AND a.kind = m.kind AND a.name = m.name
  AND a.task_id IS NOT DISTINCT FROM m.task_id AND a.conversation_id IS NOT DISTINCT FROM m.conversation_id;

ALTER TABLE artifacts ALTER COLUMN document_id SET NOT NULL;
ALTER TABLE artifacts ALTER COLUMN version_major SET NOT NULL;
ALTER TABLE artifacts ALTER COLUMN version_minor SET NOT NULL;
ALTER TABLE artifacts ALTER COLUMN change_kind SET NOT NULL;
ALTER TABLE artifacts ADD CONSTRAINT artifacts_semver_check
    CHECK (version_major >= 1 AND version_minor >= 0 AND change_kind IN ('major','minor')
           AND (change_kind = 'minor' OR version_minor = 0));

-- A unicidade passa a ser por documento: a cadeia (task, nome) deixa de ser identidade.
DROP INDEX artifacts_chain_version_uq;
CREATE UNIQUE INDEX artifacts_document_version_uq ON artifacts (document_id, version);
CREATE UNIQUE INDEX artifacts_document_semver_uq ON artifacts (document_id, version_major, version_minor);
-- Compatibilidade sem id: achar o documento pelo nome no escopo antigo (task ou conversa).
CREATE INDEX artifacts_chain_lookup_idx ON artifacts (company_id, kind, name, task_id, conversation_id);

-- Apagar a task preserva o histórico do documento: as versões de artefato perdem a task; anexos seguem em cascata.
-- Gatilho no banco para cobrir todo caminho de exclusão (task, projeto, empresa).
-- +goose StatementBegin
CREATE FUNCTION artifacts_detach_task() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    UPDATE artifact_documents SET detached = true
    WHERE id IN (SELECT document_id FROM artifacts WHERE task_id = OLD.id AND kind = 'artifact');
    UPDATE artifacts SET task_id = NULL WHERE task_id = OLD.id AND kind = 'artifact';
    RETURN OLD;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER tasks_detach_artifacts BEFORE DELETE ON tasks
    FOR EACH ROW EXECUTE FUNCTION artifacts_detach_task();

-- Documento sem versão não existe: quando a última versão some (anexo apagado em cascata com a task, o comentário ou a
-- mensagem; ou exclusão direta), o documento vai junto.
-- +goose StatementBegin
CREATE FUNCTION artifact_documents_drop_empty() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    DELETE FROM artifact_documents d
    WHERE d.id = OLD.document_id AND NOT EXISTS (SELECT 1 FROM artifacts a WHERE a.document_id = OLD.document_id);
    RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER artifacts_drop_empty_document AFTER DELETE ON artifacts
    FOR EACH ROW EXECUTE FUNCTION artifact_documents_drop_empty();

-- +goose Down
-- Volta ao modelo de cadeia (empresa, kind, task, conversa, nome). Documentos que atravessam tasks, ou dois documentos
-- de mesmo nome no mesmo escopo, se fundem por cadeia: o ordinal é renumerado por cadeia, em ordem de criação, para
-- caber no índice antigo. Nada é apagado; MAJOR.MINOR, tags e a identidade do documento se perdem.
DROP TRIGGER artifacts_drop_empty_document ON artifacts;
DROP FUNCTION artifact_documents_drop_empty();
DROP TRIGGER tasks_detach_artifacts ON tasks;
DROP FUNCTION artifacts_detach_task();
DROP INDEX artifacts_chain_lookup_idx;
DROP INDEX artifacts_document_semver_uq;
DROP INDEX artifacts_document_version_uq;
UPDATE artifacts a SET version = r.n
FROM (SELECT id, row_number() OVER (
          PARTITION BY company_id, kind, COALESCE(task_id, '00000000-0000-0000-0000-000000000000'::uuid),
                       COALESCE(conversation_id, '00000000-0000-0000-0000-000000000000'::uuid), name
          ORDER BY created_at, version, id) AS n
      FROM artifacts) r
WHERE r.id = a.id AND a.version <> r.n;
CREATE UNIQUE INDEX artifacts_chain_version_uq
    ON artifacts (company_id, kind, COALESCE(task_id, '00000000-0000-0000-0000-000000000000'::uuid),
                  COALESCE(conversation_id, '00000000-0000-0000-0000-000000000000'::uuid), name, version);
ALTER TABLE artifacts DROP CONSTRAINT artifacts_semver_check;
ALTER TABLE artifacts DROP COLUMN restored_from;
ALTER TABLE artifacts DROP COLUMN change_disputed;
ALTER TABLE artifacts DROP COLUMN change_ratio;
ALTER TABLE artifacts DROP COLUMN change_reason;
ALTER TABLE artifacts DROP COLUMN change_declared;
ALTER TABLE artifacts DROP COLUMN change_kind;
ALTER TABLE artifacts DROP COLUMN version_minor;
ALTER TABLE artifacts DROP COLUMN version_major;
ALTER TABLE artifacts DROP COLUMN document_id;
DROP TABLE artifact_documents;

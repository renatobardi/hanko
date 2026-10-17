-- +goose Up
-- BlobStore (ADR-0051): cada versão de artefato diz em que driver o conteúdo está. Os já existentes ficam em postgres
-- (artifact_blobs); com driver s3 o conteúdo está no bucket e não há linha em artifact_blobs.
ALTER TABLE artifacts ADD COLUMN storage text NOT NULL DEFAULT 'postgres' CHECK (storage IN ('postgres','s3'));
CREATE INDEX artifacts_storage_idx ON artifacts (storage, created_at, id) WHERE storage <> 'postgres';

-- +goose Down
DROP INDEX artifacts_storage_idx;
ALTER TABLE artifacts DROP COLUMN storage;

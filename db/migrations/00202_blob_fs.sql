-- +goose Up
-- Driver fs do BlobStore (ADR-0065): o conteúdo pode ficar em arquivos de um diretório local, além do postgres e do s3.
ALTER TABLE artifacts DROP CONSTRAINT artifacts_storage_check;
ALTER TABLE artifacts ADD CONSTRAINT artifacts_storage_check CHECK (storage IN ('postgres','s3','fs'));

-- +goose Down
ALTER TABLE artifacts DROP CONSTRAINT artifacts_storage_check;
ALTER TABLE artifacts ADD CONSTRAINT artifacts_storage_check CHECK (storage IN ('postgres','s3'));

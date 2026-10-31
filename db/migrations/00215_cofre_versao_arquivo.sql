-- +goose Up
-- Versão anterior de campo arquivo (ADR-0068 §3.1, #117). A troca do arquivo arquiva a versão que sai: a linha de
-- secret_field_versions guarda os metadados, e o texto cifrado do arquivo anterior fica aqui no driver postgres, com o
-- AAD da versão. Nos drivers fs e s3 o mesmo texto vai para o BlobStore, sob a chave do id da versão. Um campo pode ter
-- uma linha por versão arquivada; a linha sai com a versão (cascata).
CREATE TABLE secret_field_version_blobs (
    version_id uuid PRIMARY KEY REFERENCES secret_field_versions(id) ON DELETE CASCADE,
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    content    bytea NOT NULL CHECK (octet_length(content) <= 2097152)
);
CREATE INDEX secret_field_version_blobs_company_idx ON secret_field_version_blobs (company_id);

-- +goose Down
DROP TABLE secret_field_version_blobs;

-- +goose Up
-- Campo arquivo do cofre (ADR-0068 §5.1, fatia 3 #58). O conteúdo fica cifrado (secretbox, AAD do campo) antes de sair do
-- processo. No driver postgres ele vai para esta tabela: artifact_blobs tem FK para artifacts e não aceita o id de um
-- campo. Nos drivers fs e s3, o conteúdo cifrado vai para o BlobStore (ADR-0051), com a chave da empresa e do campo.
CREATE TABLE secret_field_blobs (
    field_id   uuid PRIMARY KEY REFERENCES secret_fields(id) ON DELETE CASCADE,
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    content    bytea NOT NULL CHECK (octet_length(content) <= 2097152)
);
CREATE INDEX secret_field_blobs_company_idx ON secret_field_blobs (company_id);

-- +goose Down
DROP TABLE secret_field_blobs;

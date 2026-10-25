-- +goose Up
-- Blob do envio de arquivo do cofre (ADR-0069 §1 e §3, fatia 4 #59). O conteúdo chega cifrado no cliente; aqui
-- entra só o blob cifrado. No driver postgres ele fica nesta tabela: artifact_blobs tem FK para artifacts e não
-- aceita o id de um envio (o mesmo motivo do 00205 para os campos). Nos drivers fs e s3, o blob cifrado vai para o
-- BlobStore (ADR-0051), com a chave da empresa e do envio na coluna blob_key do envio.
CREATE TABLE secret_send_blobs (
    send_id    uuid PRIMARY KEY REFERENCES secret_sends(id) ON DELETE CASCADE,
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    content    bytea NOT NULL CHECK (octet_length(content) <= 26215424)
);
CREATE INDEX secret_send_blobs_company_idx ON secret_send_blobs (company_id);

-- +goose Down
DROP TABLE secret_send_blobs;

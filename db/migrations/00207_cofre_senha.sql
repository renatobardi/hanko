-- +goose Up
-- Marca explícita de senha em campo (ADR-0074, fecha o §7.2 do ADR-0068). O relatório de saúde mede a entropia dos
-- campos marcados e dos que se chamam `password` ou `senha`. A marca só vale para campo texto secreto. Nenhum campo
-- existente ganha a marca: a coluna nasce falsa, e quem quiser marcar um campo antigo usa a API ou a CLI.
ALTER TABLE secret_fields ADD COLUMN password boolean NOT NULL DEFAULT false
    CHECK (NOT password OR (kind = 'text' AND sensitive));

-- +goose Down
ALTER TABLE secret_fields DROP COLUMN password;

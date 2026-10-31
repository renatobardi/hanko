-- +goose Up
-- Custo de lista na ingestão (ADR-0082 itens 1 e 3; fatia 2 da v0.8, #126). O custo real do Claude Code vem no log
-- `api_request` de mesmo `request_id` do span da chamada, e o log pode chegar antes ou depois do span. Para ligar os dois
-- quando o log chega depois, a chamada guarda o `request_id` do span. Só contrato de coluna: nenhuma linha muda.
ALTER TABLE model_calls ADD COLUMN request_id text CHECK (request_id IS NULL OR length(request_id) BETWEEN 1 AND 200);
CREATE INDEX model_calls_request_idx ON model_calls (company_id, request_id) WHERE request_id IS NOT NULL;

-- +goose Down
DROP INDEX model_calls_request_idx;
ALTER TABLE model_calls DROP COLUMN request_id;

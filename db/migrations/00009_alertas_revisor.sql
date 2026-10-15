-- +goose Up
-- Alertas operacionais, nome amigável de execução e revisão por outro modelo antes do gate.
-- Vocabulário: docs/GLOSSARIO.md (ADR-0016).

-- Alertas operacionais (Caixa de entrada e /metrics). Um alerta aberto por condição:
-- (empresa, tipo, sujeito). O job alert_tick abre quando a condição aparece, atualiza enquanto
-- ela dura e fecha sozinho quando ela some (closed_at). Critérios em docs/RUNBOOK.md.
CREATE TABLE alerts (
    id            uuid PRIMARY KEY,
    company_id    uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    kind          text NOT NULL CHECK (kind IN ('persona_silent','run_stuck','budget_near_limit')),
    subject_type  text NOT NULL CHECK (subject_type IN ('persona','run','company')),
    subject_id    uuid NOT NULL,
    severity      text NOT NULL CHECK (severity IN ('warning','critical')),
    message       text NOT NULL,
    data          jsonb NOT NULL DEFAULT '{}'::jsonb,
    opened_at     timestamptz NOT NULL,
    last_seen_at  timestamptz NOT NULL,
    closed_at     timestamptz
);
CREATE UNIQUE INDEX alerts_open_uq ON alerts (company_id, kind, subject_id) WHERE closed_at IS NULL;
CREATE INDEX alerts_open_company_idx ON alerts (company_id, opened_at DESC) WHERE closed_at IS NULL;

-- Nome amigável da execução (Adjetivo_Substantivo, derivado do id; docs/GLOSSARIO.md). Único por empresa
-- quando preenchido; runs antigas ficam com '' até o backfill do boot nomeá-las.
ALTER TABLE runs ADD COLUMN name text NOT NULL DEFAULT '';
CREATE UNIQUE INDEX runs_company_name_uq ON runs (company_id, name) WHERE name <> '';

-- +goose Down
DROP INDEX runs_company_name_uq;
ALTER TABLE runs DROP COLUMN name;
DROP TABLE alerts;

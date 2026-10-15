-- +goose Up
-- Correções do lote de revisão (ADR-0019/0020).

-- 1) Backfill corretivo de runs.model_source. A 00008 marcou 'persona' toda run com model <> '', mas
--    runs.model também guarda o modelo que o harness informou ao terminar: uma run antiga de persona SEM
--    modelo configurado, cujo harness reportou um modelo, virou 'persona' por engano. Heurística: só runs
--    criadas antes da 00008 ter sido aplicada (data lida do próprio goose_db_version, sem editar a 00008)
--    ainda marcadas 'persona' cuja persona hoje não tem modelo voltam para 'harness_default'.
--    Run nova nunca é tocada: o checkout grava a origem certa desde a 00008.
UPDATE runs r SET model_source = 'harness_default'
FROM personas p
WHERE p.id = r.persona_id AND p.model = '' AND r.model_source = 'persona'
  AND r.created_at < (SELECT min(tstamp) FROM goose_db_version WHERE version_id = 8 AND is_applied);

-- 2) Períodos de vigência das assinaturas. O rateio do custo pago só cobra os meses em que algum período
--    esteve aberto: desativar fecha o período, reativar abre outro, e os meses inativos entre os dois não
--    são cobrados. No máximo um período aberto por assinatura.
CREATE TABLE subscription_periods (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    subscription_id uuid NOT NULL REFERENCES subscriptions(id) ON DELETE CASCADE,
    started_at      timestamptz NOT NULL,
    ended_at        timestamptz,
    CHECK (ended_at IS NULL OR ended_at >= started_at)
);
CREATE UNIQUE INDEX subscription_periods_one_open ON subscription_periods (subscription_id) WHERE ended_at IS NULL;
CREATE INDEX subscription_periods_sub_idx ON subscription_periods (subscription_id, started_at);

-- Backfill com a semântica anterior: ativa = aberta desde a criação; desligada = criação até ended_at.
-- Desligada sem ended_at (criada desligada) nunca acumulou custo e continua sem período.
INSERT INTO subscription_periods (subscription_id, started_at, ended_at)
SELECT id, created_at, NULL FROM subscriptions WHERE active;
INSERT INTO subscription_periods (subscription_id, started_at, ended_at)
SELECT id, created_at, GREATEST(ended_at, created_at) FROM subscriptions WHERE NOT active AND ended_at IS NOT NULL;

-- +goose Down
-- O UPDATE corretivo de model_source não é revertido (não há como saber o valor errado anterior).
DROP TABLE subscription_periods;

-- +goose Up
-- Ajustes do contrato da rodada e da aprovação externas (ADR-0080 e 0081), achados na revisão da 00210 contra os ADRs e o
-- produtor do evento (`docker/oute-emit:508-528@845764f` do oute-agent). Ainda só contrato: nenhuma linha existe nas
-- tabelas, e nenhum dado se move.

-- 1. A decisão de um pedido não garante o sha256: `oute.canal.sha256` vem de `h.get("sha256")` e pode faltar, em especial
-- numa recusa. A 00210 exigia o sha256 em todo pedido decidido, e a ingestão recusaria uma decisão real.
-- +goose StatementBegin
DO $$
DECLARE
    c text;
BEGIN
    FOR c IN SELECT conname FROM pg_constraint
             WHERE conrelid = 'external_approval'::regclass AND contype = 'c'
               AND pg_get_constraintdef(oid) LIKE '%decided_sha256 IS NOT NULL%' LOOP
        EXECUTE format('ALTER TABLE external_approval DROP CONSTRAINT %I', c);
    END LOOP;
END $$;
-- +goose StatementEnd
ALTER TABLE external_approval ADD CONSTRAINT external_approval_decided_check
    CHECK ((state = 'decided') = (decision IS NOT NULL AND approver IS NOT NULL));

-- 2. A duração vem em segundos (`oute.canal.duration_s`), não em milissegundos: a coluna guarda o valor como o produtor o dá.
ALTER TABLE external_approval RENAME COLUMN duration_ms TO duration_s;
ALTER TABLE external_approval RENAME CONSTRAINT external_approval_duration_ms_check TO external_approval_duration_s_check;

-- 3. Quem aprovou: o evento traz o nome (`oute.canal.approver`) além do tipo (`human` ou `human_dark`); a #66 pede nenhuma
-- informação perdida.
ALTER TABLE external_approval ADD COLUMN approved_by text NOT NULL DEFAULT '' CHECK (length(approved_by) <= 200);

-- 4. Estado da rodada que as regras pedem (ADR-0080 item 6; ADR-0083 `round_stalled` e `round_old`): a hora do último evento
-- `oute.swarm.*`, da última pergunta e da última resposta. A regra da pergunta pendente compara as duas últimas; sem estas
-- colunas, cada leitura da Caixa e cada avaliação de alerta varreria `telemetry_logs`, que não tem índice por rodada.
ALTER TABLE external_round ADD COLUMN last_event_at bigint CHECK (last_event_at >= 0);
ALTER TABLE external_round ADD COLUMN last_asked_at bigint CHECK (last_asked_at >= 0);
ALTER TABLE external_round ADD COLUMN last_answered_at bigint CHECK (last_answered_at >= 0);
CREATE INDEX external_round_stalled_idx ON external_round (company_id, last_event_at) WHERE closed_at IS NULL;

-- +goose Down
-- A volta recusa se houver pedido decidido sem sha256, que o schema anterior não aceita; nada é apagado nem inventado.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM external_approval WHERE state = 'decided' AND decided_sha256 IS NULL) THEN
        RAISE EXCEPTION 'há pedido decidido sem sha256: corrija ou apague antes de desfazer a 00211';
    END IF;
END $$;
-- +goose StatementEnd
DROP INDEX external_round_stalled_idx;
ALTER TABLE external_round DROP COLUMN last_answered_at;
ALTER TABLE external_round DROP COLUMN last_asked_at;
ALTER TABLE external_round DROP COLUMN last_event_at;
ALTER TABLE external_approval DROP COLUMN approved_by;
ALTER TABLE external_approval RENAME CONSTRAINT external_approval_duration_s_check TO external_approval_duration_ms_check;
ALTER TABLE external_approval RENAME COLUMN duration_s TO duration_ms;
ALTER TABLE external_approval DROP CONSTRAINT external_approval_decided_check;
ALTER TABLE external_approval ADD CONSTRAINT external_approval_check
    CHECK ((state = 'decided') = (decision IS NOT NULL AND approver IS NOT NULL AND decided_sha256 IS NOT NULL));

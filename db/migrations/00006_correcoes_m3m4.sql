-- +goose Up
-- Correções de M3/M4: wakes sem perda, disparo manual de Fluxo separado do agendado,
-- preço de reserva para modelos desconhecidos, avisos de custo e status pedido na revisão.

-- Contador de pedidos de wake por (persona, task). Todo enfileiramento de wake o incrementa na
-- mesma transação; o worker compara o valor lido antes do checkout com o de depois e, se mudou
-- enquanto ele processava, tenta de novo (o job do river coalesce e perderia o pedido).
-- Sem chaves estrangeiras de propósito: é um contador efêmero e o enfileiramento nunca deve falhar por ele.
CREATE TABLE wake_requests (
    company_id uuid NOT NULL,
    persona_id uuid NOT NULL,
    task_id    uuid NOT NULL,
    seq        bigint NOT NULL DEFAULT 1,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (persona_id, task_id)
);

-- Disparo manual ("Rodar agora") convive com o agendado no mesmo minuto.
ALTER TABLE flow_fires ADD COLUMN manual boolean NOT NULL DEFAULT false;
ALTER TABLE flow_fires DROP CONSTRAINT flow_fires_flow_id_scheduled_for_key;
ALTER TABLE flow_fires ADD CONSTRAINT flow_fires_flow_sched_manual_key UNIQUE (flow_id, scheduled_for, manual);

-- Preço de reserva (prefixo vazio casa com qualquer modelo; vale o prefixo mais longo, então
-- só entra quando nenhum outro casa). Valores do opus: melhor superestimar do que não contar.
INSERT INTO model_prices (id, model, input_per_mtok, output_per_mtok, cache_read_per_mtok, cache_creation_per_mtok, effective_at) VALUES
    ('0192f000-0000-7000-8000-000000000005', '', 15.000000, 75.000000, 1.500000, 18.750000, '2020-01-01T00:00:00Z');

-- Um aviso cost.unknown_model por (empresa, modelo, mês).
CREATE TABLE cost_notices (
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    model      text NOT NULL,
    month      text NOT NULL CHECK (month ~ '^[0-9]{4}-[0-9]{2}$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (company_id, model, month)
);

-- Status que a persona pediu ao fechar a task por um gate de revisão (aplicado se aprovado).
ALTER TABLE gates ADD COLUMN requested_status text CHECK (requested_status IS NULL OR requested_status IN ('done','cancelled'));

-- +goose Down
ALTER TABLE gates DROP COLUMN requested_status;
DROP TABLE cost_notices;
DELETE FROM model_prices WHERE model = '';
DELETE FROM flow_fires f USING flow_fires m
    WHERE f.manual AND NOT m.manual AND f.flow_id = m.flow_id AND f.scheduled_for = m.scheduled_for;
ALTER TABLE flow_fires DROP CONSTRAINT flow_fires_flow_sched_manual_key;
ALTER TABLE flow_fires ADD CONSTRAINT flow_fires_flow_id_scheduled_for_key UNIQUE (flow_id, scheduled_for);
ALTER TABLE flow_fires DROP COLUMN manual;
DROP TABLE wake_requests;

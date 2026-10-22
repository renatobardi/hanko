-- +goose Up
-- Plugins: lacunas do M18 (ADR-0045). Aditiva: uma tabela nova; as demais contribuições (páginas, provider de ambiente,
-- fonte de skills) vivem no manifesto e nas tabelas que já existem (environments, skill_sources).

-- Gatilhos de fluxo: o admin da empresa liga um gatilho declarado por um plugin (flow_triggers do manifesto) a um ou mais
-- fluxos da empresa. Quando o plugin dispara o gatilho (flows.fire), o host dispara esses fluxos.
CREATE TABLE plugin_flow_triggers (
    plugin_id   uuid NOT NULL REFERENCES plugins(id) ON DELETE CASCADE,
    company_id  uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    trigger_key text NOT NULL CHECK (trigger_key ~ '^[a-z][a-z0-9_-]{0,47}$'),
    flow_id     uuid NOT NULL REFERENCES flows(id) ON DELETE CASCADE,
    created_by  uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL,
    PRIMARY KEY (plugin_id, company_id, trigger_key, flow_id)
);
CREATE INDEX plugin_flow_triggers_company_idx ON plugin_flow_triggers (company_id, plugin_id);
CREATE INDEX plugin_flow_triggers_flow_idx ON plugin_flow_triggers (flow_id);

-- +goose Down
DROP TABLE plugin_flow_triggers;

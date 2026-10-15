-- +goose Up
-- Harness genérico ACP (Agent Client Protocol, ADR-0023): 'acp' passa a ser um valor válido de personas.agent.
-- O comando do agente mora em personas.adapter_config. personas.reviewer_agent NÃO muda: um harness alternativo
-- de revisão não carrega adapter_config, então 'acp' não pode ser revisor alternativo (só via persona revisora).
-- Nenhuma outra tabela restringe o nome do harness (runs.agent, reviews.agent e subscriptions.agent são texto livre).
ALTER TABLE personas DROP CONSTRAINT personas_agent_check;
ALTER TABLE personas ADD CONSTRAINT personas_agent_check CHECK (agent IN ('claude', 'codex', 'acp', 'fake'));

-- +goose Down
-- Falha (de propósito) se ainda houver persona acp: apague ou migre essas personas antes de voltar.
ALTER TABLE personas DROP CONSTRAINT personas_agent_check;
ALTER TABLE personas ADD CONSTRAINT personas_agent_check CHECK (agent IN ('claude', 'codex', 'fake'));

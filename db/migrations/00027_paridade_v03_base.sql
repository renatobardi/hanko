-- +goose Up
-- Base comum das telas de paridade da v0.3 (ADR-0032 a ADR-0038): gatilhos de run e tipos de gate novos, num
-- lugar só, para que as migrations de cada funcionalidade não redefinam a mesma constraint.
--   status_card: run de sistema que gera o resumo de um card de status (ADR-0032)
--   skill_test:  run de teste de uma skill no estúdio (ADR-0035)
--   tool:        gate de chamada de tool que pede aprovação humana ("perguntar antes", ADR-0036)
ALTER TABLE runs DROP CONSTRAINT runs_trigger_check;
ALTER TABLE runs ADD CONSTRAINT runs_trigger_check CHECK (trigger IN
    ('assign','comment','manual','heartbeat','gate','flow','review','chat','memory','status_card','skill_test'));

ALTER TABLE gates DROP CONSTRAINT gates_kind_check;
ALTER TABLE gates ADD CONSTRAINT gates_kind_check CHECK (kind IN ('approval','task_review','hire','memory','tool'));

-- +goose Down
DELETE FROM gates WHERE kind = 'tool';
ALTER TABLE gates DROP CONSTRAINT gates_kind_check;
ALTER TABLE gates ADD CONSTRAINT gates_kind_check CHECK (kind IN ('approval','task_review','hire','memory'));
UPDATE runs SET trigger = 'manual' WHERE trigger IN ('status_card','skill_test');
ALTER TABLE runs DROP CONSTRAINT runs_trigger_check;
ALTER TABLE runs ADD CONSTRAINT runs_trigger_check CHECK (trigger IN
    ('assign','comment','manual','heartbeat','gate','flow','review','chat','memory'));

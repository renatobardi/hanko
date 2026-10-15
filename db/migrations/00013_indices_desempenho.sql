-- +goose Up
-- Índices de desempenho medidos com tools/seed-load + tools/perf-explain (docs/PERF.md): cada um corrige uma varredura
-- sequencial (ou um laço de varreduras) que cresce com o histórico de runs/tasks.
--
-- ATENÇÃO (bases grandes): CREATE INDEX comum toma ShareLock e bloqueia escritas na tabela enquanto constrói.
-- Aqui é aceitável porque o banco de produção ainda é pequeno (a construção leva centenas de ms). Numa base grande,
-- crie antes, FORA de transação e sem bloquear escritas, com os mesmos nomes e definições, p. ex.:
--     CREATE INDEX CONCURRENTLY runs_task_created_idx ON runs (task_id, created_at DESC);
-- Como esta migração usa IF NOT EXISTS, ela só registra o que já existe. (Índice CONCURRENTLY interrompido deixa um
-- índice INVALID: derrube-o com DROP INDEX e refaça.)

-- runs por task: última run da task no heartbeat (ListWakeCandidates), runs superadas da Caixa de entrada
-- (ListFailedRuns), listagem de runs por task, e a busca de runs na exclusão em cascata de uma task.
CREATE INDEX IF NOT EXISTS runs_task_created_idx ON runs (task_id, created_at DESC);

-- Atividade mais recente da persona (alert_tick / ListSilentPersonas): max(coalesce(finished_at, started_at, created_at))
-- vira uma sonda no fim do índice em vez de ler todas as runs da persona.
CREATE INDEX IF NOT EXISTS runs_persona_activity_idx ON runs (persona_id, (COALESCE(finished_at, started_at, created_at)));

-- Busca de run pelo nome sem diferenciar maiúsculas (ListRunViews com name): a consulta compara lower(name).
CREATE INDEX IF NOT EXISTS runs_company_lower_name_idx ON runs (company_id, lower(name));

-- Backfill de nomes no boot (ListRunsWithoutName): o conjunto é vazio depois do backfill, então o índice é vazio.
CREATE INDEX IF NOT EXISTS runs_unnamed_idx ON runs (created_at, id) WHERE name = '';

-- Listagem de tasks (updated_at DESC, number DESC) sem filtro de status: sem este índice lê a empresa inteira e ordena.
CREATE INDEX IF NOT EXISTS tasks_company_updated_idx ON tasks (company_id, updated_at DESC, number DESC);

-- max(last_human_change_at) por persona (ListSilentPersonas). Cobre também tudo o que tasks_persona_idx cobria
-- (persona_id é a primeira coluna), que fica redundante e é removido.
CREATE INDEX IF NOT EXISTS tasks_persona_human_change_idx ON tasks (persona_id, last_human_change_at) WHERE persona_id IS NOT NULL;
DROP INDEX IF EXISTS tasks_persona_idx;

-- Tasks que o heartbeat pode acordar, na ordem em que ele as lê (ListWakeCandidates: ORDER BY updated_at LIMIT n):
-- o índice parcial é pequeno (só todo/in_progress com persona) e dispensa ler e ordenar a tabela inteira a cada tick.
CREATE INDEX IF NOT EXISTS tasks_workable_idx ON tasks (updated_at) WHERE status IN ('todo', 'in_progress') AND persona_id IS NOT NULL;

-- Cada evento de run anexado faz UPDATE runs SET next_event_seq = next_event_seq + 1. Com fillfactor 100 a página da run
-- está cheia e quase nenhum UPDATE é HOT (medido: 6%), então cada evento grava uma entrada em todos os índices de runs.
-- Com 80 sobra espaço na página: o UPDATE vira HOT (medido: 100%) e deixa os índices em paz. Vale só para páginas novas;
-- para reempacotar as existentes: VACUUM FULL runs (bloqueia a tabela; em base grande use pg_repack, fora do horário).
ALTER TABLE runs SET (fillfactor = 80);

-- +goose Down
ALTER TABLE runs RESET (fillfactor);
DROP INDEX IF EXISTS tasks_workable_idx;
CREATE INDEX IF NOT EXISTS tasks_persona_idx ON tasks (persona_id) WHERE persona_id IS NOT NULL;
DROP INDEX IF EXISTS tasks_persona_human_change_idx;
DROP INDEX IF EXISTS tasks_company_updated_idx;
DROP INDEX IF EXISTS runs_unnamed_idx;
DROP INDEX IF EXISTS runs_company_lower_name_idx;
DROP INDEX IF EXISTS runs_persona_activity_idx;
DROP INDEX IF EXISTS runs_task_created_idx;

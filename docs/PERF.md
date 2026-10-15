# Desempenho de consultas — medições e ajustes

Medição das consultas quentes do Hanko num banco com carga sintética grande, antes e depois da migration
`00013_indices_desempenho.sql`. Tudo aqui é reproduzível com duas ferramentas do repositório (abaixo).

## Como reproduzir

```sh
export HANKO_TEST_DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5432/hanko_perf?sslmode=disable'

# 1. banco no estado anterior à 00013 + carga (apaga o schema public; ~3,5 min com -scale 1)
go run ./tools/seed-load -reset -migrate-to 12 -scale 1 -ref 2026-10-15T06:00:00Z

# 2. "antes": EXPLAIN (ANALYZE, BUFFERS) de cada consulta quente
go run ./tools/perf-explain -json antes.json -plans antes-planos.txt

# 3. aplica a 00013 (hanko migrate, ou goose) e mede de novo
go run ./tools/perf-explain -json depois.json -plans depois-planos.txt

# 4. tabela markdown (antes/depois/ganho/buffers/nó raiz)
go run ./tools/perf-explain -merge antes.json,depois.json
```

- `tools/seed-load` cria o banco do zero, de forma determinística (mesma `-scale` e mesmo `-ref` ⇒ mesmo banco). Termina
  com `VACUUM (ANALYZE)`, como um banco maduro. `-scale 0.1` dá um banco 10× menor para testes rápidos.
- `tools/perf-explain` lê o SQL das constantes geradas pelo sqlc em `internal/store` (o texto exato que o app executa) e
  o executa com parâmetros reais pelo protocolo estendido (plano customizado, como nas primeiras execuções do app).
  Cada medição roda dentro de uma transação que é revertida, então UPDATE/INSERT medidos não alteram a carga.
  Reporta a mediana de 3 execuções depois de 1 de aquecimento; `-only grupo/nome` filtra e `-reps N` muda as repetições.

## Carga e ambiente

| Tabela | Linhas | Heap | Índices |
|---|---:|---:|---:|
| `run_events` | 5,05 M | 1098 MB | 463 MB |
| `activity_events` | 1,00 M | 166 MB | 179 MB |
| `runs` | 205.688 | 58 MB | 65 MB |
| `task_comments` | 100.000 | 15 MB | 9 MB |
| `tasks` | 50.000 | 18 MB | 8 MB |
| `gates` | 5.797 (171 pendentes, todas as empresas) | 1 MB | 1 MB |
| `alerts` | 2.000 (6 abertos) | — | — |
| `personas` / `companies` | 30 / 3 | — | — |

Três empresas com 60 % / 30 % / 10 % das tasks; histórico de 150 dias; ~630 runs em execução (`running`/`queued`) e ~4.000
tasks em `in_progress`. Os números abaixo são da empresa grande (60 %: 30.100 tasks, 123.836 runs, 3.503 gates), mês corrente
= 15 dias de dados.

Ambiente: PostgreSQL 16.15, 2 vCPU, 8 GB de RAM, `shared_buffers=128MB`, configuração padrão (JIT ligado), cache quente (dados
em memória; em disco frio todos os tempos "antes" seriam piores). A máquina é compartilhada: variação de ±30 % entre
execuções é ruído, e só diferenças maiores que isso estão marcadas como ganho.

## Resultado

Mediana do tempo de execução (`Execution Time`) em ms. "Buffers" é o total de páginas tocadas (hit+read).

### Consultas que mudaram

| Consulta (quem chama) | Antes | Depois | Buffers | Índice / ajuste |
|---|---:|---:|---|---|
| **Wake candidates** do heartbeat (`ListWakeCandidates`) | 1309 | 1,5 | 382.531 → 1.057 | `runs_task_created_idx` (a "última run da task" e o "nenhuma run cobre a mudança" varriam `runs` inteira (205 mil linhas) para cada task candidata) e `tasks_workable_idx` (índice parcial na ordem do `ORDER BY updated_at`). Sem o ajuste ainda disparava JIT (441 ms só de compilação) |
| **alert_tick**: personas silenciosas (`ListSilentPersonas`) | 177 | 3,2 | 63.990 → 377 | `runs_persona_activity_idx` (`max(coalesce(finished_at, started_at, created_at))` por persona vira uma sonda no fim do índice) e `tasks_persona_human_change_idx` (`max(last_human_change_at)`) |
| **alert_tick**: runs travadas (`ListStuckRuns`) | 28 | 7–18 (varia com o plano) | 5.149 → 5.204 | Consulta: o último evento da run passou de `max(received_at)` (lê todos os eventos da run) para `ORDER BY seq DESC LIMIT 1` (uma sonda no índice `(run_id, seq)`). O restante do tempo é o join com `tasks` para as ~560 runs em execução da carga |
| Listagem de runs por nome (`ListRunViews`, `name`) | 103 | 0,11 | 124.406 → 10 | `runs_company_lower_name_idx` (a consulta compara `lower(name)`, o índice único existente é sobre `name`) |
| Listagem de runs de uma task (`ListRunViews`, `task_id`) | 25 | 0,08 | 7.477 → 10 | `runs_task_created_idx` (só existia o índice parcial das runs ativas). Também serve a exclusão em cascata de uma task |
| **Caixa de entrada**: runs que falharam (`ListFailedRuns`) | 68 | 2,9 | 9.519 → 5.575 | `runs_task_created_idx` (o `NOT EXISTS` "sem run mais nova na task" era um anti-join com varredura total de `runs`) |
| Listagem de tasks, página 1 (`ListTaskViews`) | 48 | 1,2 | 2.879 → 1.060 | `tasks_company_updated_idx (company_id, updated_at DESC, number DESC)`: lia a empresa inteira e ordenava |
| Listagem de tasks, `status in (todo, in_progress)` | 14 | 1,6 | 2.027 → 1.488 | idem |
| Listagem de tasks, por persona | 7,2 | 2,0 | 1.844 → 2.017 | idem |
| Listagem de tasks, `offset 20000` | 61 | 14 | 4.698 → 20.691 | índice + consulta em duas etapas (CTE `page` escolhe os ids só pelas chaves; os joins vêm depois, só para a página). Mais páginas tocadas, porém sem juntar nada das linhas puladas |
| Listagem de gates, todos (`ListGateViews`) | 21 | 3,8 | 2.378 → 545 | consulta em duas etapas (CTE `page` só em `gates`); antes juntava `tasks` inteira (50 mil linhas) antes de ordenar |
| Listagem de gates, `offset 3000` | 28 | 5,0 | 2.378 → 545 | idem |
| Boot: runs sem nome (`ListRunsWithoutName`) | 47 | 0,01 | 7.415 → 1 | `runs_unnamed_idx` parcial (`WHERE name = ''`; vazio depois do backfill) |

Consultas que ficaram **um pouco mais lentas** (custo aceito):

| Consulta | Antes | Depois | Por quê |
|---|---:|---:|---|
| Listagem de gates, só `pending` (171 linhas) | 0,55 | 2,2 | com `gates` pequena o planner prefere varrê-la para juntar com a página; o ganho aparece nas listagens grandes e cresce com a tabela |
| Boot: interromper runs ativas (`InterruptActiveRuns`, ~630 linhas) | 17 | 32 | atualiza 3 índices novos que contêm `status`/`finished_at`; roda uma vez por boot |
| Consultas que varrem muito de `runs` (rateio, custos 90 dias) | — | +7 % em buffers | `fillfactor 80` deixa a tabela ~25 % maior em páginas novas/reempacotadas |

### Consultas já boas (sem mudança relevante)

Ficam abaixo de 1 ms (índices certos já existiam): listagens de runs (página 1, por persona, `failed`), `ListActivity`
(página 1, `before` fundo, por task, painel), gates por task, `CountPendingGates`, personas em erro, revisões abertas,
alertas abertos, `SpendBetween` por persona, reservas de orçamento (`ActiveRunReserves`, `PersonaRunReserve`), runs em
execução, `ListRunEvents` (SSE catch-up por `seq`: 0,02–0,05 ms, índice `UNIQUE (run_id, seq)`), `ExportRunEventsPage`
(keyset por `seq`: 0,03 ms), `AppendRunEvent` (0,2 ms), `SetPersonaAfterRun`/`SetPersonaAwaitingGate`/`ReleasePersonaFromGate`
(o `NOT EXISTS` de run ativa da persona já usa o índice parcial único `runs_one_active_per_task`, 0,08 ms: não precisa de
índice próprio).

Entre 1 e 10 ms (cresce com o conteúdo, não com o histórico): `DashboardCounts` 5,5 ms (conta o conjunto de tasks abertas
da empresa), `MonthCost`/`SpendBetween` da empresa 9–11 ms, `SubscriptionWeekUsage` 2–3 ms, `ListWorkableTasksForPersona`
2,5 ms (557 linhas), `ExportTasks` 10 ms.

Ainda caras (proporcionais às runs do período; nenhum índice resolve, ver "Pendências"): `CostBreakdown` 35–70 ms no mês
(1,5× no 'persona' por ruído de paralelismo) e ~300 ms em 90 dias, `SubscriptionRunsInPeriod` (rateio do pago) ~45 ms,
`ExportRuns` ~150–190 ms e `ExportComments` ~40–55 ms (leem a empresa inteira), busca textual de tasks (`q`, `ILIKE '%…%'`) ~75 ms.

### Escrita

Índices novos têm custo em `INSERT`/`UPDATE`. Medido com `pgbench` (2 clientes, transações revertidas para não crescer o
banco; a máquina é compartilhada e a variação entre rodadas é de ±10 %):

| Operação | Sem a 00013 | Só os índices (fillfactor 100) | 00013 completa (fillfactor 80, runs reempacotadas) |
|---|---:|---:|---:|
| Anexar evento (`UPDATE runs … next_event_seq` + `INSERT run_events`) | 1.830–1.890 tps | 1.570–1.740 tps | 1.660–2.080 tps |
| Ciclo de uma run (INSERT + start + finish) | 1.750–1.910 tps | 1.630–1.690 tps | 1.700–1.750 tps |
| Fração de UPDATEs de `runs` que são HOT no anexo de evento | 6 % | — | 100 % |

O achado importante é a última linha: com `fillfactor 100` quase nenhum `UPDATE runs SET next_event_seq = next_event_seq + 1`
(um por evento!) era HOT, então **cada evento anexado gravava uma entrada em todos os índices de `runs`** (7 antes, 11
depois). Com `fillfactor 80` o UPDATE cabe na mesma página e deixa os índices em paz; isso compensa o custo dos índices novos
e evita que o inchaço dos índices de `runs` cresça com o volume de eventos. Só vale para páginas novas; para reempacotar as
existentes, `VACUUM FULL runs` (bloqueia a tabela) ou `pg_repack`, fora do horário.

Tamanho dos índices novos nesta carga: `runs_task_created_idx` 13 MB, `runs_persona_activity_idx` 15 MB,
`runs_company_lower_name_idx` 10 MB, `tasks_company_updated_idx` 2,4 MB, `tasks_persona_human_change_idx` 1,8 MB,
`tasks_workable_idx` 0,2 MB, `runs_unnamed_idx` 8 kB — ~43 MB (2 % do banco). `tasks_persona_idx` foi removido (prefixo de
`tasks_persona_human_change_idx`).

## Mudanças de consulta (sem mudança de semântica)

- `ListTaskViews` e `ListGateViews`: a página é escolhida primeiro só na tabela principal (`WITH page AS (… ORDER BY … LIMIT …
  OFFSET …)`) e só então recebe os joins; a ordenação é repetida no `SELECT` externo porque o join não a preserva. Mesmos
  filtros, mesma ordem, mesmos `LIMIT`/`OFFSET` (a API continua expondo `offset`; trocar por keyset mudaria o contrato).
  Coberto por `TestListGatesPaginacaoEstavel` e `TestListTasksPaginacaoEstavel` (percorrer em páginas = lista inteira, na
  mesma ordem, inclusive com `updated_at` igual) além dos testes de filtro que já existiam.
- `ListStuckRuns`: "último evento da run" = o de maior `seq` (`ORDER BY seq DESC LIMIT 1`) em vez de `max(received_at)`. `seq`
  é alocado em ordem por run (`UPDATE … RETURNING` serializa os escritores) e `received_at` vem do relógio do app, então os dois só
  divergiriam por milissegundos numa corrida entre escritores concorrentes da mesma run, irrelevante para um limite de
  minutos. É a única mudança que é equivalente "na prática" e não por construção. Coberto por `TestAlertRunStuck`.

Os testes `TestConsultasQuentesUsamIndices` e `TestBuscaDeRunPorNomeUsaIndice` pedem o plano da consulta REAL (via um
`store.DBTX` que faz `EXPLAIN` com os mesmos parâmetros) e exigem que o índice previsto apareça. Se alguém mudar a expressão
de uma consulta ou de um índice e os dois deixarem de casar (como em `lower(name)` ou `coalesce(finished_at, …)`), o teste
falha em vez de a lentidão só aparecer em produção. `TestMigration00013Indices` cobre Up e Down.

## Aplicar em produção

A 00013 usa `CREATE INDEX IF NOT EXISTS` comum, dentro da transação do goose: bloqueia escritas na tabela durante a construção.
Com o banco de produção pequeno isso leva centenas de milissegundos. Em bases grandes (milhões de runs):

1. Antes de subir a versão, crie cada índice **fora de transação** e sem bloquear escritas, com o mesmo nome e definição:
   `CREATE INDEX CONCURRENTLY runs_task_created_idx ON runs (task_id, created_at DESC);` (idem para os outros seis — ver o SQL
   na migration). Um `CONCURRENTLY` interrompido deixa um índice `INVALID`: `DROP INDEX` e refaça.
2. A migration, com `IF NOT EXISTS`, só registra o que já existe. O `DROP INDEX tasks_persona_idx` e o
   `ALTER TABLE runs SET (fillfactor = 80)` são instantâneos.
3. Reempacote `runs` fora do horário se quiser o efeito do `fillfactor` nas linhas antigas (`pg_repack` ou `VACUUM FULL runs`).

O Down remove os índices novos, restaura `tasks_persona_idx` e o `fillfactor` padrão.

## Avaliado e não aplicado

| Ideia | Resultado | Motivo |
|---|---|---|
| `pg_trgm` + GIN em `tasks.title`/`description` para a busca `q` | 75 → 3 ms | exige `CREATE EXTENSION` (privilégio/contrib) numa migration que roda no boot de qualquer instalação; um erro derrubaria o boot. Se a instalação puder: `CREATE EXTENSION pg_trgm; CREATE INDEX … USING gin (title gin_trgm_ops); … (description gin_trgm_ops);`  |
| Índice cobrindo `runs (company_id, created_at) INCLUDE (cost_usd)` para `SpendBetween`/`MonthCost` | 9,7 → 5,6 ms | ganho de 1,7× por mais um índice em `runs`; o custo continua O(runs do mês) |
| Índice parcial `runs (persona_id, company_id) WHERE status IN ('queued','running')` | sem ganho | o planner já varre o índice parcial único `runs_one_active_per_task` para qualquer filtro por status ativo (0,03–0,08 ms) |
| Índice `run_events (run_id, received_at)` para `ListStuckRuns` | — | +200 MB e uma escrita extra em cada evento (a tabela mais quente) para ganhar poucos ms; a reescrita da consulta por `seq` já resolve |
| `DashboardCounts` com índice parcial de tasks abertas | 5,5 → ~3 ms estimado | o conjunto de tasks abertas da empresa é limitado; não compensa o índice |

## Pendências (próximos passos)

1. **Gasto do mês/rateio/`CostBreakdown`** são O(runs do período). `SpendBetween` roda a cada checkout sob o advisory lock
   de orçamento (≈10 ms aqui com 20 mil runs no mês; ~200 ms com 400 mil). O caminho certo é uma tabela de agregados
   (`run_costs_daily`: empresa × persona × dia, mantida na mesma transação do `FinishRun`) — muda semântica de escrita,
   fica para um ADR.
2. **Export**: `ExportTasks`/`ExportRuns`/`ExportComments` leem a empresa inteira para a memória (só os eventos são
   paginados por `seq`). Em empresas muito grandes o limite será memória, não SQL; a solução é paginar por keyset
   `(created_at, id)` e escrever em fluxo.
3. Rever `shared_buffers` (padrão 128 MB) em produção: `run_events` e `activity_events` passam de 1,5 GB aqui.

# Banco e retenção da telemetria do oute no Hanko (#68)

Fase `strat` (AI-DLC) · 2026-10-24 · autor: agente (Claude), a pedido do Bardi · parte de #66 (decisões 9 e 13) ·
**gate fechado pelo Bardi em 2026-10-24** (§10).

**Gate:** o Bardi decidiu banco, retenção, os spans internos do Codex e a base da projeção (§10). Onde o texto das §7 e
§8 e a §10 divergem, vale a §10.

## Resumo

- **Volume.** Na base decidida no gate da #67 (ponto 9: 236.121 linhas por dia), 12 meses têm 86,2 milhões de linhas.
  Elas ocupam **51,9 GB no DuckDB** e **125,9 GB no Postgres** particionado por mês (108,4 GB de tabela e 17,5 GB de
  índice). O disco de dados do oute-server tinha 74 GB livres em 2026-10-24 (pedido `20261024-160053`).
- **Consultas de janela curta** (7 dias, conversa, Tray, alertas): as duas engines ficam abaixo de 0,2 s na mediana,
  exceto a lista de conversas no Postgres (5,5 s com 12 meses).
- **Consultas de janela longa sobre o dado bruto:** o Postgres leva 16 s (30 dias) e 87 s (366 dias); o DuckDB, 19 ms e
  219 ms. Com uma **tabela estreita de chamadas ao modelo** (338 MB por ano) e um **agregado diário** (27 MB por ano), o
  Postgres cai para 30 ms e 13 ms.
- **Ingestão com dedupe:** Postgres de 18 mil a 34 mil linhas/s; DuckDB, ~1.700 linhas/s, perto dos ~2.900 spans/s
  que já derrubaram o agent-studio no incidente #570 do oute-agent.
- **Backup e restore com 12 meses:** o Postgres fez dump em 3 min 54 s (20,8 GB) e restore em 8 min 23 s. No DuckDB, a
  cópia do jeito do agent-studio (`COPY FROM DATABASE`) **falhou por falta de memória** com 1, 2 e 3 GB, e o restore
  de um export em Parquet também falhou com 3 GB, o limite que o agent-studio usa.
- **Estado (o que hoje está no SurrealDB):** com 36 meses são 197.100 conversas. O Postgres guarda tudo em 64 MB e
  responde abaixo de 1 ms; o SurrealDB, em 116 MB de disco e 385 MB de memória, responde em 10 a 13 ms, quase tudo a
  ida e volta HTTP.
- **Por que os spans do Codex de 2026-10-05 entraram no DuckDB:** o filtro `filter/codex_noise` só existe desde
  2026-10-11 (`fadb315`, #792 do oute-agent). O dia 10-06, base de spans da projeção, também é anterior ao filtro.
- **Recomendação do autor:** Postgres sozinho (ADR-0011 continua valendo), com o dado bruto particionado por mês numa
  janela quente, tabelas derivadas sem prazo e o detalhe antigo reidratado do bucket pelo replay (decidido no gate da
  #67). Detalhe e opções de janela na §7.

## 1. Como foi medido

- **Onde.** No container de desenvolvimento, não no oute-server: 15 CPUs e 7,9 GB de RAM, dividida com outras sessões.
  O oute-server tem 4 CPUs e 24 GB de RAM; o container do agent-studio tem limite de 6 GiB (pedido `20261024-181500`,
  passo 1). Os números absolutos valem para comparar as opções entre si, não como previsão exata do host.
- **Versões.** DuckDB 1.5.6, a mesma do agent-studio (`docker/agent-studio/requirements.txt:17@845764f`). Postgres
  **16.15** local; o Hanko usa o 17 (ADR-0011), e o 17 não foi medido. SurrealDB 3.3.0, a mesma do host
  (`docker/compose.yaml:280@845764f`), binário da release conferido pelo sha256 da API do GitHub
  (`f03356497f875057126641f06757671542e0a7b75f18fd13820c2dab29349d74`).
- **Configuração.** DuckDB com 2 threads e 2 GB (o agent-studio usa 2 threads e 3 GB, `store.py:192@845764f`).
  Postgres com `shared_buffers` 512 MB, `work_mem` 64 MB, `default_toast_compression = lz4`, `jit = off`.
- **Dados.** Sintéticos, no schema do agent-studio (`store.TABLES`), com as formas medidas no host pelo pedido
  `20261024-181500`: tamanho médio de cada JSON, fração de spans sem conversa, conversas por dia, chamadas ao modelo e
  `api_request`. Gerador: [`gen.py`](2026-10-banco-telemetria/gen.py).
- **Calibração.** No mesmo volume do host (16 dias, 3,78 milhões de linhas), o sintético ocupa 2.480.943.104 bytes; a
  cópia real tem 2.500.603.904 bytes para 3,84 milhões de linhas (651 B por linha). O sintético distribui o uso por
  igual no tempo; o real teve a pausa do Bardi a partir de 2026-10-14, então as consultas de 7 dias no real tocam menos
  linhas e saem mais rápidas (§3).
- **Consultas.** As leituras que as telas e os consumidores fazem, reduzidas ao SQL que pesa:
  [`qset.py`](2026-10-banco-telemetria/qset.py), com a fonte de cada uma no código do agent-studio. O mesmo SQL do
  DuckDB rodou no banco real do host. Tempo: 1ª execução e mediana das 3 seguintes ([`bench.py`](2026-10-banco-telemetria/bench.py)).
  O cache do sistema operacional não foi limpo (sem root); a 1ª execução é a mais próxima de "frio".
- **Limites.** 36 meses não foram gerados: os números de 36 meses são extrapolação linear e estão marcados assim. A
  tabela simples (sem partição) de 12 meses não foi carregada, para caber na memória e no disco; a diferença entre
  simples e particionada está medida no volume real.

| Id | Consulta (fonte em `docker/agent-studio/agent_studio/` @845764f) |
|---|---|
| q1 | uso por host × agente × modelo, 7 dias, com p95 (`usage.py:145-172`) |
| q2 | custo real: span de chamada + log `api_request` pelo `request_id`, 7 dias (`cost.py:229-251`) |
| q3 | série diária de 30 dias (Dashboard) |
| q4 | série diária de 366 dias |
| q5 | lista de conversas, 7 dias, 50 mais novas (`conversations.py:27-89`) |
| q6 | detalhe da maior conversa: até 5000 spans e 200 logs (`conversations.py:158-213`) |
| q7 | Tray: custo de hoje por agente e erros da última hora (`tray.py:73-104`) |
| q8 | alertas: último dado por host e métricas de exportação (`alerts.py:197-311`) |
| q9 | ferramentas por `tool_name`, 7 dias (`tools.py:56-70`) |
| q10 | heatmap dia da semana × hora, 7 dias (Dashboard) |

## 2. Volume

| Medida | Valor | Fonte |
|---|---|---|
| Linhas por dia (base do gate da #67) | 236.121 (134.188 spans, 41.006 logs, 60.927 metrics) | #68, comentário do gate |
| Linhas em 12 meses | 86.184.165 | `pgload.py` (contagem de cada tabela) |
| DuckDB, 12 meses | 51.934.932.992 bytes (603 B por linha) | `bench.py duckdb` |
| Postgres particionado, 12 meses | 108,39 GB de tabela + 17,55 GB de índice = 125,94 GB | `bench.py pg m12part` |
| Postgres simples, volume real | 4,75 GB de tabela + 0,71 GB de índice | `bench.py pg r16plain` |
| 36 meses (extrapolado, não medido) | DuckDB ~156 GB; Postgres ~378 GB | 3 × 12 meses |
| Disco de dados livre no oute-server | 74 GB | pedido `20261024-160053`, passo 1 |

O Postgres ocupa ~2,4 vezes o DuckDB: as linhas têm ~1 KB e ficam abaixo do limite em que o Postgres comprime
(TOAST), enquanto o DuckDB comprime por coluna.

**Projeção sem o ruído do Codex (não decidido).** O dia 10-06 é anterior ao filtro de 2026-10-11, e 109.196 dos seus
134.188 spans são do `codex_cli_rs` (pedido `20261024-160053`). O maior dia de spans depois do filtro é 2026-10-12, com
17.574 (pedido `20261024-111550`). Com ele, a base seria 119.507 linhas por dia, cerca da metade. Esta nota usa a base
decidida pelo Bardi.

## 3. Consultas

Mediana em ms (1ª execução entre parênteses quando passa de 1 s). "Real" é o banco do host; os demais, sintéticos.

| Consulta | DuckDB real (host) | DuckDB, volume real | DuckDB, 12 meses | Postgres simples, volume real | Postgres particionado, volume real | Postgres particionado, 12 meses |
|---|---|---|---|---|---|---|
| q1 | 3 | 8 | 8 | 86 (8.598) | 67 (4.900) | 35 (4.180) |
| q2 | 18 | 107 | 120 | 676 (9.983) | 433 (5.074) | 90 (2.798) |
| q3 | 24 | 10 | 19 | 148 (4.798) | 123 (4.729) | **16.053** |
| q4 | 24 | 10 | 219 (1.076) | 150 | 120 | **86.978** |
| q5 | 5 | 15 | 15 | 996 (15.948) | 2.551 (9.689) | **5.468** |
| q6 | 26 | 12 | 14 | 1 | 1 | 2 |
| q7 | 3 | 2 | 2 | 7 | 8 | 7 |
| q8 | 5 | 2 | 3 | 89 | 115 | 51 (1.009) |
| q9 | 6 | 41 | 42 | 24 | 25 | 20 (1.123) |
| q10 | 2 | 5 | 5 | 50 | 39 | 39 (1.520) |

Fontes: pedido `20261024-181500`, passo 3 (real); [`res_duck_m12.txt`](2026-10-banco-telemetria/res_duck_m12.txt);
[`res_pg.txt`](2026-10-banco-telemetria/res_pg.txt). O volume real sintético foi medido na mesma sessão com
`bench.py duckdb`.

**Por que o Postgres é lento em q3 e q4.** O `EXPLAIN (ANALYZE, BUFFERS)` de q3 com 12 meses lê 127.305 blocos de 8 KB
para 149.257 linhas: as chamadas ao modelo são ~4% dos spans e ficam espalhadas entre o resto, então cada linha custa
uma leitura de página. O índice acha as linhas em ~10 ms; o tempo vai na leitura das páginas.

**Postgres com tabelas derivadas** ([`calls.py`](2026-10-banco-telemetria/calls.py), [`res_pg_calls.txt`](2026-10-banco-telemetria/res_pg_calls.txt)):

| Tabela | Linhas (12 meses) | Tamanho |
|---|---|---|
| `model_calls` (só as chamadas ao modelo, colunas de uso e custo) | 1.812.721 | 338.124.800 bytes |
| `calls_daily` (dia × host × agente × modelo × repositório × assinatura) | 211.577 | 27.246.592 bytes |

| Consulta | Postgres, dado bruto | Postgres, tabela derivada |
|---|---|---|
| q1 | 35 | 16 |
| q3 | 16.053 | 30 |
| q4 | 86.978 | 330 (`model_calls`); **13** (`calls_daily`) |
| q10 | 39 | 20 |

A lista de conversas (q5) tem o mesmo remédio: a conversa já é estado derivado (o `conversa:` do SurrealDB, §6), e a
lista lê a tabela de conversas, não os fatos. Medido em §6 (s1: 0,1 ms).

**Tempo atual das APIs do agent-studio** (serviço vivo, 2 chamadas com 7 s entre elas; pedido `20261024-181500`, passo 2):

| API | 1ª | 2ª |
|---|---|---|
| `/v1/usage?hours=24` | 1.052 ms | 677 ms |
| `/v1/usage?hours=168` | 686 ms | 676 ms |
| `/v1/usage?hours=720` | 6.567 ms | 4.723 ms |
| `/v1/usage?hours=8784` | 4.389 ms | 4.306 ms |
| `/v1/tray` | 586 ms | 574 ms |
| `/v1/alerts` | 326 ms | 304 ms |

O SQL dessas leituras leva até 26 ms no banco real (tabela acima). O resto do tempo das APIs está no processamento em
Python (rateio, cortes, preços), não no banco. Trocar de banco não resolve esse tempo; reescrever no Hanko, sim.

## 4. Ingestão com dedupe

Lotes de 1000 spans, 10% repetidos do lote anterior (o reenvio do #570). DuckDB com uma instrução por lote, como o
agent-studio (`store.py:139-148@845764f`); Postgres com `COPY` para tabela temporária e `INSERT … ON CONFLICT DO
NOTHING` ([`ingest.py`](2026-10-banco-telemetria/ingest.py)).

| Banco | Volume | Linhas/s (mediana de 20 lotes) | Lote mais lento |
|---|---|---|---|
| DuckDB | real | 1.715 | 638 ms |
| DuckDB | 12 meses | 1.767 | 612 ms |
| Postgres particionado | real | 18.379 | 142 ms |
| Postgres particionado | 12 meses | 33.915 | 44 ms |

O pico de 2026-10-05 teve 214.440 spans numa hora (pedido `20261024-160053`), ~60 por segundo em média; o #570 registra
rajadas de ~2.900 spans/s em reprocessamento. O agent-studio tem um escritor só (`config/otel/agent-studio.yaml:10@845764f`); o Postgres aceita escritas em
paralelo.

## 5. Backup e restore

| Banco | Volume | Backup | Restore |
|---|---|---|---|
| Postgres (`pg_dump -Fc -Z zstd:3`) | real | 12 s, 908.266.876 bytes | 30 s (`pg_restore -j 4`) |
| Postgres (`pg_dump -Fd -j 4 -Z zstd:3`) | 12 meses | 3 min 54 s, 20.800.542.522 bytes | 8 min 23 s (`pg_restore -j 4`) |
| DuckDB (`COPY FROM DATABASE`, o do agent-studio, `store.py:345`) | 12 meses | **falhou**: sem memória com 1 GB, com 2 GB (2 threads, após 189 s) e com 3 GB (1 thread, após 333 s) | — |
| DuckDB (`EXPORT DATABASE`, Parquet zstd) | 12 meses | 150 s, 19.030.046.516 bytes | **falhou**: `IMPORT DATABASE` sem memória com 3 GB, após 195 s |

Não verificado: com quanta memória a cópia e o restore do DuckDB de 12 meses passam. A hipótese do autor é o índice da
chave primária (`dedupe_key`), que o DuckDB monta em memória; não foi confirmada.

O backup do Hanko é `pg_dump` + restic para o R2, com restore mensal (ADR-0008 do Hanko). A telemetria no Postgres
entra nesse fluxo sem peça nova. Com o DuckDB, entraria um segundo backup.

## 6. Estado: SurrealDB × Postgres

O estado do agent-studio (rodada, sessão, pedido, conversa, etapa…) é derivado e se remonta do DuckDB (inventário da
#67, §3.3). Teste com 36 meses no ritmo medido: 180 conversas, 6 rodadas (3 sessões e 5 etapas cada) e 12 pedidos por
dia ([`state.py`](2026-10-banco-telemetria/state.py)).

| Medida | SurrealDB 3.3.0 (RocksDB) | Postgres 16 |
|---|---|---|
| Conversas | 197.100 | 197.100 |
| Disco | 115.717.800 bytes | 64 MB (com índices) |
| Memória do processo | 394.372 KB de RSS | — (dentro do Postgres do Hanko) |
| s1: 50 conversas mais novas com o nome da rodada | 12,1 ms | 0,1 ms |
| s2: página de uma rodada (sessões, etapas, pedidos) | 12,9 ms | 0,5 ms |
| s3: pedidos pendentes | 10,2 ms | 0,1 ms |

Uma chamada HTTP ao SurrealDB sem consulta nenhuma (`RETURN 1`) leva 12,3 ms: quase todo o tempo do SurrealDB é a ida e
volta. No host de hoje, o SurrealDB usa 331,7 MiB de 2 GiB (pedido `20261024-181500`, passo 1).

## 7. Opções

| | 1. Postgres sozinho | 2. Postgres (estado) + DuckDB (telemetria) | 3. 2 + SurrealDB (estado) |
|---|---|---|---|
| Consultas curtas | ok (§3) | ok | ok |
| Janela longa | só com tabelas derivadas (§3) | ok no bruto | ok no bruto |
| Ingestão | 18–34 mil linhas/s | ~1.700 linhas/s, um escritor | igual à 2 |
| Disco, 12 meses de bruto | 125,9 GB: **não cabe** nos 74 GB livres | 51,9 GB: cabe | 51,9 GB + estado |
| Backup/restore | `pg_dump` + restic de hoje; restore de 12 meses em 8 min 23 s | segundo backup; cópia e restore de 12 meses falharam com 3 GB | terceiro backup |
| RAM a mais | nenhuma | 3 GB do DuckDB (padrão do agent-studio) | + ~0,4 GB do SurrealDB |
| Operação | um banco, o que o Hanko já opera | dois bancos; um processo dono do arquivo DuckDB | três bancos |
| Build do Hanko | sem mudança | o Hanko compila com `CGO_ENABLED=0` (`Makefile:17`); o cliente Go do DuckDB precisa de cgo ([README do duckdb-go](https://github.com/duckdb/duckdb-go), seção do erro `undefined: conn`) | igual à 2 |
| Licença | PostgreSQL ([COPYRIGHT](https://github.com/postgres/postgres/blob/master/COPYRIGHT)) | + MIT ([LICENSE](https://github.com/duckdb/duckdb/blob/main/LICENSE)) | + BSL 1.1, sem uso como "Database Service" para terceiros, Apache 2.0 em 2030-01-01 ([LICENSE@v3.3.0](https://github.com/surrealdb/surrealdb/blob/v3.3.0/LICENSE)); não verificado juridicamente para o Hanko (Apache 2.0) |
| ADR | ADR-0011 vale como está | ADR novo que altera o ADR-0011 | ADR novo que reverte a rejeição do SurrealDB no ADR-0011 |

### Retenção

| | A. Tudo quente, sem prazo | B. Janela quente + derivado sem prazo + replay do bucket |
|---|---|---|
| O que fica no banco | todo o bruto | bruto dos últimos N dias (partições mensais; sair = `DROP` da partição); tabelas derivadas (chamadas, agregados diários, conversas e estado) sem prazo |
| Detalhe antigo | no banco | no bucket, que recebe tudo (`config/otel/agent-studio.yaml:14-16@845764f`, ADR-04 do oute-agent); volta pelo replay decidido no gate da #67 (inventário §9.3) |
| Postgres, disco | ~126 GB por ano (§2) | 90 dias ≈ 31 GB de bruto (126 GB × 90 ÷ 365, extrapolado) + derivado de 12 meses ≈ 0,43 GB (338 MB + 27 MB + estado de 64 MB em 36 meses) |
| Dado que só existe no DuckDB hoje | migra | migra igual: marcas, planos, preços e fases vão para tabelas próprias, sem prazo (inventário, Resumo) |

## 8. Recomendação do autor

**Opção 1 com retenção B:** Postgres sozinho, bruto particionado por mês numa janela quente, tabelas derivadas sem
prazo e replay do bucket para o detalhe antigo.

- Cabe no disco de hoje com folga: ~31 GB de bruto em 90 dias contra 74 GB livres. O bruto de 12 meses no Postgres não
  cabe (§2).
- As telas leem as tabelas derivadas e ficam na faixa do DuckDB (§3). O bruto serve ao detalhe da conversa e do span,
  que é rápido nas duas engines (q6: 1 a 2 ms no Postgres).
- A ingestão é 10 a 20 vezes mais rápida, com escritas em paralelo (§4), contra o padrão do #570.
- O backup e o restore entram no fluxo que o Hanko já testa todo mês (§5). No DuckDB, os dois falharam com 12 meses e
  3 GB.
- Não cria segundo banco, não muda o build sem cgo e não traz licença nova. O SurrealDB não mostrou ganho medido para o
  estado (§6).
- O replay do bucket já foi decidido no gate da #67 e é o que torna a janela quente segura: nada se perde, o detalhe
  antigo volta quando preciso.

**O que se perde com a recomendação:** consulta livre sobre o bruto de um ano inteiro fica lenta no Postgres (87 s em
q4). Quem precisar disso usa as tabelas derivadas ou o bucket. Uma análise pontual sobre o bucket com DuckDB fora do
Hanko continua possível, mas não foi medida aqui.

**Pontos para o gate do Bardi:**

1. Banco: opção 1, 2 ou 3.
2. Retenção: A ou B; com B, o tamanho da janela quente (proposta do autor: 90 dias).
3. Os spans internos do Codex anteriores ao filtro (1,13 milhão em 10-05 e o resto até 10-11): migram ou ficam só no
   bucket. Segundo o comentário do filtro, eles "ficam só no bucket (tudo, ADR-04)" e o filtro vale só para o
   destino agent-studio (`config/otel/agent-studio.yaml:14-16,81@845764f`); a integridade no bucket não foi conferida aqui. O gate da #67 (bloco 1) disse "na dúvida, migram".
4. A base da projeção: manter a decidida (236.121 linhas por dia) ou usar a de depois do filtro (119.507, §2).

## 9. Reproduzir

Scripts em [`2026-10-banco-telemetria/`](2026-10-banco-telemetria/). O pedido de leitura no host está em
[`pedido-canal-20261024-181500.sh`](2026-10-banco-telemetria/pedido-canal-20261024-181500.sh) (o texto aprovado pelo
Bardi em 2026-10-24T18:29:55Z, sha256 `12584507dc53…`).

```bash
uv venv .venv && uv pip install --python .venv/bin/python -r <oute-agent>/docker/agent-studio/requirements.txt 'psycopg[binary]==3.2.3'
.venv/bin/python gen.py real16.duckdb 16             # volume do host (grava em data/)
.venv/bin/python gen.py m12.duckdb 365                # 12 meses (~20 min, 52 GB)
.venv/bin/python bench.py duckdb data/m12.duckdb 2GB
.venv/bin/python pgload.py data/m12.duckdb m12part part   # Postgres em 127.0.0.1:56432 (PGDSN)
.venv/bin/python bench.py pg m12part
.venv/bin/python calls.py
.venv/bin/python ingest.py pg m12part
.venv/bin/python state.py 58000                      # surreal start --user … --pass … rocksdb://…
```

`gen.py`, `pgload.py` e `ingest.py` importam `agent_studio.store` de um checkout do oute-agent em
`/workspace/oute-agent` (o schema é o de `845764f`).

## 10. Decisões do gate (Bardi, 2026-10-24)

Respostas do Bardi na sessão `hanko-decom-agent-studio`, uma pergunta por vez.

| Ponto | Decisão |
|---|---|
| 1. Banco | **Só Postgres.** O ADR-0011 vale como está. As telas leem tabelas derivadas: chamadas ao modelo, agregados diários, conversas e o resto do estado (§3, §6). |
| 2. Retenção | **Tudo quente, sem prazo.** Nada sai do banco, nas palavras do Bardi: "não quero perder nada do que tenho hj, não quero corte de prazo". Sem plano de disco agora: um alerta de disco avisa, e a decisão se toma perto do limite. A janela quente da §8 não vale. |
| 3. Spans internos do Codex anteriores ao filtro | **Migram inteiros.** O Bardi escolheu não migrar o detalhe e guardar só um resumo diário, com a condição de uma prova no dado. A prova falhou nos tokens (abaixo), e pela regra combinada vale "migram". |
| 4. Base da projeção | **236.121 linhas por dia**, a decidida no gate da #67: ~126 GB por ano no Postgres (§2) e ~214 dias até os 74 GB livres de hoje. |

**Prova do ponto 3** (pedido `20261024-223745` do canal, só leitura, rc 0, aprovado em 2026-10-24T22:41:44Z):

| Conferência | Resultado |
|---|---|
| Spans com os 8 nomes do `filter/codex_noise`, agente `codex` | 1.156.027, de 2026-10-03 a 2026-10-06 |
| Com modelo · com custo · com conversa | 0 · 0 · 0 |
| **Com tokens** | **1.285, somando 157.142.069 tokens** |
| No escopo de chamada ao modelo (`cost.py:39-47@845764f`) | 0 |
| Uso da janela inteira com e sem eles: chamadas, tokens, custo | igual: 50.908, 64.723.138, US$ 0,974852 |
| Conversas cuja contagem de spans muda | 0 |

O agent-studio nunca contou esses tokens. Saber se são uso do Codex não contado ou contagem acumulada é a #81.

**Correção do perfil:** em `host_runner.py`, a constante `1760140800000000000` é 2025-10-11, não 2026-10-11. Os itens
"conversas e traces distintos por dia" e "30 nomes de span" do pedido `20261024-181500` cobrem a janela inteira, não
só o período depois do filtro. O gerador usa a média de 180 conversas por dia, que é a média da janela inteira; os
demais números não mudam. O rótulo foi corrigido no script; o texto aprovado do pedido fica como foi.

**Consequências para a v0.8:**
- Um alerta de disco do Postgres entra no Hanko desde o primeiro dia da ingestão (ponto 2). Os limites seguem a regra
  de limites por empresa do bloco 4 do gate da #67 e ficam para o contrato da v0.8.
- O replay do bucket (gate da #67, §9.3) continua como rede de segurança, não como fonte do detalhe antigo.
- Não há ADR novo para o banco. As tabelas derivadas e as partições entram no contrato da v0.8 (migrations).

# Inventário: agent-studio e Tray → destino no Hanko (#67)

Fase `strat` (AI-DLC) · 2026-10-24 · autor: agente (Claude), a pedido do Bardi · parte de #66 · **o destino de cada linha
é decisão do Bardi** (gate desta fase).

**O que o Bardi decide aqui:** o destino de cada linha das tabelas abaixo. A coluna "Destino" traz só a recomendação do
autor; ela não é decisão.

## Como ler

- Fontes do oute-agent no commit `845764f`. Abreviações:
  - `AS/x.py:N` = `docker/agent-studio/agent_studio/x.py:N`;
  - `ADR-08 §N` ou `ADR-08:N` = `docs/adr/0008-agent-studio.md`;
  - `CFG:N` = `config/agent-studio/config.toml:N`;
  - `oute:N` = `scripts/oute:N`.
- Fontes do Hanko no commit `ac360d4` (`main`).
- **Hanko hoje:** `tem`, `parcial` ou `não tem`, com a fonte.
- **Dado:** a tabela de origem que precisa migrar. `—` quando não há dado.
- Termos novos (aprovação externa, rodada externa, host da fonte, classificação de conversa, `ingest`) seguem as
  decisões 1, 5 e 6 de #66. Os nomes finais ficam para os ADRs.

## Resumo

- Rotas HTTP do agent-studio (§1), contadas nas tabelas abaixo:
  - 4 de ingestão e cópia (`/v1/logs`, `/v1/traces`, `/v1/metrics`, `/v1/backup`);
  - 6 de API de leitura (`usage`, `alerts`, `prices`, `plans`, `tray`, `rodada`);
  - 18 rotas de tela e 2 rotas de bloco;
  - 5 escritas do Bardi (`POST /marcar`, `/rodada/acao`, `/ack`, `/planos/novo`, `/fase`);
  - mais login, logout, `/healthz`, `/readyz` e `/static`.
- Tem **16 telas** de conteúdo, mais login e marcação (§2).
- Tem **3 tabelas de telemetria** e **9 tabelas próprias** no DuckDB, além de **10 tipos de registro** no SurrealDB (§3).
- **Dado que só existe no DuckDB** (o replay do bucket não repõe): `ack_marks`, `action_marks`, `plan_history`,
  `phase_marks`, `phase_jev`, `price_history` (origem `fontes`), `price_runs`, `price_checks` e a hora de chegada
  (`received_unix_nano`). A cópia de segurança (`COPY FROM DATABASE`, `AS/store.py:345`) leva todas ao bucket, em
  `backups/agent-studio/`, com 7 cópias (`oute:555`). A migração precisa ler essas tabelas do DuckDB, não do bucket.
- Tem **17 tipos de alerta** e nenhum deles existe no Hanko (§4.8).
- O Tray tem **12 itens de menu**, **3 tipos de notificação** e lê **um endpoint**, `GET /v1/tray` (§5).
- Há **dependentes escondidos** no oute-agent que quebram sem aviso (§6.1). O mais grave: `docker/oute-swarm:204` usa
  `names.py` do agent-studio para o nome da rodada, e o teto de custo do modo dark (`config/swarm.toml:4-6`) lê
  `GET /v1/usage?rodada=`.

## Medição no host (2026-10-24)

Fonte: pedidos do canal de aprovação `20261024-111422-s-leitura-hanko-67-mede-o-agent-studio-n` e
`20261024-111550-hanko-67-contagens-do-agent-studio-numa-`, aprovados pelo Bardi e rodados no oute-server por
`ssh oute-server` a partir do Mac. As contagens vêm de uma cópia consistente feita pelo próprio serviço
(`python -m agent_studio.backup`), aberta em `read_only` e apagada no fim. O banco vivo não foi aberto. A saída trouxe
só nomes e contagens.

### Tamanho

| Item | Valor |
|---|---|
| `agent-studio.duckdb` (arquivo vivo) | 3.024.629.760 bytes, mais 95.765.958 bytes de WAL |
| Cópia consistente | 2.479.108.096 bytes, em 50,864 s |
| SurrealDB (`/mnt/lxd/oute-data/surrealdb`) | 109 MB (`du -sh`) |
| Fila do collector (`oute-agent_oute-otel-queue`) | 131,2 MB (`docker system df -v`) |
| Disco de dados (`/data/agent-studio`, `/dev/sdb`) | 196 GB, 75 GB livres |
| Disco raiz do oute-server | 45 GB, 17 GB livres |

A nota de 2026-09 ([oute-agent#139](https://github.com/renatobardi/oute-agent/issues/139#issuecomment-5881197413))
media ~14 MiB gzip em 4 dias, no bucket. Os dois números não se comparam direto (gzip no bucket × banco com índices),
mas a ordem de grandeza mudou. Isso entra na pesquisa de banco (#68).

### Linhas por tabela

| Tabela | Linhas |
|---|---|
| `logs` | 450.388 |
| `spans` | 1.446.922 |
| `metrics` | 1.916.867 |
| `conversation_phase` | 3.173 |
| `phase_jev` | 1.792 |
| `price_checks` | 1.306 |
| `price_runs` | 210 |
| `ack_marks` | 57 |
| `price_history` | 21 |
| `plan_history` | 3 |
| `action_marks` | 0 |
| `phase_marks` | 0 |

Nos `logs`, o corpo soma 21.261.745 bytes; o JSON `attributes`, 536.792.388; o `resource_attributes`, 187.760.563.

### Janela e volume

- Os três sinais começam em 2026-09-30 (UTC) e vão até a hora da cópia (2026-10-24T11:16Z).
- 2 hosts: `oute-server` (408.065 logs) e `oute-mac` (42.323 logs), os dois com instância `oute-agent`.
- 3.171 conversas distintas nos `logs`; 3.167 nos `spans`.
- `service.name` nos `logs`: `claude-code` 417.738; `agent-studio` 17.084; `codex_cli_rs` 7.884; `oute` 7.171;
  `codex-app-server` 258; `codex_exec` 253.
- Pico de spans em 2026-10-05: 1.148.602 num dia (79% de todos os spans). Causa não verificada.

### Queda de logs e spans desde 2026-10-14: pausa de uso

| Dia (UTC) | logs | spans | metrics |
|---|---|---|---|
| 2026-10-11 | 20.372 | 8.949 | 46.144 |
| 2026-10-12 | 41.006 | 17.574 | 60.927 |
| 2026-10-13 | 26.454 | 9.279 | 60.002 |
| 2026-10-14 | 1.680 | 123 | 32.669 |
| 2026-10-15 a 2026-10-23 | entre 1.345 e 1.466 por dia | 19 em 10-18, 7 em 10-19, 0 nos outros dias | entre 25.632 e 47.520 por dia |
| 2026-10-24 | 11.023 | 4.966 | 22.665 |

As métricas seguiram chegando; logs e spans quase pararam entre 2026-10-14 e 2026-10-23. Explicação do Bardi
(2026-10-24, nesta sessão): ele parou de usar o oute nesse período. Não foi conferido no bucket.

### Projeção para a pesquisa de banco (#68)

Regra do Bardi (2026-10-24): **usar o dia de pico como média diária**, porque o período medido inclui a pausa.

- Bytes por linha: 2.479.108.096 bytes ÷ 3.814.177 linhas = 650 bytes, média das três tabelas na cópia.
- Dia de pico por sinal: logs 41.006 (2026-10-12, maior dia dos 14 últimos); spans 1.148.602 (2026-10-05); metrics
  60.927 (2026-10-12, maior dia dos 14 últimos). Soma: 1.250.535 linhas por dia.
- O maior dia de logs e de metrics antes de 2026-10-11 não foi medido (a saída listou só os 14 últimos dias).

| Horizonte | Volume no formato do DuckDB |
|---|---|
| 1 dia | 812,8 MB |
| 12 meses | 296,7 GB |
| 36 meses | 890,0 GB |
| Até encher os 75 GB livres do disco de dados | 92,3 dias |

Comando: `python3 -c` com os números acima (multiplicação simples; não é medição no Postgres). O pico de spans de
2026-10-05 pesa 92% da soma de pico; causa não verificada. O volume no Postgres não foi medido e é a primeira tarefa da
#68.

### Repositórios (`oute_repo`)

| Repositório | logs | spans | metrics |
|---|---|---|---|
| `oute-agent` | 350.979 | 877.843 | 1.110.790 |
| `frentes-engenharia` | 49.712 | 219.257 | 14.684 |
| `lab` | 4.360 | 337.254 | 803 |
| `hanko` | 10.221 | 4.963 | 2.555 |
| `financial-control` | 7.031 | 3.499 | 2.401 |
| `regression` | 197 | — | — |
| `proj` | 2 | — | — |
| `(sem)` | 27.886 | 4.106 | 785.634 |

- Oito valores, contando "sem repositório". Pelo nome não há repetição.
- O histórico guarda só o nome, sem dono, então **não dá para provar** que dois donos não usam o mesmo nome. Todos os
  repositórios conhecidos são de `renatobardi`; isso é **não verificado** para `regression` e `proj`.
- `regression` e `proj` parecem pastas, não repositórios. Decisão 14 de #66: entram como estão e o Bardi decide se viram
  workspace ou "sem repositório".

---

## 1. Rotas HTTP

### 1.1 Ingestão e cópia (credencial de ingestão)

| Capacidade | Hanko hoje | Destino (recomendação do autor) | Observação |
|---|---|---|---|
| `POST /v1/logs`, `/v1/traces`, `/v1/metrics`: OTLP/HTTP JSON, gzip/deflate, 413 acima de 64 MiB, 4 vagas e 20 s de espera, 503 com `Retry-After: 5`, 200 só depois do commit, cabeçalhos `X-Agent-Studio-Written`/`-Duplicate` (`AS/app.py:52,79-81,136-215`) | não tem (`internal/` só tem o exporter da própria telemetria, `internal/telemetry/telemetry.go`) | ingestão OTLP do Hanko, só na tailnet, escopo `ingest` (decisão 5) | manter a garantia "2xx só depois do commit" e a dedupe (§3.1) |
| `POST /v1/backup`: cópia consistente do DuckDB (`AS/app.py:217-236`) | parcial: `hanko backup` e `instance_backups` (ADR-0062; `db/migrations/00180_backups_e_fluxos_de_sistema.sql:8`) | backup do Hanko | depende de #68 (banco) |

### 1.2 API de leitura (credencial de leitura ou cookie)

| Capacidade | Hanko hoje | Destino (recomendação do autor) | Consumidor |
|---|---|---|---|
| `GET /v1/usage`: `hours` (até 8784) ou `from`/`to`, `tz`, `rodada`; devolve `totals`, `rows`, `series`, `by_role`, `by_phase`, `by_subscription`, `by_role_model`, `by_role_phase`, `top_sessions`, `top_conversations`, `prices` (`AS/app.py:277-313`; `AS/usage.py:426-469`) | parcial: `CostSummary` por um agrupamento, com quatro custos e tokens (`internal/service/costs.go:69-93`); sem p95, papel, fase, erros ou rodada | API de uso do Hanko, estendida | ops-observe, `oute-swarm --budget` |
| `GET /v1/alerts?at=` (`AS/app.py:316-334`) | parcial: `alerts`, com 4 tipos só de persona, run e orçamento (`db/migrations/00070_ambientes_readiness_modelos.sql`) | alertas do Hanko com tipos novos (§4.8) | ops-observe, ship-release |
| `GET /v1/prices` (`AS/app.py:337-351`) | parcial: `model_prices` só de acréscimo, sem API de escrita (ADR-0019) | preços do Hanko com conferência diária (decisão 8) | não verificado |
| `GET /v1/plans?day=&subscription=` (`AS/app.py:354-375`) | parcial: `subscriptions` e `subscription_periods`, sem histórico de valor (ADR-0019, "Consequências") | assinaturas com histórico de valor (decisão 8) | não verificado |
| `GET /v1/tray?tz=` (`AS/app.py:404-424`; `AS/tray.py:124-134`) | não tem | API do Hanko Desktop (decisão 4) | Tray, plugin herdr `oute.rodada` |
| `GET /v1/rodada?id=`: etapas, ações, `state_read` (`AS/app.py:427-451`) | não tem | API da rodada externa (decisão 6) | dispatcher (`docker/swarm.md:29`) |

### 1.3 Escrita do Bardi (credencial de marcação)

Só existem com `AGENT_STUDIO_MARK_TOKEN` de 32 caracteres ou mais (`AS/__main__.py:16,46-55`). Todas pedem o cookie de
marcação, `Origin` igual ao `Host`, `csrf` e corpo até 4096 bytes (`AS/marcar.py:56,67-80`).

| Capacidade | Hanko hoje | Destino (recomendação do autor) | Dado |
|---|---|---|---|
| `GET/POST /marcar`: entra no modo de marcação (`AS/marcar.py:148-172`) | tem: sessão de usuário com papel (ADR-0021) e reautenticação recente (ADR-0070) | sessão do Hanko; owner/admin | — |
| `POST /rodada/acao`: marca ação de etapa como feita/pendente (`AS/marcar.py:174-228`) | não tem | ação de etapa da rodada externa | `action_marks` |
| `POST /ack`: "visto" por 24 h (`AS/marcar.py:278-337`; ADR-08, adendo #537) | parcial: `inbox_state` por pessoa, com `fingerprint` (ADR-0042 §A) | Caixa por pessoa (ADR-0042), com prazo de 24 h | `ack_marks` |
| `POST /planos/novo`: valor de plano com início (`AS/marcar.py:231-273`) | parcial: assinaturas sem histórico de valor | assinaturas com histórico (decisão 8) | `plan_history` |
| `POST /fase`: troca manual da fase da conversa (`AS/marcar.py:339-385`) | não tem | classificação de conversa (decisão 6) | `phase_marks` |

### 1.4 Outras rotas

| Capacidade | Hanko hoje | Destino (recomendação do autor) |
|---|---|---|
| `GET /healthz` (`AS/app.py:238-240`) | tem: `hanko doctor` e health (ADR-0062) | health do Hanko |
| `GET /readyz`: espera da trava, idade da última gravação (até 900 s), `reason` (`AS/app.py:242-274`) | parcial: readiness de persona (ADR-0055), não de ingestão | prontidão da ingestão |
| `GET/POST /login`, `POST /logout`: cookie HMAC da credencial, 90 dias (`AS/web.py:644-671`; `AS/auth.py:22-26,47`) | tem: login Firebase/local (ADR-0007, ADR-0014) | login do Hanko; a credencial de leitura vira API key `read` |
| Cabeçalhos de segurança (CSP, `nosniff`, `no-store`) (`AS/web.py:58-64`) | tem: CSP do Hanko (ADR-0018) | padrão do Hanko |

---

## 2. Telas

Filtros comuns (`TPL/_macros.html:101-159`; `AS/web.py:55,151-153,218-234`; `AS/tabela.py:1-31`):

- janelas prontas: 24 h, 7 dias, 30 dias, 366 dias;
- `de`/`ate` no fuso, ou `from`/`to`/`hours`, até 8784 h;
- `custo=pago|lista`, `repo` (com `(sem)`), `assinatura`, `host`, `agent`;
- tabela com `ord`, `dir`, `pag`, `tam` (20, 50, 100) e `f_<coluna>`.

Carregamento por bloco: o casco não consulta banco; cada bloco carrega quando aparece; 120 s por abertura e no máximo 32
(`AS/loading.py:19-42`). Alertas e decisões ficam no topo de toda tela que não é de detalhe (`AS/web.py:500-502`).

No Hanko: telas por empresa em `/c/{company}/…` (templ + htmx, ADR-0005), com ilhas React para log virtualizado e
organograma (ADR-0026). O Hanko **não tem** filtro por repositório, host ou agente externo, nem janela de 366 dias.

| Tela (rota e fonte) | Conteúdo | Hanko hoje | Destino (recomendação do autor) |
|---|---|---|---|
| Dashboard `/` (`AS/web.py:533-577`; `AS/dashboard.py:435-474`) | KPIs, insights, chamadas, custo por modelo, latência, fases, atividade (heatmap de 7 dias), sessões, repos, assinaturas, ferramentas; comparação com a janela anterior | parcial: painel `/c/{company}/` e `/ao-vivo`, sem telemetria externa | painel da empresa com blocos de telemetria |
| Conversas `/conversas` (`AS/web.py:674-694`) | início, host, agente, duração, chamadas, tokens, custo, p95, erros | não tem (só runs do Hanko em `/execucoes`) | lista de conversas externas |
| Conversa `/conversa` (`AS/web.py:696-710`) | árvore de spans (até 5000), logs de 200 em 200, subagentes com % do custo | parcial: log de run com SSE (`run_events`, `db/migrations/00001_init.sql:130`) | detalhe da conversa externa; conteúdo só para owner/admin (decisão 10) |
| Logs e span `/conversa/logs`, `/conversa/span` (`AS/web.py:712-744`) | atributos, eventos, links, resource | não tem | idem |
| Sessões `/sessoes` (`AS/web.py:761-782`) | sessões com estado e rodada; conversas sem sessão | não tem | lista de sessões externas |
| Sessão `/sessao` (`AS/web.py:784-801`) | fase, reserva (#598/#621), abertura (`aba`/`terminal`/`sem-tela`, #825), eventos (até 200) | não tem | detalhe da sessão externa |
| Uso `/uso` (`AS/web.py:804-845`) | custo e tokens por dia; custo por papel, fase e assinatura; papel×modelo; papel×fase; sessões e conversas mais caras; "dias sem uso" | parcial: `/custos` com quatro custos e "sem uso" (`idle_usd`) (ADR-0019) | `/custos` estendido |
| Ferramentas `/ferramentas` e `/ferramenta` (`AS/web.py:603-641`) | usos por ferramenta; conversas que usaram (até 200); grupos do Bash (`AS/tools.py:31`) | parcial: `tool_invocations` só do gateway do Hanko (`db/migrations/00032_tools_gateway.sql:164`) | ferramentas, incluindo as de fonte externa |
| Preços `/precos` (`AS/web.py:1019-1027`; `AS/prices.py:345-404`) | fontes, vigente, histórico, tendência de 30 dias, gráfico | não tem tela de preços | tela de preços (decisão 8) |
| Planos `/planos` (`AS/web.py:1006-1016`) | planos e histórico; formulário com marcação | parcial: tela de assinaturas sem histórico de valor | assinaturas com histórico |
| Pedidos `/pedidos` e Pedido `/pedido` (`AS/web.py:864-897`; `TPL/proposal.html:79-87`) | pendentes e decididos; script, sha256, versões, eventos, comando `oute approve <id>` para copiar | não tem | aprovação externa (decisão 6); "aprovar" só abre o Terminal (decisão 4) |
| Rodadas `/rodadas` e Rodada `/rodada` (`AS/web.py:909-965`; `AS/etapas.py:49-145`) | rodadas com repo, estado, sessões, PRs, etapas, ciclo; barra de etapas, caixas de ação, subagentes do dispatcher (até 100) | não tem | rodada externa (decisão 6) |
| Ciclo `/ciclo?id=<dono>/<repo>#<n>` (`AS/web.py:967-987`) | resumo do ciclo e suas rodadas | não tem | rodada externa (agrupamento por issue) |
| Fases `/fases` (`AS/web.py:990-999`) | conversas com baixa confiança (até 200), formulário de troca | não tem | classificação de conversa |
| A/B do Jev por nome do trace (ADR-08:82) | não achado no código (relatório da leitura); não verificado se saiu com o jev-router (#218 do oute-agent) | — | **o Bardi decide**: confirmar se a tela existe antes de dar destino |

---

## 3. Dados

### 3.1 DuckDB: telemetria

Todas as tabelas guardam a hora do fato em ns e as colunas fixas `host_name`, `oute_instance`, `oute_agent`,
`service_name`, `session_id`, `oute_task_id`, `oute_swarm_round`, `oute_repo`, `oute_subscription`
(`AS/store.py:26-65`). O resto vai em JSON (`resource_attributes`, `attributes`).

| Tabela | Chave de dedupe | No bucket? | Destino (recomendação do autor) |
|---|---|---|---|
| `logs` | `ev:<oute.event.id>` ou `h:<sha256>` (`AS/otlp.py:132-134`) | sim | depende de #68 (banco); empresa Oute, workspace pela decisão 14 |
| `spans` | `s:<trace>:<span>` (`AS/otlp.py:216`) | sim, exceto o ruído do Codex, que fica só no bucket (ADR-08:221) | idem |
| `metrics` | `h:<sha256>` (`AS/otlp.py:278`) | sim | idem |

Acertos de dado feitos na subida, que a migração precisa preservar como estão:

- `repo_infer.apply`: repositório inferido pelo `file_path` (`AS/repo_infer.py:112`);
- `repo.apply_legacy`: #617 (antes de 2026-10-06T00:00:00Z sem repo → `oute-agent`) e #803 (`regression-*` →
  `oute-agent`) (`AS/repo.py:37-87`). Decisão 14 de #66: entram como atribuição inferida.

### 3.2 DuckDB: tabelas próprias (fora do bucket)

| Tabela | Chave | Natureza | Hanko hoje | Destino (recomendação do autor) |
|---|---|---|---|---|
| `price_history` | (model, start_unix_nano); origem `config` ou `fontes` (`AS/prices.py:48-59`) | só acréscimo | parcial: `model_prices` só acréscimo (ADR-0019 item 1) | `model_prices`, com a origem preservada |
| `price_runs` | (checked_unix_nano, source) | só acréscimo | não tem | conferência de preço (decisão 8) |
| `price_checks` | (checked_unix_nano, model), detalhe JSON | só acréscimo | não tem | idem |
| `action_marks` | (rodada, kind, key, action_id, marked_unix_nano) (`AS/marks.py:14-17`) | só acréscimo; marca do Bardi | não tem | ação de etapa da rodada externa |
| `ack_marks` | (target, acked_unix_nano); 24 h (`AS/acks.py:28,46-49`) | só acréscimo; marca do Bardi | parcial: `inbox_state` (ADR-0042) | Caixa por pessoa; **o Bardi decide** se o prazo de 24 h entra no ADR-0042 |
| `plan_history` | (subscription, start_date, registered_unix_nano) (`AS/planos.py:37-40`) | só acréscimo | não tem histórico de valor | assinaturas com histórico (decisão 8) |
| `conversation_phase` | conversation (`AS/phase.py:41-50,276`) | derivada, `INSERT OR REPLACE` | não tem | classificação de conversa |
| `phase_jev` | conversation (`AS/phase.py:290-291`) | `INSERT OR REPLACE` (o ADR-08:376 diz "acréscimo": diverge do código) | não tem | idem; a resposta do Jev não se repete igual |
| `phase_marks` | (conversation, marked_unix_nano) | só acréscimo; marca do Bardi | não tem | idem |

### 3.3 SurrealDB (namespace `oute`, banco `studio`)

Tudo é derivado e se remonta com `rebuild_state` a partir do DuckDB (`AS/rebuild_state.py:78-178`), com
`UPSERT … MERGE` (`AS/state.py:341-358`).

| Registro | Origem | Destino (recomendação do autor) |
|---|---|---|
| `rodada:<round>` (abertura, fechamento, `cycle`) | logs | rodada externa |
| `worker:[rodada, slug]` | logs | participante da rodada externa |
| `sessao:<oute.task.id>` | logs | sessão externa |
| `pedido:<oute.canal.id>` (pendente/decidido) | logs | aprovação externa |
| `conversa:<session.id>` (agente, host, instância, serviço, assinatura, fase) | logs + fase | conversa externa |
| `etapa:[rodada, tipo, chave]` (revisão vigente) | logs | etapa da rodada externa |
| `canal_snapshot:<host>` (última lista da outbox) | logs (`AS/state.py:57-67,240-256`) | aprovação externa: pedido sem arquivo sai da lista (adendo #815) |
| `acao:[…]` | `action_marks` | ação de etapa |
| `ack:[alvo]` | `ack_marks` | Caixa por pessoa |
| `plano:[sub, start, ns]` | `plan_history` | assinaturas |
| `conversa.phase*` | tabelas de fase | classificação de conversa |

Como o SurrealDB é derivado, a migração lê o DuckDB e remonta o estado no Hanko. O `surreal export` entra só na cópia
final, como garantia (decisão 13).

---

## 4. Cálculos

| Cálculo (fonte) | Regra | Hanko hoje | Destino (recomendação do autor) |
|---|---|---|---|
| 4.1 Chamada ao modelo (`AS/cost.py:39-50`) | spans `claude_code.llm_request`, `session_task.turn`, `ai_memory.llm_request`, `jev.decision`; agente `router` fora | não tem para fonte externa | custo de fonte externa |
| 4.2 Custo real (`AS/cost.py:87,229-251`) | `cost_usd` do span ou do log `api_request` de mesmo `request_id`, folga de 1 h; descartado na `zai` | parcial: real = `cost_usd` do harness (ADR-0019 item 2) | modelo de custo do Hanko (decisão 8) |
| 4.3 Classe da chamada (`AS/cost.py:216-226`) | real, `listed`, `estimated`, `unpriced` | parcial: real, lista, estimado (ADR-0019) | acrescentar `unpriced` |
| 4.4 Erros (`AS/cost.py:90-92`) | span com status 2; log com severidade ≥ 17 | não tem para fonte externa | idem |
| 4.5 Preço vigente (`AS/cost.py:128-197`) | preço da hora do fato; antes da primeira linha vale a primeira | parcial: preço vigente no fim do run (ADR-0019 item 1) | **o Bardi decide** a regra para fonte externa |
| 4.6 Custo pago rateado (`AS/rateio.py:1-16`) | mensalidade ÷ dias do mês; dividido pelo custo de lista; dia sem chamada = "sem uso"; sem plano = `no_plan_calls` | tem, com mensalidade atual (ADR-0019 item 2) | igual, com histórico de valor |
| 4.7 Preços: conferência diária (`AS/prices.py:251-313`; `AS/price_sources.py:27-42`) | na subida e a cada 24 h; modelos do seletor e com chamada em 30 dias; troca só com as duas fontes de acordo; `models.dev` e `openrouter.ai`; 20 s, 60 s, 32 MiB, teto US$ 100.000 por 1M | não tem: preço entra por migration ou seed (ADR-0019, "Consequências") | conferência no Hanko (decisão 8); é saída à internet |
| 4.8 Alertas (`AS/alerts.py`; `CFG:154-200`) | ver a tabela abaixo | parcial: 4 tipos (`persona_silent`, `run_stuck`, `budget_near_limit`, `persona_not_ready`) | tipos novos em `alerts` |
| 4.9 Fase da conversa (`AS/phase.py:29-69,199-230`) | 12 fases; sinais em ordem: manual, abertura, label, skill, revisor, dispatcher, ação, Jev; ação forte ≥ 4 pontos e 2× a segunda; Jev com confiança ≥ 0,6; padrão `build` | parcial: decision model por empresa (ADR-0056), sem classificação de conversa | classificação de conversa; **o Bardi decide** se o Jev vira o decision model do Hanko |
| 4.10 Jev (`AS/jev.py:28-64,136,167`) | `jev-1.13.0`, 3 s, até 16000 caracteres, 20 conversas por passada, parada há mais de 10 min, nova tentativa em 600 s, filtro de segredo antes do envio | parcial: decision model (ADR-0056) | idem |
| 4.11 Medição por sessão (`AS/medicao.py:14-40`; `AS/usage.py:378-407`) | top 20 sessões e conversas; contexto = `input + cache_read` (média e máximo); `output_usd`, `input_cache_usd` | não tem | uso estendido |
| 4.12 Papéis (`AS/usage.py:57,82-108`) | `subagente`, `reviewer`, `worker`, `dispatcher`, `standalone` | não tem | papel da conversa externa |
| 4.13 Repositório (`AS/repo_infer.py:1-13`; `AS/repo.py:37-53`) | inferência por mais da metade dos votos; acertos #617 e #803 | não tem | emparelhamento (decisão 14) |
| 4.14 Rodada, ciclo, etapas (`AS/state.py:32-54`; `AS/etapas.py:47`; `AS/acoes.py:1-11`) | etapas triagem, merge, kaizen, fechamento, ciclo; vereditos aprovado, reprovado, sem-revisor; revisão 1 a 9999; texto até 32768 bytes; ações = itens `id: ` de `## Ações` | parcial: revisão automática com veredito no gate (ADR-0020 item 3) | rodada externa |
| 4.15 Decisão pendente (`AS/decisions.py:16-40`) | última pergunta ≥ última resposta, sem `closed`, até 2× `lookback_hours`; até 50 | parcial: gates (ADR-0041) | aprovação externa ou item da Caixa |
| 4.16 Atenção (`AS/avisos.py`) | `ci`, `blocked`, `question`, `merged`; até 50 | parcial: Caixa (ADR-0042) | item da Caixa |
| 4.17 Insights do Dashboard (`AS/dashboard.py:28-33,489-558`) | p95 +25% com ≥ 5 chamadas; gate pendente > 10 min; modelo < 10% das chamadas e > 35% do custo; cache; ≥ 2 erros da mesma ferramenta no mesmo host; chamadas sem preço | não tem | painel |
| 4.18 Nome da rodada (`AS/names.py:41,60`) | sorteio `draw`, chamado pelo `oute-swarm` | tem: nome `Adjetivo_Substantivo` de run (`internal/domain/runname.go:10`) | **sai do agent-studio antes do corte** (§6.1) |

### 4.8 Alertas, um a um

Nenhum destes tipos existe no Hanko (`alerts_kind_check`, `db/migrations/00070_ambientes_readiness_modelos.sql`).

| Tipo | Regra (`AS/alerts.py`, `CFG:154-200`) |
|---|---|
| `queue` | fila acima de 0,5 |
| `destination_refusing` | `send_failed` subiu nos últimos 15 min |
| `host_no_data` | só `always_on_hosts` (`oute-server`), sem dado há mais de 30 min |
| `spool` | acima de 41943040 bytes, ou `dropped` subiu nos últimos 60 min |
| `quota` | ≥ 98% com o reset no futuro; exceção na janela de 5 h com menos de 20 min para o reset |
| `round_stalled` | rodada sem evento há mais de 30 min (`sessions`, `triage`, `unclosed`) |
| `round_old` | depois de 24 h de `round_stalled`, por mais 24 h |
| `llm_proxy_down` | heartbeat parado há mais de 5 min |
| `memory_capture` | acima de 20%, em 7 dias |
| `memory_hooks_untrusted` | valor acima de 0 |
| `price_changed` | some depois de 7 dias (`AS/price_alerts.py:25-26`) |
| `price_sources_diverge` | as duas fontes discordam |
| `price_source_down` | falha em 2 dias UTC seguidos |
| `price_model_unpriced` | modelo sem preço |
| `price_fixed_differs` | preço fixo difere da fonte |
| `cost_claude_diff` | diferença acima de 10% em 7 dias (`AS/cost_alerts.py:18,46`) |
| `cost_subscription_unpriced` | modelo de assinatura sem preço |

Regras gerais: host parado não liga os outros alertas (`AS/alerts.py:547-548`); `round_*` e os de preço recolhem para
"antigos" depois de 2 h (`AS/alerts.py:72`; `CFG:186`). Nunca com base na linha `Exporting failed. Dropping data` do
collector (ADR-08 §8).

---

## 5. Tray → Hanko Desktop

Destino de toda a seção: Hanko Desktop (decisão 4 de #66). Fonte: `tray/` em `845764f`.

| Capacidade (fonte) | Detalhe | Hanko hoje | Observação |
|---|---|---|---|
| Barra (`TraySnapshot.swift:168-174`) | `"<pendentes> · <alertas>"`; `"? · ?"` antes da primeira leitura | não tem | — |
| Menu, 12 itens (`TrayMenu.swift:39-146`) | erro de config; "sem leitura há X"; pedidos (ver script, aprovar, recusar); decisões do swarm; avisos das rodadas; etapas; máquinas; custo de hoje; erros na última hora; alertas (3 no menu, resto em submenu); abrir; atualizar; versão; sair | não tem | — |
| Aprovar/Recusar (`ApproveCommand.swift:5-13`; `Terminal.swift:6-20`) | id validado por `^[0-9]{8}-[0-9]{6}-[a-z0-9-]+$`; host da `~/.oute/tray-hosts`; `oute approve <id>` local ou `ssh -t <alias>` | não tem | o Hanko nunca roda nada no host (decisão 4) |
| Notificações (`Notifications.swift`; `TrayModel.swift:64-76`) | `pedido`, `etapa`, `aviso`; o aviso resolvido sai do centro de notificações | parcial: Web Push (`internal/webpush/`; `push_subscriptions`, `db/migrations/00121_notificacoes.sql:15`) | **é o único aviso sem modelo ao Bardi hoje** (`docker/swarm/acompanhamento.md:7`) |
| "Visto" em memória (`SeenProposals.swift`, `SeenSteps.swift`, `SeenAttention.swift`) | avisa só item novo, nunca na primeira leitura | parcial: `inbox_state` (ADR-0042) | — |
| Config (`TrayConfig.swift:18-75`; `TrayHosts.swift`) | `AGENT_STUDIO_READ_TOKEN` lido de `~/.oute/agent.env`; `OUTE_AGENT_STUDIO_URL`; só `https`; polling de 15 s; timeout 10 s; sem cookie, sem cache, sem redirecionamento | parcial: API key `read` (`internal/domain/ciclo_de_vida_test.go:21`) | — |
| Instalação (`oute:1789-1956`) | `swift build`, `.app` em `~/Applications/OuteTray.app`, `codesign -`, LaunchAgent `pro.oute.tray`, `tray-hosts` padrão, sync por commit | não tem | quem já instalou roda `oute tray uninstall` antes de remover o comando |
| Ícone e cores (`SakuraIcon.swift:8-25`; `Sakura.swift:3-38`) | sakura copiada do `lucide.svg` do agent-studio; cores do `studio.css` | não verificado se o Kubo DS do Hanko tem a sakura | **o Bardi decide** o ícone do Hanko Desktop |
| Testes (`tray/Tests/TrayCoreTests/`, 16 arquivos; `tests/oute-tray.test.sh`) | o CI não roda `swift test` (`.github/workflows/pr.yml:29-30`) | — | o Hanko Desktop precisa de CI próprio no macOS |

Issues abertas do oute-agent sobre o Tray: #448 (aprovar pelo menu) e #612 (tray como painel).

---

## 6. Dependentes no oute-agent

### 6.1 Dependências escondidas (quebram sem aviso)

| Dependência | Fonte | Efeito quando o agent-studio sair | Destino (recomendação do autor) |
|---|---|---|---|
| Nome da rodada usa `names.py` do agent-studio | `docker/oute-swarm:204` | a rodada abre só com o id | mover `names.py` para fora do agent-studio **antes** do corte |
| Teto de custo do modo dark lê `GET /v1/usage?rodada=` | `docker/oute-swarm:395-407,429-435`; `config/swarm.toml:4-6` (`dark_budget_usd = 1000`) | o spawn só avisa e segue, sem teto | API de uso do Hanko por rodada externa; issue #858 do oute-agent |
| Etapas, ações e decisões só aparecem no agent-studio | `docker/swarm.md:26,29`; `docker/swarm/triagem.md:31`, `auditoria.md:23`, `kaizen.md:14`, `pagina.md:8` | o Bardi perde a tela das etapas | rodada externa no Hanko; prompts com o link novo |
| Plugin herdr lê `/v1/tray` | `docker/herdr-plugins/oute.rodada/oute-rodada-sync:10,66-77` | some "$N hoje · N pend" | API do Hanko Desktop |
| `oute-regression` lê HTML do agent-studio | `docker/oute-regression:468-556` | a tarefa `studio` falha | reescrever contra o Hanko |
| `oute.canal.snapshot` só serve ao agent-studio | `oute:1120-1138`; `docker/oute-emit:553-567` | o pedido sem arquivo não sai da lista | aprovação externa no Hanko (adendo #815) |

### 6.2 Skills

| Skill | O que lê | Muda |
|---|---|---|
| `oute-aidlc-ops-observe` | `/v1/usage` (`rows[]`, `series[]`, `prices.errors`) e `/v1/alerts` (`alerts[]`, `hosts[]`) (`observe.sh:2-6,47-129`) | ler a API do Hanko (decisão 11) |
| `oute-aidlc-learn-insights` | o `observe.sh all`; link `/ciclo?id=` (`SKILL.md:83`) | idem; link novo |
| `oute-aidlc-iter-roadmap` | link `/ciclo` (`SKILL.md:70`) | link novo |
| `oute-aidlc-ship-release` | `/v1/alerts`, tipos `price_*` (`SKILL.md:108-118`) | ler alertas do Hanko |
| `oute-aidlc-ship-verify` | `STUDIO_SERVICES="oute-agent-studio oute-surrealdb"` (`scripts/verify-host.sh:6`) | tirar os dois serviços |
| `oute-aidlc-strat-opportunity`, `qa-pr-audit`, `qa-security-audit` | citam "bucket + agent-studio" e o gate `otelcol-contrib validate` com `agent-studio.yaml` | texto e gate |

### 6.3 `scripts/oute`, compose, collector e config

| Peça | Fonte | Muda |
|---|---|---|
| Segredos `AGENT_STUDIO_*` e pasta `oute-services` | `oute:55-67,161-204` | sair, por último (decisão 13) |
| Profile `agent-studio`, URLs, pastas de dados | `oute:206-283,1990-2004` | sair |
| `oute studio replay`, `rebuild-state`, `backup` | `oute:387-615,2017-2027` | sair depois do ok final |
| Subnet `studio`; reserva de fila de 4 GB | `oute:847-853,1021-1055` | sair; reserva cai para uma fila |
| Serviços `agent-studio` e `surrealdb`, volumes, rede `studio` | `docker/compose.yaml:197-306,364-387` | sair |
| `AGENT_STUDIO_URL` no serviço `agent` | `docker/compose.yaml:50-52` | trocar pela URL do Hanko |
| Exporters `otlp_http/studio_*`: filas de 300, 100 e 600 MB, `retry_on_failure.max_elapsed_time: 0`, `transform/redact`, `filter/codex_noise` | `config/otel/agent-studio.yaml` (90 linhas) | novo destino Hanko com as mesmas garantias |
| `config/agent-studio/config.toml`: fuso, preços com `fixed`, `[alerts]`, `[[plans]]` (claude Max 5x US$ 100, zai Lite US$ 18, codex Gratuito US$ 0, início 2026-10-08) | `CFG:8,38-250` | migrar os valores para o Hanko |
| `Dockerfile`: venv e código do agent-studio | `Dockerfile:53,141-147` | sair |
| Vhost `agent-studio.oute.pro`, só na tailnet | repo `lab` (#244 do lab), não verificado aqui | PR no `lab` |

### 6.4 Docs e testes do oute-agent

- Docs que mudam: `README.md`, `AGENTS.md` (regra "ferramenta nova manda consumo ao bucket + agent-studio"),
  `CONTEXT.md` (verbetes agent-studio, surrealdb, custo pago, tray, etapa, credenciais), `secrets/README.md`,
  `docs/studio-banco.md`, `docs/pt-controlado.md:51`, `docs/mapa-repo.md`, ADR-01, 02, 04, 09 e 10.
- O ADR-08 precisa de um ADR que o substitua, com a regra §11, a garantia "sem perda" por destino e a autenticação.
- Testes: 48 arquivos `tests/agent-studio*.test.sh` saem junto. Outros 20 testes dependem dele (lista no relatório da
  leitura: `compose-config`, `otelcol-*`, `oute-up`, `ops-observe`, `oute-swarm-*`, `herdr-plugin-rodada`,
  `oute-emit`, `oute-approve-snapshot` e outros).
- Issues abertas do oute-agent que citam o agent-studio: #448, #460, #612, #630, #675, #834, #835, #852, #858.

### 6.5 Atributos OTel de que o agent-studio depende

Todos continuam indo ao bucket; o que se perde é o consumidor. A ingestão do Hanko precisa ler os mesmos:

- `oute.event.id` (dedupe; `docker/oute-emit:205-214`);
- `oute.canal.*` (`docker/oute-emit:499-567`);
- `oute.swarm.*`, `oute.swarm.round.*`, `oute.swarm.session.*`, `oute.swarm.step.*`, `oute.swarm.watch.*`
  (`docker/oute-emit:643-720`);
- `oute.task.*` (`docker/oute-emit:800-815`; `docker/oute-task:105-123`; `docker/shims/oute-agent-shim:80-115`);
- `oute.quota.*`, `oute.memory.capture.*`, `oute.emit.spool.*`, `oute.herdr.sidebar.*`, `oute.llm_proxy.up`;
- `oute.agent`, `host.name`, `oute.instance` (`config/otel/collector.yaml:31-55`);
- nativos do Claude Code e do Codex: `session.id`, `gen_ai.*`, `cost_usd`, `request_id`, `event.name`.

Não verificado: quem emite `oute.served_model` (lido em `AS/otlp.py:168`).

---

## 7. Divergências entre o ADR-08 e o código

Achadas na leitura. A migração segue o **código**, salvo decisão do Bardi.

1. O código tem `/v1/prices`, `/v1/plans`, `/v1/rodada` e `/readyz`; o ADR-08 §6 lista só usage, alerts e tray.
2. O ADR-08:409 diz "únicas rotas não-GET: login, logout e ingestão"; hoje há também `/v1/backup`, `/marcar`,
   `/rodada/acao`, `/ack`, `/planos/novo` e `/fase`.
3. A página do pedido mostra o comando `oute approve <id>` para copiar (`TPL/proposal.html:79-87`); o ADR-08:409 diz
   que não monta comando com o id.
4. `phase_jev` usa `INSERT OR REPLACE` (`AS/phase.py:290-291`); o ADR-08:376 a chama de tabela de acréscimo.
5. O ADR-08 diz que as marcas "não vão ao bucket" (linhas 105, 495, 597); a cópia de segurança leva todas ao bucket em
   `backups/agent-studio/`.
6. O ADR-08:470 chama a conferência de preços de "única" chamada à internet; o ADR-08:92 e :378 dizem que o Jev é a
   segunda.
7. O "A/B do Jev" (ADR-08:82) não foi achado no código.
8. O Dashboard `/` (#469) não tem seção própria no ADR-08.

## 8. Pontos para o gate do Bardi

1. Destino de cada linha (a coluna "Destino" é só recomendação do autor).
2. O A/B do Jev: confirmar se existe antes de dar destino (§2, §7 item 7).
3. Prazo de 24 h do ack: entra no ADR-0042 ou fica só para a empresa Oute (§3.2).
4. Regra do preço vigente para fonte externa: hora do fato (agent-studio) ou fim do run (Hanko) (§4.5).
5. Jev × decision model do Hanko (§4.9).
6. Ícone do Hanko Desktop (§5).
7. Mover `names.py` para fora do agent-studio antes do corte (§6.1). É mudança no oute-agent.
8. `regression` e `proj`: viram workspace ou "sem repositório" ("Medição no host").
9. O pico de spans de 2026-10-05 (1.148.602 num dia) é uso real ou carga atípica (replay, teste)? A projeção da #68
   depende disso ("Medição no host").

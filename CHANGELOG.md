# Changelog

Formato: [Keep a Changelog](https://keepachangelog.com/pt-BR/1.1.0/) · versões: [SemVer](https://semver.org/lang/pt-BR/).
English summary at the end of each release.

## [Unreleased]

## [0.8.4] — 2026-11-01

### ai-memory 3.3.1

- O digest fixado do ai-memory passa a ser o da 3.3.1, no `docker-compose.yml`, no `.env.example` e no primeiro deploy de `deploy-prd.sh`, que antes usava `latest`. Uma instalação existente segue com o `AI_MEMORY_IMAGE` do seu `.env`.
- O diff das propostas de curadoria mostra o texto da página, não o JSON da resposta, e pede o teto de 64.000 caracteres: a 3.3 corta em 12.000 sem esse pedido, e o resto da página pareceria removido. Página maior que o teto vem com aviso de corte.
- As tools novas `memory_patch_page` e `memory_restore_page` entram na política de curadoria como retidas, e a proposta de um patch mostra as edições.
- `deploy/compose.yaml` define `AI_MEMORY_ALLOWED_HOSTS`. Sem isso o servidor recusa o Hanko com 403.

- English summary: the pinned ai-memory digest is now 3.3.1 (`docker-compose.yml`, `.env.example`, first `deploy-prd.sh` run, which used `latest`); existing installs keep their `.env` value. Curation diffs show the page text and request the 64,000-character ceiling, because 3.3 cuts at 12,000 by default. `memory_patch_page` and `memory_restore_page` are held for human review, and a patch proposal shows its edits. `deploy/compose.yaml` sets `AI_MEMORY_ALLOWED_HOSTS`, without which the server answers 403 to Hanko.

### Proxy de memória mais estrito

- O proxy `/memory-mcp` fixa o escopo da run em toda chamada e só aceita as tools e os argumentos revisados. O resto é recusado e fica no log, só com os nomes.
- A memória da persona segue o workspace da run, na ordem task, projeto, persona. Antes, uma task que herdava o workspace do projeto podia usar a memória de outro lugar.
- Personas passam a alcançar outro workspace só por concessão (`hanko memory grant`), e a busca delas não traz a área global do ai-memory.

- English summary: the `/memory-mcp` proxy pins the run's scope on every call and only accepts reviewed tools and arguments; anything else is refused and logged by name. Persona memory follows the run's workspace (task, project, persona). Personas reach another workspace only through a grant, and their search no longer returns ai-memory's global area.

## [0.8.3] — 2026-11-01

### Alerta de disco do Postgres

- Com `HANKO_DB_DISK_PATH` apontando para um caminho no mesmo disco dos dados do Postgres (por exemplo o volume dele montado só para leitura no contêiner do Hanko), o motor de alertas mede o espaço livre e abre `db_disk_low` em cada empresa com telemetria nos últimos 30 dias: `warning` abaixo de `limit.db_disk_warn_gib` (padrão 20 GiB) e `critical` abaixo de `limit.db_disk_critical_gib` (padrão 5 GiB), ambos ajustáveis por empresa. O alerta fecha quando o espaço volta. Sem o caminho, nada é medido.

- English summary: with `HANKO_DB_DISK_PATH` pointing to a path on the same disk as the Postgres data (for example its volume mounted read-only in the Hanko container), the alert engine measures free space and opens `db_disk_low` in every company with telemetry in the last 30 days: `warning` below `limit.db_disk_warn_gib` (default 20 GiB) and `critical` below `limit.db_disk_critical_gib` (default 5 GiB), both adjustable per company. The alert closes when space recovers. Without the path nothing is measured.

### Alertas de preço e de custo de lista

- O motor de alertas passa a abrir os 7 alertas de preço e de custo de agente externo: `price_changed` (preço trocado pela conferência nos últimos 7 dias, informativo), `price_sources_diverge`, `price_locked_differs`, `price_source_down` (fonte fora em 2 dias UTC seguidos), `price_model_unpriced`, `cost_subscription_unpriced` e `cost_claude_diff` (custo informado pelo Claude difere da tabela acima do limite da empresa, padrão do `limit.claude_cost_diff_pct`). Cada alerta abre em toda empresa que usou o modelo nos últimos 30 dias e fecha sozinho quando a condição some. Preço travado não alerta por divergência.

- English summary: the alert engine now opens the 7 price and list-cost alerts for external agents: `price_changed`, `price_sources_diverge`, `price_locked_differs`, `price_source_down`, `price_model_unpriced`, `cost_subscription_unpriced` and `cost_claude_diff`. Each opens in every company that used the model in the last 30 days and closes by itself when the condition clears; a locked price never alerts on divergence.

### Alertas operacionais de agente externo

- O motor de alertas passa a abrir, a partir da telemetria gravada, os alertas de fila do exporter (`queue`), destino recusando (`destination_refusing`), spool do `oute-emit` (`spool`), cota (`quota`), rodada parada e rodada antiga (`round_stalled`, `round_old`), proxy do LLM do ai-memory fora (`llm_proxy_down`), captura parcial (`memory_capture`) e hook sem `trusted_hash` (`memory_hooks_untrusted`). Cada empresa usa os próprios limites e a própria janela de leitura (`limit.*` da API de ajustes). Host parado, sem registro há mais do limite de `host_no_data`, não liga nenhum deles além do próprio `host_no_data`. Cada alerta fecha sozinho quando o dado seguinte não mostra mais o problema.

- English summary: the alert engine now opens, from stored telemetry, the exporter queue (`queue`), destination refusing (`destination_refusing`), `oute-emit` spool (`spool`), quota (`quota`), stalled and old round (`round_stalled`, `round_old`), ai-memory LLM proxy down (`llm_proxy_down`), partial capture (`memory_capture`) and hook without `trusted_hash` (`memory_hooks_untrusted`) alerts. Each company uses its own limits and read window (`limit.*` in the settings API). A stopped host, with no record for longer than the `host_no_data` limit, raises none of them besides `host_no_data` itself. Each alert closes by itself when the next data no longer shows the problem.

### Aprovação externa: o pedido do canal, só observado

- A ingestão passa a derivar, dos eventos `oute.canal.*`, a aprovação externa: o pedido (título, usuário, host, agente e sessão, do `proposed` mais antigo), cada versão diferente do script que chega, a decisão (`rejected` ou `executed`, com o aprovador `human` ou `human_dark`, rc, duração e tamanho da saída) em qualquer ordem de chegada, e a lista da outbox de cada host (a mais nova vence; lista com hora à frente do relógio do servidor é recusada). O pendente que a lista mais nova do host não tem, depois da folga de 60 s, sai das listas sem ser apagado. A API lista os pedidos e mostra cada um com o sha256 e o número de versões, para qualquer membro; só owner e admin leem o corpo do script, e cada leitura gera `activity_event`. O Hanko nunca aprova, recusa nem roda algo no host.

- English summary: ingestion now derives the external approval from the `oute.canal.*` events: the request (title, user, host, agent and session, from the oldest `proposed`), each different script version that arrives, the decision (`rejected` or `executed`, with approver `human` or `human_dark`, rc, duration and output size) regardless of arrival order, and each host's outbox list (the newest wins; a list timestamped ahead of the server clock is refused). A pending request missing from the host's newest list, after a 60 s margin, leaves the lists without being deleted. The API lists the requests and shows each with its sha256 and version count to any member; only owners and admins read the script body, and every read generates an `activity_event`. Hanko never approves, rejects or runs anything on the host.

### Assistente "Conectar ferramenta a uma persona"

- Um fluxo só liga um servidor MCP a personas: servidor (header secreto ou OAuth 2.1 com PKCE), descoberta das tools por risco, escolha das personas, modelo de acesso e prévia do que cada persona poderá fazer antes de gravar (ADR-0089).
- Modelos de acesso por empresa: "Somente leitura" e "Leitura livre, escrita com aprovação"; a escrita da conexão nova só sai da quarentena pelo modelo escolhido.
- Comando `hanko integration connect`; a gravação exige `--yes` depois da prévia.
- English summary: guided assistant that connects an MCP server to personas with an access template (read-only or read-free with write approval), in the web UI and as `hanko integration connect` (ADR-0089).

### Caixa: aprovação externa pendente e pergunta da rodada

- A Caixa passa a ter dois itens que pedem ação e contam no selo: cada pedido do canal de aprovação pendente (só para owner e admin; o item traz `oute approve <id>` para copiar e sai quando o pedido é decidido ou some da lista da outbox do host) e a pergunta da rodada ao Bardi sem resposta (a rodada aberta cuja última pergunta é igual ou mais nova que a última resposta, até o dobro de `limit.lookback_hours`). A pergunta da rodada, marcada como lida ou arquivada, volta depois do prazo `due.round_question` (24 h por padrão, ajustável por empresa).

- English summary: the inbox now has two action-needed items that count toward the badge: each pending approval-channel request (owners and admins only; the item carries `oute approve <id>` to copy and leaves when the request is decided or drops off the host's outbox list) and the unanswered round question to the Bardi (an open round whose last question is equal to or newer than the last answer, up to twice `limit.lookback_hours`). A round question marked read or archived comes back after the `due.round_question` deadline (24 h by default, adjustable per company).

### "Vence em" por tipo de alerta na Caixa

- Um alerta marcado como lido ou arquivado na Caixa volta como não lido quando o prazo do tipo vence, contado no servidor desde a marca, além de quando a impressão digital muda. Os alertas de agente externo vencem em 24 h por padrão; os tipos que o Hanko já tinha ficam "sem prazo", e nada muda para quem já usa. Cada empresa troca o prazo de cada tipo (de "sem prazo" até 30 dias) pela API de ajustes de alerta (`due.<tipo>`). Vencer não apaga a marca: só tira o efeito dela.

- English summary: a read or archived alert in the inbox comes back as unread when the type's deadline passes, counted on the server from the mark, besides when the fingerprint changes. External-agent alerts expire after 24 h by default; the alert types Hanko already had stay "no deadline", so nothing changes for current users. Each company changes the deadline of each type (from "no deadline" up to 30 days) through the alert settings API (`due.<type>`). Expiring does not delete the mark: it only removes its effect.

### Documentação: roteiro da demo da ingestão externa

- `docs/DEMOS.md` (e a versão em inglês) ganha o roteiro da demo `qa` da #125: ligar o listener de ingestão num Hanko local, criar uma chave `ingest`, mandar um fato OTLP em JSON e ler a conversa pela API. Não foi executado de ponta a ponta.
- English summary: `docs/DEMOS.md` (and the English version) gains the `qa` demo script for #125: enable the ingestion listener on a local Hanko, create an `ingest` key, post an OTLP JSON fact and read the conversation through the API. It has not been run end to end.

### Export da empresa leva a configuração externa

- O arquivo `hanko.company.v1` passa a levar, como campos opcionais, a configuração externa da empresa: agentes e hosts externos (com apelidos e, no host, o marcador de sempre ligado), emparelhamentos de repositório, o workspace `no-repo`, a chave externa e o histórico de mensalidade de cada assinatura e os limites de alerta que a empresa mudou. Conversas, sessões, rodadas, aprovações, chamadas ao modelo, alertas, preços de modelo e telemetria não viajam, e nenhuma chave `ingest` ou `read` entra. A importação valida tudo antes de gravar e cria a empresa nova com ids novos; o host importado vem sem chave e precisa de uma chave `ingest` nova. Importar para uma empresa existente (merge) avisa que a configuração externa não entra.

- English summary: the `hanko.company.v1` file now carries, as optional fields, the company's external configuration: external agents and hosts (with aliases and, for hosts, the always-on flag), repository pairings, the `no-repo` workspace, each subscription's external key and monthly history, and the alert limits the company changed. External conversations, sessions, rounds, approvals, model calls, alerts, model prices and telemetry do not travel, and no `ingest` or `read` key is included. Import validates everything before writing and creates the new company with new ids; an imported host comes without a key and needs a fresh `ingest` key. Importing into an existing company (merge) warns that external configuration is not merged.

### Gateway de tools: parâmetros `x-mcp-header` viram cabeçalho

- O gateway (ADR-0053) passa a repetir como cabeçalho `Mcp-Param-<nome>` todo argumento cuja propriedade no input schema da tool traz `x-mcp-header`. É o que o servidor MCP remoto do GitHub exige para `owner`, `repo` e afins; sem o cabeçalho ele recusava a chamada com `header mismatch: missing Mcp-Param-owner header`, e `issue_write`, `issue_read`, `list_issues` e `get_file_contents` falhavam para toda persona (só `search_*` passavam). O argumento continua no corpo, a credencial guardada vence qualquer cabeçalho de parâmetro, e só valores escalares sem quebra de linha entram.

- English summary: the tool gateway now mirrors every argument whose schema property carries `x-mcp-header` as a `Mcp-Param-<name>` request header. The remote GitHub MCP server requires it for `owner`, `repo` and the like; without it every `issue_write`, `issue_read`, `list_issues` and `get_file_contents` call failed with `header mismatch: missing Mcp-Param-owner header` (only `search_*` worked). The argument stays in the body, stored credentials win over parameter headers, and only scalar values without line breaks are forwarded.

### Organograma: os menus de filtro fecham

- Os menus **Tags** e **Níveis** do filtro do organograma agora fecham ao clicar fora, ao apertar Esc e ao abrir o outro menu. Antes só fechavam no clique no próprio título. Escolher uma tag não fecha o menu, porque o filtro aceita várias.
- English summary: the org chart's **Tags** and **Levels** filter menus now close on an outside click, on Esc and when the other menu opens; before, they only closed on a click on their own title. Picking a tag keeps the menu open because the filter accepts several.

### Rodada externa: leitura, ciclo, texto da etapa e marca da ação

- A API passa a listar as rodadas externas (com filtro por ciclo), mostrar uma rodada com participantes, etapas vigentes e ações com a marca mais nova, e mostrar o ciclo de uma issue com o resumo no topo. O texto de uma etapa aprovada sai do fato da telemetria só para owner e admin, e cada leitura gera `activity_event`; a etapa reprovada ou sem revisor não entrega o texto nem as ações. Owner e admin, com reautenticação recente, marcam uma ação como feita ou pendente: as marcas só se acrescentam e a mais nova vence; a ação que cita um pedido do canal não tem caixa. A ingestão passa a guardar o último evento da rodada e a última pergunta e resposta ao Bardi.

- English summary: the API now lists external rounds (filterable by cycle), shows a round with participants, current steps and actions with their newest mark, and shows an issue's cycle with the summary on top. The text of an approved step comes from the telemetry fact for owners and admins only, and every read generates an `activity_event`; a rejected or reviewer-less step returns neither text nor actions. Owners and admins, with recent re-authentication, mark an action done or pending: marks are append-only and the newest wins; an action that cites a channel request has no checkbox. Ingestion now stores the round's last event and the last question and answer to the Bardi.

### Rodada externa: nasce dos eventos do swarm

- A ingestão passa a derivar, dos eventos `oute.swarm.*` e `oute.task.*`, a rodada externa (abertura, fechamento, repositório ligado ao workspace pelo emparelhamento, máximo de sessões e nome do gerador único), os participantes (workers, ligados à sessão externa quando ela já chegou), o ciclo (a issue `<dono>/<repo>#<n>`) e as etapas da rodada (triagem, merge por PR, kaizen, fechamento e resumo do ciclo). A revisão mais alta de uma etapa vence em qualquer ordem de chegada; o texto da etapa fica só no fato da telemetria. Evento de etapa fora do formato do `oute-swarm` não vira estado. Reenviar o lote não muda nada.

- English summary: ingestion now derives, from the `oute.swarm.*` and `oute.task.*` events, the external round (opening, closing, repository linked to the workspace through pairing, session maximum and unique-generator name), participants (workers, linked to the external session once it arrives), the cycle (the `<owner>/<repo>#<n>` issue) and round steps (triage, per-PR merge, kaizen, closing and cycle summary). The highest revision of a step wins regardless of arrival order; the step text stays only in the telemetry fact. A step event outside the `oute-swarm` format creates no state. Resending a batch changes nothing.

### Script de diagnóstico de instalação

- `scripts/hanko-diag.sh` coleta, só leitura e com segredos mascarados, o que o suporte precisa para diagnosticar "erros no log" e "persona sem acesso ao workspace": modo de execução (host, compose com imagem agents ou distroless), variáveis efetivas, `hanko doctor`, binários e login do harness, WARN/ERROR do servidor agrupados por mensagem, empresas, workspaces, personas, readiness, runs com falha e o log delas, permissões reais de cada caminho de workspace sob `HANKO_WORKSPACES_ROOT` e se o harness alcança a API em `HANKO_AGENT_URL` e o ai-memory.

- English summary: `scripts/hanko-diag.sh` gathers, read-only and with secrets masked, what support needs to diagnose "errors in the log" and "persona has no access to the workspace": execution mode, effective variables, `hanko doctor`, harness binaries and login, server WARN/ERROR grouped by message, companies, workspaces, personas, readiness, failed runs and their logs, the real permissions of every workspace path under `HANKO_WORKSPACES_ROOT`, and whether the harness reaches the API at `HANKO_AGENT_URL` and ai-memory.

### Compose de produção mede o disco do Postgres

- O `docker-compose.yml` monta o volume `pgdata` só para leitura em `/pgdata-disk` no contêiner do Hanko e define `HANKO_DB_DISK_PATH`, o que liga o alerta `db_disk_low`. O Hanko só consulta o espaço livre do disco; não lê nem escreve nos dados do banco. Para desligar, tire a variável e a linha do volume (`docs/RUNBOOK.md`).

- English summary: the production `docker-compose.yml` mounts the `pgdata` volume read-only at `/pgdata-disk` in the Hanko container and sets `HANKO_DB_DISK_PATH`, which turns on the `db_disk_low` alert. Hanko only queries the disk's free space; it neither reads nor writes the database files. To turn it off, remove the variable and the volume line (`docs/RUNBOOK.md`).

### Recusa de tool nativa pelo modo do claude vem explicada na run

- Numa run `claude` sem política de tools nativa para a persona, o hook `PreToolUse` não entra e vale o modo de permissão do próprio claude, que em modo headless recusa o que não libera sozinho (`git log`, `which gh` e outros comandos fora da lista dele). O evento "tools negadas por permissão" passa a dizer isso e a trazer o comando que libera (`hanko tool-policy set allow --persona <slug> --native <tool>`, ou a aba Política em Integrações). Com hook, em revisão ou em outro harness nada muda (ADR-0053).

- English summary: in a `claude` run where the persona has no native tool policy, the `PreToolUse` hook is not installed and the harness's own headless permission mode denies anything it does not auto-allow. The "tools denied by permission" run event now says so and includes the command that grants the tool (`hanko tool-policy set allow --persona <slug> --native <tool>`, or the Policy tab under Integrations). Nothing changes with a hook, in review runs, or for other harnesses.

## [0.8.2] — 2026-10-31

### Limites e prazos de alerta por empresa

- A API passa a listar, trocar e voltar ao padrão os limites e os prazos de alerta da empresa (owner ou admin, com `activity_event`). A lista traz os 14 limites e os prazos de cada tipo de alerta, com o valor em vigor, o padrão (os valores da Oute), a faixa e a unidade; a empresa só guarda o que mudou. Prazo vai de 0 ("sem prazo") a 720 horas (30 dias).

- English summary: the API now lists, changes and resets the company's alert limits and deadlines (owner or admin, with an `activity_event`). The list carries the 14 limits and each alert type's deadline, with the effective value, the default (Oute's values), the range and the unit; the company only stores what it changed. A deadline goes from 0 ("no deadline") to 720 hours (30 days).

### Conferência diária de preços de modelo

- O Hanko passa a conferir os preços de modelo na subida e a cada 24 h, onde há ingestão: busca o models.dev e o OpenRouter pelo `internal/netguard` (só https, sem credencial nem redirecionamento, no máximo 32 MiB e 60 s por fonte) e confere os modelos usados nos últimos 30 dias e os que já têm preço vindo das fontes. Só troca o preço, com uma linha nova de `model_prices` de origem `sources`, quando as duas fontes concordam; o preço travado nunca é trocado, e a fonte fora do ar, a divergência ou o modelo que uma fonte não tem deixam o preço como está. Cada conferência guarda o resultado por fonte e por modelo (`equal`, `changed`, `new`, `diverges`, `locked_differs`, `no_source`, `source_down`); a falha de uma fonte guarda só o código. A API lista as conferências e mostra cada uma.

- English summary: Hanko now checks model prices at startup and every 24 h, wherever ingestion is on: it fetches models.dev and OpenRouter through `internal/netguard` (https only, no credentials or redirects, at most 32 MiB and 60 s per source) and checks the models used in the last 30 days and those that already have a price from the sources. It only changes a price, with a new `model_prices` row of origin `sources`, when both sources agree; a locked price is never changed, and a source being down, a divergence or a model one source lacks leave the price as it is. Each check stores the result per source and per model (`equal`, `changed`, `new`, `diverges`, `locked_differs`, `no_source`, `source_down`); a source failure stores only the code. The API lists the checks and shows each one.

### Histórico de mensalidade e chave externa da assinatura

- Cada assinatura passa a ter um histórico de mensalidade que só recebe linhas novas (valor, início, hora do registro e autor): o valor de um dia é o da linha com o maior início menor ou igual a ele, e a registrada por último desempata. A API lista o histórico e acrescenta uma linha (owner ou admin, com reautenticação recente); um início no passado muda o pago desses dias, e um no futuro é recusado. Trocar a mensalidade pelo cadastro da assinatura agora também acrescenta uma linha, com início hoje, e exige a reautenticação recente. A assinatura ganha a chave externa opcional (`claude`, `zai` ou `codex`, única por empresa), que liga a assinatura às chamadas externas que a declaram.

- English summary: each subscription now has an append-only monthly-value history (value, start, recording time and author): a day's value is that of the row with the greatest start not after it, and the last recorded wins a tie. The API lists the history and adds a row (owner or admin, with recent reauthentication); a start in the past changes the paid cost of those days, and a start in the future is refused. Changing the monthly value through the subscription form now also adds a row, starting today, and requires recent reauthentication. A subscription also gains the optional external key (`claude`, `zai` or `codex`, unique per company), which ties it to the external calls that declare it.

### O pago passa a ser um rateio único, com o histórico de mensalidade e as chamadas externas

- O pago de uma assinatura agora usa a mensalidade de cada dia pelo histórico (a linha com o maior início menor ou igual ao dia; antes da primeira linha vale a primeira) e divide o valor do dia entre os runs e as chamadas externas que a assinatura cobre, na proporção do custo de lista. Os dias em que a assinatura não cobra continuam sem pago, e o dia sem uso continua ocioso. O uso externo ganha o campo `cost_paid_usd` (zero sem assinatura ligada pela chave externa); a soma do pago de um dia fecha com a mensalidade do dia.

- English summary: a subscription's paid cost now uses each day's monthly value from the history (the row with the greatest start not after the day; before the first row, the first one applies) and splits the day's value between the runs and the external calls the subscription covers, in proportion to list cost. Days the subscription does not charge still have no paid cost, and a day without use is still idle. External usage gains the `cost_paid_usd` field (zero without a subscription tied by the external key); the day's paid total closes with the day's monthly value.

## [0.8.1] — 2026-10-31

### Conversa externa e chamadas ao modelo na ingestão

- A ingestão passa a criar a conversa externa do `session.id` de cada fato, com o nome do gerador único, e a manter os atributos dela (host, agente, instância, serviço, assinatura, repositório, sessão) pela hora do fato mais antiga. Os spans que são chamada ao modelo (`claude_code.llm_request`, `session_task.turn`, `ai_memory.llm_request` e `jev.decision`, fora o agente `router`) entram em `model_calls`, com o papel da chamada, e somam no `calls_daily` e nos contadores da conversa só uma vez, mesmo reenviados. O p95 e o contexto da conversa são refeitos a partir das chamadas. O custo de lista vem na fatia 2: por ora a chamada com custo real no span é `real` e as outras são `unpriced`.

- English summary: ingestion now creates the external conversation for each fact's `session.id`, named by the single generator, and keeps its attributes (host, agent, instance, service, subscription, repository, session) by the oldest fact time. Spans that are model calls (`claude_code.llm_request`, `session_task.turn`, `ai_memory.llm_request` and `jev.decision`, except the `router` agent) go into `model_calls` with the call role, and add to `calls_daily` and the conversation counters only once, even when resent. The conversation p95 and context are rebuilt from the calls. List cost comes in slice 2: for now a call with a real cost on the span is `real` and the others are `unpriced`.

### Custo de lista das chamadas externas, calculado na ingestão

- A chamada ao modelo que a ingestão grava passa a ter o custo de lista, calculado uma vez com o preço da hora do fato (a tabela de preços aplicada aos tokens, pelo prefixo de modelo mais longo e sem o prefixo do provedor), e uma das quatro classes: `real` (o custo do span ou, no Claude Code, o do log `api_request` de mesmo `request_id`, que pode chegar antes ou depois do span), `listed` (chamada de assinatura sem custo real), `estimated` (as outras sem custo real) e `unpriced` (modelo sem preço, que nunca vira zero). Na assinatura `zai` o custo informado é descartado. O custo de lista entra no `calls_daily`, na conversa e no uso externo; o log que chega depois promove a chamada a `real` e ajusta os totais.

- English summary: a model call that ingestion records now has its list cost, computed once at the fact-time price (the price table applied to the tokens, by longest model prefix and without the provider prefix), and one of four classes: `real` (the span's cost or, for Claude Code, that of the `api_request` log with the same `request_id`, which may arrive before or after the span), `listed` (a subscription call without real cost), `estimated` (other calls without real cost) and `unpriced` (a model without a price, which never becomes zero). On the `zai` subscription the reported cost is discarded. List cost goes into `calls_daily`, the conversation and the external usage; a log that arrives later promotes the call to `real` and adjusts the totals.

### Deploy: imagem construída fora do servidor

- `deploy/oute-server/deploy-prd.sh --build-local` constrói a imagem na máquina de quem roda o deploy, para a arquitetura do servidor, e a envia ao LXC `hanko-prd` com `docker save | ssh | docker load`; o servidor sobe com `docker compose up -d --no-build` e não compila. A construção e o envio rodam antes do backup e da troca dos containers, então uma falha deixa a produção como estava. Sem a opção, nada muda.
- English summary: `deploy/oute-server/deploy-prd.sh --build-local` builds the image on the machine running the deploy, for the server's architecture, and sends it to the `hanko-prd` LXC with `docker save | ssh | docker load`; the server starts it with `docker compose up -d --no-build` and never compiles. Build and transfer run before the backup and the container swap, so a failure leaves production untouched. Without the flag nothing changes.

### Emparelhamento de repositório e conteúdo da conversa externa

- A API passa a listar os emparelhamentos de repositório com workspace e a trocar o workspace de um (owner ou admin). Trocar leva o passado junto: as conversas, sessões, rodadas e chamadas do repositório passam para o workspace novo, e o `calls_daily` sai do velho e entra no novo pelas mesmas chamadas; o workspace de sistema `no-repo` não recebe emparelhamento. Também passa a mostrar o conteúdo completo de uma conversa externa (hoje os pedidos do usuário), só para owner e admin, com um `activity_event` a cada leitura. Com isso nenhuma operação da fonte externa responde mais 501.

- English summary: the API now lists repo pairings and lets owners or admins change a pairing's workspace. Changing it carries the past along: the repository's conversations, sessions, rounds and calls move to the new workspace, and `calls_daily` leaves the old one and enters the new one with the same calls; the `no-repo` system workspace cannot take a pairing. It also shows an external conversation's full content (today the user's prompts), for owners and admins only, with an `activity_event` on every read. With this, no external-source operation answers 501 any more.

### Leitura da fonte externa: sessões, conversas, classificação à mão e uso

- A API passa a responder as sessões externas (lista e detalhe), as conversas externas (lista com filtro por host, agente, workspace, sessão, classe, marca e janela, ordenada pela atividade ou pelo custo, e detalhe), as entradas da classificação de uma conversa, a troca da classe à mão (owner ou admin, com `activity_event`, que vale sobre a classificação do classificador) e o uso externo, agrupado por dia, host, agente, modelo, workspace e assinatura. O filtro `round` do uso soma só as chamadas das conversas das sessões de uma rodada, para o teto de custo do modo dark. O uso traz o custo real e as chamadas sem preço; o custo de lista e o "pago" vêm com a fatia 2.

- English summary: the API now answers external sessions (list and detail), external conversations (list filtered by host, agent, workspace, session, class, tag and window, ordered by activity or cost, and detail), a conversation's classification inputs, setting the class by hand (owner or admin, with an `activity_event`, which overrides the classifier) and external usage grouped by day, host, agent, model, workspace and subscription. The usage `round` filter sums only the calls of conversations in a round's sessions, for the dark mode cost cap. Usage carries real cost and unpriced calls; list cost and the "paid" figure come with slice 2.

## [0.8.0] — 2026-10-31

### Contrato da v0.8, grupo 3: ajustes da rodada e da aprovação externas

- A migration 00211 corrige o contrato da 00210 depois de revisá-lo contra os ADR-0080 e 0081 e contra o produtor do evento: o pedido decidido pode faltar o sha256 (numa recusa ele não vem), a duração do pedido é em segundos, o nome de quem aprovou é guardado e a rodada guarda a hora do último evento, da última pergunta e da última resposta. O uso externo ganha o filtro por rodada, e o alerta de disco do Postgres passa a avisar abaixo de 20 GiB livres e a ficar crítico abaixo de 5 GiB, com a empresa trocando os dois valores. Só o contrato: nenhuma tela, rota ou job funciona ainda.

- English summary: migration 00211 fixes the 00210 contract after reviewing it against ADR-0080 and 0081 and the event producer: a decided request may lack the sha256 (a rejection does not carry it), the request duration is in seconds, the approver's name is stored, and the round stores the time of its last event, last question and last answer. External usage gains the round filter, and the Postgres disk alert now warns below 20 GiB free and turns critical below 5 GiB, with each company able to change both values. Contract only: no screen, route or job works yet.

### Fonte externa: alerta host_no_data (#125)

- Host externo sempre ligado, não arquivado, sem fato há mais do limite da empresa abre o alerta `host_no_data` (critical). O limite é `limit.host_no_data_minutes` (padrão 30 min). Quando o fato chega, o alerta fecha sozinho (ADR-0077, item 4; ADR-0083, itens 2 e 5).
- English summary: an always-on external host without facts for longer than the company's limit opens a critical `host_no_data` alert; the next fact closes it.

### Fonte externa: agente e host externos novos na Caixa (fatia 1 da v0.8, #125)

- A Caixa de entrada lista os agentes e hosts externos criados nos últimos 30 dias como itens informativos: não entram
  na contagem nem no selo, e marcar como lido segue o de todo item. Agente ou host criado há mais de 30 dias sai da lista.
- A API da Caixa ganha os tipos `external_agent` e `external_host` no enum `kind` dos itens e das referências. Cliente que valida o enum de forma estrita precisa aceitar os dois.
- Ainda não há a seção visual desses itens na tela, nem a caixa de emparelhamento a conferir, nem renomear, arquivar ou
  juntar agente e host.

- English summary: the inbox lists external agents and hosts created in the last 30 days as informational items: they
  stay out of the count and the badge, and read state follows every inbox item. The screen section, the pairing-review
  item, and rename/archive/merge are not there yet.

### Fonte externa: classificação de conversa pela telemetria (#125)

- Cada conversa externa ganha a fase do AI-DLC pela regra do agent-studio: troca manual, abertura, label, skill, papel e
  ação, nessa ordem; a primeira que decide vale. Sem sinal forte, `build` com confiança baixa (ADR-0079, itens 1 a 3).
- A marca `regression` vai para a conversa cujo `oute.task.id` começa com `regression-` ou cujo repositório é `regression` (item 7).
- O Jev não é chamado nesta fatia: a ação fraca só marca que ele seria chamado.
- English summary: external conversations get an AI-DLC phase from telemetry, with the same signal order as the
  agent-studio; manual marks win; the `regression` tag is set from the task id or repository.

### Cofre: aviso de alcance na aprovação do gate de contratação

- Aprovar um gate `hire` devolve os avisos de alcance da persona criada (campo `warnings` do gate, ADR-0068 §2.5), como a contratação direta já fazia: quando o `adapter_config` da ficha referencia um `${secret:…}` que a persona não alcança, a resposta da aprovação (API e CLI `hanko gate approve`) traz o aviso, que nomeia a persona e o item e nunca o valor. Continua sendo aviso, não bloqueio: a concessão pode vir depois.

- English summary: approving a `hire` gate now returns the reach warnings of the created persona (gate `warnings` field, ADR-0068 §2.5), matching direct hire: when the spec's `adapter_config` references a `${secret:…}` the persona cannot reach, the approval response (API and `hanko gate approve` CLI) carries the warning, naming the persona and the item, never the value. Still a warning, not a block: the grant can come later.

### Cofre: campo arquivo pela tela

- "Novo campo" do item aceita o tipo `file`: a tela envia o arquivo (até 1 MiB, como `hanko secret field add --kind file`), o campo é sempre secreto e o conteúdo nunca volta à página; a lista mostra só o nome e o tamanho.
- "Trocar arquivo" troca o conteúdo pelo arquivo novo. O arquivo anterior vai para o histórico (ADR-0068 §3.1), a versão do campo sobe, e restaurar a versão anterior põe o arquivo de volta como versão nova (§3.3). "Apagar campo" apaga o arquivo do campo e o das versões anteriores.

- English summary: the secret's "New field" form accepts the `file` kind: the screen uploads the file (up to 1 MiB, like `hanko secret field add --kind file`), the field is always secret and the content never comes back to the page; the list shows only the name and size. "Replace file" replaces the content with the new file: the previous file goes to the history (ADR-0068 §3.1), the field's version goes up, and restoring a previous version puts its file back as a new version (§3.3). "Delete field" deletes the field's file and the files of its previous versions.

### Cofre: delegação de segredo pessoal pela tela e pela CLI

- A tela de um segredo pessoal ganhou "Delegações deste segredo": delegar a uma task, conversa ou run que você pediu, com validade de 1 hora, 24 horas (padrão) ou 7 dias; listar as delegações do segredo com alvo, validade e estado; e revogar. A lista não mostra o valor do segredo. A CLI ganhou `hanko secret delegation create <segredo> --target-type ... --target <id> [--expires 24h]`, `hanko secret delegation ls` e `hanko secret delegation revoke <id>`. A API (`POST /companies/{company}/user-secrets/{name}/delegations`, `GET /me/delegations`, `DELETE /me/delegations/{id}`) não mudou.

- English summary: a personal secret's screen gained "Delegations of this secret": delegate it to a task, conversation or run you requested, valid for 1 hour, 24 hours (default) or 7 days; list the secret's delegations with target, validity and state; and revoke. The list never shows the secret's value. The CLI gained `hanko secret delegation create <secret> --target-type ... --target <id> [--expires 24h]`, `hanko secret delegation ls` and `hanko secret delegation revoke <id>`. The API did not change.

### Cofre: variáveis de ambiente da persona pela tela

- A página da persona ganhou a seção "Variáveis de ambiente": cada variável do ambiente do adapter mostra o nome e a referência a segredo (`${secret:NOME#campo}` ou, para campo arquivo, `${secretfile:NOME#campo}`). O admin põe, troca e tira variáveis pela tela; a referência precisa existir na empresa. Valor em claro de uma versão antiga aparece como `***` e não sai da página. A tela não aceita valor em claro.

- English summary: the persona page gained an "Environment variables" section: each variable of the adapter's environment shows its name and its secret reference (`${secret:NAME#field}` or, for a file field, `${secretfile:NAME#field}`). Admins set, change and remove variables from the screen; the reference must exist in the company. A plain value from an older version shows as `***` and never leaves the page. The screen does not accept plain values.

### Contrato da v0.8, grupo 2: tipos de domínio e API do custo e dos alertas externos em 501

- O grupo 2 do contrato da v0.8 ganha os tipos de domínio dos 17 alertas de agente externo (severidade `critical` para `host_no_data`, `destination_refusing`, `spool` e `quota`, `warning` para os outros 13), o id derivado do sujeito sem id, os limites e prazos por empresa com os valores da Oute como padrão, e `RunCostClass`, que dá a classe de custo de um run a partir de `cost_source` e da assinatura. Também ganha 7 operações no OpenAPI, que respondem 501 até a fatia 2 (#126): histórico de mensalidade, conferências de preços e ajustes de alerta. Nenhuma tela, rota ou job funciona ainda.

- English summary: contract group 2 of v0.8 gains the domain types for the 17 external-agent alerts (severity `critical` for `host_no_data`, `destination_refusing`, `spool` and `quota`, `warning` for the other 13), the derived id for subjects without an id, per-company limits and deadlines defaulting to Oute's values, and `RunCostClass`, which gives a run's cost class from `cost_source` and its subscription. It also gains 7 OpenAPI operations that answer 501 until slice 2 (#126): monthly-value history, price checks and alert settings. No screen, route or job works yet.

### Contrato da v0.8, grupo 1: tipos de domínio e API da fonte externa em 501

- O grupo 1 do contrato da v0.8 ganha os tipos de domínio (origem, classe de conversa, papel e classe de custo da chamada, tipo de chave do emparelhamento, normalização da URL do remoto) e 21 operações no OpenAPI, que respondem 501 até a fatia 1 (#125) e a fatia 2 (#126): agentes, hosts, emparelhamentos, sessões e conversas externas, uso externo e chaves de ingestão. As rotas de ingestão têm o OpenAPI próprio, `api/openapi-ingest.json`, gerado por `hanko openapi --ingest`, porque o listener público não as serve (ADR-0076). Nenhuma tela, rota ou ingestão funciona ainda.

- English summary: contract group 1 of v0.8 gains its domain types (origin, conversation class, call role and cost class, pairing key kind, remote URL normalization) and 21 OpenAPI operations that answer 501 until slice 1 (#125) and slice 2 (#126): external agents, hosts, pairings, sessions and conversations, external usage and ingest keys. The ingestion routes have their own OpenAPI, `api/openapi-ingest.json`, generated by `hanko openapi --ingest`, because the public listener does not serve them (ADR-0076). No screen, route or ingestion works yet.

### Contrato da v0.8, grupo 2: custo elevado e alertas de agente externo

- A migration 00209 cria o contrato do custo elevado e dos alertas de agente externo (ADR-0082 e 0083): origem e trava do preço de modelo, chave externa da assinatura, histórico de mensalidade (cada assinatura que já existe ganha a linha inicial com o valor atual, então nenhum "pago" de hoje muda), conferência diária de preços, os 17 tipos de alerta e os cinco sujeitos novos, e os limites e prazos de cada empresa. Os runs do Hanko não mudam no banco. Só o contrato: nenhuma tela, rota ou job funciona ainda.

- English summary: migration 00209 adds the contract for the elevated cost model and the external-agent alerts (ADR-0082 and 0083): price origin and lock, subscription external key, monthly-value history (every existing subscription gets its initial row with the current value, so no existing "paid" figure changes), the daily price check, the 17 alert types and five new subjects, and per-company limits and deadlines. Hanko runs do not change in the database. Contract only: no screen, route or job works yet.

### Contrato da v0.8, grupo 1: fonte externa e ingestão

- A migration 00208 cria o contrato da fonte externa e da ingestão (ADR-0075 a 0079, 0084 e 0086): agente, host e emparelhamento com os apelidos, sessão e conversa externas com a classificação, as tabelas de telemetria bruta particionadas por mês, as chamadas ao modelo e o agregado diário. A chave de API ganha o escopo `ingest`, presa a uma empresa, e o workspace ganha a marca de sistema para o `no-repo`. Só o contrato: nenhuma tela, rota ou ingestão funciona ainda, e nenhum dado se move.

- English summary: migration 00208 adds the contract for the external source and ingestion (ADR-0075 to 0079, 0084 and 0086): agent, host and repo pairing with their aliases, external session and conversation with classification, monthly-partitioned raw telemetry tables, model calls and the daily aggregate. API keys gain the `ingest` scope, tied to a company, and workspaces gain a system flag for `no-repo`. Contract only: no screen, route or ingestion works yet, and no data moves.

### Contrato da v0.8, grupo 3: rodada e aprovação externas, busca e disco

- A migration 00210 cria o contrato da rodada externa e da aprovação externa (ADR-0080 e 0081): ciclo, rodada, participante, etapa (só a revisão vigente, com o texto no fato da ingestão), ação, marca do Bardi (só se acrescenta), pedido do canal com as versões do script e a lista da outbox de cada host. `source_kind` entra nesses conceitos. A busca ganha a coluna gerada nas conversas e nas sessões externas (ADR-0078). O alerta de disco do Postgres (`db_disk_low`) entra na lista de alertas da empresa. A Caixa aceita os itens de pergunta da rodada e de aprovação externa. Os tipos de domínio e as operações novas respondem 501 até a fatia 3 (#127) e a fatia 2 (#126). Só o contrato: nenhuma tela, rota ou job de rodada funciona ainda.

- English summary: migration 00210 adds the contract for the external round and the external approval (ADR-0080 and 0081): cycle, round, participant, step (only the current revision, with the text in the ingestion fact), action, the Bardi mark (append-only), the channel request with the script versions and each host's outbox list. `source_kind` goes on those concepts. Search gets a generated column on external conversations and sessions (ADR-0078). The Postgres disk alert (`db_disk_low`) joins the company alert list. The inbox accepts the round question and the pending external approval items. The new domain types and operations answer 501 until slice 3 (#127) and slice 2 (#126). Contract only: no round screen, route or job works yet.

### Contrato das tags de persona e dos filtros do organograma

- A migration 00212 cria o cadastro de tags de persona por empresa (nome único sem distinguir caixa, cor de uma paleta fechada, descrição, arquivada) e a ligação persona ↔ tag, que não liga tag e persona de empresas diferentes. A persona ganha a coluna `tag_names`, que entra na busca com peso B (ADR-0087). Os tipos de domínio, a paleta, os limites e os verbos dos eventos ficam em `internal/domain/persona_tags.go`. As 11 operações novas (cadastro, arquivar, deletar, aplicar e tirar tag, recorte do organograma por tag e por nível) respondem 501 até a fatia 1. Só o contrato: nenhuma tela, comando ou tool de tag funciona ainda.

- English summary: migration 00212 adds the per-company persona tag registry (name unique case-insensitively, color from a closed palette, description, archived) and the persona ↔ tag link, which cannot join a tag and a persona from different companies. Personas get a `tag_names` column, searched with weight B (ADR-0087). Domain types, the palette, the limits and the event verbs live in `internal/domain/persona_tags.go`. The 11 new operations (registry, archive, delete, tag and untag a persona, org chart slice by tag and level) answer 501 until slice 1. Contract only: no tag screen, command or tool works yet.

### Fonte externa: agente, host e emparelhamento com workspace (fatia 1 da v0.8, #125)

- A ingestão agora liga cada fato às entidades da empresa, na mesma transação do lote: o agente externo e o host externo
  nascem do primeiro fato com esse nome (com `activity_event`), e um nome que chega com outra caixa vira apelido do mesmo
  agente. A hora do último fato do host só sobe.
- O repositório do fato se emparelha com o workspace de mesmo slug; sem ele, o Hanko cria um workspace só de metadado,
  sem caminho em disco. Fato sem repositório vai para o workspace de sistema `no-repo`. Empresa arquivada recebe o bruto
  e nenhuma entidade nova.
- Ainda não há tela, renomear, arquivar ou juntar agente e host, caixa de entrada para emparelhamento a conferir, corrigir
  emparelhamento, classificação de conversa e a conversa em si.

- English summary: ingestion now derives the external agent and host from each fact, in the same batch transaction. Agent
  names differing only in case become aliases of one agent; a host's last-seen time only moves forward. A fact's repository
  pairs with the workspace of the same slug, or gets a metadata-only workspace; facts without a repository go to the system
  `no-repo` workspace. Archived companies get raw telemetry only. No UI, rename/archive/merge, inbox items, pairing
  correction, classification or conversations yet.

### Fonte externa: agentes e hosts externos, listar, renomear, arquivar, sempre ligado e juntar (fatia 1 da v0.8, #125)

- As rotas de agente e de host externos deixam de responder 501: listar, ver, renomear, arquivar ou voltar, marcar
  "sempre ligado" (só host) e juntar dois agentes ou dois hosts. Alterar e juntar são de owner ou admin. Cada alteração
  gera evento de atividade. O nome antigo vira apelido do mesmo agente ou host, e o agente ou host juntado fica
  arquivado com os apelidos, as sessões e as conversas no que ficou.
- A junção recusa agente ou host que fica arquivado (409), trava as duas linhas, preserva o horário de arquivamento do juntado, e move também as chamadas do modelo e as aprovações. Renomear para um texto que já é apelido de outro agente ou host dá 409.
- Ainda não há telas, o alerta `host_no_data`, a troca de workspace de um emparelhamento, nem classificação de conversa.

- English summary: external agent and host routes stop answering 501: list, get, rename, archive/unarchive, always-on
  (host only), and merge two agents or two hosts. Changes are owner/admin only and each writes an activity event. The
  old name becomes an alias of the same entity, and the merged entity is archived with its aliases, sessions and
  conversations moved to the one that stays. No screens, no `host_no_data` alert, no repository-pairing change yet.

### Fonte externa: filtro de segredo no decision model (#125)

- Texto com padrão de segredo colado (chave privada, chave de API, token do GitHub, chave AWS, token do Slack, chave do Google, JWT, Bearer, URL com senha, atribuição de senha ou token) não sai do Hanko em nenhuma feature. A invocação vira erro `secret_detected`, com só o tipo do padrão no ledger e no evento `decision.invoked`, nunca o trecho (ADR-0079, item 6).
- English summary: the decision model refuses prompts that contain a secret pattern, in every feature, before calling the provider; the ledger and the event keep only the pattern kind.

### Gerador único de nome

- O Hanko ganha o gerador único de nome (ADR-0084), sobre as listas de 256 + 256 palavras aprovadas: o nome derivado do id (`ExternalName`, com 16 variações e, no fim, o sufixo de 3 dígitos), a escolha do nome livre da empresa (`ChooseExternalName`) e o sorteio entre os livres (`DrawExternalName`). A conversa externa e a rodada externa passam a poder ter nome; a ligação delas ao gerador vem no PR seguinte da fatia.

- English summary: Hanko gains the single name generator (ADR-0084) over the approved 256 + 256 word lists: the name derived from the id (`ExternalName`, with 16 variations and, last, the 3-digit suffix), picking a name the company has free (`ChooseExternalName`) and drawing among the free ones (`DrawExternalName`). External conversations and rounds can now have names; wiring them to the generator comes in the slice's next PR.

### Ingestão de telemetria de agente externo: o núcleo (fatia 1, 1/5)

- O Hanko passa a ter um listener próprio de ingestão (`HANKO_INGEST_ADDR`, desligado por padrão) que recebe o OTLP/HTTP em protobuf (com gzip) de `/v1/traces`, `/v1/logs` e `/v1/metrics`, o replay do bucket em JSON de `/v1/replay/{sinal}` e a prontidão em `/v1/ready`. Só aceita a chave de escopo `ingest`, uma por host e presa à empresa, que se cria, lista e revoga em `/api/v1/companies/{company}/ingest-keys` (owner ou admin). As linhas e a chave de dedupe reproduzem byte a byte as do agent-studio, conferidas por vetores gerados do código original. O fato repetido não duplica nada, o 2xx só vem depois do commit, e as partições mensais das tabelas de telemetria são criadas na partida e a cada 24 h. A resolução de agente e host, a conversa e o custo vêm nos PRs seguintes da fatia.

- English summary: Hanko now has its own ingestion listener (`HANKO_INGEST_ADDR`, off by default) that receives OTLP/HTTP protobuf (gzip allowed) on `/v1/traces`, `/v1/logs` and `/v1/metrics`, the JSON bucket replay on `/v1/replay/{signal}` and readiness on `/v1/ready`. It only accepts the `ingest`-scoped key, one per host and tied to a company, created, listed and revoked at `/api/v1/companies/{company}/ingest-keys` (owner or admin). Rows and dedupe keys reproduce the agent-studio's byte for byte, checked against vectors generated from the original code. A repeated fact never duplicates, 2xx only comes after commit, and the monthly partitions of the telemetry tables are created at startup and every 24 h. Agent and host resolution, conversations and cost come in the next PRs of the slice.

### Fonte externa: junção soma o agregado diário e escolhe a lista da outbox (#153)

- Juntar agente ou host soma os contadores do agregado diário (`calls_daily`) do juntado nas linhas do que fica com a
  mesma chave (dia, host, agente, workspace, modelo, assinatura). Linha sem colisão só troca de dono. Repetir a junção não soma de novo.
- Juntar host escolhe a lista da outbox (`external_outbox_snapshot`) de hora mais nova; em empate fica a do que fica.
- English summary: merging an external agent or host sums the juntado's daily aggregate (`calls_daily`) into the
  surviving rows with the same key (day, host, agent, workspace, model, subscription); rows without collision just change owner. Merging a host keeps the newest outbox
  snapshot, and on a tie the surviving host keeps its own. Repeating a merge does not sum again.

### Palavras do gerador único de nome

- O Hanko ganha as listas do gerador único de nome (ADR-0084): 256 adjetivos e 256 substantivos em inglês, as 64 + 64 do oute mais 192 + 192 novas, em `internal/domain/names/names.txt`. Ainda não há gerador: o arquivo é o dado que ele vai ler, e um teste confere o formato, a contagem e a unicidade.

- English summary: Hanko gains the word lists of the single name generator (ADR-0084): 256 adjectives and 256 nouns in English, the oute's 64 + 64 plus 192 + 192 new ones, in `internal/domain/names/names.txt`. There is no generator yet: the file is the data it will read, and a test checks format, count and uniqueness.

### Fonte externa: sessão externa a partir da ingestão (fatia 1 da v0.8, #125)

- A ingestão agrupa os fatos pelo `oute.task.id` que a fonte declara e cria a sessão externa da empresa, uma vez, com
  `activity_event`. Repositório, agente e rodada da sessão seguem a hora do fato mais antigo que os trouxe (migration
  00213). Fato sem `oute.task.id` não cria sessão.
- Ainda não há conversa externa, gerador de nome, classificação, tela nem leitura.

- English summary: ingestion groups facts by the `oute.task.id` the source declares and creates the external session
  of the company once, with `activity_event`. Repository, agent and round follow the oldest fact that carried them
  (migration 00213). A fact without `oute.task.id` creates no session. No conversation, name generator, classification,
  screen or reading yet.

### Tags de persona: cadastro, aplicação, recorte do organograma, CLI, MCP e export

- As personas ganham tags de um cadastro por empresa (ADR-0087): criar, editar, arquivar, desarquivar e deletar (com a contagem de personas que perdem a tag), aplicar, tirar e criar e aplicar numa transação só. Os papéis valem no service (viewer lê, member escreve, admin arquiva e deleta), e nenhuma persona ou run token escreve tag. Toda escrita grava o seu `activity_event`, e a busca acha a persona pelo nome da tag. A API lista o recorte do organograma por tag (alguma, todas ou sem tag), por nível e por profundidade. A CLI tem `hanko persona tag`, o MCP mostra as tags em `hanko_persona_list` (com filtro) e em `hanko_whoami`, e o export e o import da empresa levam o cadastro e as ligações. As telas vêm num PR seguinte.

- English summary: personas get tags from a per-company registry (ADR-0087): create, edit, archive, unarchive and delete (with the count of personas that lose the tag), apply, remove, and create-and-apply in one transaction. Roles apply in the service (viewer reads, member writes, admin archives and deletes), and no persona or run token writes tags. Every write records its `activity_event`, and search finds the persona by the tag name. The API lists the org chart slice by tag (any, all or untagged), by level and by depth. The CLI has `hanko persona tag`, MCP shows the tags in `hanko_persona_list` (with a filter) and in `hanko_whoami`, and the company export and import carry the registry and the links. The screens come in a following PR.

### Tags de persona: edição em lote na lista de Personas

- A lista de Personas ganha a seleção múltipla, com "marcar todas", e uma barra para aplicar ou tirar uma tag das personas marcadas. Ao aplicar, um nome novo cria a tag. O lote roda numa transação só e grava um evento por persona alterada; se uma persona não puder receber a tag (teto de 20 ou tag arquivada), nenhuma recebe (ADR-0087, fatia 2).

- English summary: the Personas list gets multiple selection, with "select all", and a bar to apply or remove a tag from the selected personas. When applying, a new name creates the tag. The batch runs in a single transaction and records one event per changed persona; if one persona cannot take the tag (the 20-tag cap or an archived tag), none does (ADR-0087, slice 2).

### Tags de persona nas telas: aba Tags, ficha, lista e filtros do organograma

- Personas ganha a aba Tags, com criar, editar, arquivar, desarquivar e deletar (a confirmação diz quantas personas perdem a tag). A ficha da persona mostra as tags, tira uma tag e aplica pelo nome; um nome que não existe vira "criar tag «nome»". A lista de Personas e os nós do organograma mostram até 3 chips coloridos e "+N". No organograma, o menu de cada nó aplica, tira e cria tags, e os filtros recortam por tag (alguma, todas ou sem tag), por nível e por profundidade, nos modos Destacar e Podar mantendo o caminho. O filtro fica na URL e no cookie `hanko_org` (ADR-0087). O import da empresa passa a gravar o evento de cada tag criada e de cada tag aplicada.

- English summary: Personas gets a Tags tab to create, edit, archive, unarchive and delete tags (the confirmation says how many personas lose the tag). The persona page shows its tags, removes one and applies one by name; a name that does not exist becomes "create tag «name»". The Personas list and the org chart nodes show up to 3 colored chips and "+N". On the org chart, each node's menu applies, removes and creates tags, and the filters slice by tag (any, all or untagged), by level and by depth, in Highlight and Prune-keeping-the-path modes. The filter lives in the URL and in the `hanko_org` cookie (ADR-0087). The company import now records the event of each tag it creates and each tag it applies.

## [0.7.2] — 2026-10-25

### Cofre: pendências do #72 e do #86

- Aviso na contratação: quando o `adapter_config` da persona referencia um `${secret:…}` que ela não alcança, a API (campo `warnings` da persona), a CLI e a página da persona avisam. É aviso, não bloqueia: a concessão pode vir depois, e a run só falha no despacho.
- Uso no despacho: a resolução de `${secret:…}` do `adapter_config` e do env dos servidores MCP grava o uso do segredo da empresa em `secret_access_log` (via `dispatch`), com a run e a persona. O valor não entra no registro.
- Coleção na criação: `hanko secret create --collection`, o campo `collection` da API e a escolha no formulário da UI. Sem escolha, o segredo entra na coleção padrão.
- Remover o que não existe responde "não encontrado" (404), em vez de sucesso: remover um segredo de uma coleção que não o tem, ou apagar uma coleção que já não existe.

- English summary: vault leftovers from #72 and #86: a warning on hire when the persona's adapter_config references a secret it cannot reach (API `warnings`, CLI and persona page; not blocking); dispatch-time use of company secrets (adapter_config and MCP env) recorded in secret_access_log (via dispatch, no value); collection choice on secret creation (`hanko secret create --collection`, API `collection`, UI form; default when omitted); 404 instead of success when removing a secret from a collection that does not hold it or deleting an already-deleted collection.

### Cofre: exportar e importar (.hkv) e importar .env aceitam prova por Google e passkey (#92)

- Quando o servidor pede prova recente, as telas de exportar e importar o cofre cifrado (`.hkv`) e a de importar `.env` oferecem "Confirmar identidade": o mesmo diálogo do perfil (senha, passkey ou Google). Depois da prova, envie o formulário de novo sem a senha da conta (ADR-0070 §3). Antes, só a senha da conta valia nessas telas.
- English summary: the encrypted-vault (`.hkv`) export and import screens and the `.env` import screen now accept a passkey or Google (Firebase) proof, through the profile's proof dialog, besides the account password.

### Perfil: prova recente pelo Google para contas do Firebase (#98)

- Em **Meu perfil › Passkeys**, a conta do provider `firebase` confirma o diálogo "Confirme sua identidade" pelo Google (login do SDK, ID token com `auth_time` recente) e a ação que abriu o diálogo é repetida. Antes, a conta só via "Entre de novo para confirmar" e não cadastrava a primeira passkey (ADR-0070 §3).
- Conta `local` e conta com passkey seguem como antes.
- English summary: on the profile page, Firebase accounts confirm the "Confirm your identity" dialog with a Google sign-in through the Firebase SDK (ID token with recent `auth_time`), and the action that opened the dialog is repeated. Before, these accounts could not register their first passkey (ADR-0070 §3).

## [0.7.1] — 2026-10-25

### Cofre: campo arquivo pela CLI, varredura de pastas e migração entre drivers (fatia 3, #58)

- CLI `hanko secret field add <segredo> <campo> --kind file --file <caminho>`: envia o arquivo (de 1 byte a 1 MiB) cifrado, como os demais campos arquivo. Campo arquivo é sempre secreto e não leva `--value`, `--generate` nem `--visible`; a recusa sai antes de chamar o servidor. Com `--user`, vale para o segredo pessoal.
- Boot: apaga as pastas `hanko-secret-*` deixadas em `/tmp` por runs que morreram no meio, quando mais velhas que `run_timeout` + 1 h. Erro vira aviso no log e não derruba o boot (ADR-0073).
- `hanko secret rekey` recifra os arquivos dos campos um a um, em páginas de 100 ids. Antes, carregava o conteúdo de todos de uma vez.
- `hanko blobs migrate` move também os arquivos dos campos entre postgres, fs e s3, com conferência byte a byte e retomada. Enquanto a migração corre, a leitura acha o arquivo no driver que o tem.
- Sair da empresa apaga do driver de blob (fs ou s3) os arquivos dos segredos pessoais, e não só as linhas do postgres.
- English summary: `hanko secret field add --kind file --file` uploads a file field (up to 1 MiB) sealed like the other file fields, with `--user` for personal secrets; the boot sweep removes stale `hanko-secret-*` directories older than `run_timeout` + 1 h (ADR-0073); `hanko secret rekey` re-seals file fields one by one in pages of 100 ids; `hanko blobs migrate` also moves file-field content between postgres, fs and s3, with byte-by-byte verification and resume; leaving a company deletes personal file-field objects from fs and s3 after commit.

### Cofre para agentes: delegação só com o alvo aberto e uso do segredo da empresa (fatia 5, #60)

- Delegação de segredo pessoal deixa de valer enquanto a task está fechada (feita ou cancelada) ou a conversa arquivada; reaberta, volta a valer (ADR-0068 §6.3).
- `GET /companies/{company}/secrets/{name}/usage` responde: onde e quando um segredo da empresa foi usado. Vê o admin e o membro com `view` ou `manage` numa coleção que contém o segredo.
- O fato `delegation.expired` não é emitido; o motivo está no ADR-0068 §6.
- Sem migration.
- English summary: a personal-secret delegation stops working while its task is closed (done or cancelled) or its conversation is archived, and works again when reopened (ADR-0068 §6.3). `GET /companies/{company}/secrets/{name}/usage` now lists where and when a company secret was used; admins and members with `view` or `manage` on a collection that holds the secret can see it. The `delegation.expired` fact is not emitted; ADR-0068 §6 says why. No migration.

### Cofre para agentes: envio com link, gestão e tool da persona (fatia 4, #59)

- Envio de arquivo: `POST /companies/{company}/sends` aceita `kind: file` com o tamanho do arquivo (até 25 MiB), sem conteúdo; o conteúdo cifrado sobe em `PUT /companies/{company}/sends/{id}/content`, só de quem criou o envio, com o tamanho declarado mais a cifra. Subir de novo troca o conteúdo.
- Gestão do envio: `GET /companies/{company}/sends` (admin vê todos; membro, só os que criou), `GET …/sends/{id}` e `DELETE …/sends/{id}` (apaga o conteúdo antes do prazo, pelo criador ou pelo admin). O blob sai junto com o conteúdo, no prazo, no limite de acessos ou no pedido.
- Envio de arquivo ainda não abre pelo link: a página recebe recusa clara, sem contar acesso. A entrega do arquivo para a página fica para a fatia seguinte.
- Tool MCP `hanko_send_create(item, campo?, expira_em?, max_acessos?)`: a persona cria um envio de um campo que alcança e recebe o link, sem digitar o valor. Política `ask` por padrão, como `hanko_secret_get`; pedido inválido nem abre aprovação. O link entra na redação do log da run.
- English summary: encrypted file sends (up to 25 MiB, uploaded after creation by the sender only), send list, read and delete by role (admin sees all, members their own), and the persona MCP tool `hanko_send_create` with an `ask` default policy. File sends do not open through the public link yet.

### Cofre: tela para exportar e importar o cofre cifrado (.hkv) (fatia 7, #62)

- Configurações › Segredos › Cofre cifrado (.hkv) (só admin): exporta o cofre com uma senha do arquivo (mínimo de 12 caracteres) e baixa o `.hkv`; importa um `.hkv` de outra instância com a senha do arquivo. As duas operações pedem a senha da conta quando o servidor exige reautenticação recente.
- Importar grava tudo ou nada: senha errada ou arquivo alterado não grava nada. Segredo que já existe não é trocado.
- Nenhuma senha volta à página, e o arquivo sai só na resposta do download.
- English summary: a new Settings › Secrets › Encrypted vault (.hkv) page (admins only) exports the vault as a downloadable `.hkv` file under a file password (at least 12 characters) and imports a `.hkv` from another instance with that password. Both ask for the account password when the server requires recent re-authentication. Imports are all-or-nothing, a wrong password writes nothing, and no password is echoed back.

### Cofre: marca de senha em campo (fatia 6, #61)

- Campo pode ser marcado como senha (ADR-0074): entra no achado "fraco" do relatório de saúde mesmo com outro nome (`chave`, `pin`). Vale só para campo texto secreto. Marca via `POST …/fields`, `PATCH …/fields/{field}` e `hanko secret field add|set` (`--password`, `--no-password`).
- Campos `password` e `senha` continuam no relatório pelo nome, como antes.
- English summary: a vault field can be marked as a password (ADR-0074). Marked fields join the weak-password finding of the health report even with another name. Only secret text fields can be marked. Set with `POST …/fields`, `PATCH …/fields/{field}` or `hanko secret field add|set` (`--password`, `--no-password`).

### Cofre para agentes: ações sensíveis da fatia 1 ligadas à reautenticação (#56)

- Doze operações do contrato (fatia 0, #55) saem do 501 e passam a responder: revelar campo, listar histórico, revelar versão anterior, restaurar versão e apagar campo, em `secrets` e em `user-secrets`; criar e revogar concessão de coleção em `secret-collections/{coleção}/grants`.
- Todas são ações sensíveis (ADR-0070 §1) e exigem reautenticação recente no service: sessão sem prova recebe `401 reauth_required` com a janela; API key de escopo `full` e modo trusted passam (ADR-0070 §5).
- Revelar segue uma vez por versão (campo e versão do histórico); restaurar cria versão nova; apagar campo leva o histórico junto e o campo `value` não se apaga.
- Com isso, uma concessão de coleção nova pode ser criada por HTTP: até esta versão, as personas só alcançavam segredos pela coleção `padrão` (concedida a todas).
- Sem migration.
- English summary: twelve contract operations now respond instead of 501 — reveal field, list history, reveal and restore a previous version, and delete a field (for both company and personal secrets), plus creating and revoking a collection grant. All are sensitive actions under ADR-0070 and require recent re-authentication in the service (401 `reauth_required` without it; full-scope API keys and trusted mode pass). Reveal stays once per version, restore still creates a new version, and deleting a field drops its history while the `value` field cannot be deleted. No migration.

## [0.7.0] — 2026-10-25

### Cofre para agentes: campos, coleções e histórico (fatia 1, #56)

- Segredo com N campos: `${secret:ITEM#campo}` lê um campo; `${secret:ITEM}` continua lendo o campo `value`. Campo visível (aparece para member) ou secreto (nunca aparece na listagem).
- Coleções com concessões a persona, subárvore do organograma, todas as personas ou humano (`use`, `view`, `manage`). Na configuração (`adapter_config`) da persona, um item que ela não alcança falha a run antes do adapter, com o nome do item e da persona, nunca o valor.
- Migração 00204: cada segredo ganha o campo `value` com o mesmo material; cada empresa ganha a coleção `padrão`, concedida a todas as personas. Nada que roda hoje quebra.
- Histórico por campo com retenção de 5 versões ou 90 dias (configurável em `secrets.history_keep_versions` e `secrets.history_keep_days`); restaurar uma versão cria versão nova.
- API: criar, listar, mudar e trocar valor de campo; CRUD de coleção e segredos da coleção. Revelar campo, histórico, restaurar e conceder ou revogar concessão exigem reautenticação (#57) e seguem em 501.
- CLI `hanko secret field …` e `hanko secret collection …`; tela de campos e coleções em Configurações › Segredos.
- English summary: vault fields and collections (ADR-0068 §1–§3) — secrets with N fields, collections with use/view/manage grants, migration 00204 copies each secret into the `value` field and creates the default collection granted to all personas, per-field history with 5-version/90-day retention, and restore as a new version. Reveal, history, restore and grants wait for re-authentication (#57) and stay 501 in the API.

### Cofre para agentes: contrato (ADR-0068, 0069, 0070)

- ADRs do cofre para agentes (segredos com campos, coleções, `hanko_secret_get`, TOTP, campo arquivo, histórico, delegação de segredo pessoal, saúde, import de `.env` e export cifrado), do envio com chave no fragmento do link e da reautenticação recente com passkey.
- Migration 00203: tabelas novas do cofre, do envio e das passkeys; colunas `secrets.rotate_after_days` e `sessions.reauth_at`/`reauth_method`; alerta `secret_rotation_due`. Nenhum dado existente é movido e nada muda no comportamento atual.
- API: 54 operações novas no OpenAPI (tag `vault`, `auth` e `public`), que respondem 501 até cada fatia ser entregue.
- O pacote do cofre externo passou de `internal/vault` para `internal/extvault`; `provider = "vault"` na API e no banco não muda.
- English summary: vault-for-agents contract — ADRs 0068–0070, migration 00203 (schema only, no data moved), 54 new API operations that return 501 until each slice ships, and `internal/vault` renamed to `internal/extvault`.

### Cofre para agentes: delegação de segredo pessoal e saída do humano (fatia 5, #60)

- O dono de um segredo pessoal o delega às runs de uma task ou conversa que ele pediu, ou a uma run em curso (`POST /companies/{company}/user-secrets/{name}/delegations`). A delegação vale por 24 h por padrão, no máximo 7 dias, ou até ser revogada (`DELETE /companies/{company}/me/delegations/{id}`). Só quem pediu o trabalho delega a ele.
- `${secret:user/NOME}` no texto que a run recebe e `hanko_secret_get` com `item = user/NOME` só resolvem com delegação válida do dono da task ou da conversa. Sem ela, ou numa run de outro humano, a run falha cedo com o nome da referência e nunca com o valor. A delegação é conferida de novo depois do gate da tool.
- Cada uso entra no registro do item com a delegação; o dono o vê em `GET /companies/{company}/user-secrets/{name}/usage` (quando, em que run e por qual persona).
- Saída do humano da empresa: os segredos pessoais dele são apagados na mesma transação, as delegações caem junto, e o fato `secret.user_purged` registra quantos itens e quais delegações ativas havia (ids, alvo e prazo; sem nome nem valor).
- English summary: owners delegate personal secrets to the runs of a task or conversation they requested (default 24 h, max 7 days, revocable); `${secret:user/NAME}` and `hanko_secret_get user/NAME` resolve only with a valid delegation from the requester of the run's work; uses are logged per delegation and shown to the owner; removing a member deletes their personal secrets and records the active delegations in `secret.user_purged`.

### Cofre para agentes: envio por link com a chave no fragmento (fatia 4, #59)

- Envio de texto por link `/s/<id>#<chave>`: o servidor guarda só o texto cifrado (AES-256-GCM). A chave vai só no fragmento, que o navegador não envia; não aparece no banco, nos eventos nem no log.
- Criação por humano pela API (exige reautenticação recente): o navegador ou a CLI gera a chave, cifra e envia só o texto cifrado.
- Criação por persona pelo service: `item#campo` de coleção concedida à persona; o servidor cifra, monta o link e descarta a chave. A tool MCP `hanko_send_create` (política `ask`) fica para a entrega seguinte.
- Página de recebimento `/s/<id>`, sem conta: pede a senha quando há, abre pela API pública e decifra no navegador com WebCrypto. Senha errada não devolve o texto e, depois de 5 erros, bloqueia o envio por 15 minutos.
- Expira por prazo e por limite de acessos; o último acesso já apaga o conteúdo, e o job `send_purge_tick` (a cada 5 min) apaga o que ninguém abriu.
- Ainda não entram nesta entrega: envio de arquivo (upload do blob), listar, consultar e apagar envios, a tool MCP da persona, a tela e o comando da CLI de criação, e o limite de taxa da rota pública (ADR-0058).
- English summary: secure send by link (ADR-0069, #59): text ciphertext only on the server, key in the URL fragment, human creation via API with recent re-authentication, persona creation from a granted `item#field` (server encrypts and drops the key), password proof with lock after 5 failures, expiry by time and access count with purge, and a public page that decrypts in the browser with WebCrypto. File sends, list/get/delete, the persona MCP tool, the UI/CLI creation and the public-route rate limit are still pending.

### Cofre para agentes: import de .env e export cifrado entre instâncias (fatia 7, #62)

- Import de `.env` (`hanko secret import-env ARQUIVO|-`, tela Configurações › Segredos › Importar .env): comentários, `export`, aspas simples e duplas e linhas vazias. Um segredo por variável, ou um segredo só com um campo por variável (`--item NOME`; a primeira variável vira o campo `value`). A prévia mostra o mapeamento, os conflitos de nome e as linhas ignoradas, nunca valores; `--collection` escolhe a coleção de destino e `--overwrite` rotaciona o que já existe em vez de pular. Gravar exige admin e reautenticação recente; é tudo ou nada.
- Export cifrado do cofre (`hanko secret export --out cofre.hkv`) e import em outra instância (`hanko secret import cofre.hkv`): arquivo `.hkv` com AES-256-GCM sob chave PBKDF2-SHA256 (600 000 iterações) derivada da senha (mínimo de 12 caracteres, lida do stdin com `--password-stdin` ou de `HANKO_VAULT_PASSWORD`). Leva segredos com todos os campos (arquivo inclusive), coleções e concessões; nomes e valores ficam dentro do conteúdo cifrado. Senha errada ou arquivo alterado falha sem gravar nada. Segredo que já existe no destino fica como está. Concessão a persona ou humano só vale se o sujeito existir no destino, e só em coleção que o import criou.
- API: `POST /companies/{company}/vault/import-env/preview`, `…/vault/import-env`, `…/vault/export` e `…/vault/import`. O `hanko export` de empresa continua levando só referências (ADR-0022).
- English summary: `.env` import (CLI and web) with a preview of the mapping, name conflicts and the target collection, and an encrypted vault export (`.hkv`, AES-256-GCM, PBKDF2-SHA256 at 600,000 iterations) that imports into another instance all-or-nothing; a wrong password writes nothing. Both writes need an admin and recent re-authentication. The company `hanko export` still carries references only.

### Cofre para agentes: reautenticação recente e passkey (ADR-0070)

- Ação sensível pede prova recente de identidade: senha (provider `local`), token do Firebase com `auth_time` recente (provider `firebase`) ou passkey. A janela padrão é de 5 minutos e pode ser mudada com `HANKO_REAUTH_MINUTES` (1 a 60). Sem prova, a API responde 401 com `reauth_required`, e a tela abre o pedido de prova.
- Passkey por usuário na tela de perfil (até 10 por usuário), com verificação de usuário obrigatória e contador de assinatura conferido. Cadastrar e remover passkey também exigem prova recente.
- API key de escopo `read` recebe 403 em ação sensível; a de escopo `full` passa, e o fato `auth.*` registra o id da chave.
- Fatos `auth.reauthenticated`, `auth.passkey_added` e `auth.passkey_removed` vão para cada empresa ativa do usuário.
- English summary: sensitive actions now require a recent identity proof (password, Firebase `auth_time`, or passkey) for 5 minutes by default (`HANKO_REAUTH_MINUTES`); the profile page manages passkeys; read-scope API keys get 403 and full-scope keys pass with their key id on the audit fact.

### Cofre para agentes: gerador, saúde e alerta de rotação (fatia 6, #61)

- Gerador configurável (ADR-0068 §7.1): tamanho de 8 a 128, classes de caractere (minúsculas, maiúsculas, dígitos, símbolos) ou frase-senha de 3 a 12 palavras. Vale na API (`POST /api/v1/secrets/generate`), na criação e na troca de valor de segredo e de campo (`generator`), e na tela de segredos. Sem opções, continua o padrão de sempre.
- Relatório de saúde por empresa (`GET /api/v1/companies/{company}/vault/health`, na tela Configurações › Segredos › Saúde): repetidos (comparados por HMAC do valor, sem decifrar), velhos (prazo do item ou da coleção), sem uso (90 dias), órfãos (nenhuma persona, integração, ambiente, plugin, canal, gatilho, modelo de decisão ou credencial git usa o segredo) e fracos (campo `password` ou `senha` com entropia estimada abaixo de 60 bits). Sem chamada a serviço externo; só admin.
- Alerta `secret_rotation_due` (ADR-0020): um por segredo com campo além do prazo de rotação, na Caixa de entrada. Fecha sozinho quando o campo é rotacionado.
- `PATCH /api/v1/companies/{company}/secrets/{name}` muda a descrição e o prazo de rotação do item (0 tira o prazo).
- Campos locais passam a guardar o HMAC do valor. Campos criados antes desta versão só entram na comparação de repetidos depois de `hanko secret rekey`, que preenche o HMAC de todos.
- English summary: secret generator (length 8–128, character classes or a 3–12 word passphrase, in the API, the create/rotate forms and the UI; no options keeps the old default), a health report with duplicates by HMAC without decrypting, stale, unused, orphan and weak findings (admin only, no network call), a `secret_rotation_due` alert per overdue secret, and a PATCH for a secret's description and rotation deadline. Fields created before this release need one `hanko secret rekey` to fill their HMAC.

### Cofre para agentes: tool `hanko_secret_get`, TOTP e campo arquivo (fatia 3, #58)

- Tool MCP `hanko_secret_get(item, campo)`: a persona lê um campo de um item das coleções que ela alcança. A política de tools começa em `ask`: a primeira chamada abre um gate, e "sempre permitir" grava a regra da persona. Item fora das coleções é recusado antes do gate. Cada chamada fica no registro de uso, sem o valor.
- Campo TOTP: a tool devolve só o código do momento e os segundos que faltam para ele vencer. A semente não sai pela tool nem pela API, e `${secret:ITEM#totp}` é recusado.
- Campo arquivo (até 1 MiB): cifrado com o AAD do campo antes de sair do processo. No driver postgres, o texto cifrado fica na tabela nova `secret_field_blobs` (migração 00205), porque `artifact_blobs` tem FK para artefatos; nos drivers fs e s3, fica no BlobStore. Criado pela API com `content_base64`; não se revela nem se troca (apaga-se e cria-se de novo).
- `${secretfile:ITEM#campo}` no adapter da persona vira um arquivo 0600 num diretório temporário 0700, apagado ao fim da run, inclusive quando ela falha. Em ambiente SSH, a run falha cedo com "arquivo de segredo ainda não é suportado em ambiente SSH"; sandbox e cloud também recusam nesta entrega.
- O valor entregue pela tool entra na redação do log da run, e a lista de valores é esquecida ao fim dela.
- `hanko secret rekey` recifra também os arquivos dos campos; sem isso, trocar `HANKO_SECRET_KEY` deixaria os arquivos ilegíveis.
- English summary: MCP tool `hanko_secret_get` (ADR-0068 §4) with an `ask` default policy and gate, per-run redaction of the delivered value; TOTP fields return only the current code; file fields (up to 1 MiB) are sealed with the field AAD before the driver (migration 00205 adds `secret_field_blobs` for the postgres driver); `${secretfile:}` becomes a 0600 file in a 0700 temporary directory removed when the run ends, even on failure; SSH, sandbox and cloud runs refuse it for now.

## [0.6.0] — 2026-10-23

### Paridade v0.3 (ADR-0032 a 0038; migrations 00027–00034)

- Cards de status por persona (`/cards-de-status`): montagem por filtros, resumo gerado pela persona, atualização agendada, revisões; tools MCP `hanko_status_card_*`, API e CLI (ADR-0032).
- Sala com várias personas (`/sala`): participantes por conversa, turnos ordenados, API e CLI (ADR-0033).
- Pipelines e cases (`/pipelines`, `/cases`), fila de revisão (`/revisao`) e aprendizados com treino de decisão (`/aprendizados`) (ADR-0034).
- Skills: versões imutáveis com diff e restauração, versão fixada por persona, fontes git (`HANKO_SKILL_SOURCES=true`, só markdown, https público) e estúdio de teste de skills (ADR-0035).
- Integrações: catálogo de tools com quarentena, perfis de ferramentas (escopo empresa/projeto/persona, `ask` abre gate `tool`), gateway das runs, gateways nomeados com token `hkgw_`, auditoria sem argumentos em claro (ADR-0036).
- Ambientes de execução remotos: SSH (`HANKO_ENV_SSH_ALLOW_HOSTS`, known_hosts obrigatório) e sandbox em contêiner (`HANKO_ENV_SANDBOX=docker`); ambiente por persona e padrão da empresa; `openssh-client` na imagem de agentes (ADR-0037).
- Plugins de terceiros: processo externo JSON-RPC por stdio, desligado sem `HANKO_PLUGINS_DIR`, capacidades checadas pelo host, tools `plugin_<id>_<tool>`, eventos, jobs e widgets só de dados; exemplo em `examples/plugins/hello` e `docs/PLUGINS.md` (ADR-0038).
- Todos os interruptores do operador vêm desligados; migrations 00027–00034 aplicam no deploy.
- English summary: v0.3 parity features — status cards, multi-persona room, pipelines/cases/review queue/learnings, skill versions/sources/studio, tool catalog/profiles/gateways, SSH and sandbox environments, and sandboxed third-party plugins; all operator switches default to off.

### UI alinhada ao Kubo DS

- Fonte Inter Variable servida pelo próprio binário (sem CDN); tokens de cor com os nomes e valores OKLCH do Kubo DS (`--background`, `--foreground`, `--card`, `--muted`, `--border`, `--ring`, `--sidebar-*`), escuro também por `.dark`.
- Cards planos com anel (sem borda nem sombra), raios na escala `--radius-*`, tipografia só na escala `--text-*`, shell com sidebar de 256px e cabeçalho de 72px.
- Botões em pílula com tamanhos `xs`/`sm`/`lg`, hover a 80% e afundar 1px ao pressionar; inputs com fundo `input/30` e anel de foco de 3px; badge concluída = secondary.
- Glifo por persona (migration 00026 `personas.icon`, catálogo fechado de 18, padrão `bot`): API (`icon` no hire/update e na saída), export/import, CLI `--icon`, seletor na contratação e na página da persona, avatares e chips em toda a UI.
- Quadro de tasks (`/tasks?visao=quadro`) com uma coluna por status; troca de tema claro · escuro · sistema no rodapé da sidebar (`POST /tema`, cookie `hanko_theme`); Configurações saiu do grupo Organização e ficou no rodapé.
- Estilos inline dos templates viraram classes na escala de espaço do DS.
- Organograma com o glifo da persona (sprite de símbolos; SVG estático e ilha).
- Tasks: quadro com arrastar para mudar o status; lista com agrupar (status, prioridade, persona, projeto), ordenar e filtrar (persona, prioridade); aba Minhas; visão salva em cookie.
- Projeto em abas (Visão geral, Tasks em lista ou quadro, Workspace, Orçamento, Configuração) e workspace em abas (Visão geral, Tasks, Execuções, Configuração).
- Timeline do trabalho (`/timeline`): faixas por persona, barras por execução, 1/7/30 dias.
- Painel ao vivo (`/ao-vivo`): execuções rodando com o fim do log, atualização a cada 5 s.
- Configurações › Ambientes (verificações reais do servidor), Adapters (harnesses, personas, preços), Plugins (pontos de extensão) e Exportar e importar (pela API).
- Catálogo de times prontos (`/personas/catalogo`) e troca da própria senha para login local.
- English summary: the UI now follows the Kubo DS (Inter, OKLCH tokens, ring cards, radius/type scales, pill controls, focus ring), with a per-persona glyph, a light/dark/system theme toggle, a draggable task board with saved views, tabbed project and workspace pages, a run timeline, a live panel, environment/adapter/plugin settings, export/import in the UI, ready-made teams and self-service password change (ADR-0031).

### Anexos maiores: 64 MiB por arquivo, 10 arquivos e 128 MiB por envio, cota de 5 GiB (ADR-0065)
- Driver `fs` do BlobStore: artefatos e anexos em arquivos de um diretório local (`HANKO_BLOB_DRIVER=fs`, `HANKO_BLOB_DIR`), sem banco nem S3. O `hanko-prd` passa a usá-lo num volume próprio; o deploy migra o que está no Postgres, e `backup.sh`/`restore.sh` cobrem o volume.
- Sem `HANKO_ARTIFACT_MAX_BYTES`, o limite por arquivo passa a ser o teto do driver de blobs: 64 MiB com fs ou S3, 16 MiB com Postgres (antes 5 MiB).
- Um envio (chat, sala, comentário, cartão de anexos) leva no máximo 10 arquivos e 128 MiB somados; a dica do campo mostra os limites e o erro pede para mandar em partes.
- Cota padrão por empresa: 5 GiB (antes 512 MiB).
- vhost nginx do `deploy-prd.sh --edge`: aceita 130 MiB por pedido (antes 20 MiB, abaixo do envio máximo), 5 min para corpo lento e sem buffer no proxy.
- English summary: new `fs` blob driver (local directory) used by production; attachments default to the blob driver's ceiling (64 MiB on fs/S3, 16 MiB on Postgres), uploads cap at 10 files / 128 MiB, the default company quota is 5 GiB, and the production nginx vhost accepts the full upload.

### A persona enxerga imagens e PDFs anexados (ADR-0066)
- Antes de cada run, os anexos da task ou da conversa são gravados no diretório de trabalho, em `.hanko-anexos/<run>/`, e o briefing dá o caminho de cada um; a persona abre com a ferramenta de leitura de arquivos do harness (o Read do claude lê imagem e PDF). Antes, binário só vinha pelo MCP em base64, até 1 MiB, e o modelo não o enxergava.
- A pasta tem `.gitignore`, some no fim da run e tem teto de 512 MiB por run; o que não couber segue pelo `hanko_attachment_get`.
- English summary: run attachments are written to `.hanko-anexos/<run>/` in the work dir and the briefing gives each path, so personas can open images and PDFs with their file tools instead of unreadable base64.

### Compositor: colar anexo, miniaturas, Enter envia, caixa fixa e PDF grande (ADR-0066, adendo)
- Colar imagem ou arquivo (Ctrl/Cmd+V) no chat, na sala e nos comentários de task anexa e mostra a miniatura; print sem nome ganha `colado-AAAA-MM-DD-HHMM.png`.
- Anexos aparecem como miniaturas (imagem pequena é a própria miniatura; o resto, ícone e extensão), com nome e tamanho ao passar o mouse; a prévia antes de enviar tem um X para remover cada arquivo. O seletor nativo "Choose Files" some.
- Enter envia, Shift+Enter quebra a linha (no celular o Enter quebra a linha); vale também para a primeira mensagem de uma conversa nova.
- A caixa de mensagem fica fixa embaixo da tela por padrão, com um alfinete para soltá-la (a escolha fica no navegador), e setas flutuantes levam ao início e ao fim da conversa; a conversa abre no fim e acompanha mensagem nova só se você estiver perto do fim.
- PDF grande (acima de 15 MiB ou 100 páginas): o heartbeat extrai o texto e divide em partes (de até 20 páginas e ~10 MiB) antes da run, e o briefing manda ler o texto primeiro. `poppler-utils` e `qpdf` entram na imagem do container.
- English summary: paste-to-attach, attachment thumbnails, Enter-to-send, a pinned composer with start/end arrows, and large PDFs extracted to text and split into 20-page parts before a run.

### Documentos de artefato com versão MAJOR.MINOR (ADR-0064)

- Todo artefato é um documento de identidade estável (migration 00201 `artifact_documents`): alterar é publicar com o id do documento e vira a versão seguinte mesmo com outro nome, em outra task ou no chat. A persona não cria mais um documento novo para uma alteração.
- Versões numeradas **MAJOR.MINOR**: quem publica declara `major` (mudança pesada) ou `minor` (ajuste) com um motivo; o servidor mede o diff por linha e marca a versão como "classificação questionada" quando a medida contradiz o declarado. Sem declaração, vale a medida. Versões antigas viram `N.0`.
- A persona lê e restaura artefatos: tools MCP `hanko_artifact_list`, `hanko_artifact_get` (por versão, com histórico) e `hanko_artifact_restore`; `hanko_artifact_put` ganha `artifact`, `change`, `change_reason` e `tags`. O briefing da run (task, chat e sala) lista os documentos com o id para alterar, e o protocolo proíbe recriar com outro nome.
- Restaurar uma versão anterior cria uma versão nova por cima (sempre major, com a origem registrada); reclassificar renumera só a mais nova; projeto, workspace e tags livres do documento filtram a lista. Apagar uma task preserva as versões de artefato.
- UI: selos major/minor, motivo e alerta de disputa no histórico, botão Nova versão com a escolha major/minor, restaurar, reclassificar, tags e filtro por tag. CLI `hanko artifact put --artifact --change --reason --tag`, `get --version`, `restore`, `reclassify`, `tag`. API `…/artifact-documents/{documento}` e `…/artifacts/{id}/restore|reclassify`. Export/import levam documento, MAJOR.MINOR e tags (arquivos antigos continuam válidos).
- English summary: every artifact is now a document with a stable identity and MAJOR.MINOR versions; a change is declared major or minor and checked against the line diff; personas can list, read and restore artifacts through MCP and see them in the run briefing, so edits become new versions of the same document instead of new documents; restore, reclassify and free tags in the UI, CLI and API.

### Correção — botão de fixar e setas do compositor
- O botão da caixa fixa agora mostra o estado: "Fixado" (contorno e ícone preenchido) quando a caixa está presa embaixo, e "Fixar" (discreto) quando ela rola junto com a conversa.
- As setas de ir ao início e ao fim da conversa aparecem com pouca rolagem (120 px, antes 400 px): uma conversa de poucas telas já as mostra.
- English summary: the pin button now reads "Pinned" with a filled icon when the composer is pinned and "Pin" when it is not, and the start/end arrows appear after 120 px of scroll instead of 400 px.

### Correção — personas `claude` conseguem usar integrações MCP (ADR-0059)
- O claude headless negava toda tool de integração porque só `mcp__hanko` estava em `--allowedTools`. Agora os servidores que apontam para o gateway de tools também entram; perfil, política, gate e auditoria continuam decidindo cada chamada no servidor. Revisão e fase de plano não recebem a liberação.
- English summary: headless `claude` personas could not call any MCP integration (only `mcp__hanko` was allowed). Tool-gateway servers are now allowed too; the gateway still enforces profile, policy, gates and audit per call. Review and plan-only runs are unchanged.

### Correção — compositores de chat, sala e comentário da task
- O texto digitado sumia ~1 s depois da última tecla: o autosave do rascunho disparava a limpeza do formulário (o evento `htmx:afterRequest` da caixa de texto subia até o `<form>`). Agora só o envio do próprio formulário, com resposta 2xx, limpa o texto e os arquivos; um erro mostrado no aviso não apaga mais o que foi escrito.
- Anexos escolhidos ou arrastados eram descartados pelo mesmo motivo antes do envio; agora chegam com a mensagem.
- O autosave entra na fila do formulário (`hx-sync="closest form:queue last"`): o envio espera o salvamento em andamento, e nada regrava o rascunho que o envio apagou. O que for digitado ou anexado durante o envio fica na caixa.
- A lista da conversa não se atualiza durante um envio, e uma atualização em andamento é cancelada, para não trocar a mensagem nova por uma lista velha. A conversa da task passa a se atualizar sozinha enquanto há run ativa ou até 10 min depois do último comentário humano (`GET /c/{company}/tasks/{task}/conversa`, que lê só a conversa).
- Erros sem fragmento (5xx de proxy, falha de rede) aparecem no aviso da página; o botão de envio fica desabilitado durante o envio.
- English summary: chat/room/task composers no longer wipe the text and the chosen files when the draft autosaves; sends can't be hidden by an in-flight poll, the task thread refreshes while the persona works, and network/server errors show in the flash.

### Correção — `deploy-prd.sh` chamava `/hanko`, que não existe na imagem do prd
- A imagem do prd (`Dockerfile.agents`) instala o binário em `/usr/local/bin/hanko`; o deploy chamava `/hanko` para `doctor`, `update --check` e as units do systemd. O redirecionamento esvaziava `hanko-backup.service`/`.timer` antes de o comando falhar, desligando o backup diário, e o deploy terminava com erro. Agora o deploy usa o `hanko` do PATH e cada unit é gerada num `.tmp` que só substitui a atual se saiu inteira. Teste novo roda o bloco com `docker` falhando, vazio e ok.
- English summary: `deploy-prd.sh` called `/hanko`, absent from the prd image, which emptied the backup systemd units and failed the deploy. It now uses `hanko` from PATH and writes units atomically; a new test covers failure, empty output and success.

### Correções do `hanko doctor` e do `hanko update --check`

- `hanko doctor`: a checagem do ai-memory usa o mesmo teste do servidor (o `/health`, se a imagem tiver, senão a tool MCP `memory_status`). A imagem atual não expõe `/health` e o doctor avisava "HTTP 404" com a memória saudável.
- `hanko update --check` num build de commit (como o do `deploy-prd.sh`) não anuncia mais "versão nova" quando a release do canal é anterior; diz que é build de desenvolvimento e mostra a versão do canal. O JSON ganha `dev`.
- English summary: `hanko doctor` checks ai-memory the way the server does (falls back to the `memory_status` MCP tool when the image has no `/health`), and `hanko update --check` no longer suggests a "newer" stable release to a commit build.
- Release: o cask do Homebrew usa `hooks.post.install_steps` (passos declarativos) no lugar do `install` em Ruby, depreciado no goreleaser v2.19, que reprovava o `goreleaser check` do CI.

### Correção — integração MCP nova nasce fechada para escrita (ADR-0067)
- O formulário de servidor MCP novo não vem mais com "Vale para todas as personas" marcado e avisa que persona sem perfil recebe todas as tools.
- Integração criada a partir de agora: a primeira descoberta do catálogo põe em quarentena as tools que escrevem, apagam ou não declaram o risco (inclusive quando a descoberta acontece na primeira execução). Integrações já existentes mantêm a linha de base.
- Regra por risco do perfil exige escolha explícita; escolha vazia ou desconhecida é recusada em vez de virar "Permitido".
- English summary: new MCP integrations no longer default to all personas; their first catalog discovery quarantines write/destructive/unknown tools (existing integrations keep the baseline); profile risk rules require an explicit choice.

### Correção — migrations fora de ordem numérica
- O goose recusava subir quando uma migration de número menor chegava depois de uma maior já aplicada (`detected N missing (out-of-order) migrations`), o que as faixas de numeração por marco (00050, 00080, 00090, 00100, 00120…) tornam inevitável. `db.Migrate` agora aceita migrations fora de ordem; teste reproduz o banco na 00100 recebendo a 00050, 00080 e 00090.
- English summary: `db.Migrate` now allows out-of-order migrations (goose `WithAllowOutofOrder`), needed because milestones reserve numbering ranges that land out of numeric order; covered by a test that starts from a database at version 100.

### Correção — `HANKO_PLUGINS_DIR` e `HANKO_PLUGINS_DATA_DIR` agora são lidas do ambiente
- O carregador de config ignorava essas duas variáveis (só valiam no YAML), então os plugins de terceiros nunca ligavam por ambiente. Teste novo garante que toda tag `env` de `Config` é lida.
- English summary: `HANKO_PLUGINS_DIR`/`HANKO_PLUGINS_DATA_DIR` were silently ignored when set via environment; fixed, with a guard test covering every `env` tag.

### Correção — seta no centro da conversa e Feed do time fixo
- A seta de ir ao início/fim agora é uma só, centrada na coluna da conversa logo acima da caixa: na metade de cima aparece a que desce ao fim, na de baixo a que sobe ao início. Conversas curtas não mostram seta.
- O Feed do time (coluna da direita da sala) fica fixo ao rolar por padrão, com o mesmo botão Fixar/Fixado da caixa de conversa; a escolha fica no navegador.
- English summary: a single scroll arrow now sits centered over the conversation above the composer (down in the upper half, up in the lower half), and the Team feed column stays pinned by default with a Pin/Pinned toggle.

### M10 — Workspace de execução com Git, padrões de projeto e "Nova sessão" (ADR-0054)

- Workspace com Git (migration 00060, `HANKO_GIT_WORKSPACES`, desligado por padrão): o admin liga o Git de um workspace que tem repositório (`hanko workspace git enable`) e o servidor mantém um **clone espelho** (bare) sob `<raiz>/<empresa>/.hanko/mirrors`. A credencial é uma referência `${secret:NOME}` (ADR-0050) resolvida só na busca: token https por cabeçalho no ambiente do processo, chave ssh em arquivo 0600 temporário; o valor não aparece em log, atividade, erro nem URL gravada. Endereço validado (https/ssh/scp, host interno recusado), `git` do sistema por exec sem shell.
- **Worktree + branch por task**: ao abrir uma run, o executor cria o worktree `hnk-123` a partir da base do remoto e roda a persona dentro dele (commits com o nome da persona; o prompt diz a branch e proíbe push). Estados provisionando/pronto/falhou/divergente/removido, reconciliação (pasta sumiu → recria reaproveitando a branch; branch trocada → aviso e estado divergente). Ao concluir/cancelar a task, ou quando a branch é integrada à base, o tick `worktree_tick` tira a pasta do disco (trabalho solto vira um commit de segurança; a branch fica no espelho até ser integrada).
- **Aba Git** no workspace, no projeto e na task (`/c/<empresa>/tasks/HNK-123/git`): status, diff contra a base, log, à frente/atrás/integrada e um **browser de arquivos somente leitura** (confinado ao worktree, `.git` oculto, binário sem conteúdo, 256 KiB por arquivo). API `…/workspaces/{ws}/git`, `…/tasks/{task}/git/{diff,log,tree,file}`, `…/projects/{p}/git`; CLI `hanko workspace git show|enable|sync|disable`, `hanko task git status|diff|log|ls|cat|clean`, `hanko project git`.
- **Padrões de projeto** (#34): instruções do projeto entram no system prompt depois das da persona; meta padrão herdada pelas tasks novas; import automático das skills do repositório clonado (reusa as fontes de skills do ADR-0035, lendo do espelho, sem rede); a política de revisão (ADR-0040) ganhou formulário na aba Padrões do projeto. `hanko project defaults`, `PATCH …/projects/{p}/defaults`.
- **Nova sessão** (#16): `hanko task new-session`, `POST …/tasks/{task}/new-session`, botão no painel da task. O próximo run consome o pedido (registrado na atividade, `task.session_reset` e `run.session_reset_applied`) e o briefing avisa que não há conversa anterior. As runs de task já começavam sessão nova; o marcador é o contrato para a continuação de runs (#12).
- Env nova: `HANKO_GIT_WORKSPACES`. Pasta `.hanko` da empresa reservada (workspace com `path` dentro dela é recusado). Dependência Go nova: nenhuma; requer `git` 2.31+ no PATH quando ligado.
- English summary: opt-in execution workspaces with Git (`HANKO_GIT_WORKSPACES`): a mirror clone of the workspace repository (credential = a company secret reference, never logged), one worktree and branch per task (`hnk-123`) that the persona commits in, states provisioning/ready/failed/diverged/removed, cleanup when the task closes or its branch is merged, a Git tab (status, diff, log, read-only file browser) on workspace, project and task; project defaults (instructions after the persona's, default goal, repository skills import reusing skill sources, review policy form); and a task "New session" action recorded in the activity (ADR-0054, revises ADR-0025).

### M11 — Ambientes de execução, readiness e catálogo de modelos (ADR-0055)

- Ambientes (#35, sobre o ADR-0037): o **local** passa a poder ser cadastrado explicitamente (`hanko env create servidor --driver local`), para forçar o local numa persona ou projeto quando o padrão da empresa é remoto e para dar variáveis ao harness local. O driver `sandbox` (docker) agora fala com o motor por uma interface (`execenv.DockerClient`), testada sem daemon. Novo driver **`cloud`** (sandbox em nuvem, provider **Daytona** escolhido por critério no ADR): um sandbox por execução, acesso pelo gateway SSH com token de curta duração e a mesma sincronização por tar do driver ssh; o sandbox é apagado no fim e mantido (com apagamento automático pelo provider) quando a execução ou a sincronização falha. A chave de API do provider é cifrada e aceita `${secret:NOME}`.
- Precedência do ambiente **persona > projeto > padrão da empresa > local**, com ambiente por projeto (`PUT …/projects/{p}/environment`, `hanko project env`, Projeto › Configuração) e a **origem gravada na execução** (`environment_id`, `environment_source`; tela da execução e API). Arquivar um ambiente leva também os projetos. Propriedade testada em `domain.ChooseEnvironment` e ponta a ponta contra o banco.
- Probe real com **capacidades**: roda um script no alvo (inclusive o local) e relata `sync`, `git`, `claude`, `codex`, `hanko-reachable` (o alvo alcança o Hanko pela URL pública) e `network`/`isolated`/`persistent`.
- Readiness da persona (#10): teste por harness com passos nomeados (`environment`, `binary`, `auth`, `model`) gravado em `persona_readiness`, "Testar agora" na página da persona (a contratação já testa ao abrir a página), `hanko persona test|readiness` e job periódico `readiness_tick` (`HANKO_READINESS_INTERVAL`, padrão 15 min, 0 desliga). Falha abre o alerta `persona_not_ready` e **bloqueia o despacho** (task, chat e revisão) com o motivo, até um teste passar; nunca testada não bloqueia.
- Catálogo híbrido de modelos (#17): aliases do harness + `model_prices` + modelos vistos por testes e por execuções; seletor de modelo com "Outro…" na contratação, aviso "sem preço" (cai no preço de reserva), `GET …/models` e `hanko model list`.
- Migration `00070`; variáveis novas `HANKO_ENV_CLOUD`, `HANKO_ENV_CLOUD_API_URL`, `HANKO_ENV_CLOUD_SSH_HOST`, `HANKO_ENV_CLOUD_SSH_KNOWN_HOSTS`, `HANKO_READINESS_INTERVAL`. O cliente do Daytona foi testado só contra um servidor de teste; confira com uma conta real antes de depender dele.
- English summary: environments gain an explicit local driver, a docker client interface testable without a daemon and a cloud sandbox driver (Daytona, chosen by documented criteria) behind the same interface; the environment of a run follows persona > project > company default > local and the origin is stored on the run; probes report real capabilities; persona readiness (binary, login, model as named steps) can be tested on demand and periodically, opens an alert and blocks dispatch with the reason; the model catalog merges harness aliases, the price table and what probes and runs reported, with a "no price" warning (ADR-0055).

### M12 — Decision model e harnesses (ADR-0056)

- Decision model por empresa (migration 00080 `decision_models`, `decision_features`, `decision_invocations`): serviço interno "decidir" sobre um endpoint compatível com chat completions (OpenRouter por padrão, `base_url` configurável com a proteção SSRF das integrações). A chave é um segredo da empresa referenciado por `${secret:NOME}` (resolvido a cada chamada; apagar o segredo em uso é recusado) e o modelo é escolhido do catálogo do provedor (`GET /models`, preço por token guardado para estimar o custo).
- Features registradas, ligadas por empresa (nascem desligadas) e com modelo próprio opcional: **triagem** (sugere persona e prioridade como comentário do sistema na task; não atribui), **classificador de fase** (etapa de um pipeline para um texto) e **nome e resumo**. A resposta é validada contra o universo permitido e o texto de entrada é tratado como dado.
- Cada invocação (ok ou erro) é uma linha do ledger com ator `system` e um `activity_event` `decision.invoked`; o Custos ganha o bloco "Decision model" (por feature e modelo) e o custo soma ao gasto mensal da empresa (orçamento). API `…/decision`, `…/decision/catalog|features|invocations|classify-phase|name-summary`, `…/tasks/{task}/triage`; CLI `hanko decision show|catalog|set|rm|feature|invocations|triage|phase|name`; Configurações › Decision model.
- Harnesses novos em `personas.agent`: **`devin`** (Devin CLI de 1ª classe: `devin acp` sobre o motor ACP, executável fixado pelo operador em `HANKO_DEVIN_BIN`, fora do gate `HANKO_ACP_ALLOW`), **`pi`** (`pi --mode json -p`), **`cursor`** (`cursor-agent` em stream-json) e **`http`** (o Hanko roda o loop de tool-use sobre um endpoint compatível com chat completions, com as tools MCP do Hanko; reabre o teste com modelo local). Pi e Cursor usam as tools do Hanko pela CLI `hanko` autenticada com o run token.
- Presets ACP (`{"preset":"gemini"|"opencode"}`) e teste de contrato no CI com um agente ACP falso para devin, gemini e opencode. Probe de prontidão sem prompt e sem custo (`POST …/personas/{persona}/probe`, `hanko persona probe`): binário, protocolo, autenticação (agente que exige login falha citando os métodos anunciados), endpoint e modelo.
- Env/yaml novas: `HANKO_DEVIN_BIN`, `HANKO_PI_BIN`, `HANKO_CURSOR_BIN` (`devin_bin`, `pi_bin`, `cursor_bin`). Nenhuma dependência Go nova.
- English summary: a per-company decision model (chat-completions endpoint, OpenRouter by default; the key is a `${secret:NAME}` company secret; model picked from the provider catalog) with registered features (triage, phase classifier, name and summary) whose every call is a system-actor ledger row shown in Costs and added to the company spend; new harnesses `devin` (ACP, operator-fixed command), `pi`, `cursor` and `http` (Hanko's own tool-use loop), ACP presets for gemini and opencode with a CI contract test against a fake ACP agent, and a no-cost persona readiness probe (ADR-0056).

### Resiliência de run (M13, ADR-0039)

- Continuação limitada de run interrompido (#12): a queda do processo ou o encerramento do servidor não perde mais o trabalho. A run interrompida ganha uma continuação (`continuation_of_run_id`, migration 00090) que retoma a sessão do harness e leva um briefing do que já foi feito; máximo de 2 continuações automáticas, depois a run vai para a Caixa com o diagnóstico. O `session_ref` passa a ser gravado assim que o harness o anuncia.
- Card de diagnóstico e ações do operador (#13): causa, tentativa, próxima ação e dono na página da run e na Caixa (aba Falhas); Tentar de novo, Continuar e Encerrar na UI, em `POST /runs/{id}/actions` e em `hanko run retry|continue|close`. Tool de efeito externo em andamento ao morrer vira "desfecho incerto": nada reinicia sozinho sem decisão humana.
- Steer por capability do adapter (#14): `claude` (stdin em stream-json), `acp` (cancela o turno e reenvia na mesma sessão) e `fake` injetam a mensagem na run viva; `codex` interrompe e continua. A UI mostra o modo; API `POST /runs/{id}/steer`, CLI `hanko run steer`.
- Fila de follow-ups unificada, chat e task (#15): ordem e motivo da espera, editar e descartar antes da entrega (UI, `GET|POST …/followups`, `PATCH|DELETE /followups/{id}`, `hanko followup`); cancelar a run dispara a seguinte com os pendentes, no chat também (ajusta o ADR-0027 §4); safety net do heartbeat para follow-up parado.
- English summary: an interrupted run now continues on its own (up to 2 automatic continuations that resume the harness session with a briefing) and otherwise lands in the inbox with a diagnosis card (cause, attempt, next action, owner) and Retry / Continue / Close actions; a tool with an external effect in flight makes the outcome uncertain and requires a human decision. Adapters declare a steer capability (claude, ACP and the fake inject into the live run; codex interrupts and continues), and chat and task follow-ups share one ordered, editable queue where cancelling a run starts the next one (ADR-0039).

### M14 — Coordenação de tasks (ADR-0040)

- Dependências bloqueantes entre tasks (migration 00100 `task_dependencies`): relação N:N, sem ciclo (checagem no service e trigger no banco, com lock por empresa) e na mesma empresa (FK composta), até 50 por task. "Bloqueada" é derivado (dependência aberta) e nenhuma execução abre enquanto durar; ao fechar a última dependência (done ou cancelled) o responsável é acordado na mesma transação, e a liberação anda em cascata (A → B → C).
- Dependências na API (`…/tasks/{task}/dependencies`, `…/relations`, `depends_on` no create), na CLI (`hanko task dep add|rm|list`, `task create --depends-on`), no MCP (`hanko_task_dependency`, `depends_on` em `hanko_task_create`) e na UI (bloco Dependências, selo "Aguarda dependências").
- Árvore pai/filho: breadcrumb dos ancestrais e lista de subtasks (progresso n/m, "Nova subtask") na task; reparentar (`parent` / `clear_parent`, `hanko task parent`, `hanko_task_update parent`) com pai na mesma empresa, sem ciclo e até 10 níveis (trigger). O pai acorda quando o último filho fecha e não conclui com filho aberto (422); override humano com motivo (`force_close` + `override_reason`, `hanko task status … --force --reason`), registrado em `task.close_override`.
- Pausa herdada por subárvore (`task_holds`, `held_wakes`): `hanko task hold|resume|held`, `POST …/hold|resume`, banner no thread e aba **Pausadas** na Caixa. O despacho, o wake manual e a rede de segurança do heartbeat recusam; as runs na fila são canceladas; a que já roda termina sozinha; retomar acorda os wakes que foram recusados. A herança é calculada (reparentar muda o efeito na hora).
- Política de revisão por camada, task > projeto > persona, campo a campo: `require_review` (tri-estado) e revisor (`inherit`, `none`, persona ou harness+modelo). `hanko task review-policy`, `hanko project review-policy`, `PUT …/review-policy` (admin). A run grava a origem da política que valeu (`policy_origin`, tela da execução).
- O briefing da persona ganha a seção "Coordenação" (ancestrais, dependências com estado, subtasks, política de revisão) e o protocolo ensina a dividir o trabalho com `depends_on`.
- Importação de empresa recusa árvore de tasks com mais de 10 níveis. Contador OTel `hanko.task.coordination` e spans por caso de uso.
- English summary: tasks can now depend on each other (blocking, no cycles, same company; "blocked" is derived and the assignee wakes automatically, cascading, when the last dependency closes), form a parent/child tree (breadcrumb, subtask list, parent wakes on the last child and cannot complete with an open child unless a human overrides with a reason), be paused with a hold that the whole subtree inherits (dispatch refuses, resume wakes the refused requests, Inbox tab), and carry a per-task review policy with task > project > persona precedence recorded on the run (ADR-0040).

### M15 — Gates mais ricos e contratação (ADR-0041)

- **Pergunta** (`kind=question`, #18): a persona monta um formulário (escolha única, múltipla ou texto, até 12 campos) com `hanko_gate_request kind=question`; o humano responde na tela do gate, na Caixa, por `POST …/gates/{id}/answer` ou `hanko gate answer <gate> --set chave=valor`. A resposta é validada contra o formulário, vira um comentário de sistema na thread e acorda a persona com as respostas estruturadas. **Dispensar** (`dismissed`) e **reabrir**.
- **Plan-review** (`kind=plan`, #19): política opt-in por task, projeto ou persona (task > projeto > persona; `hanko plan-policy`, `PUT …/plan-policy`, painel da task e página da persona). Sem plano aprovado, o run só planeja: o claude roda em modo `plan` sem Edit/Write, o codex em sandbox somente leitura, e o `/mcp` recusa toda tool de escrita fora de ler, comentar, publicar o plano (`hanko_artifact_put`) e pedir o gate. Aprovado, a execução recebe o plano no briefing; mudanças pedidas replanejam.
- **Reenvio e audiência** (#21): `hanko_gate_resubmit` / `hanko gate resubmit` / `POST …/resubmit` reenvia no mesmo gate depois de mudanças pedidas (revisão +1, versões anteriores imutáveis em `gate_revisions`, diff na tela e em `…/revisions`). `audience` (usuários e papéis) restringe quem decide e quem vê o gate na Caixa; o owner sempre pode.
- **Contratação por gate** (#8): a persona propõe com `hanko_hire_request` (ficha sem `env`); aprovar o gate cria a persona na mesma transação. Política da empresa `require_hire_gate` (`hanko company policy hire-gate=on`, tela de Configurações) obriga também os humanos a passar pelo gate (`hanko persona hire --gate`).
- **Persona principal e de sistema** (#9): `hanko persona primary` escolhe a persona padrão das tasks que a pessoa cria sem responsável; `hanko persona system` (owner) marca personas de sistema, que não se desligam e só um owner edita.
- **Incidentes de orçamento** (#29): teto estourado (empresa, projeto ou persona) abre um incidente na Caixa (aba Orçamento); resolver com subir o teto, manter pausado ou liberar só um run (`hanko budget incidents|resolve`, `POST …/budget-incidents/{id}/resolve`). Subir o teto por outro caminho, ou a virada do mês, resolve sozinho.
- Migration `00110_gates_estendidos.sql`; nenhuma dependência Go nova; sem env nova.
- English summary: gates gain `question` (structured forms answered by a human, dismissable and reopenable) and `plan` kinds; opt-in plan review makes a run plan-only (claude plan mode, codex read-only sandbox, MCP write tools refused) until the plan gate is approved, then injects the approved plan; gates can be resubmitted in place with immutable revisions and a diff, and restricted to an audience; personas can propose hires whose approval creates the persona atomically (optionally mandatory for humans too); users get a primary persona and owners can mark system personas that cannot be turned off; budget overruns open Inbox incidents resolved by raising the ceiling, keeping the pause or releasing a single run (ADR-0041).

### M16a — Caixa por pessoa, visões "Minhas" e metas em árvore (ADR-0042)

- Caixa de entrada por pessoa (migration 00120 `inbox_state`): lido, não lido e arquivado são de cada usuário; os itens continuam derivados da empresa. Arquivar limpa a Caixa de quem arquivou sem fechar a task nem decidir o gate; o item reaparece como novo se muda (impressão digital por tipo). Abas **Não lidos** e **Arquivados**, selo de não lidos no menu (htmx), "Marcar tudo como lido". API `GET …/inbox?view=` e `POST …/inbox/state`, CLI `hanko inbox list|read|unread|archive|unarchive|read-all`. Fatos `inbox.archived|unarchived|read_all`.
- `@menção` em comentários (`comment_mentions`): `@slug` de persona e `@handle` de membro (parte local do e-mail, `renato.bardi@…` → `@renato-bardi`). Humano citado recebe o item **Menção** na Caixa; persona citada por humano é acordada na task mesmo sem ser a responsável (a run dela não muda status nem atribuição). Persona que cita humano, no comentário ou no resumo da run, pede atenção pela Caixa dele.
- Avisos ao vivo por usuário: toast por SSE (`GET /c/{empresa}/caixa/eventos`) e **web push no PWA** (migration 00121). VAPID e criptografia `aes128gcm` implementados com a stdlib (`internal/webpush`, sem dependência nova); par de chaves gerado na primeira necessidade e guardado cifrado em `instance_secrets` (precisa de `HANKO_SECRET_KEY`). Preferências por tipo (menção, gate, execução que falhou, alerta) em Configurações › Notificações, com aparelhos e aviso de teste; `hanko notify prefs|set|test`. Nova env `HANKO_PUSH_SUBJECT` (contato do VAPID; padrão: e-mail do dono ou URL pública https). Envio sempre depois do commit, em segundo plano; métrica `hanko.notifications`.
- **Minhas** por participação (criei, atribuí, fui mencionado, comentei, audiência de gate): aba Minhas das tasks com o motivo de cada linha, `?mine=true` / `hanko task list --mine`, e "Minha audiência" nos gates (`hanko gate list --mine`).
- Busca única cobre **metas, projetos, gates e artefatos** (só a versão mais recente da cadeia), além dos tipos de antes (migration 00123). Atividade filtrável por ator: `GET …/activity?actor=system|persona:<slug>|human:<e-mail>`, `hanko activity --actor`, seletor em `/atividade` (migration 00124).
- **Metas em árvore** (migration 00122 `goals.parent_id`): sem ciclo e no máximo 4 níveis (trigger no banco + regra de domínio), progresso do pai = média das submetas não abandonadas, pai atinge o alvo quando as submetas atingem (e reabre se uma volta), briefing da run com a cadeia até a raiz, UI em árvore com recuo e seletor de meta-pai, `hanko goal create --parent`, `goal update --parent|--clear-parent`.
- English summary: the Inbox is now per person (read/unread/archived state per user, items reappear when they change, unread badge and views), `@mentions` in task comments (humans land in their Inbox, personas are woken when a human mentions them), per-user live toasts over SSE and PWA web push with per-kind preferences (VAPID + RFC 8291 encryption implemented with the standard library, keys generated at runtime and stored encrypted; requires `HANKO_SECRET_KEY`), "Mine" views by participation, search over goals/projects/gates/artifacts, activity filtered by actor, and goals as a tree (max 4 levels, parent progress is the average of its sub-goals) (ADR-0042).

### M16b — Acesso, ciclo de vida da empresa e import (ADR-0057)

- Empresa: editar nome, fuso e descrição (`PATCH /companies/{c}`, `hanko company edit`; o prefixo só muda com zero tasks); **arquivar** (pausa as personas, cancela as execuções, desliga os fluxos e deixa a empresa somente leitura: toda escrita de domínio é recusada no ponto único do `activity_event`), **restaurar** (`--resume` reativa o que o arquivamento mexeu) e **excluir** (só owner, só arquivada, nome digitado; o export automático fica em `company_deletions` e pode ser baixado). Migration 00125.
- Import com **prévia** (`dry_run`, `hanko import --dry-run`, tela `/importar`): contagens, ação por item e avisos; destino empresa nova ou **existente** (`into`, só a configuração entra) com conflito por item pular/renomear/substituir (`on_conflict`, `resolve`). O motor é reaproveitável pelo instalador de pacotes de time.
- `hanko login` pelo navegador (`/cli-auth/<código>`, API key com escopo `full|read` e nome do dispositivo, entregue uma única vez), `hanko logout`, `hanko whoami` (`GET /whoami`), chaves de leitura recusadas em escritas e no MCP, dispositivo e escopo na tela de API keys (revogar já existia). Migration 00126.
- Pedidos de entrada: link público da empresa (`/pedir-acesso/<token>`), fila de pedidos e aprovação com escolha do papel (UI em Membros, `hanko member join-link|requests|approve|deny`). Migration 00127. Tela de usuários da instância (`/admin/usuarios`, `GET /users`, `hanko users`).
- English summary: companies can be edited, archived (read-only, restorable), and deleted (owner only, archived only, name confirmation, automatic export kept); imports have a dry-run preview and can merge configuration into an existing company with per-item skip/rename/replace; `hanko login` authorizes the CLI through the browser with a scoped, per-device API key plus `whoami`; companies get a public join link with admin approval and role choice; admins get an instance users screen (ADR-0057).

### M17 — Conexões OAuth, fork de skills e pacotes de time (ADR-0043, ADR-0044)

- **Conexões OAuth para MCP remoto** (Integrações › Conexões, `hanko connection`): OAuth 2.1 com PKCE S256, descoberta dos metadados (recurso protegido e servidor de autorização), registro dinâmico de cliente e renovação do token. O token fica cifrado como segredo (`conn.<slug>.access|refresh`) e a integração MCP correspondente o usa por `${secret:…}`; identidade da empresa (as personas usam) ou pessoal (só o dono). Estados `rascunho → aguardando autorização → conectada / expirada / com erro / revogada`, probe de autenticação, "concluir depois" e "reconectar". Uma persona pede uma conexão (`hanko_connection_request`): abre-se um gate `connection`, a task espera e, quando o humano autoriza no navegador, o gate é aprovado, a task retoma e a persona é acordada. Renovação sob demanda no gateway de tools e pela varredura periódica. Exige `HANKO_PUBLIC_URL` e `HANKO_SECRET_KEY`. Migration 00130.
- **Skills**: fork de skill (principalmente de fonte) em cópia local editável, com história própria (`hanko skill fork`, página da skill); cada execução passa a registrar as skills e revisões que recebeu (`hanko run skills <run>`, linha "Skills" na página da run). O restante do item (revisões, pin, fontes, estúdio) já existia (ADR-0035). Migration 00131.
- **Pacotes de time `hanko.team.v1`**: exportar um time (personas + skills + rotinas) e instalar um pacote numa empresa existente pelo motor de import com prévia (ADR-0057): personas pausadas, rotinas desligadas, conflito por item (pular/renomear/substituir), passo de assinatura por harness, segredos exigidos conferidos, catálogo embutido (3 pacotes) e arquivo/URL pela CLI. `hanko teams catalog|show|export|preview|install|history`, seção "Pacotes de time" no Catálogo de times. Migration 00132.
- English summary: OAuth 2.1 + PKCE connections to remote MCP servers (dynamic client registration, encrypted refreshable tokens, company or personal identity, auth probe, recoverable setup, and a `connection` gate that resumes the task once a human authorizes in the browser); skill fork plus a per-run snapshot of the skill revisions received; and `hanko.team.v1` team packages (export a team, preview and install into an existing company paused, with a subscription step and a built-in catalog).

### M18 — Plugins: abas, gatilhos de fluxo, provider de ambiente, fonte de skills e SDK (ADR-0045)

- **Concessão de capacidades pelo dono da instalação**: conceder e revogar uma a uma (`hanko plugin cap grant|revoke`, Configurações › Plugins › Capacidades concedidas). Revogar vale na hora para tudo o que a capacidade sustenta; vai para `admin_audit` e para a atividade das empresas. Quatro capacidades novas: `pages`, `flows.trigger`, `environments.provider`, `skills.source`.
- **Abas de plugin** (`pages`): o plugin entrega o HTML (`get_page`) e a aba aparece no grupo "Plugins" da barra lateral. A página roda num iframe `sandbox="allow-scripts"` sem `allow-same-origin`, com CSP sem rede, e conversa com o plugin só por `window.hanko.call` (postMessage → ponte → `page_call`, com o papel de quem chamou). `hanko plugin tabs`.
- **Gatilhos de fluxo** (`flows.trigger`): o plugin declara gatilhos e dispara com `flows.fire`; o admin da empresa liga cada gatilho a fluxos existentes (`hanko plugin trigger list|link|unlink`, página do plugin). Migration 00140.
- **Provider de ambiente** (`environments.provider`): `cloud.provider = plugin:<id>/<key>`; o plugin implementa `env.ping|create|ssh|delete` e o sandbox usa o mesmo caminho SSH endurecido do driver cloud.
- **Fonte de skills** (`skills.source`): fontes `plugin://<id>/<key>` usam a sincronização, as revisões, o fork e o pin que já existiam (sem depender de `HANKO_SKILL_SOURCES`). `hanko plugin skill-sources`.
- **SDK Go** (`github.com/renatobardi/hanko/sdk/plugin`) e `hanko plugin new <id>` (esqueleto com manifesto, tool, aba com ponte e gatilho); exemplo `examples/plugins/showcase`. Protocolo e segurança em `docs/PLUGINS.md`.
- English summary: plugins can now contribute sandboxed iframe tabs with a postMessage bridge, flow triggers (linked to flows by a company admin), cloud-sandbox environment providers and skill sources; the installation owner grants and revokes capabilities one by one (effective immediately); a Go SDK and `hanko plugin new` scaffold ship with a showcase example.

### M19 — Canais externos e gatilhos de fluxo (ADR-0046, ADR-0047)

- **Canais** (#42): Configurações › Canais e `hanko channel …`. Driver **Telegram** (webhook com `secret_token`, só chat privado): conversar com a persona, anexos até a cota, e **decidir gates por botão** (`approval`, `task_review`, `tool`, `memory`), sempre como a pessoa vinculada (`DecideGate` com o mesmo papel/audiência). Driver **GitHub** (`X-Hub-Signature-256`): um comentário de pessoa vinculada, em repositório da lista, que cita a menção do canal cria task (ou comentário) para a persona; o resumo volta ao PR e `hanko channel review` / tool MCP `hanko_publish_review` publica uma review (APPROVE só por admin humano).
- **Vínculo de identidade** por código de uso único (15 min, só o hash é guardado), provado no canal; revogável. Sem vínculo o canal não faz nada.
- **Entrega com estado** (`na fila → entregue | falhou`, 6 tentativas com backoff, reenvio) por jobs `channel_send`/`channel_inbound` na fila; entrada deduplicada por `external_id`.
- **Respostas rápidas** (feature `fast_response` do decision model): responde perguntas simples com um retrato curto da empresa ou escala para a persona; nunca decide gate.
- **Variáveis de fluxo** (#22): `{{nome}}`, obrigatórias (`nome!`), padrões, embutidas `{{data}}`, `{{n}}`, `{{semana}}`, `{{disparo}}`; `hanko flow run --var`.
- **Gatilhos de fluxo** (#23): webhook de entrada com HMAC (`POST /hooks/flows/{id}`, janela de 5 min, idempotência por entrega, limite por gatilho), gatilho por evento interno (verbo/prefixo + filtro, na mesma transação do fato), cron opcional, **revisões imutáveis** com restauração e **arquivar** distinto de desativar. Exportar/importar levam variáveis e arquivado (gatilhos e segredos não viajam).
- Migrations `00150_fluxos_variaveis_gatilhos.sql` e `00151_canais.sql`; nenhuma dependência Go nova; sem env nova (usa `HANKO_PUBLIC_URL`).
- English summary: external channels (Telegram with button-driven gate decisions as the linked user, GitHub comments that wake a persona and publish reviews) with one-time identity linking, delivery state with retries and decision-model quick replies; flows gain declared variables, HMAC webhook and internal-event triggers, optional cron, immutable revisions with restore, and archiving (ADR-0046, ADR-0047).

### M20 — Etapas do projeto sobre as tasks e cards de status com decision model, cron e artefato (ADR-0048)

- **Etapas do projeto** (#45): um projeto ganha um pipeline de etapas ordenadas (aba **Etapas**, `PUT …/projects/{p}/stages`, `hanko stage set`); cada etapa mapeia um status de task (`todo`, `in_progress`, `in_review` para etapa humana) e, opcionalmente, uma persona. A task criada por uma pessoa num projeto com etapas nasce na primeira (persona e status dela, persona acordada). A persona conclui a etapa com `hanko_stage_complete` (ou dando a task como `done`, que conclui a etapa e nunca fecha a task); a task avança sozinha e acorda a persona seguinte. A última etapa concluída encerra a task.
- **Gate de saída por etapa**, reaproveitando o que já existe: `none` (avança na hora; a política de revisão da persona/projeto/task continua valendo), `approval` (gate de aprovação pedido pelo sistema) ou `review` (o gate `task_review` com a **revisão automática** da política vigente). Só os gates param o pipeline; aprovado avança, mudanças pedidas ou rejeição devolvem a etapa à persona com a nota.
- **Aprendizados**: cada decisão de gate numa etapa vira histórico imutável e um fato de memória `pipeline.learning` (`hanko/pipelines/<projeto>/<etapa>/…`); as últimas com nota (ou sem aprovação) voltam no briefing da persona da etapa ("Aprendizados desta etapa"). `hanko stage learnings`, `GET …/stage-learnings`, aba Etapas.
- Mão humana: `hanko stage advance|leave|start|complete|show`, `POST …/tasks/{t}/stage/advance|leave|start|complete`, painel "Etapa do projeto" na task; `TaskView.stage` nas listagens. Tirar uma etapa que ainda tem tasks é conflito (409).
- **Cards de status** (#46): **motor** `persona` (a resumidora de sempre) ou `decision` (o decision model da empresa escreve o resumo numa chamada só, pela feature nova `card_summary`, desligada por padrão; os filtros vêm de quem cria); política **cron** (`refresh_policy.mode=cron`, `cron`, `cron_tz`, `cron_skip_unchanged`; `hanko status-card create --cron "0 7 * * 1-5" --tz America/Sao_Paulo`) que atualiza no horário, com ou sem mudanças; o **resultado de cada atualização vira uma versão** do artefato `card-<título>.md`, e o card pode aparecer no **Painel** (`on_dashboard`, `GET …/dashboard/status-cards`). Falha do modelo deixa o card em erro mantendo o último resumo bom.
- Correção: o gate que movia a task (`moveTask`) zerava o modelo escolhido à mão; agora preserva.
- Migration `00160_pipelines_de_tasks_e_cards.sql`; nenhuma dependência Go nova; nenhuma env nova.
- English summary: projects get an ordered stage pipeline over tasks (each stage maps a task status and a persona; the persona completes it with `hanko_stage_complete`, the task advances and wakes the next persona; stage exit gates reuse approval and the `task_review` gate with the automatic review, and only gates stop the flow; gate decisions become stage learnings in an immutable history, a `pipeline.learning` memory fact and the stage persona's briefing). Status cards can use the company decision model as engine (one call, `card_summary` feature), a `cron` refresh policy, publish every update as a new version of a card artifact and show on the Dashboard (ADR-0048).

### M7 — Fundações: segredos, settings da instância e BlobStore (ADR-0050, ADR-0051)

- Segredos como recurso da empresa (migration 00035 `secrets`): AES-256-GCM com `HANKO_SECRET_KEY` (AAD por segredo), rotação com versão, "revelar uma vez por versão" e valor gerado pelo servidor mostrado uma vez. Referência `${secret:NOME}` em headers e variáveis de integrações MCP e no `adapter_config` da persona (`env` e argumentos), resolvida só no despacho da run e incluída na redação do log (`adapter.Spec.Secrets`). Apagar um segredo em uso é recusado (lista os usos; `--force` apaga).
- O `env` da persona deixa de ficar em claro no banco: `HirePersona` troca cada valor por um segredo `persona.<slug>.<VARIAVEL>` e o boot migra as personas existentes (idempotente; `hanko secret migrate-env`). Sem `HANKO_SECRET_KEY` nada muda e o boot avisa. `hanko secret rekey`/`hanko integration rekey` reescrevem integrações e segredos.
- Segredos de usuário (`${secret:user/NOME}`, `hanko secret --user`): só o dono lista, revela e usa; recusados em configuração compartilhada. Provider de cofre externo opcional (API KV v2; `HANKO_VAULT_ADDR`/`HANKO_VAULT_TOKEN`), lido a cada despacho.
- Segredos na API (`…/secrets`, `…/user-secrets`), na CLI (`hanko secret create|ls|show|rotate|reveal|rm|rekey|migrate-env`) e em Configurações › Segredos. Testes de não-vazamento (atividade, webhooks, export, log, eventos da run, comentários, briefing).
- `instance_settings` (migration 00036) para toggles não sensíveis (`acp.*`, `integrations.*`, `artifacts.*`, `run.reserve_usd`, `alerts.*`), com trilha `instance_setting_audit`; env vence o banco e trava a chave; mudar vale sem reiniciar. API `/instance/settings`, CLI `hanko settings list|get|set|unset|audit`, Configurações › Instância.
- `BlobStore` para o conteúdo dos artefatos (migration 00037 `artifacts.storage`): driver `postgres` (padrão, igual ao de antes) e `s3` compatível (SigV4 próprio, sem SDK; `HANKO_BLOB_DRIVER`, `HANKO_S3_*`). Limite por arquivo de até 64 MiB com S3. `hanko blobs migrate --from --to` (retomável, confere sha256 e tamanho) e `hanko blobs status`.
- Env novas: `HANKO_BLOB_DRIVER`, `HANKO_S3_ENDPOINT|REGION|BUCKET|PREFIX|PATH_STYLE|ACCESS_KEY|SECRET_KEY`, `HANKO_VAULT_ADDR|MOUNT|NAMESPACE|TOKEN`.
- English summary: company secrets (AES-256-GCM, versioned rotation, reveal-once) referenced as `${secret:NAME}` in MCP integrations and the persona `adapter_config`, resolved only at dispatch and redacted from logs; the plaintext persona env is migrated into encrypted secrets; user-scoped secrets visible only to their owner; an optional external vault provider (KV v2); non-sensitive instance settings with an audit trail where the environment variable wins and locks the key; and a `BlobStore` for artifact content with the default Postgres driver and an S3-compatible driver (in-house SigV4, up to 64 MiB per file) plus `hanko blobs migrate`.

### M8 — Revisões append-only, anexos, rascunhos e chat (ADR-0052)
- Instruções da persona agora têm histórico imutável (autor, quem aplicou, motivo, sha256): diff, restaurar (UI `/personas/{p}/instrucoes`, CLI `hanko persona history|restore`, API), a run grava a revisão usada, e a persona propõe mudança pelo gate `instruction` (`hanko_persona_instruction_propose`). O gatilho `hanko_revision_immutable()` é a base para fluxos e gates.
- Anexos (`artifacts.kind=attachment`) em task, comentário, conversa e mensagem: arrastar e soltar na UI, `hanko attach`/`hanko attachment`, API `/attachments` e tools MCP `hanko_attachment_list|get|put`; mesma cota e CSP dos artefatos; a lista entra no briefing da run.
- Rascunhos no servidor por usuário + alvo (task ou conversa), com autosave por htmx, limpos ao enviar (API `/drafts/...`).
- Chat: `/new` reinicia o contexto com um marcador (conversa direta e sala); tools MCP `hanko_project_create` e `hanko_goal_create`.
- English summary: append-only instruction revisions with diff/restore and persona proposals via gate; attachments (UI drag-and-drop, CLI, API, MCP) listed in the run briefing; server-side drafts with autosave; chat `/new`; MCP tools to create projects and goals. Migrations 00040–00043.

### M9 — Política de tools e gateway (ADR-0053)

- Motor de política `off/ask/allow` por persona × tool com padrão da empresa (migration 00050 `tool_policies`): alvo `all`, `integration`, `tool` ou `native` (tool do harness ou de plugin pelo nome). Precedência persona > empresa, tool > integração > curinga e, no empate, desligar vence perguntar vence permitir; a política vence o perfil de ferramentas nas runs, mas nunca abre tool em quarentena, desligada ou removida. No gateway nomeado (cliente de fora) as políticas só apertam. `hanko tool-policy list|set|delete`, `GET/PUT/DELETE …/tool-policies`, aba **Política** em Integrações.
- `ask` vira gate `tool` com prazo (24 h para integração e plugin, 10 min para tool nativa; a varredura vence os pendentes e a próxima chamada igual abre gate novo). **Sempre permitir** na decisão do gate (`gate approve --always`, `"always": true`, botão "Aprovar e sempre permitir"; só admin, só persona) grava a política `allow` da persona para a tool.
- Tools nativas do harness no mesmo motor: o claude chama o hook `PreToolUse` (`hanko hook pretooluse`, ligado sozinho quando há política nativa; falha fechada) e o ACP consulta a política em cada `session/request_permission` (a run espera o gate). Tools de plugin também passam pelo motor.
- Tool nova em quarentena avisa na Caixa de entrada (aba Tools, só admin). Testar uma tool como a persona X (`integration tools <i> --test <tool> --as <persona> [--execute]`, `POST …/tools/{tool}/test`, aba Política): simulação sem efeito ou chamada de verdade auditada como teste.
- A auditoria das chamadas ganhou origem (run, gateway, teste, hook, acp, plugin), ator, trecho do resultado **redigido** e de onde veio a decisão; filtro `source` na API.
- Endpoint único da run `/mcp/run` (tools de todas as integrações como `<integração>__<tool>`, mesmo perfil, políticas e auditoria), ligado por `HANKO_TOOLS_SINGLE_ENDPOINT=true`.
- English summary: a per-persona × tool `off/ask/allow` policy engine with a company default now sits on top of tool profiles (persona beats company, most specific target wins, deny wins ties; it never opens quarantined tools, and only tightens named external gateways). `ask` opens a `tool` gate with a timeout, "approve and always allow" saves a persona policy, and the same engine covers harness-native tools (Claude `PreToolUse` hook, ACP permission requests) and plugin tools. New quarantined tools warn in the Inbox, a tool can be tested as persona X, the call log records source, actor and a redacted result preview, and `HANKO_TOOLS_SINGLE_ENDPOINT` gives the harness one aggregated `/mcp/run` endpoint for integrations (ADR-0053).

### Qualidade e operação (trilha contínua, fase "Já")

- `SECURITY.md` (+ `SECURITY.pt-BR.md`): como reportar vulnerabilidade em privado, versões suportadas, escopo. `.github/dependabot.yml` com atualização semanal de gomod, npm (`ui/islands`) e github-actions.
- CI separado por propósito: `ci` (PR rápido; os jobs `check` e `release-dry-run` seguem exigidos), `security` (govulncheck, `trivy fs`, revisão de dependências; também semanal), `e2e` e `evals` (esqueletos para #57 e #58), `edge` (imagem `ghcr.io/renatobardi/hanko:edge` e binários snapshot a cada push no `main`) e `release` (tag `v*`, agora com as notas da versão).
- Piso de cobertura no CI (`scripts/coverage-gate.sh`): `internal/domain` e `internal/heartbeat` ≥ 80 %, `internal/service` ≥ 70 %, `internal/api` relatado (integração). Domínio subiu de 60 % para 100 % com testes de tabela dos validadores e classificadores.
- Testes de propriedade (`testing/quick`, semente fixa) para o motor de orçamento: limiares exatos, monotonicidade, janela do mês em vários fusos, preenchimento de dias e ordenação.
- Canais de release: `edge` (main) e `stable` (tag `vX.Y.Z`); notas geradas de `changelog.d/*.md` por `scripts/release-notes.sh` (`notes` e `cut`, que funde no `CHANGELOG.md`); fluxo em `docs/RELEASING.md` (+ en). "Toda feature entra com teste" no CONTRIBUTING.
- English summary: SECURITY policy and Dependabot; CI split by purpose (ci, security, e2e/evals skeletons, edge, release); per-package coverage floors enforced in CI with property tests for the budget engine; edge/stable release channels with release notes built from `changelog.d/` (ADR-0049).

### Trilha contínua, onda 1 — claim do primeiro dono, rate limit, erros via OTel, e2e e design guide (ADR-0058)

- First-admin claim (#53, migration 00170 `owner_claims`): sem nenhum usuário (modo authenticated, provider local), o boot imprime no stderr um link de uso único `/claim/<token>` (token aleatório de 32 bytes, guardado só como SHA-256, vale 1 h). A página cria o primeiro dono, abre a sessão e invalida o link na mesma transação (duas disputas = no máximo um dono); link usado, expirado ou desconhecido responde o mesmo 404. O token não passa pelo logger estruturado (que exporta por OTLP) e é mascarado em span/log. `hanko auth claim-link` emite outro sem reiniciar; `hanko auth create-owner` segue como fallback documentado.
- Rate limit geral (#51, migration 00171 `rate_limit_buckets`): token bucket no Postgres (uma instrução atômica por decisão) por API key, usuário, run ou IP, para escritas, MCP, webhooks de entrada (`/hooks/…`, reservado ao M19) e relatos de erro do navegador. Estourou: `429` com `Retry-After`, `RateLimit-Limit` e `RateLimit-Remaining`. Limites padrão seguros e configuráveis sem reiniciar em Configurações › Instância (`ratelimit.*`; env `HANKO_RATELIMIT_*` vence e trava). Falha do banco libera (fail-open). Métrica `hanko.ratelimit.decisions{class,outcome}`. Testes de propriedade do bucket e comparação do SQL com o modelo puro.
- Erros via OTel (#49), sem SaaS: todo 5xx inesperado e todo panic de handler vira span event `exception` (com pilha), log `ERROR` com `exception.stacktrace` e o contador `hanko.errors{source,exception_type}`. Erros de JavaScript são relatados por `hanko.js` (no máximo 5 por página) para `POST /telemetry/errors`, que exige sessão e mesma origem, sanitiza (sem query/fragmento) e emite o span `browser.error`. Painel "Hanko · Erros" provisionado no Grafana do compose (`deploy/grafana/`).
- E2E (#57): pacote `e2e/` (Playwright 1.56.1, fora do binário) com 19 jornadas contra Postgres real e o adapter fake, mais screenshot diff do catálogo do DS em claro e escuro. `.github/workflows/e2e.yml` preenchido (PR que toca UI/handlers/migrations/`e2e/`, e `main`; Postgres 17; relatório em falha).
- Dev labs (#70): `/design-guide` (Kubo DS em claro e escuro, lado a lado ou separado) e `/dev/ux` (ux-labs: thread com 500 comentários e run com 10 mil linhas sobre o log virtualizado real). Só no modo trusted ou para dono/admin da instalação; os demais recebem 404. Nada é gravado.
- Correções achadas pelo e2e: `hanko.js` tinha erro de sintaxe (o navegador descartava o arquivo inteiro: ilhas, atalhos e quadro arrastável não rodavam) e `hanko.css` tinha um `@media` sem fechar (regras de skills, plugins e tools só valiam em tela estreita). Ambos com teste/fumaça contra a volta.
- Env novas: `HANKO_RATELIMIT_ENABLED`, `HANKO_RATELIMIT_WRITES_PER_MIN`, `HANKO_RATELIMIT_IP_PER_MIN`, `HANKO_RATELIMIT_MCP_PER_MIN`, `HANKO_RATELIMIT_WEBHOOKS_PER_MIN`, `HANKO_RATELIMIT_BROWSER_ERRORS_PER_MIN`.
- English summary: the first owner of a fresh installation is now created from a single-use `/claim/<token>` link printed to stderr at boot (hash-only storage, 1 h expiry, `hanko auth claim-link` to reissue, `create-owner` kept as a fallback); a Postgres-backed token-bucket rate limit (per API key, user, run and IP) returns `429` with `Retry-After` for writes, MCP, incoming webhooks and browser error reports, with settings-backed limits and OTel metrics; server exceptions and browser JavaScript errors flow through OpenTelemetry as span events, error logs with stack traces and a provisioned Grafana "Errors" dashboard; a Playwright suite (19 journeys against real Postgres and the fake adapter, plus screenshot diffs) runs in `e2e.yml`; and `/design-guide` plus `/dev/ux` give admins and developers a living Kubo DS catalog and synthetic load scenarios.

### Operação da instância: `hanko doctor`, `update`, `service` e `backup` (ADR-0062, #64 #65)
- `hanko doctor` diagnostica Postgres, migrations (pendentes por conjunto, não por ordem), OTel, ai-memory, harnesses, segredos (sem imprimir valores), disco, pg_dump e idade do último backup; sai com 1 só se algo falhou.
- `hanko update --channel stable|beta` troca o binário pela release do GitHub com backup antes (aborta se o backup falhar), sha256 obrigatório, troca atômica e `--rollback`; recusa contêiner e binário de desenvolvimento.
- `hanko service install|uninstall|status|restart` instala unit do systemd ou plist do launchd (sistema ou `--user`); `hanko service print compose|timer` gera as units usadas pelo `deploy-prd.sh`, que agora também roda `doctor` e `update --check`.
- `hanko backup create|list|verify|restore|schedule`: tar com `pg_dump -Fc`, conteúdo de artefatos em S3 e manifest sha256; restore substitui o banco em uma transação, recusa servidor no ar e schema mais novo que o binário. Retenção `HANKO_BACKUP_KEEP`.
- Configurações › Backups: histórico, "Fazer backup" em segundo plano e agendamento por fluxo de sistema (`system_flow_tick` no heartbeat). API: `GET/POST /instance/backups`, `GET/PUT /instance/backup-schedule`. Migration 00180.
- Env novas: `HANKO_HOME`, `HANKO_BACKUP_DIR`, `HANKO_BACKUP_KEEP`, `HANKO_PG_DUMP_BIN`, `HANKO_PG_RESTORE_BIN`, `HANKO_PSQL_BIN`; da CLI: `HANKO_UPDATE_CHANNEL`, `HANKO_UPDATE_REPO`, `HANKO_UPDATE_TOKEN`.
- English summary: new `hanko doctor`, `update` (backup first, sha256, atomic swap, rollback), `service` (systemd/launchd) and `backup` (create/list/verify/restore/schedule) commands, plus a Settings › Backups screen with history and a system-flow schedule.

### `hanko serve --embedded-pg` para desenvolvimento e trial (ADR-0063, #47)
- Sobe um Postgres local descartável com `initdb`/`pg_ctl` de uma instalação do PostgreSQL (nada é baixado), senha por arquivo, só em 127.0.0.1, e roda o servidor em cima dele. Recusa `HANKO_DATABASE_URL` e root. Produção continua com Postgres externo.
- English summary: `hanko serve --embedded-pg` starts a disposable local Postgres from locally installed binaries (nothing downloaded), for dev/trial only; production keeps an external Postgres.

### Trilha contínua, onda 2B — instalação, `hanko mcp` e evals (ADR-0060)

- Instalação (#54): `scripts/install.sh` baixa a release do GitHub, confere o sha256 e instala sem root (`HANKO_VERSION`, `HANKO_INSTALL_DIR`, `HANKO_DRY_RUN`); testado sem rede em `scripts/test-install.sh` (no `ci`). O goreleaser passa a gerar o cask do Homebrew (`brew install --cask renatobardi/tap/hanko`, macOS), enviado ao tap só quando existe `HOMEBREW_TAP_GITHUB_TOKEN`.
- `hanko mcp` (#52): servidor MCP em stdio que repassa tudo ao `/mcp` do servidor remoto com a credencial da CLI (`--token`, `HANKO_TOKEN` ou o contexto de `hanko login`). Serve a IDEs que só falam stdio, sem npm.
- Evals (#58): `cmd/hanko-evals` com cenários de comportamento (gate antes de produção, delegação, dependência, não vazar segredo) e de chaos (run morta, banco fora) no mesmo runner. Por padrão roda contra um roteiro de mentira, sem rede e sem custo; o modelo real só entra com `--live`/`HANKO_EVALS_LIVE=1` e `ANTHROPIC_API_KEY`, nunca em pull request, com teto de gasto (`HANKO_EVALS_MAX_USD`) e custo marcado como teste. `.github/workflows/evals.yml` preenchido (nightly + manual).
- Env novas: `HANKO_VERSION`, `HANKO_INSTALL_DIR`, `HANKO_RELEASES_URL`, `HANKO_DRY_RUN` (install.sh); `HANKO_EVALS_LIVE`, `HANKO_EVALS_SUITE`, `HANKO_EVALS_TRIALS`, `HANKO_EVALS_MODEL`, `HANKO_EVALS_MAX_USD` (evals).
- English summary: `scripts/install.sh` installs a release binary with checksum verification and the goreleaser config now emits a Homebrew cask (uploaded only when a tap token exists); `hanko mcp` bridges stdio MCP clients to the remote server using the CLI credential; and `hanko-evals` runs behavior and chaos scenarios against a scripted fake by default, calling a real model only behind an explicit live flag (never on pull requests, spend-capped, cost tagged as test).

### Trilha contínua, onda 3 — onboarding, contextos, preferências, novidades e site de docs (ADR-0061)

- Onboarding (#67, migration 00200 `onboarding_state`): assistente retomável em `/c/{empresa}/primeiros-passos` (empresa → harness com teste → primeira persona ou pacote de time → primeira task), derivado do que a empresa já tem (fechar a aba e voltar cai no passo certo). O teste do harness (`claude`, `codex`, `fake`) roda antes de existir persona, reaproveitando o probe de readiness; o pacote de time instala e liga a persona de topo. O Painel mostra um cartão enquanto o assistente está ativo; criar a empresa já cai nele. `hanko onboard` faz o mesmo pela CLI, executando os passos que as flags permitem e dizendo o comando para continuar. API `/companies/{c}/onboarding[/probe|/skip|/dismiss|/team|/task]`; fatos `onboarding.*`.
- Contextos da CLI (#68): `hanko context list|current|use|add|rm` sobre o mesmo arquivo do `hanko login`; `add` aceita a API key só por `--token-stdin` e confere o servidor com `whoami`; `--context`/`HANKO_CONTEXT` escolhe o contexto de uma chamada. `hanko sandbox up|down|list`: instância descartável por branch ou worktree (banco `hanko_sbx_<branch>` no Postgres que você já usa, porta determinística, contexto `sandbox-<branch>`) para test-drive sem tocar nos dados reais.
- Favoritos, recentes e perfil (#69, migration 00200 `nav_items`, `user_profiles`): estrela na barra de cima, grupos Favoritos e Recentes na sidebar (guardados no servidor, por pessoa e empresa); `/c/{empresa}/perfil` com nome, avatar (glifo + cor), fuso e idioma; o idioma do perfil vale quando não há cookie e o fuso vale nas telas da empresa. CLI `hanko profile show|set` e `hanko favorite list|add|rm`; API `/me/profile`, `/companies/{c}/nav`.
- O que há de novo (#61): o CHANGELOG do binário (embutido, sem rede) mostra as novidades da versão instalada num cartão do Painel e em `/c/{empresa}/novidades`; dispensável por pessoa e por versão (`user_dismissals`). CLI `hanko whats-new`; API `/whats-new`.
- Site de docs (#62): `go run ./tools/docsite -out dist/docs` (ou `make docs-site`) gera HTML estático autossuficiente de `docs/` e `docs/en` com índices e `sitemap.txt`; nada é publicado. `docs/feature-map.json` mapeia as funcionalidades por domínio (UI, CLI, API, MCP, roteiro, e2e, evals) e um teste o confere contra o código real.
- Env novas (CLI): `HANKO_SANDBOX_DATABASE_URL`, `HANKO_CONTEXT`. Sem env de servidor nova.
- English summary: a resumable first-steps wizard (company → tested harness → first persona or team package → first task) in the UI and `hanko onboard`; `hanko context add/use/list` and disposable per-branch `hanko sandbox` instances for test-drives; server-side favorites, recents and an own profile (name, avatar, time zone, language); a "What's new" card read from the embedded CHANGELOG of the installed version, dismissible per person; and a static docs-site generator with a code-verified feature map by domain.

## [0.2.0] — 2026-10-15

- Artefatos versionados (ADR-0028, migration 00019 `artifacts` e `artifact_blobs`): arquivos de tasks, execuções e projetos com versões imutáveis (mesmo nome na mesma task = próxima versão; mesmo conteúdo não cria versão), limite por arquivo (`HANKO_ARTIFACT_MAX_BYTES`, 5 MiB) e cota por empresa (`HANKO_ARTIFACT_QUOTA_BYTES`, 512 MiB).
- Artefatos na API (`…/artifacts`), na CLI (`hanko artifact put|list|get|diff|rm|usage`), no MCP (`hanko_artifact_put`) e na tela `/artefatos` (envio, filtros, prévia, versões e diff de texto); seções nas páginas da task, da execução e do projeto.
- Entrega segura do conteúdo (`internal/artifactsrv`): `nosniff`, CSP com `sandbox`, `Cross-Origin-Resource-Policy`; só texto puro, Markdown e imagem raster abrem no navegador, o resto baixa.
- Export/import com `--with-artifacts` (campo opcional `artifacts` em `hanko.company.v1`, até 20 MiB; o formato não muda para quem não usa).
- Metas (ADR-0028, `goals`, `goal_updates`, `tasks.goal_id`): valor inicial, alvo e atual, progresso de 0 a 100%, atingida ao cruzar o alvo (atividade `goal.achieved`); API `…/goals`, CLI `hanko goal …` e `hanko task create --goal`, tela `/metas` com gráfico SVG, quadro "Metas" no projeto e meta na página da task.
- Metas no briefing da run e tools MCP `hanko_goal_list` e `hanko_goal_update`; "Metas atingidas" (informativo) na Caixa de entrada.
- English summary: versioned artifacts (upload, versions, text diff, safe serving, quota, export/import with `--with-artifacts`) and measurable goals (progress, achieved on crossing the target, run briefing, MCP tools, inbox) replace the two "coming soon" screens.

### Correções pós-deploy (prd, 2026-10-15)

- Personas no prd (adapter `claude`): `--allowedTools mcp__hanko` em runs com escrita (o modo headless negava as tools MCP do Hanko); revisão somente leitura não recebe a liberação.
- `HANKO_AGENT_URL`: base pela qual o agente (processo filho) chama `/mcp` e `/memory-mcp`; vazio = `HANKO_PUBLIC_URL`. O deploy do prd usa o loopback interno (`http://127.0.0.1:7420`), sem depender de DNS/TLS públicos.
- Comentário humano em task `in_review` ou `blocked` acorda a persona e reabre a task (`in_progress`); antes o wake era enfileirado e o checkout o recusava sem aviso. Heartbeat e atribuição seguem exigindo `todo`/`in_progress`.
- Diagnóstico das runs `claude`: o evento inicial lista o estado de cada servidor MCP (`mcp: hanko=connected`) e o final lista as tools negadas por permissão.
- Busca de memória lê o formato real do ai-memory (`{"hits":[{id,path,title,snippet,rank}]}`); `rank` vira score.
- Personas `claude` aceitam `adapter_config.env` (variáveis só da persona, ex.: `ANTHROPIC_BASE_URL`/`ANTHROPIC_AUTH_TOKEN`/`ANTHROPIC_MODEL` para um servidor compatível com a API Anthropic); as variáveis da run vencem e credenciais são redigidas dos eventos.
- Teste de modelo local (oMLX no Mac via Tailscale, 2026-10-15): a conexão a partir do container funciona, mas os modelos testados (Qwen2.5-Coder-14B, Qwen3-8B-4bit) não emitem `tool_use`; a persona simples segue no Haiku (ver ROADMAP).
- `HANKO_MEMORY_READ_TOOL` passa a ter padrão `memory_read_page` (tool real do ai-memory): o diff da curadoria mostra o conteúdo atual sem configuração.
- CI: Actions em majors com runtime Node 24 (checkout, setup-go, setup-node v7; golangci-lint-action v9; goreleaser-action v7) e runners em `ubuntu-26.04`, validado em ramo antes da troca do `ubuntu-latest` (19/10/2026; runner-images#14748).
- Deploy do prd: snapshots do LXC só com `--snapshot` (o backup pré-deploy continua); CI de integração roda `-p 1 -timeout 30m`.
- English summary: prd persona fixes (MCP tool permission, internal agent URL, comments reopening in_review/blocked tasks, MCP/denial diagnostics), real ai-memory search format, Node 24 Actions and pinned ubuntu-24.04 runners.

### Correções da revisão

- Memória na busca usa a tool real `memory_query` do ai-memory (confirmada em `tools/list`): um escopo por workspace da empresa, nunca `global`; consulta sanitizada para o FTS5 e realce HTML do resultado vira texto. A política de curadoria passou a conhecer os nomes reais das tools do ai-memory.

- ACP (ADR-0023): o comando do harness `acp` virou decisão do operador. `HANKO_ACP_ALLOW` (padrão desligado) liga o harness e `HANKO_ACP_COMMANDS` restringe a uma lista de executáveis; a checagem vale ao contratar/importar (422) e de novo ao iniciar a run.
- `adapter_config.env` (e valores de flags como `--api-key`) voltam mascarados (`***`) na API e na CLI; só os nomes das variáveis aparecem.
- Webhooks: a poda do histórico só apaga entregas encerradas (pending/retrying não se perdem); entrega que sumiu da tabela encerra o job com erro registrado; `run.failed` também dispara para run `interrupted`.
- Revisão automática de persona ACP sem revisor dedicado mantém o `adapter_config` quando o harness é o mesmo da autora.
- procrun: depois do `Wait`, o grupo de processos sempre recebe SIGKILL (sem órfãos de MCP stdio); ACP `fs/*` recusa arquivo não regular (FIFO, device) antes de abrir.
- Import aplica os slugs reservados de integração e o gate de MCP stdio; `skillmd.Parse` e as rotas de skills limitam o tamanho antes do parse; o máximo de `deliveries?limit` é 100 na API e no service.

- Skills (ADR-0029): habilidades reutilizáveis em `SKILL.md`, ligadas por persona; o Claude as recebe como skills de verdade (`--add-dir`) e codex/ACP por índice no prompt e pela tool MCP `hanko_skill_get`; limites por skill e por run; `hanko skill …`, tela `/skills` com editor e pré-visualização, painel na persona, campo opcional `skills` no `hanko.company.v1`.
- Integrações (ADR-0029): servidores MCP da empresa (http, ou stdio só se `HANKO_INTEGRATIONS_ALLOW_STDIO`) injetados nas runs, e webhooks de saída assinados com HMAC-SHA256 (`X-Hanko-Signature`), entrega por outbox transacional com retry exponencial, histórico de entregas, proteção SSRF; `hanko integration …`, tela `/integracoes`.
- Segredos de integração cifrados em repouso (AES-256-GCM, `HANKO_SECRET_KEY`, rotação com `HANKO_SECRET_KEY_PREVIOUS` e `hanko integration rekey`), nunca devolvidos pela API, UI, CLI, export, eventos ou logs; o segredo de webhook gerado pelo servidor aparece uma única vez.
- Export/import: `integrations` (sem segredos; importadas desligadas), `skills` e `personas[].skills` são campos opcionais do `hanko.company.v1`.
- Migration 00020 (`skills`, `persona_skills`, `integrations`, `integration_personas`, `webhook_deliveries`).
- Ilhas React (ADR-0026): infraestrutura de build em `ui/islands/` (esbuild, versões fixas, `make islands`, bundle commitado e conferido no CI, orçamento de tamanho por ilha).
- Log da run virtualizado (10 mil+ eventos fluidos): carga pelo fim com histórico por cursor (`GET …/events?tail=true` e `before=<seq>`), SSE para o resto sem perder nem duplicar, pausa ao rolar para cima, filtro por tipo, busca e payload de tool call expansível.
- Organograma interativo: arrastar uma persona sobre outra muda `reports_to` (ciclos recusados), zoom/pan, colapsar ramos e indicador de run ativa; viewer e member não arrastam.
- Comentários ancorados a um trecho da descrição da task ou do resumo de uma run (migration 00017, `anchor` na API, `hanko task comment --quote`, MCP `quote`): a âncora é re-localizada pelo texto e vira "âncora perdida" quando o trecho some.
- Adapter `acp` (ADR-0023): `agent = "acp"` roda qualquer agente que fale o Agent Client Protocol v1 por stdio, com o comando em `adapter_config` (validado ao contratar/importar), política de permissão `allow`/`reject`/`read_only`, `fs/*` confinado ao workspace, cancelamento por `session/cancel`; migration 00014; contratação pela UI, CLI (`--config-json`) e API. / `acp` adapter: runs any ACP v1 agent over stdio.

- Busca única da empresa (ADR-0024): tela Buscar (`/` foca o campo), `GET /api/v1/companies/{c}/search`, `hanko search` e a tool MCP `hanko_search`, com full-text do Postgres sem extensão (acento e caixa dobrados, prefixo na última palavra, `"frase"`, `-excluir`, `OR`, chave `HNK-n` direta), ranking por relevância e recência, trecho com marcas sem HTML, e a memória da empresa quando há provider (migration `00015`).

- Consolidação de memória governada (ADR-0030): persona curadora periódica como run de sistema (gatilho `memory`, custo no ledger, desligada por padrão), propostas que removem/fundem/reescrevem com gate `memory` no Inbox e aplicação por job com retry, permissões de memória por workspace no proxy `/memory-mcp`.
- Seção "Memória" nas Configurações da empresa; API `/companies/{c}/memory`; CLI `hanko memory status|config|consolidate|history|proposals|approve|reject|retry|grants|grant|revoke`; variável `HANKO_MEMORY_READ_TOOL`.
- Chat (ADR-0027): conversa direta de um humano com uma persona, fora de uma task. Cada mensagem dispara uma run com o gatilho `chat` (mesmo adapter, ledger, orçamento, trava semanal, alertas e log), retoma a sessão do harness quando ele sabe e responde com o resumo final; UI em `/c/{empresa}/chat`, API `/conversations`, CLI `hanko chat <persona> "mensagem"` (`--no-wait`, `--conversation`, `list`, `show`), conversas na página da persona e runs de chat identificadas em Execuções e Custos (migration 00018).

### Correções da revisão
- Curadoria de memória (ADR-0030): a task de sistema da consolidação sempre roda com gatilho `memory` (a rede de segurança do heartbeat não a faz mais virar uma run comum e contornar a política de propostas), não acorda por comentário nem por "Acordar" manual, e o proxy/executor decidem "é curadoria" pela task da run.
- Aplicação de propostas: sem snooze longo (retry manual e varredura agem na hora), tentativa marcada antes da chamada (`attempt_started_at`, migration 00023) para repetir sem efeito duplicado, sha256 recalculado antes de aplicar, `company_id` nas queries de marcação, e o proxy só sobrepõe `workspace`/`project` que a chamada já traz.
- Chat (ADR-0027): sem polling eterno com mensagens esperando sem run (aviso "Mensagens esperando resposta"), sem atividade/webhook `conversation.created` de conversa recusada, e runs de chat falhas na Caixa de entrada com link para a conversa.
- Workspaces (ADR-0025): caminhos novos precisam ficar em `HANKO_WORKSPACES_ROOT/<slug-da-empresa>/...` (os antigos seguem funcionando, com aviso na UI) e o diretório é revalidado (symlinks) antes de cada run; fora da raiz, diretório temporário e evento de aviso. / Review fixes: curation runs cannot be bypassed by the wake trigger, proposal application is idempotent and verified, chat and workspace hardening.

### Correções da revisão (artefatos e metas)

- Cota de artefatos por empresa não é mais furável por uploads simultâneos de nomes diferentes (trava consultiva por empresa antes da trava da cadeia).
- A meta da própria task entra sempre no briefing da run (o corte em 5 podia descartá-la).
- `hanko_artifact_put`: o corpo aceito pelo transporte MCP acompanha `HANKO_ARTIFACT_MAX_BYTES` (antes ~3 MiB de arquivo, pelo padrão de 4 MiB do SDK).
- Run token de chat (sem task) publica artefatos pela API REST; informar `task` com esse token é recusado.
- Migration 00025: `artifacts.task_id` passa a `ON DELETE CASCADE` (apagar a task apaga as versões dela em vez de fundi-las à cadeia sem task).
- Nome de artefato recusa caracteres de formatação Unicode (bidi, largura zero), na publicação e na importação.
- O corpo da rota de envio da API usa o limite configurado, não o máximo absoluto de 16 MiB.
- Markdown de artefato não carrega imagem externa (vira link): sem pixel de rastreio.

- Busca (ADR-0024, migration 00024): o grupo Memória só aparece quando o servidor tem a tool `memory_search` (aviso único no log); `hanko_fold` remove caracteres de formatação (hífen suave, ZWJ, BOM...) e a tokenização Go x Postgres ficou coerente (Nl, marcas combinantes), com testes de paridade estritos; prefixo só com 2+ letras (3+ em comentários) e teto de 2 s por consulta com mensagem amigável; o campo de busca usa `hx-replace-url`; `serve` avisa se o banco está em `lc_ctype=C`; RUNBOOK com locale, mudança de `hanko_fold` e instalações grandes.
- Ilhas (ADR-0026): `mountReact` restaura o HTML do servidor se a ilha quebrar ao renderizar; destaque da busca do log com offsets corretos (`İ`); `GET /runs/{id}/events` de run de outra empresa ou inexistente dá 404; bundles reconstruídos.
- English summary: review fixes — search memory group only with the `memory_search` tool, formatting-character folding (migration 00024), strict Go/SQL parity tests, prefix floor and 2 s statement timeout, `hx-replace-url`; islands restore the server HTML on render errors, correct highlight offsets, 404 for foreign-company run events.

## [0.1.0] — 2026-10-14 (marco interno, sem tag; incluído na 0.2.0)

Primeira versão pública. Em produção em https://hanko.oute.pro desde 2026-10-14.

### Núcleo (M0–M1)
- Binário único em Go 1.27; Postgres 17 como única dependência de dados; migrations goose embutidas; fila river.
- Domínio com linguagem ubíqua (ADR-0016): empresa, workspace, projeto, persona, task (`HNK-n`), run, comentário,
  activity com `event_id` idempotente, `occurred_at`/`received_at` e `actor_type`.
- Heartbeat: wake coalescido sem perda, checkout atômico, rede de segurança por tempo sem mudança humana.
- Adapters `claude` (`-p --output-format stream-json`), `codex` (`exec --json`) e `fake`; ambiente do harness por
  allowlist (segredos do servidor nunca chegam ao agente) e redação de segredos nos eventos.
- API REST `/api/v1` (huma, `api/openapi.json` versionado), SSE do log ao vivo, CLI completa (`hanko …`).
- OpenTelemetry sempre ligado: traces, métricas (OTLP + `/metrics`) e logs; zero phone-home.

### UI, MCP e memória (M2)
- UI web (templ + htmx, CSS próprio com tokens do Kubo DS): painel, tasks, thread, runs ao vivo, personas, claro/escuro, 390 px.
- Servidor MCP `/mcp` (tools `hanko_*`) para os agentes.
- Memória entre sessões com ai-memory ligada por padrão: briefing no despacho, handoff entre runs, `memory_outbox`
  com backoff, proxy `/memory-mcp` (o token da memória nunca vai ao agente).

### Organograma e gates (M3)
- `reports_to`, delegação só pela subárvore, sem ciclos; organograma em SVG.
- Gates de aprovação humana (aprovação, revisão de task, contratação) com trilha de auditoria; Caixa de entrada e Decisões.

### Custos, orçamentos e fluxos (M4)
- Ledger de custo por run (reportado × estimado), orçamento por empresa e persona com hard stop e reserva por run em voo.
- Fluxos agendados (cron + fuso), "Rodar agora", catch-up sem acúmulo.

### Autenticação e produção (M5)
- `HANKO_MODE=authenticated` sempre por padrão: provider `local` (argon2id) e `firebase` (Google + e-mail/senha,
  ID token validado via JWKS), sessões com teto absoluto, API keys `hk_…`, limites contra força bruta (ADR-0017/0018).
- Deploy de produção no oute-server (`deploy/oute-server/deploy-prd.sh`): LXC, compose com Postgres, LGTM e ai-memory
  (digest fixado), nginx + certbot no host, vhost também no IP da tailnet, smoke pós-deploy e snapshot.
- Backup: timer diário no LXC (`pg_dump -Fc` + tar consistente do ai-memory) coletado pelo restic semanal do lab;
  `restore.sh --check` (banco descartável) e `--apply`.

### Lote 2026-10-15
- Assinaturas como entidade, quatro custos (real, lista, estimado, pago com rateio da mensalidade), histórico de
  preços append-only, trava semanal por assinatura, origem da escolha de modelo no run (ADR-0019).
- Alertas operacionais (persona silenciosa, run travada, orçamento ≥ 80%) na Caixa de entrada e em `/metrics`;
  nome amigável de run (`Adjetivo_Substantivo`); revisão automática por outro modelo antes do gate humano (ADR-0020).
- Modelo do Claude: `claude-haiku/sonnet/opus` traduzidos para os aliases do CLI.
- Multi-humano: papéis por empresa (owner/admin/member/viewer), autorização no service (API, UI, MCP e SSE),
  convites por link de uso único, `hanko member …`, `hanko auth user …` para o dono da instalação (ADR-0021).
- Troca de empresa na sidebar; formulário do revisor na persona; página "nenhuma empresa".
- `hanko export` / `hanko import` de uma empresa em JSON `hanko.company.v1`, sem ids nem segredos (ADR-0022); a empresa importada entra pausada (`--active` liga), o export de logs sai em fluxo e o arquivo não é anônimo.
- Projetos e workspaces de primeira classe: CRUD em UI, API, CLI e MCP; caminho validado dentro de `HANKO_WORKSPACES_ROOT`; orçamento mensal por projeto no guard do ADR-0020 (recusa a run, alerta a 80%); filtro por projeto em tasks e custos; pasta da run por task > projeto > persona com a origem gravada; export/import com os campos novos (ADR-0025).
- UI e CLI bilíngues pt-BR/en (UI: cookie `hanko_lang` > `Accept-Language`; CLI: `HANKO_LANG` > locale).
- Deploy: backup consistente antes de subir versão nova; restore com rollback do banco anterior.
- Documentação em inglês (`README.md`, `CONTRIBUTING.md`, `docs/en/`).
- CI no GitHub: golangci-lint (gosec, revive, staticcheck), govulncheck, testes com `-race` e integração com Postgres,
  diff de openapi/templ/sqlc e dry-run do goreleaser.
- PWA instalável: manifest, ícones (192/512/maskable), service worker só com assets estáticos versionados e página
  `/offline` bilíngue; nenhum HTML autenticado, API ou SSE entra no cache.

### Desempenho
- Migration `00013_indices_desempenho.sql` e ajustes de consulta medidos com `tools/seed-load` + `tools/perf-explain`
  numa carga de 5 M de eventos de run, 1 M de atividades, 200 mil runs e 50 mil tasks (`docs/PERF.md`): wake candidates do
  heartbeat 1,3 s → 1,5 ms, personas silenciosas (alert_tick) 177 → 3 ms, runs por nome/por task 25–100 ms → 0,1 ms, Caixa
  de entrada (runs que falharam) 68 → 3 ms, listagem de tasks 48 → 1 ms, de gates 21 → 4 ms. `fillfactor 80` em `runs`
  torna HOT o UPDATE de `next_event_seq` de cada evento anexado. Em bases grandes, criar os índices antes com
  `CREATE INDEX CONCURRENTLY` (ver o cabeçalho da migration).

### Corrigido
- 37 achados das revisões independentes do M1–M5 (token de API não validado, segredos herdados pelo agente,
  estouro de orçamento, laço de runs, wake perdido, gate em task alheia, primeiro visitante virando dono, entre outros),
  cada um com teste.
- `heartbeat_interval_sec` acima do int4 era truncado; agora é recusado (0 a 30 dias).
- Quatro revisões independentes do lote de 2026-10-15 (50+ achados): run de revisão recebia o token do Hanko pela config do
  MCP de memória e podia editar o workspace; ordem de locks com risco de deadlock; restore destrutivo sem volta;
  token de convite em traces/logs; flag de convidado permanente; posse de identidade no provider local; comparação
  de e-mail com dobra Unicode; entre outros — cada um com teste.

**English:** first public release — single Go binary control plane for AI agent teams: tasks, org chart, heartbeat,
human approval gates, budgets with hard stop, scheduled flows, cross-session memory, subscriptions and four-way cost
tracking, operational alerts, automatic second-model review, always-on OpenTelemetry, authenticated by default.

# Changelog

Formato: [Keep a Changelog](https://keepachangelog.com/pt-BR/1.1.0/) · versões: [SemVer](https://semver.org/lang/pt-BR/).
English summary at the end of each release.

## [Unreleased]

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

## [Não lançado] — v0.1.0

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

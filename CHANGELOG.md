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

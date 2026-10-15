# Changelog

Formato: [Keep a Changelog](https://keepachangelog.com/pt-BR/1.1.0/) · versões: [SemVer](https://semver.org/lang/pt-BR/).
English summary at the end of each release.

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
- `hanko export` / `hanko import` de uma empresa em JSON `hanko.company.v1`, sem ids nem segredos (ADR-0022).
- UI bilíngue pt-BR/en (cookie `hanko_lang` > `Accept-Language`).
- Deploy: backup consistente antes de subir versão nova; restore com rollback do banco anterior.
- Documentação em inglês (`README.md`, `CONTRIBUTING.md`, `docs/en/`).
- CI no GitHub: golangci-lint (gosec, revive, staticcheck), govulncheck, testes com `-race` e integração com Postgres,
  diff de openapi/templ/sqlc e dry-run do goreleaser.
- PWA instalável: manifest, ícones (192/512/maskable), service worker só com assets estáticos versionados e página
  `/offline` bilíngue; nenhum HTML autenticado, API ou SSE entra no cache.

### Corrigido
- 37 achados das revisões independentes do M1–M5 (token de API não validado, segredos herdados pelo agente,
  estouro de orçamento, laço de runs, wake perdido, gate em task alheia, primeiro visitante virando dono, entre outros),
  cada um com teste.
- `heartbeat_interval_sec` acima do int4 era truncado; agora é recusado (0 a 30 dias).
- Duas revisões independentes do lote de 2026-10-15 (24 achados): run de revisão recebia o token do Hanko pela config do
  MCP de memória e podia editar o workspace; ordem de locks com risco de deadlock; restore destrutivo sem volta;
  token de convite em traces/logs; flag de convidado permanente; posse de identidade no provider local; comparação
  de e-mail com dobra Unicode; entre outros — cada um com teste.

**English:** first public release — single Go binary control plane for AI agent teams: tasks, org chart, heartbeat,
human approval gates, budgets with hard stop, scheduled flows, cross-session memory, subscriptions and four-way cost
tracking, operational alerts, automatic second-model review, always-on OpenTelemetry, authenticated by default.

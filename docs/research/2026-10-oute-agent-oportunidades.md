# oute-agent → Hanko: oportunidades (2026-10-13)

Leitura do repo `~/Projects/Github/oute-agent` (v0.8.8, 549 commits, 10 ADRs, bash + Python, 3 bancos). Fonte: `CONTEXT.md`, ADR-01/02/04/07/08/10, `config/select/models.toml`, `config/agent-studio/config.toml`.

## 1. Leitura estratégica — a sobreposição

| oute-agent hoje | Hanko quer ser |
|---|---|
| agent-studio: telemetria, custo, uso por papel/fase, alertas, página da rodada, tray | dashboard, budget, activity, observabilidade |
| canal de aprovação (`oute-propose` → `oute approve`), pedidos, credencial de marcação | approvals (M3) |
| swarm: dispatcher tria → ok humano → 1 worker por issue → etapas → kaizen | org chart, delegação, checkout atômico (M3), routines (M5) |
| seletor de modelo por fase, reserva por cota, trava semanal | política de modelo por agente, budget hard stop (M4) |
| runtime: container com Claude/Codex, worktree por sessão, segredos, addons | **não é o Hanko** — é o que o adapter `cli` executa |

70% do agent-studio + canal + swarm é domínio do Hanko. Sem decisão explícita, viram dois produtos com o mesmo domínio.

**Proposta (ADR-0015):** Hanko = control plane; oute-agent = runtime/sandbox onde o adapter `cli` executa (local, SSH ou `docker exec`). agent-studio, canal e dispatcher migram para dentro do Hanko entre v0.1 e v0.3 e são desligados. Hanko ganha usuário zero real, meses de telemetria real e um runtime endurecido de graça.

## 2. Incorporar no v0.1

### M1 — loop mínimo
- **Trace única control plane + agente.** Adapter `cli` injeta `OTEL_RESOURCE_ATTRIBUTES=hanko.company_id,hanko.agent_id,hanko.run_id,hanko.issue_id,hanko.subscription` + `CLAUDE_CODE_ENABLE_TELEMETRY=1` (+ `ENHANCED_TELEMETRY_BETA`). Spans `claude_code.llm_request` com `cost_usd` chegam ao mesmo collector com o `run_id`. Trace por run atravessa Hanko → agente → modelo. (ADR-04/08.)
- **Idempotência em toda escrita vinda de agente.** `event_id` determinístico (tipo + run + hora do fato + chave + ocorrência) como chave de dedupe em comentário, status, evento. At-least-once no cliente, dedupe no servidor. (`oute.event.id`, ADR-08 §2.)
- **Hora do fato ≠ hora de chegada.** `occurred_at` + `received_at` em `activity_event`/`run_event`; consultas pela hora do fato; UTC no banco, `timezone` de exibição por company. (ADR-08 #415.)
- **Redação de segredo** em log e telemetria desde o 1º registro (padrões de credencial). (ADR-04 adendo.)
- **Papel do ator.** `actor_type ∈ {agent, human, system}` em todo `activity_event` — o `oute.agent=human`.
- **Subagente visível.** Chamada com `query_source_safe` começando em `agent.`/`agent_id` recebe papel `subagent` no ledger; custo atribuído ao run pai, quebrado por papel. (ADR-08 #832.)

### M2 — UI + MCP
- **Carregamento por bloco** com shimmer e alternativa sem JS (ADR-08 #536) — cai como luva no htmx (`hx-trigger="load"` por bloco).
- **Tabelas com ordem/filtro/página pela URL** (#529) — estado na URL, não em JS.

### M3 — org chart + approvals
- **Pedido = modelo de `approval`.** Id determinístico, conteúdo + sha256, estados `pending | executed(rc) | refused | expired`; decisão sempre por `human`; pendente sem artefato correspondente some da lista (#815). Audit trail completo em `activity_event`.
- **Escopos de credencial** (ADR-08 §6 — nasceu de um vetor real: agente em yolo com a credencial de ingestão forja evento de outro pedido). API keys do Hanko com escopo: `agent` (escreve só no próprio run/issue), `read` (tray, dashboards), `mark` (aprovar/reconhecer — só sessão humana). Agente nunca forja evento de outro run.
- **Dispatcher → ok humano → spawn.** Gate de aprovação antes de o manager abrir workers; 1 worker por issue com checkout atômico. (swarm §triagem.)
- **Etapas publicadas + revisor headless.** `issue_step` (triagem, merge, kaizen, fechamento) no thread, com veredito de um **revisor de outro modelo** gravado por sha256 antes de ir ao humano; reprovado = fechado até o humano abrir. É o "crítico cego" nativo. (ADR-08 #489.)
- **Nome amigável** `Adjetivo_Substantivo` + id técnico para rodadas/runs (#605) — humano lembra nome, máquina usa id.

### M4 — budget (fonte mais rica)
- **Preço vigente com histórico append-only.** Seed por config (`fixed = true` trava), conferência diária em models.dev + OpenRouter, troca só quando **as duas concordam**, custo sempre pelo preço da **hora do fato**, nunca recalcula. (ADR-08 #339.) A conferência diária é uma routine built-in (M5).
- **Quatro custos:** real (`cost_usd` do span ou do log `api_request` de mesmo `request_id`), lista, estimado, **pago** (mensalidade do plano ÷ dias do mês, rateada no dia pelo custo de lista; "sem uso" quando o dia não tem chamada; soma do mês fecha com as mensalidades). Ninguém no mercado mostra custo pago de assinatura. (ADR-08 #748.)
- **Saída × entrada+cache** por chamada, `context.avg/max` por sessão, top-N sessões/conversas mais caras. (#751.)
- **Assinatura como entidade.** `subscription` (claude, codex, zai…) com `weekly_guard_pct`, janelas 5h/7d lidas por `quota` reader, status (`claude auth status`), `prefixes` de modelo. Política por grupo de fase: raciocínio `claude→zai→codex`, execução `zai→claude→codex`; "nunca recusa por cota, exceto trava semanal". (ADR-02 #598/#629/#742.) No Hanko: `agent.model_policy` + `company.subscriptions`; hard stop de budget = o mesmo mecanismo da trava.
- **Precedência de escolha de modelo:** manual > label > fase > classificador > default, com `origem da escolha` gravada no run. (ADR-02.)
- **Alertas** com critério e limite documentados, nunca por string de log (ADR-08 §8): no Hanko — agente sem heartbeat, run preso, budget ≥ 80 %, fila/collector.

### M6 — release
- `VERSION` + `changelog.d/` por PR → `CHANGELOG.md` na release; `scripts/release`.

## 3. v0.2+
- **AI-DLC como workflow template**: `issue.phase` (12 fases + `ctx`), gate humano por fase, outcome; skills/instruções por fase. (ADR-07.) Hanko pode ter "workflow templates" por company; AI-DLC é o primeiro.
- **Lições / insights / ciclo (learn → iter)**: memória de longo prazo com aprovação humana — insight (padrão com evidência) → lição (regra, nível repo/swarm/agentes/skill) ou melhoria (issue). Responde "memória entre sessões" no produto. (ADR-07 adendo #109.)
- **Modo dark** (ADR-10): `autonomy: gated | dark` por company/agent, juiz (modelo de raciocínio) decide triagem/merge/fechamento; contrato de deploy por hash.
- **ai-memory** como provider de memória (MCP + hooks), `project-strategy repo-root`.
- **Addons**: skills `hanko-*` montadas read-only, com plano B inline — catálogo de skills v1.
- **Tray** Mac (Swift) lendo API `read` — só depois do M6.

## 4. Não trazer
- SurrealDB / DuckDB / 3 bancos (ADR-0011): tudo em Postgres; telemetria bruta fica no collector/LGTM, Hanko guarda só o ledger.
- Python + bash como linguagem de produto; testes em bash.
- Vaultwarden como dependência obrigatória → provider opcional de secrets (v0.2).
- Tailnet como fronteira de auth → Hanko tem auth própria (ADR-0007/0014).
- Jev/TypeSafe (classificador externo com chave) → fase por label/manual no v0.1.
- ADR de 167 KB: no Hanko ADR curta + `CONTEXT.md` derivado + glossário.

## 5. Práticas de doc a adotar agora
- `CONTEXT.md` derivado dos ADRs + **glossário** com termos a evitar (linguagem ubíqua: company, agent, issue, run, approval, routine, subscription, step, actor). `docs/agents/domain.md` como regra de leitura.
- `changelog.d/` + `VERSION`.
- Regra "medido, não suposto" nos adendos de ADR.

## 6. Próximos passos propostos
1. ADR-0015 — fronteira Hanko × oute-agent (control plane × runtime) e plano de migração do agent-studio.
2. `docs/CONTEXT.md` + glossário do Hanko antes do contrato do M1.
3. M1 contrato já com: `event_id`, `occurred_at/received_at`, `actor_type`, `subscription` no schema; adapter `cli` com `OTEL_RESOURCE_ATTRIBUTES`.

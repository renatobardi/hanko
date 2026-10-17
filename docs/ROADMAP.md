# Roadmap v0.1

**Status 2026-10-15:** M0–M5 entregues e em produção (https://hanko.oute.pro). Falta só o gate humano do processo: demos gravadas e tags `v0.1.0-mN`/`v0.1.0`. Parte do v0.2 já entrou (abaixo). Ver `docs/SESSION.md`.

**Capacidade declarada: 60 h/semana (40 h dias úteis + 20 h fim de semana).** Meta: **6 semanas** (5 de entrega + 1 de folga). Gargalo deixa de ser hora de código e passa a ser o gate humano (revisão, decisão, demo) — por isso a folga.

Alavancas (ADR-0009): contrato antes de código → 3 trilhas paralelas de agentes; incremento executável + commit por sessão (`AGENTS.md`); demo semanal; ≥70% cobertura só em domain/heartbeat.

Regras de parada:
- Milestone estoura 2x → corta escopo, nunca estica prazo.
- M1 não roda na semana 1 → para e reavalia.
- Pós-M2: UI >50% das horas → reavalia ADR-0005.
- Duas semanas seguidas abaixo de 40 h reais → replaneja para 8 semanas (não força ritmo).

| M | Semana | Entregável | Demo |
|---|---|---|---|
| M1 ✅ | 1 | Contrato (migrations com vocabulário ADR-0016 + `event_id`, `occurred_at/received_at`, `actor_type`, `memory_outbox`) → company, persona (adapter `cli`), task, heartbeat (river), run, log SSE, comentário. API huma + CLI. OTel do 1º request. | `hanko serve` → contratar persona → criar task → run → log ao vivo → comentário. Trace no Grafana. |
| M2 ✅ | 2 | UI host (Kubo DS): painel, tasks/board, task + thread, execuções ao vivo, contratar persona. MCP server. **ai-memory ligado**: personas (MCP+hooks) e Hanko (outbox + briefing no despacho). | Run 2 no mesmo workspace recebe handoff do run 1. Dogfood começa. |
| M3 ✅ | 3 | Organograma (reports_to, delegação, checkout atômico) + Gates (audit trail, Caixa de entrada/Decisões). | Persona delega; humano decide gate; decisão vira memória. |
| M4 ✅ | 4 | Custos e orçamentos (ledger, hard stop, assinaturas) + Fluxos (river periodic). | Orçamento estoura → run bloqueado. Fluxo diário cria task sozinho. |
| M5 ✅ | 5 | Auth firebase/local; deploy `hanko-prd` (pg17 + ai-memory + LGTM; borda nginx do host), backup/restore, smoke, docs en, UI pt-BR/en, CHANGELOG. | hanko.oute.pro com login Google. |
| — | 6 | Folga: estouro, bugs do dogfood, polish. | — |

## v0.2
Entregue em 2026-10-15 (antes do planejado):
- Assinaturas, quatro custos, trava semanal, origem do modelo (ADR-0019).
- Alertas operacionais, nome amigável de run, revisão automática por outro modelo (ADR-0020).
- Multi-humano: papéis por empresa (owner/admin/member/viewer), convites, autorização no service (ADR-0021).
- Troca de empresa na UI; formulário do revisor; UI bilíngue (pt-BR/en).
- `hanko export` / `hanko import` de empresa (ADR-0022).
- Busca única da empresa: tela Buscar, API, CLI e tool MCP, full-text do Postgres e memória (ADR-0024).

- Consolidação de memória governada: curadoria periódica como run de sistema, propostas com gate humano, permissões por workspace (ADR-0030).

- ACP: harness genérico por stdio (JSON-RPC), liberado pelo operador com `HANKO_ACP_ALLOW` (ADR-0023).
- Ilhas React: log de run virtualizado, organograma interativo, editor ancorado de comentários (ADR-0026).
- Projetos e workspaces com raiz por empresa e orçamento de projeto (ADR-0025); chat direto com persona (ADR-0027).
- Artefatos versionados e metas (ADR-0028); skills e integrações MCP/webhook (ADR-0029).
- PWA e CLI em inglês.
- Quatro revisões independentes e as correções de todos os achados.

Pendente: tag `v0.1.0`/`v0.2.0` e demos.

## v0.3 (em andamento)
- UI alinhada ao Kubo DS e telas de paridade (ADR-0031): tokens, fonte e cards do DS, glifo por persona, tema claro/escuro, quadro de tasks arrastável e visões salvas, projeto e workspace em abas, timeline, painel ao vivo, ambientes/adapters/plugins, export/import pela UI, times prontos, troca da própria senha.
- Paridade v0.3 (ADR-0032 a 0038, migrations 00027–00034): cards de status; sala com várias personas; pipelines, cases, fila de revisão e aprendizados; versões, fontes e estúdio de skills; catálogo de tools, perfis e gateways; ambientes SSH e sandbox; plugins de terceiros. Fora do escopo: export/import de pipelines, cases e perfis de tools; eventos de webhook de fatos de case; cases na busca unificada; SSE nos gateways.

## Depois (ideias, sem data)
- Modelo local no Mac (oMLX) por persona: harness `claude` com `adapter_config.env` (`ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_MODEL`) apontando ao endpoint Anthropic do oMLX; não suportado oficialmente pela Anthropic para modelos não-Claude. **Testado em 2026-10-15 (Mac 24 GB, oMLX 0.7.0 via Tailscale): inviável por ora.** Rede, chave e `/v1/messages` funcionam a partir do container do Hanko, mas Qwen2.5-Coder-14B e Qwen3-8B-4bit devolvem a chamada de tool como texto (sem `tool_use`), e o Qwen3.8-27B não cabe na RAM com o Mac em uso. Persona operacional simples fica no Haiku; reavaliar com mais RAM ou com um modelo de tool calling comprovado no oMLX. Se testar de novo: guard de memória `aggressive`, `sudo purge` antes e `--tools Bash` para encolher o prompt.
- Harness Pi (`pi --mode json -p`, sem MCP: tools do Hanko pela CLI `hanko` com o run token) e Devin CLI (provavelmente via o harness ACP existente, `devin acp`); adiados por decisão do dono.

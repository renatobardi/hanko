# Roadmap v0.1

**Status 2026-10-14:** M0, M1 e M2 implementados e testados (ver `docs/SESSION.md`). Falta a demo gravada para fechar os gates e criar as tags.

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
| M3 | 3 | Organograma (reports_to, delegação, checkout atômico) + Gates (audit trail, Caixa de entrada/Decisões). | Persona delega; humano decide gate; decisão vira memória. |
| M4 | 4 | Custos e orçamentos (ledger, hard stop, assinaturas) + Fluxos (river periodic). | Orçamento estoura → run bloqueado. Fluxo diário cria task sozinho. |
| M5 | 5 | Auth firebase/local, deploy `hanko-prd` (pg17 + ai-memory + LGTM + caddy), docs, en, `v0.1.0`. | hanko.oute.pro com login Google. |
| — | 6 | Folga: estouro, bugs do dogfood, polish. | — |

## v0.2 (não planejado)
ACP; ilhas (log virtualizado, organograma interativo, editor ancorado); multi-humano; troca de empresa na UI; consolidação de memória via gateway; PWA.

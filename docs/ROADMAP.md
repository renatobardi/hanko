# Roadmap v0.1 — "Núcleo completo"

**Premissa: ≥20 h/semana + 3 trilhas paralelas de agentes por milestone. NÃO CONFIRMADA.** Abaixo de 20 h, cada milestone dobra (volta ao plano de 12 semanas).
Cadência (ADR-0009): milestones curtos, demo gravada ao fim de cada, tag `v0.1.0-mN`. Checkpoint de demo toda semana, mesmo no meio de um milestone.

## Alavancas de velocidade (ADR-0009, revisão 2026-10-13)
1. **Contrato antes de código** — cada milestone abre com migrations + tipos de domínio + OpenAPI (1 sessão). Só então 3 trilhas paralelas (agentes) implementam contra o contrato.
2. **Incremento visível por sessão** — toda sessão termina com algo executável + commit. Zero sessões "só refatorando". (`AGENTS.md`)
3. **MCP server sai do M1** — CLI já dá controle; MCP entra no M2 junto com o dogfood.
4. **Demo semanal** — não só no fim do milestone.
5. **Qualidade onde importa** — ≥70% de cobertura só em `domain` e `heartbeat`; resto best-effort até v0.1.

Regras de parada:
- Milestone estoura 2x → corta escopo. Nunca estica prazo.
- M1 não roda em 1 semana (2 com <20 h) → para tudo e reavalia o projeto.
- Checkpoint pós-M2: UI consumindo >50% das horas → reavalia ADR-0005.

| M | Semana | Entregável | Demo (critério de pronto) |
|---|---|---|---|
| M1 | 1 | Loop mínimo: company, agent (adapter `cli`), issue, heartbeat (river), run, log SSE, comentário do agente. API huma + CLI. OTel ligado desde o 1º request. Zero UI. Trilhas: (a) schema+sqlc+domain, (b) heartbeat+adapter cli, (c) API+CLI. | `hanko serve` → `hanko agent hire` → criar issue → agente executa → log ao vivo → comenta na issue. Trace do run visível no Grafana. |
| M2 | 2–3 | UI host (board, issue + thread, run log ao vivo, hire, settings) + MCP server. | Mesmo fluxo pelo browser. **Dogfood: Hanko constrói Hanko a partir daqui.** |
| M3 | 4–5 | Org chart (reports_to, delegação, checkout atômico) + approvals (gate + audit trail). | Agente delega sub-issue; humano aprova/recusa; trilha auditável. |
| M4 | 6 | Budget/cost ledger por run/agent/company; hard stop. | Agente estoura budget → run bloqueado → alerta. |
| M5 | 7 | Routines (river periodic). | Rotina diária cria issue e dispara agente sozinha. |
| M6 | 8 | Auth `firebase` (Google + email/senha) em hanko.oute.pro e `local` no self-host; deploy `hanko-prd` com LGTM; docs; en; `v0.1.0`. | hanko.oute.pro no ar com login Google. |

Base realista sem as alavancas: 12 semanas (M1 2 · M2 3 · M3 2 · M4 2 · M5 1 · M6 2).

## v0.2 (backlog, não planejado)
ACP engine; ilhas: log viewer virtualizado, org chart interativo, editor com comentários ancorados; multi-humano; troca de org na UI; polish visual; PWA; memória de longo prazo de agentes (learnings entre runs).

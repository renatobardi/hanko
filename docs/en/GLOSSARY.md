> Português: [docs/GLOSSARIO.md](../GLOSSARIO.md)

# Glossary — Hanko (ADR-0016, accepted)

One term per concept. Code, API, MCP and database use English; the UI is in Brazilian Portuguese (pt-BR). When in doubt, the UI term decides. The "UI (pt-BR)" column shows what the interface displays, so you can map screenshots to code.

| Term (code) | UI (pt-BR) | Definition | Avoid |
|---|---|---|---|
| company | Empresa | The multi-org isolation unit. `company_id` is on every domain table. | org, tenant |
| persona | Persona | A role hired into the company (CTO, engineer, reviewer): instructions, model, budget, and position in the org chart. | agent |
| agent | — | The harness that executes a persona in a run: `claude`, `codex`, `acp` (any ACP agent), `fake`. Appears only in adapter/config. | — |
| task | Task | A unit of work assigned to a persona. Status: backlog, todo, in_progress, in_review, blocked, done, cancelled. Priority: critical, high, medium, low. | issue, ticket |
| run | Execução | One execution of a persona on a task, triggered by the heartbeat. Status: queued, running, done, interrupted, failed, cancelled, expired. One task has 0..n runs; at most one active run per task. | job |
| conversation | Conversa (Chat) | A direct conversation between a human and a persona, outside a task (ADR-0027). Each human message triggers a run with the `chat` trigger; the run's final summary is the reply. Status: open, archived. One active run per conversation; messages that arrive while it runs wait and are answered together by the next one. | thread (that is the task's), room |
| gate | Gate | A point where a human decides (approve, reject, request changes). Always amber in the UI. Audit trail in activity. | approval, review |
| flow | Fluxo | A definition plus triggers (cron, event, manual) that creates tasks/runs. | routine, schedule |
| workspace | Workspace | The directory (and, as metadata, repository and branch) where runs work; lives inside `HANKO_WORKSPACES_ROOT/<company-slug>`. Scope of memory (ai-memory) and artifacts. Run precedence: task > project > persona (ADR-0025). | repo |
| integration | Integração | A connected external service (GitHub, Slack, ...). | connector, app |
| goal | Meta | A measurable objective: a start value, a target and a current value; progress runs from 0 to 100% and the goal is achieved on crossing the target. Tasks link to an active goal (ADR-0028). | objective, OKR |
| project | Projeto | Groups tasks, points to a workspace, is active or archived and may have a monthly budget (ADR-0025). | — |
| org_chart | Organograma | `reports_to` between personas; delegation follows the tree. | hierarchy |
| inbox / decision | Caixa de entrada / Decisão | What is waiting on a human: gates, blockers, alerts. | — |
| cost / budget | Custos e orçamentos | A ledger per run, rolling up to persona and company; budgets with a hard stop. | spend |
| subscription | Assinatura | A harness plan (claude, codex, ...): quota, weekly limit, paid cost. | — |
| activity | Atividade | `activity_event`: every domain fact, written in the same transaction, with `actor_type` (persona, human, system), `occurred_at` and `received_at`. | log, audit (it is the use, not the name) |
| heartbeat | — | A mechanism, never shown in the UI: the scheduler that wakes personas and dispatches runs (river). | — |
| handoff | Handoff | The summary one session leaves for the next (ai-memory). | — |
| user | Usuário | An authenticated human (authenticated mode): `local` or `firebase` provider, installation role owner, admin or member (`owner` sees every company). The `human` actor of a fact carries the user's `id`. | account |
| member | Membro | A user's link to a company with the role owner, admin, member or viewer (ADR-0021). A non-member cannot see the company (404). | — |
| invite | Convite | Email invite with a role, random token (only its hash is stored), valid for 7 days, accepted at `/convite/{token}`. No email is sent: the link is shown to the admin. | — |
| session | Sessão | A user's `hanko_session` cookie (30-day sliding); only the token hash is stored in the database. | — |
| api key | Chave de API | An `hk_...` key belonging to a user, for API, MCP and CLI; only the hash is stored; revocable. Not the persona's run token. | token (ambiguous) |
| artifact | Artefato | A file delivered by a persona or uploaded by a person, with immutable versions: the same name on the same task creates the next version (ADR-0028). | attachment, work product |
| skill, secret | Skill, Segredo | Named as shown. | plugin (only in the harness's native sense) |
| memory consolidation | Consolidação de memória | System run (`memory` trigger) where the curator persona deduplicates and summarises a workspace's memory; cost goes to the ledger. | — |
| memory proposal | Proposta de curadoria | A destructive curator write held by the proxy, with diff and sha256; reaches ai-memory only after the `memory` gate. | pending write (ambiguous) |
| memory grant | Concessão de memória | Persona × workspace permission (`read`/`write`) to reach another workspace's memory in the same company. | — |
| task dependency | Dependência | `task_dependencies`: a task is dispatched only when all its dependencies close (done or cancelled). No cycles, same company, at most 50. **Blocked** is derived (an open dependency), never a stored status; the `blocked` status stays the "parked waiting for someone" one (ADR-0040). | blocker (ambiguous with the `blocked` status) |
| subtask / hold | Subtask / Pausa | A subtask is a task with `parent_id` (up to 10 levels); the parent wakes when the last one closes and cannot complete with an open child (human override with a reason). A hold (`task_holds`) freezes the task and, by computed inheritance, its whole subtree: no run opens; resuming wakes the requests that were refused (ADR-0040). | freeze, suspension |
| review policy | Política de revisão | `require_review` and the automatic reviewer resolved by layer, task > project > persona, field by field; the run records where each decision came from (ADR-0040). | — |
| continuation | Continuação | A run opened from an interrupted run (`continuation_of_run_id`): it resumes the harness session and receives a briefing of what was already done. At most 2 automatic ones per chain (ADR-0039). | retry (that is another action) |
| diagnosis | Diagnóstico | The card of a run that stopped: cause, attempt, next action and owner; carries the uncertain outcome when there is one. | — |
| uncertain outcome | Desfecho incerto | A tool with an external effect was in progress when the run died; only a human decides whether it continues. | — |
| steer | Mudar o rumo | An operator message to an active run: injected live (claude, acp) or by interrupt-and-continue (codex). | — |
| follow-up | Follow-up | An instruction or message that waits for the active run in the unified queue (task and chat); editable and discardable until delivered. | — |

## How the core terms fit together

A **company** hires **personas** and arranges them in an **org chart**. Work is a **task** assigned to a persona; the **heartbeat** wakes the persona and starts a **run** using a harness (the **agent**: `claude` or `codex`) inside a **workspace**. When a decision needs a human, the persona (or a review rule) opens a **gate**, which shows up in the **inbox**. A **flow** creates tasks on a schedule. Every run adds to the **cost** ledger and is checked against the **budget**. Everything that happens is recorded as **activity**, and a **handoff** carries context to the next session.

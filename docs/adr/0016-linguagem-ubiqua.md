# ADR-0016 — Linguagem ubíqua: o vocabulário da UI é o do domínio
Data: 2026-10-13 · Status: aceita (2026-10-13)

## Contexto
A UI (Kubo DS, 29 artboards) definiu o vocabulário em pt-BR: Empresa, Persona, Task, Execução, Gate, Fluxo, Integração, Meta, Projeto, Workspace, Organograma, Caixa de entrada, Decisões, Custos e orçamentos. O domínio em Go ainda usa os nomes genéricos da categoria (agent, issue, approval, routine). Dois vocabulários = drift entre tela, código, API e MCP.

## Decisão
Código, API, MCP, banco e docs usam o equivalente em inglês do termo da UI; a UI mostra o termo em pt-BR (i18n). Um termo por conceito; `docs/GLOSSARIO.md` lista os termos e os que se evitam.

| Código / API | UI pt-BR | Evitar |
|---|---|---|
| `company` | Empresa | org, tenant |
| `persona` | Persona | agent (reservado ao harness: claude, codex) |
| `task` | Task | issue, ticket |
| `run` | Execução | job, heartbeat run |
| `gate` | Gate | approval, review |
| `flow` | Fluxo (definição + gatilhos → runs) | routine, cron, schedule |
| `integration` | Integração | connector, app |
| `goal` / `project` | Meta / Projeto | — |
| `workspace` | Workspace | repo, dir |
| `org_chart` / `reports_to` | Organograma | hierarchy |
| `inbox` / `decision` | Caixa de entrada / Decisão | — |
| `cost` / `budget` | Custos e orçamentos | spend |
| `skill` · `artifact` · `secret` · `activity` | Skill · Artefato · Segredo · Atividade | — |
| `secret_field` · `vault` · `collection` · `send` · `delegation` | Campo · Cofre · Coleção · Envio · Delegação (ADR-0068, ADR-0069) | pasta, compartilhamento |

`heartbeat` continua como nome do **mecanismo** (pacote `internal/heartbeat`), nunca como conceito de UI. `agent` = o harness que executa (adapter), não a persona contratada.

## Consequências
Renomeia o contrato do M1 antes de existir (custo zero agora, alto depois). Status e prioridades seguem as listas da UI: task `backlog · todo · in_progress · in_review · blocked · done · cancelled`; persona `active · idle · running · paused · error · awaiting_gate · off`; run `queued · running · done · interrupted · failed · cancelled · expired`; prioridade `critical · high · medium · low`.

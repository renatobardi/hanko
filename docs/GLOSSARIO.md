> English: [docs/en/GLOSSARY.md](en/GLOSSARY.md)

# Glossário — Hanko (ADR-0016, aceita)

Um termo por conceito. Código, API, MCP e banco em inglês; UI em pt-BR. Em dúvida, o termo da UI decide.

| Termo (código) | UI | Definição | Evitar |
|---|---|---|---|
| company | Empresa | Unidade de isolamento multi-org. `company_id` em toda tabela de domínio. | org, tenant |
| persona | Persona | Papel contratado na empresa (CTO, engenheira, revisor): instruções, modelo, orçamento, posição no organograma. | agent |
| agent | — | O harness que executa uma persona num run: `claude`, `codex`. Só aparece em adapter/config. | — |
| task | Task | Unidade de trabalho atribuída a uma persona. Status: backlog · todo · in_progress · in_review · blocked · done · cancelled. Prioridade: critical · high · medium · low. | issue, ticket |
| run | Execução | Uma execução de uma persona sobre uma task, disparada pelo heartbeat. Status: queued · running · done · interrupted · failed · cancelled · expired. 1 task → 0..n runs; 1 run ativo por task. | job |
| gate | Gate | Ponto em que um humano decide (aprovar, recusar, pedir mudança). Sempre âmbar na UI. Audit trail em activity. | approval, review |
| flow | Fluxo | Definição + gatilhos (cron, evento, manual) → cria tasks/runs. | routine, schedule |
| workspace | Workspace | Diretório/repositório onde personas trabalham. Escopo da memória (ai-memory) e dos artefatos. | repo |
| integration | Integração | Serviço externo conectado (GitHub, Slack…). | connector, app |
| goal / project | Meta / Projeto | Direção: meta mede, projeto agrupa tasks. | — |
| org_chart | Organograma | `reports_to` entre personas; delegação segue a árvore. | hierarchy |
| inbox / decision | Caixa de entrada / Decisão | O que espera o humano: gates, bloqueios, alertas. | — |
| cost / budget | Custos e orçamentos | Ledger por run → persona → company; orçamento com hard stop. | spend |
| subscription | Assinatura | Plano de um harness (claude, codex…): cota, trava semanal, custo pago. | — |
| activity | Atividade | `activity_event`: todo fato de domínio, na mesma transação, com `actor_type` (persona, human, system), `occurred_at`, `received_at`. | log, audit (é o uso, não o nome) |
| heartbeat | — | Mecanismo: scheduler que acorda personas e despacha runs (river). Nunca na UI. | — |
| handoff | Handoff | Resumo que uma sessão deixa para a próxima (ai-memory). | — |
| user | Usuário | Humano autenticado (modo authenticated): provider `local` ou `firebase`, papel owner · admin · member. O ator `human` de um fato carrega o `id` do usuário. | account, member |
| session | Sessão | Cookie `hanko_session` de um usuário (30 dias deslizantes); só o hash do token fica no banco. | — |
| api key | Chave de API | `hk_…` de um usuário para API, MCP e CLI; só o hash fica no banco; revogável. Não é o run token da persona. | token (ambíguo) |
| skill · artifact · secret | Skill · Artefato · Segredo | Como nomeados. | plugin (só no sentido nativo do harness) |

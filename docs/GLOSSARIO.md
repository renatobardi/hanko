> English: [docs/en/GLOSSARY.md](en/GLOSSARY.md)

# Glossário — Hanko (ADR-0016, aceita)

Um termo por conceito. Código, API, MCP e banco em inglês; UI em pt-BR. Em dúvida, o termo da UI decide.

| Termo (código) | UI | Definição | Evitar |
|---|---|---|---|
| company | Empresa | Unidade de isolamento multi-org. `company_id` em toda tabela de domínio. | org, tenant |
| persona | Persona | Papel contratado na empresa (CTO, engenheira, revisor): instruções, modelo, orçamento, posição no organograma. | agent |
| agent | — | O harness que executa uma persona num run: `claude`, `codex`, `acp` (qualquer agente ACP), `fake`. Só aparece em adapter/config. | — |
| task | Task | Unidade de trabalho atribuída a uma persona. Status: backlog · todo · in_progress · in_review · blocked · done · cancelled. Prioridade: critical · high · medium · low. | issue, ticket |
| run | Execução | Uma execução de uma persona sobre uma task, disparada pelo heartbeat. Status: queued · running · done · interrupted · failed · cancelled · expired. 1 task → 0..n runs; 1 run ativo por task. | job |
| conversation | Conversa (Chat) | Conversa direta de um humano com uma persona, fora de uma task (ADR-0027). Cada mensagem humana dispara uma run com gatilho `chat`; o resumo final da run é a resposta. Status: open · archived. Uma run ativa por conversa; mensagens que chegam enquanto ela roda esperam e são respondidas juntas pela seguinte. | thread (é o da task), sala |
| run name | Nome da execução | `Adjetivo_Substantivo` em pt-BR (ex.: `Veloz_Raposa`) derivado do id da run, gravado nela e único na empresa; aparece ao lado do id na UI e na CLI e serve de referência em `hanko run logs <nome>`. | apelido, alias |
| automatic review | Revisão automática | Parecer de outro modelo (persona revisora ou harness alternativo) registrado no gate `task_review` antes da decisão humana: veredito aprovado/reprovado, justificativa e sha256 do pacote revisado. Não decide nada. | aprovação automática |
| gate | Gate | Ponto em que um humano decide (aprovar, recusar, pedir mudança). Sempre âmbar na UI. Audit trail em activity. | approval, review |
| flow | Fluxo | Definição + gatilhos (cron, evento, manual) → cria tasks/runs. | routine, schedule |
| workspace | Workspace | Diretório (e, como metadado, repositório e branch) onde as runs trabalham; vive dentro de `HANKO_WORKSPACES_ROOT/<slug-da-empresa>`. Escopo da memória (ai-memory) e dos artefatos. Precedência na run: task > projeto > persona (ADR-0025). | repo |
| integration | Integração | Serviço externo conectado (GitHub, Slack…). | connector, app |
| goal | Meta | Objetivo mensurável: valor inicial, alvo e valor atual; o progresso vai de 0 a 100% e a meta é atingida ao cruzar o alvo. Tasks se ligam a uma meta ativa (ADR-0028). | objetivo, OKR |
| project | Projeto | Agrupa tasks, aponta para um workspace, está ativo ou arquivado e pode ter orçamento mensal (ADR-0025). | — |
| org_chart | Organograma | `reports_to` entre personas; delegação segue a árvore. | hierarchy |
| inbox / decision | Caixa de entrada / Decisão | O que espera o humano: gates, bloqueios, alertas. | — |
| cost / budget | Custos e orçamentos | Ledger por run → persona → company; orçamento com hard stop. | spend |
| subscription | Assinatura | Plano de um harness (claude, codex…): cota, trava semanal, custo pago. | — |
| activity | Atividade | `activity_event`: todo fato de domínio, na mesma transação, com `actor_type` (persona, human, system), `occurred_at`, `received_at`. | log, audit (é o uso, não o nome) |
| heartbeat | — | Mecanismo: scheduler que acorda personas e despacha runs (river). Nunca na UI. | — |
| handoff | Handoff | Resumo que uma sessão deixa para a próxima (ai-memory). | — |
| user | Usuário | Humano autenticado (modo authenticated): provider `local` ou `firebase`, papel na instalação owner · admin · member (`owner` vê todas as empresas). O ator `human` de um fato carrega o `id` do usuário. | account |
| member | Membro | Vínculo de um usuário com uma empresa, com papel owner · admin · member · viewer (ADR-0021). Quem não é membro não enxerga a empresa (404). | — |
| invite | Convite | Convite por e-mail com papel, token aleatório (só o hash fica no banco), válido por 7 dias, aceito em `/convite/{token}`. Sem envio de e-mail: o link é mostrado ao admin. | — |
| session | Sessão | Cookie `hanko_session` de um usuário (30 dias deslizantes); só o hash do token fica no banco. | — |
| api key | Chave de API | `hk_…` de um usuário para API, MCP e CLI; só o hash fica no banco; revogável. Não é o run token da persona. | token (ambíguo) |
| skill | Skill | Habilidade reutilizável em markdown (`SKILL.md`) que uma persona carrega sob demanda; ligada por persona (ADR-0029). | plugin (só no sentido nativo do harness) |
| integration | Integração | Serviço externo da empresa: servidor MCP injetado nas runs ou webhook de saída assinado com HMAC (ADR-0029). Os segredos ficam cifrados e nunca voltam. | connector |
| artifact | Artefato | Arquivo entregue por uma persona ou enviado por uma pessoa, com versões imutáveis: o mesmo nome na mesma task cria a versão seguinte (ADR-0028). | anexo, work product |
| secret | Segredo | Como nomeado. | — |
| memory consolidation | Consolidação de memória | Run de sistema (gatilho `memory`) em que a persona curadora deduplica e resume a memória de um workspace; custo no ledger. | — |
| memory proposal | Proposta de curadoria | Escrita destrutiva da curadoria retida pelo proxy, com diff e sha256; só vai ao ai-memory após o gate `memory`. | pending write (ambíguo) |
| memory grant | Concessão de memória | Permissão persona × workspace (`read`/`write`) para alcançar a memória de outro workspace da empresa. | — |
| task dependency | Dependência | `task_dependencies`: a task só é despachada quando todas as dependências fecham (done ou cancelled). Sem ciclo, mesma empresa, no máximo 50. **Bloqueada** é derivado (dependência aberta), nunca um status gravado; o status `blocked` continua sendo o "parado esperando alguém" (ADR-0040). | blocker, bloqueio (ambíguo com o status `blocked`) |
| subtask / hold | Subtask / Pausa | Subtask é uma task com `parent_id` (até 10 níveis); o pai acorda quando a última fecha e não conclui com filha aberta (override humano com motivo). Pausa (`task_holds`) congela a task e, por herança calculada, toda a subárvore: nenhuma run abre; retomar acorda os wakes recusados (ADR-0040). | freeze, suspensão |
| review policy | Política de revisão | `require_review` e revisor automático resolvidos por camada, task > projeto > persona, campo a campo; a run grava de onde veio cada decisão (ADR-0040). | — |

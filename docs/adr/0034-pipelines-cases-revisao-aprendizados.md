# ADR-0034 — Pipelines, cases, fila de revisão, aprendizados e treino de decisão
Data: 2026-10-16 · Status: aceita

## Contexto
O trabalho das personas vivia só em tasks. Faltava um registro estruturado do que está sendo trabalhado (um lead, um
incidente, um pedido de conteúdo) que atravesse várias tasks, passe por etapas com revisão humana e cujas decisões
voltem para as personas como aprendizado. O menu reservava Pipelines, Cases, Fila de revisão e Aprendizados ("Em breve").

## Decisão
1. **Modelo (migration 00030).** `pipelines` (key única na empresa, modo estrito, arquivamento), `pipeline_stages`
   (key única no pipeline, posição, tipo `working|review|done|cancelled`, `config` jsonb validado no service),
   `pipeline_transitions` (arestas), `cases` (número por empresa em `case_sequences`, identificador `<PREFIXO>-C<n>`,
   tipo livre, `key` opcional com índice único (empresa, tipo, key) para upsert, título, resumo até 64 KiB, campos
   planos até 64 KiB, status, etapa, pai, projeto, `version`, sugestão pendente, `approved_version` +
   `reapproval_required`, `attention`, terminal, contadores de filhos, run de origem), `case_task_links`
   (`origin|work|automation|conversation`), `case_blockers`, `case_events` (imutável por trigger; ator persona exige
   `run_id`) e `decision_examples` (único por origem e autor). Toda tabela tem `company_id` (ADR-0010).
2. **Configuração da etapa.** `fields[]` (text, multiline, select, number, date, url; obrigatório; no formulário de
   criação), `what_happens_here`, `disabled` + motivo, `approval {required, approver: any_member | member:<user> |
   persona:<persona>}` (persona só em etapa working), `automation {persona_id, instructions ≤ 8 KiB, priority}`,
   `review {approve_to, reject_to, request_changes_to, require_reject_reason, require_changes_reason}` (aprovar e recusar
   obrigatórios em etapa review) e `gates.require_children_terminal`. Até 30 etapas e 300 transições por pipeline.
3. **Trava otimista.** Toda escrita no case exige `expected_version` e sobe a versão em 1; versão diferente é 409 com
   a versão atual (corpo da API, MCP e telas dizem qual é).
4. **Transição** (uma transação, `FOR UPDATE` no case): pipeline ativo, versão confere, etapa alvo ligada; da etapa
   de revisão só se sai decidindo; sair de etapa com aprovação só pelo aprovador (admin humano destrava quando o
   aprovador é um usuário ou uma persona); filhos terminados (se a etapa exige) e bloqueios abertos impedem, salvo
   para cancelar; campos obrigatórios da etapa de saída preenchidos; modo estrito: fora das arestas só com `force` +
   motivo, por member humano (evento `transition_forced`); mudou depois de aprovado → a próxima saída tem de ir para
   revisão (`review_outdated`). Depois: status derivado do tipo da etapa, terminal, evento, contadores e "atenção" do
   pai, `done` resolve os bloqueios dos dependentes (e os põe na fila como atenção), comentário de sistema nas tasks
   abertas ligadas e a automação da etapa de destino.
5. **Automação.** Entrar numa etapa com persona cria uma task para ela (ator sistema, chave de idempotência
   `case-automation:<case>:<etapa>:<evento>`), ligada como `automation`, com o **pacote de contexto** na descrição:
   case, etapa, "o que acontece aqui", instruções, resumo, campos, campos obrigatórios vazios, a última decisão de
   revisão com o motivo, **"Aprendizados desta etapa"** (as 8 últimas decisões com motivo daquela etapa, até 4 KiB) e o
   histórico recente. Pedir mudanças devolve a uma etapa working e roda a automação de novo com o motivo. Persona
   ausente ou projeto arquivado viram `automation_failed` sem derrubar a movimentação. O mesmo pacote está em
   `GET …/cases/{id}/context`.
6. **Persona** (run token, sem papel; ADR-0021): cria e atualiza cases (o par tipo+key faz upsert), liga a task da
   própria run (`origin` na criação, `work` depois), sugere transições com justificativa e confiança e move só entre
   etapas working (saindo de etapa com aprovação apenas se for a aprovadora). Nunca decide revisão, nunca força, não
   resolve sugestão, não desenha pipeline.
7. **Revisão.** Decisões `approve|reject|request_changes`, motivo obrigatório conforme a etapa, edições opcionais
   aplicadas antes, movimentação para o destino configurado e um evento `review_decided` com a decisão, o motivo, a
   versão aprovada e um retrato do case. Só humano member (o aprovador da etapa). A fila junta cases em etapa review,
   sugestões pendentes e atenção; aprovação em lote com a versão de cada item e resultado por item. A Caixa de entrada
   ganha a aba "Revisão" com a contagem, que leva a `/revisao`.
8. **Aprendizados e treino de decisão.** `/aprendizados` lista os `review_decided` e `transition_forced` por dia em
   frases, com filtros por pipeline, etapa e decisão. Um gate decidido ou uma revisão de case pode ser salvo como
   **exemplo de decisão** (member humano) com notas; o snapshot é montado no momento do corte (a task com comentários e
   runs até a decisão, o histórico do case até ali, a decisão). Só o autor edita (com histórico) e apaga; só admin
   exporta em JSONL (`GET …/decision-examples/export.jsonl`, até 10 000 linhas).
9. **Interfaces.** API sob `/companies/{c}`: `pipelines` (CRUD, `stages`, `stages/order`, `transitions`, `cases`),
   `cases` (lista, criação/upsert, GET/PATCH, `transition`, `suggest`, `resolve-suggestion`, `review`, `links`,
   `blockers`, `events`, `children`, `context`, `conversation`), `review-queue`, `review-queue/bulk`, `learnings`,
   `decision-examples` (+ `export.jsonl`). MCP: `hanko_case_create|get|update|list|transition|suggest` e
   `hanko_pipeline_list` (o get traz o histórico com os motivos e os aprendizados da etapa). CLI: `hanko pipeline`,
   `hanko case`, `hanko review`, `hanko learnings`, `hanko decision-example`. UI: `/pipelines` (lista e criação),
   `/pipelines/{key}` (quadro por etapa com arrastar e soltar em `hanko.js` — o mesmo POST do formulário "Mover para"
   da página do case, com a versão do card —, configuração e atividade), `/cases` (busca, filtros, agrupar, ordenar,
   lista ou árvore), `/cases/{identificador}` (revisão, sugestão, campos, tasks, filhos, bloqueios, histórico, "Abrir
   conversa", "Salvar como exemplo"), `/revisao`, `/aprendizados`; painel "Cases" na task; "Salvar como exemplo" no gate.

## Consequências
- O service é a única barreira; a UI e a CLI só escondem controles e mostram o motivo da recusa.
- **Export/import (ADR-0022): fica de fora nesta versão.** A configuração de pipelines faria sentido levar para outra
  instalação, mas as etapas referenciam personas e usuários por id (automação, aprovador) e os cases são estado de
  operação; mapear isso no `hanko.company.v1` pede uma decisão própria. Uma empresa importada chega sem pipelines.
- A automação cria uma task por entrada na etapa; a coalescência é a da task (uma run ativa por task), não por case.
- O pacote de contexto e os comentários de sistema são gravados em pt-BR, como as demais mensagens do Hanko.
- Os fatos novos (`case.*`, `pipeline.*`, `decision_example.*`) vão para a atividade, mas não são eventos assináveis
  dos webhooks (ADR-0029) nesta versão.
- A busca única (ADR-0024) ainda não indexa cases.

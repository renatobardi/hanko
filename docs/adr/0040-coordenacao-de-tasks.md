# ADR-0040 — Coordenação de tasks: dependências, árvore, pausa herdada e política de revisão por task
Data: 2026-10-17 · Status: aceita

## Contexto
Uma persona que recebe uma task grande já consegue criar subtasks (`hanko_task_create` herda a task do run como pai), mas o Hanko não sabe nada sobre a relação entre elas: `parent_id` é só um rótulo. Não há como dizer "B só começa depois de A", o pai não acorda quando o último filho termina, pode ser concluído com filho aberto, e pausar o trabalho de uma frente inteira exige mexer task por task. A política de revisão (`require_review` e o revisor automático) vive só na persona, então uma task de risco alto não pode ganhar revisão a mais, nem uma de baixo risco ficar sem. Este ADR cobre os itens #1 a #4 do marco M14 (`docs/PLANO-DECISOES.md`).

## Decisão

### A. Dependências bloqueantes (#1)
1. **Modelo.** Tabela `task_dependencies` (`task_id` depende de `depends_on_id`), N:N, chave primária (task, dependência), `company_id` nas duas pontas por **chave estrangeira composta** (`(company_id, id)` de `tasks`): uma dependência entre empresas diferentes é impossível no banco. `task_id <> depends_on_id`. Teto de 50 dependências por task.
2. **Sem ciclo, em duas camadas.** O service consulta o caminho (CTE recursiva) e devolve `ErrConflict` com a cadeia que fecharia o ciclo (`HNK-3 → HNK-2 → HNK-1`). Um trigger `BEFORE INSERT` no banco repete a checagem sob `pg_advisory_xact_lock` por empresa, porque duas inserções concorrentes (A→B e B→A) não se enxergam antes do commit. O domínio tem a mesma regra pura (`domain.WouldCycle`, `domain.HasCycle`) usada nos testes de propriedade.
3. **`bloqueada` é derivado, nunca gravado.** Uma task está bloqueada quando tem ao menos uma dependência **aberta** (status fora de `done`/`cancelled`). Não existe status novo e `tasks.status` não muda (o status `blocked` continua sendo o "parado esperando alguém" de gate/humano). A view da task traz `open_dependencies` e `blocked`; a lista e o quadro mostram o selo.
4. **Fechada = `done` ou `cancelled`.** Uma dependência cancelada libera quem dependia dela (senão a cadeia travaria para sempre); o evento e o briefing dizem quais dependências foram canceladas, para a persona decidir se ainda faz sentido seguir.
5. **Efeitos.** O despacho (`CheckoutRun`), o wake manual e a rede de segurança do heartbeat recusam task bloqueada. Quando uma task fecha, na **mesma transação**, cada dependente que ficou sem dependência aberta ganha o evento `task.unblocked` e um wake do responsável (gatilho `assign`; nenhum gatilho novo de run, para não redefinir `runs_trigger_check`). O mesmo vale ao **remover** a última dependência aberta. Como o fechamento de cada task repete a regra, a liberação em **cascata** (A → B → C) é consequência, sem laço no código.
6. **Autoria.** Membro cria e remove; persona só mexe em dependências de task que ela possui, ou de personas abaixo dela no organograma (mesma regra do `UpdateTask`). Fatos: `task.dependency_added`, `task.dependency_removed`, `task.unblocked`.
7. **Superfícies.** API `…/tasks/{task}/dependencies` (GET, POST, DELETE `…/{dependency}`) e campo `depends_on` no create; CLI `hanko task dep add|rm|list`; MCP `hanko_task_dependency` (add/remove/list) e `depends_on` em `hanko_task_create`; UI: bloco "Dependências" na task, selo "Bloqueada" na lista, no quadro e no cabeçalho.

### B. Árvore pai/filho (#2)
1. `tasks.parent_id` já existe. Um trigger de banco garante o que o service também checa: pai da **mesma empresa**, **sem ciclo** e profundidade máxima de 10 níveis. Passa a ser possível **reparentar** (`parent` no PATCH, `clear_parent`).
2. **Pai acorda ao fechar o último filho.** Quando uma task fecha e o pai fica sem filho aberto, o pai (se aberto e com responsável) ganha o fato `task.children_done` e um wake (gatilho `assign`), na mesma transação.
3. **Pai não conclui com filho aberto.** Mover um pai para `done` com filho aberto é `ErrChildrenOpen` (422, `children_open`) com a lista dos filhos. **Override humano com motivo**: `force_close` + `override_reason` (1 a 500 caracteres) — só ator humano, papel de membro; persona recebe `ErrNotAllowed`. O motivo vai no fato `task.close_override`. Cancelar o pai continua permitido (abandonar a frente é decisão legítima; para congelar sem fechar existe a pausa). O gate `task_review` aprovado por humano fecha como sempre (a decisão humana é o override) e registra `open_children` no fato.
4. **UI.** Breadcrumb dos ancestrais no cabeçalho da task e lista de filhos (com status, responsável, progresso `n/m`) e botão "Nova subtask".

### C. Pausa herdada por subárvore (#3)
1. **Modelo.** Tabela `task_holds` (uma linha por task pausada: motivo de 1 a 500 caracteres, quem e quando). Só a raiz da pausa tem linha; **os descendentes herdam por cálculo** (nunca copiamos a pausa para os filhos): o hold efetivo de uma task é o da própria task ou, se não houver, o do ancestral mais próximo. Reparentar muda o efeito na hora, sem migração de dados.
2. **Efeito.** Task com hold efetivo: o despacho recusa (`ErrInvalid` "held", que o worker trata como "nada a fazer"), o heartbeat não a considera, o wake manual é recusado e as runs **ainda na fila** da subárvore são canceladas na hora da pausa. Uma run que já está rodando termina sozinha (não matamos trabalho em andamento; o próximo despacho é que fica recusado). Comentários e edições humanas continuam possíveis.
3. **Retomar acorda os wakes pendentes.** Todo wake recusado por pausa (o despacho de uma task pausada, ou um wake que a coordenação não enfileirou porque a task estava pausada, inclusive a liberação por dependência que fechou durante a pausa) é guardado em `held_wakes` (uma linha por task: persona e gatilho). `ResumeTask` apaga a pausa e, na mesma transação, reenvia o wake guardado de cada task da subárvore que **não continue pausada** por outra raiz (as que continuam ficam guardadas). Uma persona ociosa que nunca foi recusada não é acordada à toa.
   Concorrência: pausar, retomar e abrir run se serializam por um lock consultivo por empresa (compartilhado no checkout, exclusivo em pausar/retomar), sempre o primeiro lock da transação, então nenhuma run abre no meio de uma pausa em andamento. A rede de segurança do heartbeat usa a função SQL `task_effectively_held` (e a checagem de dependência aberta) no mesmo critério.
4. **Visibilidade.** A view da task traz `hold` (motivo, quem, quando, task de origem e `inherited`); a task mostra o banner "Pausada" (e "herdada de HNK-n") no cabeçalho e na thread, e a Caixa ganha a aba **Pausadas** (raízes de pausa, com "Retomar"). Fatos: `task.held`, `task.resumed`. Quem pausa e retoma: membro humano; a persona não se pausa.

### D. Política de revisão por task (#4)
1. **Camadas e precedência: task > projeto > persona.** `require_review` e o revisor automático passam a ter três camadas. Task e projeto guardam sobreposições em tabelas laterais (`task_review_overrides`, `project_review_policies`), com `require_review` tri-estado (`NULL` herda) e `reviewer_mode` (`inherit`, `none` = vai direto ao gate humano, `custom` = persona revisora ou harness+modelo). A persona segue na própria tabela. Cada decisão é resolvida **por campo**: a origem de `require_review` e a do revisor podem ser camadas diferentes.
2. **Origem gravada no run.** A tabela `run_policy_origins` (uma linha por run) guarda `review_required_source` e `reviewer_source` (`task|project|persona`), gravada no despacho e atualizada quando a persona tenta fechar a task; a run de revisão guarda de onde veio o revisor. A tela da execução mostra "Revisão: obrigatória (origem: task)".
3. **Quem pode.** Alterar a política da task é papel de **admin** (é governança); a persona nunca altera a própria.
4. **Compatibilidade.** Sem sobreposição em nenhuma camada, o comportamento é exatamente o anterior (a persona decide). O projeto só ganha as colunas da política aqui; as telas de defaults de projeto do M10 reaproveitam `project_review_policies`.

## Alternativas descartadas
- Guardar `blocked` como status: duplicaria a verdade (a dependência fecha em outra task) e exigiria reconciliar a cada fechamento; derivado é correto por construção.
- Copiar a pausa para cada descendente: reparentar e criar filhos exigiria propagar; o cálculo pelo ancestral é uma CTE barata (profundidade ≤ 10).
- Novos gatilhos de run (`dependency`, `children`): exigiriam redefinir `runs_trigger_check`, ponto de conflito entre marcos; o motivo do wake vai no fato de atividade e no briefing.
- Colunas novas em `tasks`/`runs`/`projects`: tabelas laterais mantêm o modelo gerado dessas tabelas intacto para os marcos paralelos.

## Consequências
- Migration 00100 (faixa 00100–00109): `task_dependencies` (+ trigger anti-ciclo), trigger de pai em `tasks`, `task_holds`, `held_wakes`, `task_review_overrides`, `project_review_policies`, `run_policy_origins`, função `task_effectively_held`, índices `tasks_parent_idx` e `tasks_company_id_id_uq`. Aditiva; o Down desfaz.
- `db/queries/runs.sql` (`ListWakeCandidates`) ganha duas condições (dependência aberta, `task_effectively_held`); é a única mudança em consulta existente.
- Os textos em inglês entram em `internal/i18n/en_zz_coordenacao.go`, nome que ordena por último: o `init` dele roda depois de todos os outros catálogos e **não** dá panic por chave repetida (marcos paralelos podem traduzir o mesmo texto).
- Export/import (`hanko.company.v1`): leva a árvore pai/filho (a validação recusa mais de 10 níveis), não leva dependências, pausas nem políticas por task. Um formato `v2` com elas fica para quando o time pedir.
- Dependências Go novas: nenhuma.
- O briefing da run ganha as seções "Dependências", "Subtasks" e "Revisão desta task" (só quando há o que dizer).
- Limites conhecidos: dependência não cruza empresas; hold não mata run em andamento; o status `blocked` e o `bloqueada` derivado convivem (o primeiro é manual/gate, o segundo é calculado).
- Testes: propriedade (grafo aleatório nunca cria ciclo, liberação em cascata por ordem topológica) no domínio e contra o banco; integração do demo (quebrar em três com dependência, cascata, pausar o pai congela tudo).

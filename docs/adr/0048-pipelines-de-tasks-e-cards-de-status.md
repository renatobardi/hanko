# ADR-0048 — Pipelines sobre tasks e cards de status com decision model, cron e resultado em artefato
Data: 2026-10-21 · Status: aceita

## Contexto
O M20 do plano (itens #45 e #46) fecha a Onda 4. Dois conceitos já existem desde o v0.3 e este ADR **não os duplica**:

- **Pipelines de cases** (ADR-0034): `pipelines`, `pipeline_stages`, `cases`. O pipeline organiza *cases* (um lead, um incidente),
  com revisão humana própria. Ele não move tasks.
- **Cards de status** (ADR-0032): resumos vivos mantidos por uma persona resumidora, com política `manual|interval|reactive`,
  revisões imutáveis do resumo e arquivamento. O resumo vive só dentro do card e o card não aparece no Painel.

O que o plano pede e faltava:

1. **#45 — pipelines sobre tasks.** Estágios por projeto mapeados a um status de task e a uma persona; avançar acorda a
   persona do estágio; o avanço reaproveita gates e a revisão automática; as decisões de gate viram aprendizados em memória
   governada. É a base do workflow por fase (triagem → dev → review → QA), sem criar uma segunda entidade de trabalho: a
   unidade continua sendo a task.
2. **#46 — cards de status.** Pergunta salva com persona **ou decision model** (ADR-0056) como motor, agenda por **cron**, e
   o resultado como **artefato versionado** (ADR-0028) mostrado no Painel. Arquivar já existe.

Na interface a palavra "pipeline" já designa os pipelines de cases (menu Pipelines). Por isso o recurso novo aparece como
**Etapas do projeto** (aba "Etapas" do projeto, painel "Etapa do projeto" na task e `hanko stage`); "pipeline do projeto" fica
só no texto técnico e nos ids de API.

## Decisão

### A. Etapas do projeto (#45) — migration 00160
1. **Modelo.** `project_stages` (empresa, projeto, `key` `^[a-z][a-z0-9_-]{0,39}$`, nome, posição, `status`
   `todo|in_progress|in_review`, `persona_id` opcional `ON DELETE SET NULL`, `exit_gate` `none|approval|review`, instruções até
   8 KiB; 1 a 12 por projeto), `task_stages` (uma linha por task: etapa atual e estado `working|completing|finished`) e
   `task_stage_events` (histórico **imutável** por trigger, como `gate_revisions`). Toda tabela tem `company_id`. Nenhuma tabela
   de gate, de revisão ou de memória nova: o pipeline reaproveita as existentes.
2. **Mapeamento a status e persona.** Entrar numa etapa muda o status da task para o da etapa e a atribui à persona dela
   (sem persona = etapa humana: a task fica sem responsável). Uma etapa com persona só pode mapear status trabalhável
   (`todo`, `in_progress`); `in_review` é de etapa humana, porque o despacho não acorda persona em `in_review`. O avanço é
   o mesmo caminho de uma reatribuição: `task.status_changed` e `task.assigned` na atividade, run da persona anterior
   cancelada e `wake` (gatilho `assign`) da persona nova. A exceção: quando é a **própria persona** que conclui a etapa, a
   run dela **não** é cancelada, termina sozinha (o resumo vira comentário) e o wake da seguinte espera por ela
   (a task só tem uma run ativa; o job de wake já reagenda).
3. **Entrada.** Uma task criada **por uma pessoa**, num projeto com etapas, **sem persona escolhida**, nasce na primeira
   etapa (persona e status dela, wake). Task criada por persona, por fluxo, por case ou de sistema não entra sozinha;
   `StartTaskStage` (API, CLI, tela) põe uma task existente numa etapa.
4. **Concluir a etapa.** A persona chama `hanko_stage_complete` (MCP; também `POST …/stage/complete` e `hanko stage
   complete`). Se uma persona dá a task de pipeline como `done` (`hanko_task_update`), o service **intercepta**: vira
   "etapa concluída", nunca "task concluída" — senão a primeira persona fecharia a task e pularia as outras. Uma pessoa que
   fecha a task à mão fecha de verdade e a task sai do pipeline (`finished`, evento `left`). Concluir a última etapa fecha a
   task como `done`.
5. **Gate de saída — reuso, não duplicação.** `exit_gate` da etapa:
   - `none`: a task avança na hora. A política de revisão da persona/projeto/task (ADR-0040) **continua valendo**: se ela exige
     revisão, a etapa se comporta como `review`.
   - `approval`: abre um gate `approval` pedido pelo sistema (sem persona presa), a task vai para `in_review` e **ninguém é
     acordado**. É o único lugar em que o pipeline para.
   - `review`: reaproveita `task_review` e a **revisão automática** (`requestTaskReview`): se há revisor configurado, a revisão
     por outro modelo roda antes do gate humano; sem revisor, o gate nasce direto. Em etapa humana (sem autora) o gate
     `review` vira `approval`.
   A etapa fica `completing` até a decisão. Concluir de novo nesse estado é no-op.
6. **Decisão do gate** (`afterGateDecided`; o gate de etapa é reconhecido por a task estar `completing` e o gate ser
   `approval` ou `task_review`): `approved` avança (ou fecha, na última); `changes_requested` e `rejected` devolvem a etapa à
   persona dela, com o status mapeado, e ela é acordada com gatilho `gate` e a nota na thread. O caminho comum
   (task → `done`/`todo`) não roda para esses gates: a etapa decide.
7. **Aprendizados = decisões de gate em memória governada.** Cada decisão grava um evento `gate_decided` (decisão + nota) e um
   fato de memória `pipeline.learning` em `hanko/pipelines/<projeto>/<etapa>/<data>-<TASK>-<gate>.md` (pelo outbox, na mesma
   transação, no workspace da task ou do projeto). "Governada" porque o conteúdo nasce de uma decisão **humana** de gate (não
   de uma escrita livre da persona) e passa pela mesma entrega de memória de todo fato. As até 8 últimas decisões da etapa
   **com nota ou sem aprovação** voltam no briefing da persona ("Aprendizados desta etapa", ≤ 4 KiB), como o pacote de contexto
   dos cases (ADR-0034, item 5). É a base do workflow por fase: cada fase aprende com o que o humano pediu nela.
8. **Mão humana.** `AdvanceTaskStage` (member humano; persona recebe `ErrNotAllowed`) move a task para a etapa seguinte ou para
   qualquer etapa (`to`), por cima dos gates — o gate pendente do pipeline é expirado; `LeaveTaskStage` tira a task do
   pipeline. Mexer no pipeline do projeto é de **admin**; ler é de qualquer membro. Tirar uma etapa que ainda tem tasks é
   conflito (409), não perda silenciosa.
9. **Superfícies.** API `GET|PUT|DELETE /companies/{c}/projects/{p}/stages`, `GET …/stage-learnings`, `GET
   /tasks/{t}/stage`, `POST /tasks/{t}/stage/start|complete|advance|leave`; `TaskView.stage` nas listagens; CLI `hanko stage
   list|set|clear|learnings|show|start|complete|advance|leave`; MCP `hanko_stage_complete` (fora da lista da fase de plano,
   portanto recusada nela); UI aba "Etapas" do projeto (admin edita uma linha por etapa, mais três em branco; os
   aprendizados aparecem embaixo) e painel "Etapa do projeto" na task.
10. **Fora desta entrega.** Export/import (ADR-0022): as etapas referenciam personas por id, como os pipelines de cases
    (ADR-0034); uma empresa importada chega sem etapas. Não há detecção automática de "etapa terminou" por heurística:
    quem conclui é a persona (ou um humano), explicitamente.

### B. Cards de status (#46) — mesma migration 00160
11. **Motor `decision`.** `status_cards.engine` (`persona`, padrão, ou `decision`). No motor decision não há resumidora nem
    montagem: o card nasce `active`, com os filtros informados por quem criou (vazio = as tasks abertas) e `query_version =
    1`. A atualização é **uma chamada** ao decision model da empresa (ADR-0056) pela feature nova **`card_summary`**
    (registrada em código; nasce desligada, como as demais): o Hanko monta o retrato das tasks observadas (filtros, mudanças,
    tasks com status/prioridade/persona/último comentário humano, até 24 KiB, tratado como dado) e a resposta é um objeto JSON
    `{title, summary, mentioned}` validado (resumo não vazio, UTF-8, ≤ 64 KiB). Só uma **pessoa** escolhe esse motor (uma
    persona não consome o decision model).
12. **A chamada HTTP fica fora da transação.** A avaliação (dentro da transação do card) abre a rodada (`status_card_updates`,
    sem task) e devolve um *job*; quem chamou (tick, "Atualizar agora", criação, restauração) executa a chamada depois do
    commit; a escrita do resumo é outra transação, que confere que a geração ainda é a que abriu o job
    (`generating_since`) — uma geração substituída ou arquivada não grava. Falha (modelo desligado, HTTP, resposta inválida)
    fecha a rodada `failed`, põe o card em `error` com o motivo e **mantém o último resumo bom**; o card em erro não se atualiza
    sozinho, como no motor persona (ADR-0032, item 6). Uma geração que não termina em 10 min (processo reiniciado no meio)
    vira erro. Tokens, custo e modelo da chamada entram na rodada (contam no teto diário do card) e a invocação entra no
    livro do decision model com ator `system` (custos da empresa, ADR-0056).
13. **Cron.** `refresh_policy.mode = "cron"` com `cron` (5 campos), `cron_tz` (IANA, padrão UTC) e `cron_skip_unchanged`.
    Vale para os dois motores. O tick (`status_card_tick`) avalia o card e gera quando o horário do cron passou desde a
    última geração — **com ou sem mudanças** (um cron é intenção explícita), a menos que `cron_skip_unchanged`. Nunca
    gerado = vencido. A próxima verificação é o próximo disparo (`robfig/cron/v3`, já dependência do repositório pelos
    fluxos, ADR-0013; nenhuma dependência nova). Teto diário de tokens, teto por hora e horário ativo continuam valendo.
    O gatilho da rodada é `cron` (constraint recriada com a lista completa).
14. **Resultado como artefato versionado.** Toda atualização bem-sucedida, de qualquer motor, publica o resumo (`# título` +
    markdown) como o artefato `card-<título>.md` da empresa (sem task): **uma versão por atualização**, o mesmo conteúdo não
    cria versão (ADR-0028). O nome é fixado na primeira publicação (`artifact_name`) e `artifact_id` aponta para a versão
    mais nova. A publicação acontece **depois** do commit do resumo e nunca derruba a atualização (se falhar, vai para o log e
    a próxima publica de novo); o artefato tem a própria atividade `artifact.created` e o card registra
    `status_card.artifact_published`.
15. **Painel.** `on_dashboard` põe o card no Painel (até 6, o atualizado há menos tempo primeiro): título, o resumo, quando foi
    atualizado, quando é a próxima, e o link para o artefato versionado. `GET /companies/{c}/dashboard/status-cards`.
    O card continua não aparecendo nos *números* do Painel (ADR-0032, item 9).
16. **Superfícies.** API: `engine`, `queries`, `on_dashboard` e a política `cron` na criação/edição, e `engine`,
    `artifact_id`, `artifact_name`, `on_dashboard` na leitura; CLI `hanko status-card create --engine decision --cron "0 7 *
    * 1-5" --tz America/Sao_Paulo --dashboard --project site --status blocked`; UI: motor, filtros do decision model, modo
    cron e "Mostrar no Painel" no formulário, link do artefato no resumo, seção "Cards de status" no Painel. O motor é
    imutável depois de criado (trocar exigiria remontar filtros ou escolher resumidora; crie outro card).

## Consequências
- Um projeto com etapas muda o que "criar task" faz para quem não escolhe persona: o responsável vira o da primeira etapa
  (e não a persona principal da pessoa, ADR-0041). Quem quiser outra persona a escolhe ao criar.
- `moveTask` deixava o campo `model` da task em branco ao mover por gate; a correção (preservar o modelo escolhido à mão) entra
  neste marco porque o pipeline passa a mover tasks por gate com frequência.
- O motor decision gasta do **orçamento da empresa** (não de persona) e exige a feature `card_summary` ligada; sem ela a
  atualização falha com a mensagem do decision model e o card fica em erro com o último resumo bom.
- Limites conhecidos: o tick executa as chamadas do decision model em sequência (cada uma com o prazo configurado, até 300 s);
  muitos cards decision vencendo ao mesmo tempo alongam o tick (a trava consultiva impede dois ticks juntos). Cron de
  resolução de minuto, no ritmo do tick.
- Telemetria: span `hanko.decision.invoke` (já existente) cobre a chamada; `service.stage_complete` e
  `service.status_card_decision` são spans novos; a métrica `hanko.decision.calls` ganha a feature `card_summary`.

# ADR-0032 — Cards de status: resumos vivos mantidos por uma persona
Data: 2026-10-16 · Status: aceita

## Contexto
Quem acompanha um time de personas quer saber, sem abrir task por task, como está um recorte do trabalho: "tudo que
está bloqueado no projeto Site e os bugs críticos", "o que a Hana pesquisa esta semana". A lista de tasks filtra, mas
não resume; o Painel mostra números, não a história. O ADR-0031 deixou os cards de status para o roadmap por exigirem
modelo de dados próprio. O resumo precisa ser escrito por uma persona (é trabalho de linguagem), mas sem abrir um
segundo caminho de execução: orçamento, ledger, alertas e o log da run têm de valer como para qualquer run, e o gasto
tem de ser previsível mesmo quando as tasks mudam o tempo todo.

## Decisão
1. **Modelo** (migration 00028). `status_cards` (empresa, título 0..200 com `title_pinned`, interesse 1..20 000
   caracteres — 4 000 quando quem escreve é uma persona —, persona resumidora `ON DELETE SET NULL`, filtros em jsonb
   com `query_version`, política em jsonb, estado `compiling|active|error|paused_budget|paused_hours`, pendências,
   retrato das tasks da última versão, tasks citadas, resumo em markdown até 64 KiB com revisão, geração em
   andamento, motivo da falha, próxima verificação, arquivamento e autor). `status_card_revisions` guarda o histórico
   imutável do resumo (50 por card) e `status_card_updates` as rodadas (`compile|full|incremental`, gatilho
   `manual|interval|reactive|restore|query`, task e run, mudanças, retrato, tokens, custo e modelo da run, status
   `running|ok|failed`; 200 por card). Limites: 100 cards por empresa e 20 criados por persona, contados sob trava
   consultiva da empresa.
2. **Filtros reaproveitam a lista de tasks.** Um filtro combina (E) status, prioridade, persona, projeto, meta, texto
   e "só abertas"; uma task entra se casar com qualquer filtro (OU), de 1 a 10 filtros. O card observa no máximo 300
   tasks (o excedente aparece como aviso). As tasks de sistema (de cards e da curadoria de memória) nunca entram: o
   card não observa a si mesmo.
3. **Montagem e atualização são tasks de sistema da resumidora.** Criar o card, mudar o interesse (o mesmo interesse
   não remonta), trocar a resumidora de um card sem filtros ou pedir "Montar de novo" cria a task "Montar card de
   status: <título>" (prioridade baixa, sem fato de memória), acordada com o gatilho `status_card` (já no banco desde a
   00027). A resumidora grava os filtros com a tool MCP `hanko_status_card_set_query`; isso sobe `query_version` e abre
   na hora a primeira atualização, do zero. Cada atualização é a task "Atualizar card de status: <título>" com o
   pacote de contexto na descrição (até 60 KiB: interesse, filtros, tasks com status, prioridade, persona e último
   comentário humano, mudanças desde a versão anterior e, no incremental, o resumo anterior); a resumidora grava com
   `hanko_status_card_set_summary`. Como na curadoria (ADR-0030), a task de sistema é identificada pelo vínculo
   (`status_card_updates.task_id`), não pelo gatilho: `createRun` força `status_card` para ela, ela não acorda por
   comentário nem por "Acordar" manual (`WakeTask` recusa com 422) e a rede de segurança do heartbeat a retoma.
   A run recebe um prompt de sistema próprio (`BuildStatusCardPrompt`), não consome handoff nem fecha sessão de
   memória e não recebe skills nem MCPs da empresa (run de sistema, ADR-0029).
4. **Só a resumidora, só na geração ativa.** As duas tools aceitam apenas o run token da persona resumidora do card,
   na task de geração ativa dele e na etapa certa; a rodada precisa estar `running`. Uma geração substituída
   (montagem nova, arquivamento) deixa de valer. A API REST não expõe essas escritas.
5. **Avaliação e política.** Cada avaliação roda os filtros, monta o retrato (task → status, prioridade, persona,
   `updated_at`, último comentário humano) e compara com o retrato da última versão, filtrando pelos gatilhos
   escolhidos (status, entrada/saída, comentário humano, responsável, qualquer alteração). Em ordem: teto diário de
   tokens (soma das rodadas do dia no fuso da política) estourado → `paused_budget` até o dia seguinte, e o "Atualizar
   agora" é recusado com o motivo; fora do horário ativo → `paused_hours` até o próximo início (só para o automático);
   política `manual` só conta pendências; `reactive` espera o debounce (que reinicia quando o conjunto de mudanças
   muda) e respeita o teto por hora; `interval` gera no horário se algo mudou. O tipo é `full` quando pedido, sem
   resumo, com mais de 10 mudanças, com filtros novos, depois de 9 incrementais seguidos ou na restauração; senão
   `incremental`. Gravar o resumo cria a revisão, adota o retrato da geração (mudanças ocorridas durante a geração
   aparecem na verificação seguinte), zera as pendências e agenda a próxima verificação.
6. **Falha visível, nunca silenciosa.** O fim da run (qualquer desfecho; gancho em `FinishRun`, no cancelamento de run
   na fila e nas órfãs da partida, como o `memoryRunEnded`) grava tokens, custo e modelo do ledger da run na rodada e
   fecha a task de sistema. Se a resumidora não gravou, a rodada fica `failed` e o card `error` com o motivo; o último
   resumo bom continua à vista ("Mostrando o último resumo bom:"). Um card em erro não se atualiza sozinho (não gasta
   em laço): sai do erro com "Atualizar agora" ou "Montar de novo". Uma geração que não termina em 2 h (resumidora
   pausada, sem orçamento, fila parada) vira erro com o motivo e a task é cancelada.
7. **Tick periódico.** `status_card_tick` (river, no ritmo do `flow_tick`) avalia os cards com `next_eval_at` vencido,
   sob trava consultiva de transação (um tick por vez) e um card por transação com `FOR UPDATE SKIP LOCKED`. O modo
   reactive é polling de 1 min com debounce, não evento; interval verifica a cada 5 min; manual a cada 15 min.
8. **Papéis (ADR-0021).** Viewer lê; member cria, edita e atualiza qualquer card (é ferramenta de acompanhamento);
   arquivar e restaurar é de quem criou ou de admin; apagar é de admin. Uma persona cria cards pelo MCP
   (`hanko_status_card_create`, resumidora padrão: ela mesma) e só altera os que criou; `hanko_status_card_list`
   mostra os dela e o card da geração em andamento. Run tokens leem pela API e não escrevem.
9. **Superfícies.** API `/api/v1/companies/{c}/status-cards` (lista com `?archived=`, criar, ler, alterar com
   `archived`, apagar, `updates`, `revisions`, `recompile`, `refresh {full}`, `preview`); CLI
   `hanko status-card list|show|create|update|archive|restore|delete|refresh|recompile|history|preview`; MCP
   `hanko_status_card_set_query|set_summary|list|create`; UI `/c/{empresa}/cards-de-status` (grade de 2 colunas, estado
   vazio, arquivados recolhidos) e o detalhe com abas Resumo (seletor de revisão, "Entrou nesta atualização",
   "Citadas na última atualização"), Configuração, Tasks observadas, Histórico (tokens e custo estimado) e Filtros
   (depuração), com polling htmx enquanto monta ou atualiza (como o chat, ADR-0027). O card não aparece no Painel.
10. **Export/import (ADR-0022): fora.** Os filtros apontam para ids de persona, projeto e meta (metas não viajam) e o
    resumo e as rodadas são estado de operação; levar só o interesse para outra instalação exigiria remontar de
    qualquer forma. `hanko.company.v1` não muda.

## Consequências
- Um card gasta tokens como qualquer run da resumidora (conta no orçamento dela e da empresa e na trava semanal); o
  teto diário do card e o teto por hora limitam o gasto de cada card. Recomenda-se uma resumidora num modelo barato.
- As tasks de sistema aparecem na lista de tasks (como as da curadoria); a página da task ainda mostra "Acordar", e o
  service recusa com a mensagem que aponta para "Atualizar agora".
- Os resumos e as mensagens de falha gravadas ficam no idioma em que foram produzidos (pt-BR); só a interface é
  traduzida.
- Limites conhecidos: polling (latência de até 1 min mais o debounce); 300 tasks observadas por card; o retrato não
  guarda a descrição, então uma edição dela só conta em "qualquer alteração" (pelo `updated_at`); trocar a resumidora durante uma geração faz a rodada falhar (a escrita é recusada) e pede um novo pedido.

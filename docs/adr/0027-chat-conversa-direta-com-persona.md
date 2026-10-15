# ADR-0027 — Chat: conversa direta de um humano com uma persona
Data: 2026-10-15 · Status: aceita

## Contexto
Toda interação com uma persona passava por uma task: para tirar uma dúvida, pedir um parecer ou combinar uma direção
era preciso abrir uma task, atribuir e esperar a run. O menu lateral já reservava "Chat" ("Em breve"). A conversa direta
precisa de histórico próprio, mas não deve criar um segundo caminho de execução: o que protege o dinheiro e a operação
(fila, adapter, ledger de custos, orçamentos com hard stop, trava semanal da assinatura, alertas, log da run) já existe
na run e deve valer igual para o chat.

## Decisão
1. **Modelo.** `conversations` (empresa, persona, título 1..200, `open|archived`, autor, `last_message_at`) e
   `conversation_messages` (autor `human|persona|system`, corpo 1..100000, `run_id` opcional), ambas com `company_id`
   (ADR-0010) e índices para a lista (por empresa e última mensagem, por persona), para a fila de mensagens pendentes
   e para a thread. Migration 00018.
2. **Uma mensagem humana = uma run.** A run de chat é uma run comum: `runs.task_id` passou a aceitar nulo e há
   `runs.conversation_id`, com `runs_chat_shape_check` (gatilho `chat` ⇔ conversa e sem task; os demais gatilhos
   ⇔ task e sem conversa). O gatilho novo é `chat`. Assim ledger, orçamento por empresa/persona, trava semanal, alertas,
   "Execuções" e o log ao vivo funcionam sem exceção. No ledger a run de chat cai no "projeto" `chat` (rótulo Chat).
3. **Despacho síncrono, sem job de wake.** `SendMessage` faz, na mesma transação: grava a mensagem, confere persona
   (status), orçamentos (`checkBudgets`) e trava semanal, cria a run `queued` e enfileira `execute_run` (river). Se a
   persona não pode responder (pausada, em erro, desligada, orçamento estourado, trava semanal) a mensagem **não é
   gravada** e o erro leva o motivo (409 na API, aviso na UI); os efeitos colaterais da recusa (pausa por orçamento,
   alertas) são confirmados — mesmo padrão do despacho de tasks. Em `CreateConversation` com mensagem, uma recusa não
   deixa conversa vazia.
4. **Coalescência.** Uma run ativa por conversa (índice único parcial `runs_one_active_per_conversation`; a linha da
   conversa, `FOR UPDATE`, é o ponto de serialização — também usado na revisão/gate de runs de chat). Mensagens que chegam
   com uma run ativa ficam com `run_id` nulo (até 20) e a run **recolhe todas** ao ser lançada; quando a run termina, se
   há pendentes, o `FinishRun` abre a run seguinte na mesma transação. Nunca há duas respostas concorrentes nem mensagem
   perdida. Cancelar ou ser interrompida não dispara a próxima run (as pendentes seguem na fila até a próxima mensagem).
5. **Prompt.** `system` = instruções da persona + protocolo de conversa (sem task; abrir task com `hanko_task_create`;
   ações irreversíveis viram task + gate). `prompt` = título, ids, histórico recente (as mensagens mais novas que
   cabem em 24 KiB, no máximo 4 KiB por mensagem, com aviso das omitidas), memória do projeto (handoff e briefing, como
   nas outras runs) e as **mensagens novas** da run. **Retomada de sessão:** se a run anterior da conversa terminou com
   sucesso, no mesmo harness, e o adapter implementa `SessionResumer` (`claude` e `fake`), o executor repassa o
   `SessionRef` e o prompt não repete o histórico. Adapter sem a capacidade sempre recebe o histórico. A memória de
   longo prazo recebe um fato curto por run de chat, como nas demais.
6. **Resposta.** O resumo final da run vira a mensagem da persona (corpo markdown, até 90 000 bytes) com o `run_id`
   (link para o log). Run que termina sem resumo, falha, é cancelada, interrompida ou expira deixa uma mensagem de
   **sistema** explicando o que houve — nunca silêncio.
7. **Token e MCP.** O run token de chat tem `ConversationID` e **nenhuma task** (`IssueForConversation`); é válido só
   enquanto a run está ativa. É o mesmo MCP `hanko_*`: `whoami` mostra a conversa; `hanko_task_create` funciona (sem
   `parent` implícito) e **cita a conversa na descrição** da task criada; `hanko_gate_request` sem `task` falha com
   mensagem clara. A API REST recusa run tokens nas rotas de conversa (a conversa é entre um humano e uma persona).
8. **Papéis (ADR-0021).** Todo membro lê (viewer inclusive); criar conversa, enviar mensagem, arquivar e reabrir exigem
   `member`; só ator humano escreve. Não membro recebe 404. Arquivar não cancela uma run em andamento (a resposta ainda
   chega); conversa arquivada não aceita mensagem até ser reaberta.
9. **API.** `GET/POST /companies/{c}/conversations`, `GET …/{id}`, `GET/POST …/{id}/messages`,
   `POST …/{id}/archive|reopen`. `Run` ganha `conversation_id`/`conversation_title`, `task_id` passa a ser opcional e o
   gatilho `chat` entra no enum.
10. **UI.** `/c/{empresa}/chat` (lista com abas Abertas/Arquivadas/Todas e formulário de nova conversa com a escolha da
    persona) e `/c/{empresa}/chat/{id}` (thread em markdown por `internal/ui/md`, formulário htmx). **Resposta ao vivo
    por polling htmx** (`hx-trigger="every 2s"` enquanto há run ativa ou mensagem na fila, com o indicador
    "pensando…"; ocioso, o fragmento volta sem o gatilho e o polling para sozinho) — escolhido no lugar de um tópico
    SSE por conversa porque a thread é pequena, o fragmento é idempotente e não há conexão longa a derrubar atrás de
    proxy; o log detalhado continua no SSE da run. A página da persona lista as conversas dela; "Execuções" e a página
    da run identificam runs de chat ("Chat: título", ligando à conversa).
11. **CLI.** `hanko chat <persona> "mensagem"` abre a conversa, espera a resposta (consulta a cada 2 s; `--timeout`,
    padrão 5 min) e a imprime; `--no-wait` volta com o id; `--conversation <id|prefixo>` continua uma conversa;
    `hanko chat list|show`. Falha da persona (aviso de sistema) sai com código 1.

## Consequências
- O chat gasta dinheiro como qualquer run: conta no orçamento da persona e da empresa e na trava semanal; uma persona
  pausada ou sem orçamento não conversa, com o motivo à vista.
- Tabelas e consultas que assumiam `runs.task_id` obrigatório (revisão automática, gates, export/import, alertas de
  run travada, custos) foram ajustadas; o export/import ignora runs de chat e conversas (estado de operação, como gates e
  alertas) — é um limite conhecido, não um bug.
- As mensagens de **sistema** gravadas no banco (avisos de falha) e as respostas das personas ficam no idioma em que
  foram produzidas (pt-BR); só a interface é traduzida.
- A thread carrega até 200 mensagens por vez (a API aceita até 500); conversas muito longas mostram só o fim. Busca
  dentro de conversas e anexos ficam fora desta versão.
- Sem SSE por conversa, a latência de exibição da resposta é de até ~2 s; trocar o polling por um tópico não muda o
  modelo nem a API.
- Uma pessoa pode ter várias conversas com a mesma persona; elas são independentes (sessões e históricos próprios) e
  cada uma tem no máximo uma run ativa, mas a persona pode estar rodando uma task e conversas ao mesmo tempo (cada run
  responde ao próprio orçamento e à mesma trava semanal).

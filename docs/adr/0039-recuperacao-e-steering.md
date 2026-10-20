# ADR-0039 — Recuperação de run, diagnóstico e steering
Data: 2026-10-17 · Status: aceita

## Contexto
Uma run morria de três jeitos silenciosos: o processo do servidor caía (a run virava `interrupted` no boot e a task
ganhava uma run nova do zero, sem sessão nem lembrança do que já tinha sido feito), o harness falhava (a Caixa mostrava
só o texto do erro) ou o operador queria mudar o rumo e só podia esperar o fim (comentário) ou cancelar. Pior: se o
processo morria no meio de uma tool com efeito externo (um `git push`, uma escrita no Hanko), a run nova podia repetir
o efeito sem que ninguém soubesse. E a fila de mensagens que esperam a run existia só no chat, sem forma de ver a ordem,
editar ou descartar.

## Decisão
1. **Continuação limitada (#12).** Uma run `interrupted` (queda do processo ou encerramento do servidor) gera sua
   **continuação**: nova run da mesma task/conversa com `continuation_of_run_id`, `continuation_attempt` (0 = original) e
   `continuation_reason` (`recovery`, `steer`, `retry`, `continue`). A continuação retoma o `session_ref` da run
   anterior quando o adapter sabe retomar (`SessionResumer`) e recebe um **briefing** no prompt: tentativa, o que a
   run anterior já tinha feito (cauda do log: falas e tools) e, se houver, a decisão do operador. **Máximo de 2
   continuações automáticas** por cadeia (`domain.MaxContinuations`); a terceira interrupção encerra a automação.
   O `session_ref` passa a ser gravado assim que o harness o anuncia (`adapter.Event.SessionRef`), porque uma queda
   dura nunca devolve o Result.
   - Mecânica: a run antiga recebe `resume_pending` + `resume_reason` na mesma transação que a interrompe; o
     **checkout** seguinte da task (qualquer gatilho) consome a marca e abre a continuação. O wake nasce na mesma
     transação e o `ListWakeCandidates` também enxerga a marca (rede de segurança). Em conversa de chat a continuação é
     aberta direto na transação (não há wake), re-vinculando as mensagens humanas da run morta à nova.
   - Não é nova ordem de checkout nem novo gatilho: a constraint `runs_trigger_check` não muda (evita colisão com outros
     marcos que a redefinem). Orçamento, trava semanal, status da persona e workspace valem igual.
2. **Diagnóstico e decisão humana (#13).** Toda interrupção grava `runs.diagnosis` (JSON): `cause`
   (`process_restart|server_shutdown|agent_error|timeout|cancelled`), `cause_detail`, `attempt`/`max_attempts`,
   `next_action` (`auto_continue|human_decision|none`), `owner` (`system|human`) e, quando existe, `uncertain` (a tool
   em andamento). Falhas e expirações calculam o mesmo card na leitura (sem coluna). A run que **esgotou** as
   continuações ou tem **desfecho incerto** fica com `decision_pending = true` e entra na Caixa (aba Falhas) com o card;
   a task sai do radar do heartbeat enquanto a decisão não vier. Ações (`POST /runs/{id}/actions`, `hanko run
   retry|continue|close`, botões na run e na Caixa; papel `member`; só humano):
   - **Tentar de novo** (`retry`): run nova do zero (sem sessão), com o briefing do que falhou; zera a contagem.
   - **Continuar** (`continue`): continuação com sessão + briefing; em desfecho incerto o briefing carrega a
     decisão e manda **verificar o estado antes de repetir o efeito**.
   - **Encerrar** (`close`): resolve o card sem nova run; a task em andamento vai para `blocked` (não é re-acordada).
   Cada ação grava `activity_event` (`run.decision`) na mesma transação.
3. **Desfecho incerto.** Na interrupção, o service reconstrói as tool calls abertas do log (`tool_use` sem
   `tool_result`, casadas por id e, na falta dele, por ordem). Uma tool aberta é **de efeito externo** a menos que
   esteja na lista de leitura (`domain.ToolHasExternalEffect`: Read/Glob/Grep/LS/WebFetch/WebSearch, `hanko_*_get|list|
   search`, tools ACP `read|search|think|fetch`…). Lista conservadora: o que não se reconhece é tratado como efeito.
   Com uma aberta de efeito externo **não há continuação automática**: `next_action = human_decision`.
4. **Steer por capability (#14).** Cada adapter declara `SteerMode`: `inject` (entrega a mensagem na run viva), `restart`
   (interrompe e continua) ou `none`. `claude` injeta por stdin em `--input-format stream-json` (uma linha
   `{"type":"user","message":{...}}` por mensagem; o stdin só fecha quando todo turno enviado devolveu `result`), `acp`
   injeta cancelando o turno em curso (`session/cancel`) e enviando a mensagem como novo `session/prompt` **na mesma
   sessão** (o processo e o contexto continuam), `fake` injeta no laço de eventos, `codex` é `restart`. A capability
   estática por nome de harness vive em `domain.SteerModeFor` (a UI mostra o modo sem consultar adapters; um teste
   garante que bate com os adapters). `SteerRun` grava um follow-up com `delivery = steer|restart`:
   - `inject`: publica `run.steer` no broker; o executor repassa ao adapter por `Spec.Steer` e, no **ack** do adapter,
     o service marca o follow-up consumido (vira comentário/mensagem humana vinculado à run) e registra o evento
     "mensagem injetada" no log. Sem ack (a run acabou antes), o follow-up segue na fila e é entregue à run seguinte.
   - `restart`: cancela a run ativa; ao terminar, a task é re-acordada com o follow-up e a nova run é continuação
     (`reason = steer`, com sessão se o harness retoma).
5. **Fila de follow-ups unificada (#15).** Visão única `Followup` (chat e task): posição, **motivo da espera**
   (`run_active|steer_pending|restart_pending|decision_pending|persona_unavailable|unassigned|starting`), editar e
   descartar enquanto `pending`. Chat: continua em `conversation_messages` (`run_id` nulo = na fila; ganhou `edited_at` e
   `delivery`), por compatibilidade com o ADR-0027. Task: tabela nova `task_followups`. Consumo = **checkout da run**:
   cada follow-up pendente vira comentário humano da task (na ordem de entrada) e fica ligado à run que o recolheu. Quando
   a run termina por qualquer motivo e há pendentes, o `FinishRun` re-acorda a task. **Cancelar dispara o próximo run
   com os pendentes** — também no chat (reverte a regra do ADR-0027 §4 para cancelamento; interrupção continua sendo
   tratada pela continuação).
6. **API/UI/CLI.** `RunDetail` ganha `continuation_*`, `diagnosis` e `steer_mode`; `POST /runs/{id}/actions`,
   `POST /runs/{id}/steer`, `GET /tasks/{id}/followups`, `GET /conversations/{id}/followups`,
   `POST /tasks/{id}/followups`, `PATCH|DELETE /followups/{id}`. UI: card de diagnóstico na run e na Caixa, caixa de
   steer na run ativa mostrando o modo ("injeta ao vivo" / "interrompe e continua"), painel de fila na task e no chat.
   CLI: `hanko run retry|continue|close|steer`, `hanko followup list|add|edit|discard`.

## Consequências
- Uma queda do servidor deixa de perder trabalho: a run volta sozinha, com sessão, no máximo 2 vezes, e nunca repete
  efeito externo sem uma decisão registrada.
- Dependência Go nova: nenhuma. Migration 00090 (aditiva). Nenhum gatilho novo de run.
- Riscos conhecidos: o `result` do claude em stream-json multi-turno repete custo/uso acumulado da sessão (o Hanko fica
  com o último); o ACP não tem "injeção" padronizada, então o modo `inject` do ACP é cancelar-e-reenviar dentro da
  sessão (pode abortar a tool em curso do agente); a lista de tools de leitura é uma allowlist manual.
- Fora do escopo: retomar sessão do codex (sem `SessionResumer`; a continuação dele leva só o briefing no prompt);
  continuação automática de falha de agente (`failed` segue manual: Tentar de novo).

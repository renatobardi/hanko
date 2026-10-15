# ADR-0020 — Alertas operacionais e revisão automática antes do gate
Data: 2026-10-15 · Status: aceita

## Contexto
O operador só descobria persona parada, execução travada ou orçamento quase no fim olhando telas. E o gate de
revisão (`task_review`) chegava ao humano sem nenhuma segunda opinião, mesmo havendo outro harness/modelo disponível.

## Decisão
1. **Alertas são estado do banco, nunca texto de log.** Tabela `alerts`, um aberto por (empresa, tipo, sujeito)
   (índice único parcial). O job periódico `alert_tick` (river, sob advisory lock) abre, atualiza e fecha sozinho:
   - `persona_silent`: persona com heartbeat agendado sem rodar há mais de `HANKO_ALERT_SILENT_FACTOR` × o intervalo (3);
   - `run_stuck`: run `running` sem evento há mais de `HANKO_ALERT_RUN_STUCK_AFTER` (15 min);
   - `budget_near_limit`: orçamento ≥ 80% (crítico a partir de 100%), reaproveitando o cálculo do hard stop.
   Aparecem na Caixa de entrada (aba Alertas) e em `/metrics` (`hanko_alerts_open{kind}`).
2. **Nome amigável de execução** `Adjetivo_Substantivo`, derivado do id, único por empresa; humano lembra o nome,
   máquina usa o id. CLI e API aceitam os dois.
3. **Revisão automática não decide.** A persona autora pode apontar um revisor: outra persona ou o mesmo papel com
   harness/modelo alternativo. Quando a task entra em revisão, o job `start_review` espera a run da autora terminar e
   abre uma run headless `trigger=review` (somente leitura, sem credenciais do Hanko). O parecer — veredito, justificativa,
   sha256 do pacote revisado (task + histórico + entrega) e run — fica gravado no gate. Reprovado marca o gate; a decisão
   continua humana. Sem veredito válido (falha, expiração após 2 h, revisor pausado ou barrado por orçamento/trava),
   o gate abre do mesmo jeito, com o motivo. Sem revisor configurado, nada muda.

## Consequências
- A constraint `runs_trigger_check` agora inclui `review`; migration que redefinir a lista precisa mantê-lo.
- O sha256 cobre o pacote mostrado ao revisor, não o estado git do workspace.
- Configuração do revisor por API e CLI; o formulário na UI fica para depois.
- A run de revisão não carrega credencial nenhuma: sem run token, sem MCP do Hanko, sem MCP de memória (o proxy
  autentica pelo run token) e sem `HANKO_URL` no ambiente. O harness roda forçado em somente leitura, ignorando o
  `adapter_config` da persona (claude: `--permission-mode plan` e edição desabilitada; codex: `--sandbox read-only`;
  `extra_args` descartados).
- O parecer vive só no gate: a run de revisão não posta o resumo como comentário da persona nem a acorda por
  comentário perdido.
- Ordem única de locks: task → run → review. `StartReview`, `FailReview`, a varredura de revisões e o fim/cancelamento
  de uma run de revisão travam a task antes da run/revisão, a mesma ordem de `UpdateTask` e `cancelTaskRun`. Fechar a
  task cancela também a run de revisão aberta.
- Revisão em harness alternativo é cobrada na assinatura ativa da empresa cujo `agent` é o do revisor (a da persona
  quando ela cobre esse harness) e respeita a trava semanal dela. O revisor alternativo precisa diferir da autora em
  agent ou modelo.
- A Caixa de entrada lista, de forma informativa (não conta como pendência), as tasks `in_review` cuja revisão ainda
  não abriu o gate.

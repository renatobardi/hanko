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

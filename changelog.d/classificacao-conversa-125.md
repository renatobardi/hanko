### Fonte externa: classificação de conversa pela telemetria (#125)

- Cada conversa externa ganha a fase do AI-DLC pela regra do agent-studio: troca manual, abertura, label, skill, papel e
  ação, nessa ordem; a primeira que decide vale. Sem sinal forte, `build` com confiança baixa (ADR-0079, itens 1 a 3).
- A marca `regression` vai para a conversa cujo `oute.task.id` começa com `regression-` ou cujo repositório é `regression` (item 7).
- O Jev não é chamado nesta fatia: a ação fraca só marca que ele seria chamado.
- English summary: external conversations get an AI-DLC phase from telemetry, with the same signal order as the
  agent-studio; manual marks win; the `regression` tag is set from the task id or repository.

# ADR-0082 — Modelo de custo elevado: fonte externa, `unpriced`, histórico de mensalidade e conferência de preços
Data: 2026-10-25 · Status: aceita (Bardi, 2026-10-25) · Issue: #66 (v0.8) · Usa: ADR-0029, ADR-0070, ADR-0075, ADR-0076, ADR-0078 · Altera: ADR-0019

## Contexto
O Hanko tem um modelo de custo por run, com quatro custos: real, lista, estimado e pago (ADR-0019 item 2). O preço vale
no fim do run. Não há histórico de mensalidade: o pago histórico usa o valor atual. Preço novo só entra por migration
ou seed (ADR-0019, "Consequências"). O preço se acha pelo prefixo mais longo que casa com o modelo
(`internal/domain/readiness.go:164-190`). A assinatura é da empresa e cobre runs por harness e prefixo de modelo
(`internal/domain/domain.go:375-395`).

O agent-studio calcula o custo de cada chamada externa ao modelo (`agent_studio/cost.py@845764f` do oute-agent):
- **Chamada.** São os spans `claude_code.llm_request`, `session_task.turn`, `ai_memory.llm_request` e `jev.decision`.
  O agente `router` fica fora (`cost.py:39-47`).
- **Custo real.** É o `cost_usd` do span ou, sem ele, o do log `api_request` de mesmo `request_id`, com folga de 1 h.
  Na assinatura `zai`, esse custo é descartado, porque o valor que o Claude Code calcula para o `glm-*` não é real
  (`cost.py:55,83-87,229-251`).
- **Classe.** `real`, `listed` (chamada de assinatura), `estimated` ou `unpriced`. O `unpriced` nunca vira zero
  (`cost.py:216-226`).
- **Erro.** Span com status 2 ou log com severidade ≥ 17 (`cost.py:89-92`).
- **Preço.** Vale o da hora do fato. Antes da primeira linha do modelo, vale a primeira. A busca é pelo nome exato e
  tira o prefixo do provedor (`cost.py:128-140`).
- **Pago.** A mensalidade que valia naquele dia ÷ os dias do mês. O valor se divide entre todas as chamadas da
  assinatura, na proporção do custo de lista. Dia sem chamada é "sem uso". Assinatura sem plano naquele dia conta
  "sem plano", nunca US$ 0 (`rateio.py:1-16`).
- **Mensalidade.** `plan_history` só se acrescenta. Vale a linha com o maior início menor ou igual ao dia, e a
  registrada por último desempata (`planos.py:1-20`). A chamada declara a assinatura (`oute.subscription`: `claude`,
  `zai` ou `codex`).
- **Conferência de preços.** Roda na subida e a cada 24 h. Confere os modelos do seletor e todo modelo com chamada em
  30 dias. O preço só troca quando as duas fontes concordam. Um modelo com trava manual nunca é trocado pela fonte. O
  resultado de cada modelo e de cada fonte fica gravado (`prices.py:1-60`). Os limites são 20 s por operação, 60 s por
  fonte, 32 MiB e US$ 100.000 por 1M tokens; a falha guarda só o código, nunca o corpo (`price_sources.py:25-36`).

No gate da #67, o Bardi decidiu: preço da hora do fato para a fonte externa, calculado uma vez na ingestão; os runs do
Hanko seguem com o preço do fim do run (`docs/research/2026-10-inventario-agent-studio.md` §9.1 ponto 4). Também
decidiu a classe `unpriced`, o histórico de mensalidade, a conferência diária e a origem preservada (§9.2 bloco 3). A
decisão 8 da #66 pede um modelo de custo só, com o custo histórico migrado com o valor que o agent-studio calculou, sem
recálculo. As escolhas abaixo são do Bardi, em 2026-10-25.

## Decisão
1. **Custo da chamada externa.** As regras do agent-studio para a chamada, o custo real (com a regra da `zai`) e o erro
   valem como estão, para o fato que chega pela ingestão (ADR-0076). O custo de lista se calcula uma vez na ingestão,
   com o preço da hora do fato, e se grava; nada se recalcula. Os runs do Hanko seguem com o preço do fim do run
   (ADR-0019 item 1).
2. **Classes de custo.** Valem para runs e chamadas externas: `real`, `listed`, `estimated` e `unpriced`. Um modelo sem
   preço fica `unpriced` e nunca vira zero: as somas o contam à parte. `listed` é a chamada ou o run de assinatura sem
   custo real; `estimated`, os outros sem custo real. O `runs.cost_source` de hoje (`reported`, `estimated`, `none`;
   `db/migrations/00004_custos_fluxos.sql:11`) se mapeia assim: `reported` → `real`, `none` → `unpriced`, e
   `estimated` → `listed` ou `estimated`, conforme o run tenha assinatura.
3. **Uma regra para achar o preço.** O Hanko tira o prefixo do provedor e busca o prefixo mais longo, que inclui o nome
   exato. Os apelidos conhecidos casam pela família, como hoje. Nada casando, o resultado é `unpriced`, e o alerta
   `price_model_unpriced` (ADR de alertas) aponta. Uma chamada externa de modelo novo sem linha própria cai no preço da
   família, como estimada.
4. **Assinatura pela chave declarada.** A assinatura do Hanko ganha uma chave externa opcional (`claude`, `zai`,
   `codex`). Uma chamada externa que declara essa chave cai nessa assinatura. A chamada cuja chave não tem assinatura
   fica "sem plano", contada à parte. Os runs do Hanko seguem pelos prefixos.
5. **Rateio único.** O valor do dia de uma assinatura se divide entre os runs e as chamadas externas que ela cobre, na
   proporção do custo de lista. Dia sem nenhum dos dois é "sem uso". O denominador é sempre o dia inteiro, sem filtro,
   para que as partes fechem com o valor do dia (`rateio.py:11-13@845764f`).
6. **Histórico de mensalidade** (altera a primeira consequência do ADR-0019). Cada assinatura tem um histórico de
   valores, que só se acrescenta: valor, início e hora do registro. O valor de um dia é o da linha com o maior início
   menor ou igual a ele; a registrada por último desempata. O pago de todos os dias usa esse valor, inclusive o dos runs
   que já existem. Cada assinatura nasce com uma linha: o valor atual, com início na criação. Trocar um valor é
   acrescentar uma linha, por owner ou admin com reautenticação recente (ADR-0070), com `activity_event`. O pago
   passado só muda com uma linha de início no passado, e o `activity_event` registra a troca. As
   `subscription_periods` continuam: fora delas, não há cobrança.
7. **Conferência diária de preços.** O Hanko confere os preços na subida e a cada 24 h, com as regras e os limites de
   `prices.py` e `price_sources.py@845764f`. Confere os modelos do catálogo e todo modelo com chamada ou run em 30
   dias, e só troca o preço quando as duas fontes concordam. A saída à internet passa pelo `internal/netguard`
   (ADR-0029). O preço novo entra como linha nova de `model_prices`, com a origem (`config`, `sources` ou `migrated`).
   A trava manual vira uma marca da linha de preço, que a fonte nunca troca. Cada conferência grava o resultado por
   fonte e por modelo: `equal`, `changed`, `new`, `diverges`, `locked_differs`, `no_source`, `source_down`. Os alertas
   de preço leem a conferência mais recente.
8. **Migração.**
   - O custo histórico de cada chamada migra com o valor e a classe que o agent-studio calculou, com a origem
     `migrated`, sem recálculo.
   - As 21 linhas de `price_history` vão para `model_prices` com a origem.
   - As 3 de `plan_history` vão para o histórico da assinatura da chave correspondente, marcadas `migrated`.
   - `price_runs` e `price_checks` migram como histórico da conferência.
   - O ensaio confere 100% contra as somas do agent-studio.

## Consequências
- O pago dos runs do Hanko passa a usar o valor de cada dia; com uma linha só por assinatura, nenhum número antigo
  muda.
- O Hanko passa a falar com duas fontes públicas de preço, uma vez por dia.
- Os runs do Hanko passam a usar as mesmas quatro classes; não verificado se alguma soma de hoje conta o `none` como
  zero.
- A tabela `model_prices` continua de todas as empresas; a conferência a mantém, não a empresa.

## Alternativas descartadas
- **Assinatura pelos prefixos de modelo**: quebra na `zai`, em que o `glm-*` roda pelo Claude Code, e muda a conta do
  histórico.
- **Assinaturas separadas** para runs e chamadas externas: a mesma mensalidade se cobraria duas vezes.
- **Histórico só daqui para frente**: duas regras de pago, e a fonte externa não poderia usar a mesma.
- **Duas regras de preço** (exato para a fonte externa, prefixo para os runs): a mesma tabela responderia de dois
  jeitos.
- **Preço exato para tudo**: os runs com modelo novo ficariam sem preço.

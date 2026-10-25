# ADR-0083 — Alertas de agente externo e "vence em" por tipo na Caixa
Data: 2026-10-25 · Status: aceita (Bardi, 2026-10-25) · Issue: #66 (v0.8) · Usa: ADR-0075, ADR-0076, ADR-0077, ADR-0080, ADR-0082 · Altera: ADR-0042

## Contexto
O agent-studio calcula 17 tipos de alerta da telemetria, na hora da consulta e sem estado guardado. Um alerta liga
quando o dado mostra o problema e desliga quando o dado seguinte não mostra mais (`agent_studio/alerts.py:1-62@845764f`
do oute-agent):

- 10 de pipeline e de agente: `queue`, `destination_refusing`, `host_no_data`, `spool`, `quota`, `round_stalled`,
  `round_old`, `llm_proxy_down`, `memory_capture` e `memory_hooks_untrusted`;
- 5 de preço: `price_changed`, `price_sources_diverge`, `price_source_down`, `price_model_unpriced` e
  `price_fixed_differs` (`price_alerts.py`);
- 2 de custo: `cost_claude_diff` e `cost_subscription_unpriced` (`cost_alerts.py:18,46`).

Os limites estão em `config/agent-studio/config.toml:154-200@845764f`. As regras gerais são três:
- **Host parado.** Um host parado não liga fila, recusa, spool nem cota.
- **"Antigos".** Os `round_*` e os de preço recolhem para "antigos" depois de 2 h (`alerts.py:44-62,72`).
- **Nunca pela linha do collector.** Nenhum alerta se baseia na linha `Exporting failed. Dropping data`, porque
  nenhuma consulta lê o corpo de log (`alerts.py:50-52`).

O "visto" (`ack_marks`) vale 24 h e volta antes se a ocorrência muda (`acks.py:1-28@845764f`).

No Hanko, o alerta é uma linha da empresa, com tipo, sujeito, severidade (`warning` ou `critical`), abertura, último
visto e fechamento (`db/migrations/00009_alertas_revisor.sql:8-20`). Hoje há 4 tipos
(`db/migrations/00070_ambientes_readiness_modelos.sql:51`). Um item da Caixa só volta quando a impressão digital muda,
e nunca vence (ADR-0042 §A item 2).

O gate da #67 decidiu (`docs/research/2026-10-inventario-agent-studio.md` §9.1 ponto 3 e §9.2 blocos 4 e 5):
- os 17 tipos com os mesmos nomes e regras;
- limites por empresa;
- só os alertas abertos migram;
- "vence em" por tipo no Hanko todo;
- as 57 `ack_marks` vão para `inbox_state`.

O host parado e o `host_no_data` estão no ADR-0077 itens 4 e 5. As escolhas abaixo são do Bardi, em 2026-10-25.

## Decisão
1. **Os 17 tipos entram em `alerts`**, com os nomes e as regras de `alerts.py`, `price_alerts.py` e
   `cost_alerts.py@845764f`. O `alerts_kind_check` passa de 4 para 21 tipos. Os sujeitos novos são: host externo,
   agente externo, rodada externa, modelo e fonte de preço. As dimensões de cada tipo são as de `acks.py:36-44@845764f`
   e formam a identidade da ocorrência. Uma ocorrência nova abre uma linha; quando o dado deixa de mostrar o problema, a
   linha fecha. Um nome muda: `price_fixed_differs` vira `price_locked_differs`, para casar com a marca de trava e com
   o `locked_differs` do ADR-0082 item 7. A migração mapeia o nome e o ensaio o registra.
2. **Avaliação no Hanko.** Um job avalia as regras sobre os fatos da ingestão (ADR-0076), pela hora do fato. Uma regra
   que falha não derruba as outras. As regras gerais valem como estão:
   - host parado não liga os outros alertas desse host (ADR-0077 item 5);
   - `round_*` e os de preço recolhem para "antigos" depois de 2 h;
   - `price_changed` some em 7 dias;
   - nenhum alerta lê o corpo de log: nunca a linha `Exporting failed. Dropping data`.
3. **Limites por empresa.** Cada limite de `config.toml:154-200@845764f` vira um limite da empresa, com estes valores
   como padrão da Oute: fila 0,5; recusa em 15 min; host sem dado em 30 min; spool 41.943.040 bytes ou `dropped` em
   60 min; cota 98%, com folga de 20 min na janela de 5 h; rodada parada em 30 min; `lookback_hours` 24; proxy parado
   em 5 min; captura até 20% em 7 dias; "antigos" em 2 h; diferença de custo 10%. Owner e admin trocam, com
   `activity_event`.
4. **Alertas de preço em cada empresa afetada.** `price_changed`, `price_model_unpriced` e `price_locked_differs` abrem
   nas empresas com run ou chamada daquele modelo nos últimos 30 dias. `price_sources_diverge` e `price_source_down`
   abrem em toda empresa com uso nesse período. Os de custo (`cost_*`) são da empresa das chamadas.
5. **Severidade.** `critical` para o que perde dado ou trava trabalho: `host_no_data`, `destination_refusing`, `spool`
   e `quota`. `warning` para os outros 13.
6. **"Vence em" por tipo** (altera o ADR-0042 §A item 2). Um item marcado volta como não lido e não arquivado quando a
   impressão digital muda **ou** quando o prazo do tipo vence, contado no servidor desde a marca. Os prazos padrão são:
   - 24 h para os alertas da fonte externa e para a pergunta da rodada (ADR-0080 item 6), como hoje;
   - "sem prazo" para os tipos que o Hanko já tem, e nada muda para quem já usa.

   Cada empresa troca o prazo de cada tipo, de "sem prazo" até 30 dias. A Oute nasce com os valores de hoje. Vencer
   não apaga a marca: só tira o efeito dela.
7. **Migração.** Só os alertas abertos na hora do corte entram como abertos; os passados se recalculam das regras sobre
   a telemetria migrada, e o ensaio confere os abertos contra o `GET /v1/alerts` do agent-studio. As 57 `ack_marks`
   vão para `inbox_state`, com a validade que tinham, atribuídas ao Bardi e marcadas `migrated`; o ensaio confere que
   cada alvo aponta para o item certo.

## Consequências
- `llm_proxy_down`, `memory_capture` e `memory_hooks_untrusted` ficam no núcleo, com nomes genéricos o bastante. Só
  ligam para quem manda essas métricas.
- Um problema de preço aparece uma vez em cada empresa afetada; hoje é só a Oute.
- O histórico de alertas passa a existir no Hanko, recalculado; não verificado se o agent-studio guarda algum além
  das marcas (inventário §9.2 bloco 4).

## Alternativas descartadas
- **24 h para todos os tipos**: o gate e a task bloqueada arquivados voltariam todo dia.
- **Alertas de preço numa empresa operadora da instância**: criaria um conceito de operador que o Hanko não tem.
- **Calcular na hora da consulta, como o agent-studio**: o Hanko guarda o alerta como linha (ADR-0020), e a Caixa e
  as notificações dependem dela.

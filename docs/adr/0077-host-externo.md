# ADR-0077 — Host externo
Data: 2026-10-25 · Status: proposta (aguarda aceite do Bardi) · Issue: #66 (v0.8) · Usa: ADR-0075, ADR-0076

## Contexto
Cada fato da telemetria do oute traz o host em que o agente externo rodou (`host_name`, preenchido pelo collector de
cada host, `config/otel/collector.yaml:31-55@845764f` do oute-agent). Hoje são 2: `oute-server` e `oute-mac`
(`docs/research/2026-10-inventario-agent-studio.md:87`). O agent-studio usa o host para a saúde da telemetria: fila e
spool do collector por host, `host_no_data` só para os hosts sempre ligados (padrão `oute-server`, 30 min sem nenhum
registro) e "host parado não liga os outros alertas" (`agent_studio/alerts.py:4-13,44,89-90,547-548@845764f`). As telas
filtram por host. O Hanko já tem **ambiente de execução** (ADR-0037, ADR-0055), que é onde o Hanko executa uma persona,
com driver e probe. A #66 chamava este conceito de "host da fonte"; o ADR-0075 descartou "fonte". As escolhas abaixo
são do Bardi, em 2026-10-25.

## Decisão
1. **Host externo.** É a máquina onde roda um agente externo. Pertence à empresa. O Hanko só observa: nunca executa
   nada num host externo (decisão 4 da #66). Não é um ambiente de execução; se a mesma máquina for também um ambiente
   `ssh`, os dois seguem conceitos separados.
2. **O host do fato é o declarado.** O host externo de um fato é o `host_name` que ele traz. A chave `ingest` que o
   entregou (ADR-0076 item 3) fica gravada como "entregue por". Assim o replay feito no `oute-server` com objetos do Mac
   (`scripts/oute:427@845764f`) e o histórico migrado, que não tem chave, caem no host certo.
3. **O host externo nasce na ingestão**, como o agente externo (ADR-0075 item 2): `activity_event` e item na Caixa
   ("host externo novo"). Owner e admin renomeiam, arquivam ou juntam dois hosts externos, com `activity_event`; juntar
   leva o passado do host juntado.
4. **Sempre ligado.** O host externo tem a marca "sempre ligado", que owner e admin mudam. Só host sempre ligado gera
   `host_no_data`; os outros mostram só o último dado. Na migração, `oute-server` entra marcado, igual ao padrão de hoje.
5. **Host parado.** Sem nenhum fato há mais do limite da empresa (padrão da Oute: 30 min), o host está parado, e os
   alertas de fila, recusa, spool e cota desse host não ligam. Os alertas em si são de ADR seguinte.
6. **Telas.** O host externo é filtro comum das telas de agente externo (bloco 6 do gate da #67) e tem uma lista com o
   último dado e há quanto tempo, como a `hosts[]` de hoje (`alerts.py:305-311@845764f`).

## Consequências
- Um fato pode declarar um host que não é o da chave. O Hanko não recusa nem avisa; o "entregue por" fica para
  auditoria.
- O alias `ssh` com que o Tray abre o `oute approve` em outro host (`~/.oute/tray-hosts`) fica no ADR do Hanko Desktop,
  não no host externo: o Hanko nunca roda nada no host.
- Hosts externos entram no export da empresa (decisão 6 da #66); o formato fica para o contrato do v0.8.

## Alternativas descartadas
- **Reaproveitar "ambiente", com um tipo "observado"**: mistura onde o Hanko executa com onde só observa; driver,
  probe e atribuição para persona não valem aqui.
- **"Máquina"**: não diz que é observado, e já aparece em outro sentido no ADR-0025.
- **O host do fato ser o da chave**: o replay do Mac pelo servidor cairia no `oute-server`, e o histórico ficaria sem
  host.
- **Item na Caixa quando o host declarado diverge do da chave**: o Bardi preferiu só gravar o "entregue por".

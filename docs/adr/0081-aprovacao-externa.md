# ADR-0081 — Aprovação externa: pedido do canal observado, script restrito e decisão no host
Data: 2026-10-25 · Status: proposta (aguarda aceite do Bardi) · Issue: #66 (v0.8) · Usa: ADR-0042, ADR-0075, ADR-0076, ADR-0077, ADR-0078

## Contexto
O canal de aprovação do oute leva ao host o que um agente não pode rodar sozinho. O agente propõe um script pelo
`oute-propose`, e o Bardi o aprova ou recusa no host, com o `oute approve` (ADR-01 do oute-agent). O agent-studio
mostra esses pedidos, mas não decide nada (`agent_studio/proposals.py:1-16@845764f` do oute-agent):

- **Pedido.** É identificado pelo `oute.canal.id` e nasce do evento `oute.canal.proposed`. O corpo desse evento traz o
  script, o título, o usuário (`as`), o agente e a origem (`state.py:268-275@845764f`).
- **Decisão.** Vem do `oute.canal.decided`, com a decisão (`recusado` ou `executado`), o rc, a duração, o tamanho da
  saída, o aprovador (`human` ou `human-dark`) e o sha256 do que rodou. Esse evento não tem corpo: a saída do host
  nunca sai do host (`docker/oute-emit:508-528@845764f`).
- **Conferência.** Um `proposed` forjado com o mesmo id pode trocar o script que a página mostra. Por isso a página
  exibe o sha256 no formato do `oute approve` e conta quantas versões diferentes do pedido chegaram
  (`proposals.py:12-15,157-192@845764f`).
- **Lista da outbox.** Cada host manda a sua lista no `oute.canal.snapshot`, e a mais nova vence. Um pendente que não
  está na lista tirada mais de 60 s depois dele sai da tela, mas nada é apagado (#815; `proposals.py:29,108-117@845764f`;
  `state.py:57-67@845764f`).
- **Comando para copiar.** A página do pendente mostra `oute approve <id>` para copiar. Ela só monta o comando quando o
  id está no padrão; fora dele, manda escolher pela lista (`templates/proposal.html:79-87@845764f`).

No Hanko, "aprovação" já é a `ToolCallApproval` dos gates (ADR-0041) e a `StageApproval` dos pipelines
(`internal/domain/pipelines.go:120`). Este ADR segue as decisões 4, 6 e 10 da #66 e o bloco 7 do gate da #67
(`docs/research/2026-10-inventario-agent-studio.md` §9.2: a migração segue o código). As escolhas abaixo são do Bardi,
em 2026-10-25.

## Decisão
1. **Nomes.** O conceito é `external_approval` no código e **Aprovação externa** na UI; o "externa" o separa das
   aprovações do próprio Hanko. O estado é `pending` ou `decided`. As decisões são `rejected` e `executed` (hoje
   `recusado` e `executado`), e o aprovador é `human` ou `human_dark`. Os campos vão para o inglês, mapeados um a um,
   e o ensaio registra o mapeamento para a conciliação.
2. **O Hanko só observa.** A aprovação externa nasce dos eventos `oute.canal.*` da ingestão (ADR-0076). O host dela é
   o que o evento declara (ADR-0077), o agente é o agente externo (ADR-0075) e, quando houver, a sessão é a sessão
   externa (ADR-0078). O Hanko nunca aprova, recusa nem roda nada no host (decisão 4 da #66). A página diz isso e
   mostra `oute approve <id>` para copiar, na mesma regra de id de hoje. No Hanko Desktop, "aprovar" abre o Terminal no
   `oute approve`.
3. **Script restrito.** Só owner e admin veem o corpo do script, e cada abertura gera `activity_event`, como o conteúdo
   completo (decisão 10 da #66). Os outros papéis veem título, estado, decisão, host, agente, sha256 e versões. A saída
   do host nunca entra no Hanko.
4. **Conferência mantida.** A página mostra sempre o sha256 do que exibe, no formato do `oute approve`, e o número de
   versões diferentes que chegaram. Com mais de uma versão, a página avisa. O Hanko não escolhe qual versão é a
   verdadeira.
5. **Lista da outbox.** A regra do #815 vale como está: a lista mais nova de cada host vence, e o pendente que ela não
   tem, depois da folga de 60 s, sai das listas, da Caixa e do Hanko Desktop sem ser apagado. Lista com hora mais de
   300 s à frente do servidor é recusada como forjada (`state.py:62@845764f`).
6. **Caixa.** Cada aprovação externa pendente é um item da Caixa (ADR-0042) para owner e admin, e sai dela quando
   decide ou some da outbox. A pergunta da rodada segue no ADR-0080 item 6, como item próprio.
7. **Migração.** Os pedidos e as decisões se refazem dos eventos migrados, e o ensaio confere 100% contra o estado do
   agent-studio (pendentes, decididos e os tirados pela regra do #815).

## Consequências
- Quem não é owner nem admin acompanha o canal sem ver a topologia do host.
- Um `proposed` forjado continua possível pelo container do agente; a defesa segue sendo conferir o sha256 no
  `oute approve`.
- Os alertas ligados ao canal ficam no ADR de alertas, e o menu de pedidos fica no ADR do Hanko Desktop.

## Alternativas descartadas
- **"Pedido externo"** (`external_request`): é mais fiel a "pedido do canal", mas perde a palavra que diz o que o pedido
  espera.
- **Script visível para qualquer pessoa com login**: deixaria a topologia do host aberta para quem entrar depois.
- **Aprovar pelo Hanko**: contraria a decisão 4 da #66; o Hanko passaria a rodar coisa no host.
- **Tirar o comando para copiar** (como diz o ADR-08:409): a migração segue o código (bloco 7 do gate da #67).

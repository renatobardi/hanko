# ADR-0080 — Rodada externa: participante, etapa da rodada, ação e ciclo
Data: 2026-10-25 · Status: proposta (aguarda aceite do Bardi) · Issue: #66 (v0.8) · Usa: ADR-0042, ADR-0070, ADR-0075, ADR-0076, ADR-0078

## Contexto
O agent-studio monta a rodada do swarm a partir dos eventos `oute.swarm.*` dos logs (`agent_studio/etapas.py:1-33@845764f`
do oute-agent). A rodada tem abertura, fechamento, repositório, número de sessões, nome amigável (`Brave_Otter`) e o
ciclo, a issue `<dono>/<repo>#<n>`. O worker é uma sessão da rodada (`state.py:11@845764f`). A etapa é o texto que o
dispatcher publica num ponto de parada (`oute.swarm.step.published`), conferido por um revisor de outro modelo. Os
tipos de etapa são `triagem`, `merge` (um por PR), `kaizen`, `fechamento` e `ciclo`. Cada etapa tem revisão (a mais alta
vence, em qualquer ordem de chegada), sha256, veredito (`aprovado`, `reprovado`, `sem-revisor`) e conferência de
referências (`ok`, `falhou`, `ausente`) (`state.py:34-51@845764f`). O texto vai até 32.768 bytes e é dado não confiável,
em Markdown restrito: seções `Decisão`, `Ações` e `Detalhe` (`etapas.py:13-16,41@845764f`).

A ação é uma linha `id: …` da seção `## Ações`. O id é do dispatcher e não muda entre revisões; só a revisão vigente
conta. Ação que cita `pedido:<id>` não tem caixa: o estado dela é o do pedido do canal (`acoes.py:1-15@845764f`). O
Bardi marca a ação como `feita` ou `pendente` em `action_marks`: só se acrescenta, a mais nova vence
(`marks.py:1-20@845764f`). O dispatcher lê essas marcas em `GET /v1/rodada?id=` com a credencial de leitura, como dado
e nunca como instrução (`docker/swarm.md:29@845764f`; `app.py:427-451@845764f`). A decisão pendente é a pergunta que o
dispatcher grava com `oute-swarm ask` e fecha com `answered` (`decisions.py:1-40@845764f`).

No Hanko, "etapa" já é o `Stage` dos pipelines e das etapas de projeto (ADR-0034, ADR-0048;
`internal/domain/pipelines.go:337`). Decisão 6 da #66 e os blocos 2, 3 e 5 do gate da #67
(`docs/research/2026-10-inventario-agent-studio.md` §9.2). As escolhas abaixo são do Bardi, em 2026-10-25.

## Decisão
1. **Nomes.** No código, em inglês (ADR-0078 item 4); na UI, em pt-BR:

   | Hoje | Código | UI |
   |---|---|---|
   | rodada | `external_round` | Rodada externa |
   | worker | `round_participant` | Participante |
   | ciclo | `round_cycle` | Ciclo |
   | etapa | `round_step` | Etapa da rodada (sempre qualificada) |
   | ação | `step_action` | Ação |

   Tipos de etapa: `triage`, `merge`, `kaizen`, `closing`, `cycle_summary`. Vereditos: `approved`, `rejected`,
   `no_reviewer`. Conferência: `ok`, `failed`, `missing`. Estado da ação: `done`, `pending`. A migração mapeia um a um
   e o ensaio registra o mapeamento para a conciliação.
2. **Nasce na ingestão.** A rodada externa, o participante e a etapa nascem dos eventos `oute.swarm.*` que chegam pela
   ingestão (ADR-0076), na mesma regra de `etapas.py@845764f`: entra toda rodada com abertura, e também a rodada com
   etapa publicada cuja abertura não chegou. O repositório da rodada liga a rodada ao workspace pelo emparelhamento
   (ADR-0075). O participante liga à sessão externa (ADR-0078).
3. **Etapa da rodada.** A revisão mais alta vence, em qualquer ordem de chegada; o texto fica no fato, nunca copiado
   para o estado. O texto é dado não confiável: o Markdown restrito se interpreta só em dados e o template escapa tudo.
   Etapa `rejected` ou `no_reviewer` sai com um aviso fixo do Hanko e o texto fechado. `cycle_summary` não é posição da
   barra da rodada; aparece no topo do ciclo.
4. **Ação e marca.** As regras de `acoes.py@845764f` valem como estão: só a revisão vigente conta, id repetido vale o
   primeiro, e a ação com `pedido:` não tem caixa (o estado vem da aprovação externa). A marca exige sessão owner ou
   admin da empresa com reautenticação recente (ADR-0070) e gera `activity_event`. As marcas só se acrescentam e a mais
   nova vence. As `action_marks` migram atribuídas ao Bardi, marcadas `migrated`.
5. **Leitura pelo dispatcher.** O dispatcher lê a rodada externa pela API do Hanko com a chave `read` do swarm, a mesma
   do `oute-swarm --budget` (ADR-0076 item 10). A marca segue dado, nunca instrução nem confirmação: a regra de
   `docker/swarm.md:29@845764f` continua no prompt do dispatcher, com o link novo.
6. **Pergunta da rodada.** A decisão pendente vira item da Caixa (ADR-0042), não aprovação externa (bloco 5 do gate da
   #67). A regra de pendente é a de `decisions.py@845764f`: rodada sem fechamento cuja última pergunta é igual ou mais
   nova que a última resposta, até o dobro de `lookback_hours`. O texto da pergunta é dado não confiável.
7. **Ciclo.** O ciclo agrupa as rodadas por issue, com a página do ciclo e o resumo `cycle_summary` no topo.
8. **Custo por rodada.** A API de uso do Hanko filtra por rodada externa (bloco 3 do gate da #67), para o teto de custo
   do modo dark (`config/swarm.toml:4-6@845764f`).

## Consequências
- "Etapa" sozinha, no Hanko, continua sendo o `Stage`; a etapa da rodada aparece sempre qualificada.
- O dispatcher e o `oute-swarm --budget` dividem uma chave: revogá-la corta as duas leituras juntas.
- A marca ganha autor de verdade, e a reautenticação passa a valer para marcar ação.
- O alerta `round_stalled` fica no ADR de alertas; o nome da rodada, no ADR do gerador único; as etapas no menu, no ADR
  do Hanko Desktop.

## Alternativas descartadas
- **Etapa da rodada como "relatório da rodada"** (`round_report`): some com a colisão, mas troca a palavra que o Bardi usa
  hoje.
- **Rodada externa como pipeline** do Hanko: o pipeline é trabalho que o Hanko conduz, com etapas e revisão humana
  (ADR-0034); a rodada só é observada pela telemetria.
- **Chave `read` própria para o dispatcher**: uma chave a mais no mesmo container, com o mesmo dano num vazamento.
- **Pergunta da rodada como aprovação externa**: a pergunta não tem script nem decisão aprovar/recusar.

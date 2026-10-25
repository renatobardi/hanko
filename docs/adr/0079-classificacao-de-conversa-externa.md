# ADR-0079 — Classificação de conversa externa, Jev no decision model e filtro de segredo
Data: 2026-10-25 · Status: proposta (aguarda aceite do Bardi) · Issue: #66 (v0.8) · Usa: ADR-0078 · Altera: ADR-0056

## Contexto
O agent-studio dá a cada conversa uma fase do AI-DLC (12 fases, `strat` a `iter`), com origem e confiança, por
classificação póstuma (`agent_studio/phase.py:1-60@845764f` do oute-agent). Os sinais valem nesta ordem, e o primeiro
com sinal decide: abertura do `oute-task`, label da issue, skill `oute-aidlc-<fase>` usada, papel (dispatcher → `plan`,
revisor → `qa`), ação por regras fixas sobre ferramenta, caminho e comando (só testados, nunca extraídos), e o Jev
com confiança ≥ 0,6. A troca manual vale sobre tudo. Sem sinal forte, a fase sai com confiança baixa; o padrão é
`build`, e o consumo fora de conversa é `ops`. A resposta do Jev e a troca manual são gravadas; o resto se refaz.

O Jev é chamado sozinho para a conversa parada há mais de 10 min, até 20 por passada, com 3 s de teto, até 16.000
caracteres do primeiro pedido, só por `https://`, nova tentativa em 600 s, e nunca com texto que tenha padrão de
segredo (`phase.py:41-45`; `jev.py:1-72,136-167@845764f`). O decision model do Hanko (ADR-0056 §A) tem um provedor só
(compatível com OpenAI), as features `triage`, `phase_classifier` e `name_summary`, e nenhuma roda sozinha (item 6).
No gate da #67, o Bardi decidiu que o Jev vira o segundo provedor e que a regressão é uma marca de origem
(`docs/research/2026-10-inventario-agent-studio.md` §9.1 pontos 5 e 8). O oute é um produto especializado em AI-DLC
(Bardi, 2026-10-25). As escolhas abaixo são do Bardi, em 2026-10-25.

## Decisão
1. **Classificação de conversa externa** é um conceito genérico do núcleo: cada conversa externa (ADR-0078) tem uma
   classe, a origem dela, a confiança (`high` ou `low`) e, quando houver, a troca manual, que vale sobre tudo. O
   núcleo não conhece fase nenhuma.
2. **Classificador `aidlc`, em código.** A regra de `phase.py@845764f` é portada como está, num classificador `aidlc`
   atrás de uma interface de classificador; a empresa o liga. A empresa Oute nasce com ele ligado. As classes são as
   12 fases do AI-DLC; o padrão sem sinal é `build`; o consumo fora de conversa é `ops`. Outro método, no futuro, é
   outro classificador.
3. **Identificadores em inglês** (ADR-0078 item 4). Origens: `opening`, `label`, `skill`, `role`, `action`,
   `decision_model`, `manual` (hoje `abertura`, `label`, `skill`, `papel`, `acao`, `jev`, `manual`). Confiança: `high`,
   `low` (hoje `alta`, `baixa`). Os ids das regras de ação também vão para o inglês. A migração mapeia um a um e o
   ensaio registra o mapeamento para a conciliação.
4. **Jev é o segundo provedor do decision model**, ao lado do compatível com OpenAI, com o primitivo de escolha que o
   oute já usa, só por `https://` e com a chave como `${secret:NOME}` (ADR-0056 item 2). A classificação usa a feature
   `phase_classifier`, com as classes do classificador no lugar das etapas do pipeline. A empresa Oute usa o Jev nela.
5. **Segundo plano, só para esta feature** (altera o item 6 do ADR-0056). Um job chama o decision model para a conversa
   externa que chegou ao passo `decision_model` sem sinal forte, com os limites de hoje: parada há mais de 10 min, até
   20 por passada, 3 s, até 16.000 caracteres do primeiro pedido, nova tentativa em 600 s, confiança mínima 0,6. A
   feature segue desligada por padrão; a Oute nasce ligada. Cada chamada vai ao `decision_invocations` com
   `actor_type = 'system'` e soma ao gasto da empresa (ADR-0056 item 5). As outras features continuam sem segundo plano.
6. **Filtro de segredo em todo o decision model**, com qualquer provedor e qualquer feature. Texto que bate com um dos
   10 padrões de `jev.py:53-64@845764f` não sai do Hanko: a invocação vira erro `secret_detected`, com só o tipo do
   padrão (`private_key`, `api_key`, `github_token`, `aws_key`, `slack_token`, `google_key`, `jwt`, `bearer`,
   `url_with_password`, `assignment`) no ledger e no `activity_event`, nunca o trecho. Na classificação, a conversa fica
   no passo anterior e não é perguntada de novo enquanto o texto não muda.
7. **Marcas de conversa** (`tags`). A conversa externa tem um conjunto de marcas curtas em inglês, separado da classe.
   O classificador `aidlc` põe `regression` quando o `oute.task.id` começa com `regression-` ou o repositório é
   `regression`. Custo e conversas filtram e agrupam por marca. No histórico, essas conversas levam também a
   atribuição inferida (ADR-0075 item 7).
8. **Migração.** A resposta do Jev gravada (`phase_jev`) e as trocas manuais (`phase_marks`) migram com a origem; a
   resposta do Jev não se repete igual, por isso não se recalcula. O resto da classificação se refaz pelo classificador
   e o ensaio confere 100% contra `conversation_phase`. A troca manual migra atribuída ao Bardi, marcada `migrated`.
   No ADR-08 do oute-agent, `phase_jev` aparece como "acréscimo", mas o código faz `INSERT OR REPLACE`
   (inventário §3.2): vale o código.

## Consequências
- O AI-DLC fica num lugar só do código, e o núcleo continua sem saber de fase.
- `triage` e `name_summary` passam a recusar texto com padrão de segredo; um texto que só parece segredo não é enviado,
  e o ledger mostra o tipo.
- O custo do Jev passa a aparecer no bloco "Decision model" do Custos da empresa Oute.
- Mudar a regra do `aidlc` muda a classe das conversas que se refazem; a resposta do Jev gravada e a troca manual
  ficam.

## Alternativas descartadas
- **Classificação genérica com as regras como dado da empresa**: mais código e um editor de regras que precisaria de
  validação, sem ganho para o oute, que é especializado em AI-DLC.
- **AI-DLC direto no núcleo**: contraria a decisão 6 da #66.
- **Classificador no próprio oute**: a classificação lê os spans de ferramenta da conversa inteira, que o oute não guarda
  fora do agent-studio.
- **Jev só sob pedido**: as conversas sem sinal forte ficariam com confiança baixa, uma função a menos que hoje.
- **Segundo plano como regra geral do decision model**: abre mais do que o v0.8 precisa.
- **Filtro de segredo só na classificação**: deixaria `triage` e `name_summary` mandarem segredo para fora.
- **Mascarar o segredo e seguir**: um padrão que não pega o segredo inteiro deixaria vazar o resto.
- **Coluna `is_regression`** ou **classe `regression`**: a primeira é do oute no núcleo; a segunda perde a fase.

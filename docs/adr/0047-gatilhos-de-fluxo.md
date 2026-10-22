# ADR-0047 — Variáveis e gatilhos de fluxo, revisões e arquivar
Data: 2026-10-21 · Status: aceita

## Contexto
O Fluxo (ADR-0004 e ADR-0005) é uma definição com agenda cron: a cada ocorrência cria uma task para uma persona. Faltam
três coisas que o plano pede (itens #22 e #23 de `docs/PLANO-DECISOES.md`, M19): o pedido gerado não tem parâmetros
(só `{{data}}` e `{{n}}`), o único gatilho é o relógio (não dá para um sistema externo nem um fato do próprio Hanko
disparar o fluxo) e não há histórico das mudanças da definição nem como aposentar um fluxo sem apagá-lo.

## Decisão

### A. Variáveis (#22)
1. **Declaração.** `flows.variables` (jsonb, lista): `{name, label, required, default}`. `name` casa
   `^[a-z][a-z0-9_]{0,39}$`, único no fluxo, no máximo 20 variáveis; `default` e valores têm no máximo 2000 caracteres.
   `label` é só para a tela. Os nomes `data`, `n`, `semana` e `disparo` são **built-ins** e não podem ser declarados.
2. **Uso.** `{{nome}}` (espaços dentro das chaves são aceitos) no título e na descrição da task gerada. Built-ins:
   `data` (AAAA-MM-DD da ocorrência no fuso do fluxo), `n` (número do disparo), `semana` (semana ISO, `2026-W43`) e
   `disparo` (origem: `agenda`, `manual`, `webhook` ou `evento`). Ao salvar, um marcador que não é built-in nem está
   declarado é recusado (`ErrInvalid`, campo `task_title`/`task_description`): erro de digitação aparece na hora, não
   no primeiro disparo.
3. **Substituição em uma passada só.** O valor entra como texto e **nunca é expandido de novo** (um valor
   `{{outra}}` ou `${secret:X}` chega à task literal). Marcador desconhecido na hora de renderizar (fluxo editado
   entre o disparo e a renderização não acontece: tudo é uma transação) fica como está.
4. **Resolução.** Valor informado e não vazio, senão o `default`, senão (se `required`) o disparo é recusado com
   `ErrInvalid` listando as faltantes. Valor para variável não declarada é recusado (manual) ou ignorado (webhook e
   evento, que recebem carga arbitrária). **A agenda exige que toda variável obrigatória tenha default**: o cron não
   tem quem preencha. O "Rodar agora" pede as obrigatórias (formulário na UI, `--var nome=valor` na CLI, `variables`
   na API). O valor efetivo de cada disparo fica em `flow_fires.variables` e aparece no histórico.

### B. Gatilhos (#23)
1. **`flow_triggers`** (migration 00150): `(id, company_id, flow_id, kind webhook|event, name, enabled, secret_ref,
   rate_per_min, event_verb, event_filter, mapping, created_at, updated_at, last_fired_at)`. Um fluxo pode ter vários
   gatilhos de qualquer tipo, além (ou no lugar) da agenda. **O cron passa a ser opcional**: `flows.cron = ''` significa
   "sem agenda" (sem `next_fire_at`); um fluxo sem agenda e sem gatilho só dispara no "Rodar agora".
2. **Webhook.** `POST /hooks/flows/{id-do-gatilho}`, sem login, autenticado por **HMAC**: o mesmo esquema dos webhooks de
   saída (ADR-0029), cabeçalho `X-Hanko-Signature: t=<unix>,v1=<hex>` com HMAC-SHA256 de `<t>.<corpo>` e tolerância de
   5 minutos. O segredo é `secret_ref = ${secret:NOME}` (ADR-0050): ao criar o gatilho o servidor **gera** o segredo
   (`hook-<fluxo>-<gatilho>`), devolve o valor uma vez e depois ele só se revê/gira em Segredos. A rota fica sob
   `/hooks/` (reservada no rate limit por IP do ADR-0058); além disso há **limite por gatilho** (`rate_per_min`, padrão 30,
   1–600, balde no Postgres). Corpo até 256 KiB.
   - **Idempotência.** `X-Hanko-Delivery` (ou `Idempotency-Key`) vira `dedupe_key`: a mesma entrega repetida devolve o
     disparo existente (200, `duplicate`) e não cria task.
   - **Carga → variáveis.** Corpo JSON objeto. `mapping` é `{variável: "caminho.pontuado"}`; variável sem mapeamento lê a
     chave de mesmo nome no topo. Número e booleano viram o texto JSON, objeto e lista viram JSON compacto (cortado em
     2000). Corpo que não é JSON objeto só vale se nenhuma variável for obrigatória.
   - **Respostas.** 202 `{fire_id, task_key}`; 200 duplicado; 401 assinatura ausente/inválida/vencida; 404 gatilho
     inexistente; 409 fluxo desativado ou arquivado; 413 corpo grande; 422 variável obrigatória faltando; 429 limite.
     Nada da resposta revela o segredo ou a definição do fluxo.
3. **Evento interno.** O gatilho assina um **verbo de atividade** (`task.created`, `gate.decided`, ou prefixo `gate.*`) com
   `event_filter` opcional (`{chave: valor}` sobre `data` do fato, comparação de texto). Avalia-se dentro de
   `txc.activity`, **na mesma transação do fato** (um fato confirmado dispara; um desfeito não), com
   `dedupe_key = <id da atividade>`. `mapping` lê `data.<caminho>` do fato, além de `subject_id`, `verb` e `actor`.
   - **Sem laço.** Fatos `flow.*` não disparam nada, e **enquanto um disparo cria a sua task, os fatos que ele mesmo
     gera não são reavaliados** (um fluxo "ao criar task, crie task" não se realimenta, nem em cadeia entre fluxos).
   - O limite por gatilho (`rate_per_min`) vale aqui também; o excedente é descartado e registrado como
     `flow.throttled` (sem task).
4. **Origem do disparo.** `flow_fires.source` (`schedule|manual|webhook|event`) e `trigger_id`. A chave única passa a
   `(flow_id, scheduled_for, source)` para a agenda e o manual (idempotência por minuto/ocorrência, como antes) e
   `(flow_id, dedupe_key)` para webhook/evento. Disparo por gatilho **não** move a agenda, **não** considera a task
   anterior aberta (cada carga é um trabalho) e o ator é `system`; o fato `flow.fired` leva `source` e `trigger_id`.

### C. Revisões e arquivar
1. **`flow_revisions`** (append-only, sem UPDATE/DELETE, no padrão do ADR-0052): `(flow_id, revision, snapshot, summary,
   autor, created_at)`. O snapshot é a definição (nome, descrição, agenda, fuso, persona, projeto, prioridade, modelos,
   variáveis, gatilhos **sem segredo**). Cada criação e cada edição que muda algo grava uma revisão na mesma transação,
   com `summary` = campos alterados. `flows.revision` é a vigente. Listar/ver: `GET …/flows/{f}/revisions`, tela do fluxo
   e `hanko flow revisions`. **Restaurar** uma revisão é uma edição nova que reaplica o snapshot (a história só cresce).
2. **Arquivar ≠ desativar.** `archived_at`: o fluxo arquivado some da lista padrão (`?archived=1` / aba "Arquivados"), não
   dispara por nenhum caminho (agenda, "Rodar agora", webhook → 409, evento), mantém tasks e disparos antigos e pode ser
   **desarquivado** (volta desativado, para ninguém ser surpreendido por disparos). Desativar continua sendo o botão de
   pausa de quem ainda quer ver o fluxo na lista. Fatos `flow.archived` e `flow.unarchived`.
3. **Fora de escopo, por decisão do plano:** sobrescrever persona/projeto no "Rodar agora".

### D. Superfícies
Service (`CreateFlow`/`UpdateFlow` com variáveis; `CreateFlowTrigger`, `UpdateFlowTrigger`, `DeleteFlowTrigger`,
`ListFlowTriggers`, `FireFlowWebhook`, `ArchiveFlow`, `FlowRevisions`, `RestoreFlowRevision`); API REST
(`/flows/{f}/triggers`, `/revisions`, `/archive`, `/unarchive`, `/run` com `variables`); CLI (`hanko flow trigger
add|ls|rm`, `flow revisions|restore|archive|unarchive`, `flow run --var`); UI (editor de variáveis, gatilhos, revisões,
arquivar, formulário do "Rodar agora"). OTel: contador `hanko.flow.fires` por origem e resultado e span
`hanko.flow.fire`. Exportação/importação levam as variáveis e o estado de arquivado; **gatilhos e segredos não viajam**
(são da instalação).

## Consequências
- O webhook de entrada é a primeira rota pública que cria trabalho: a defesa é HMAC + janela de tempo + rate limit por IP
  e por gatilho + corpo limitado + segredo fora do banco em claro. Quem tem o segredo pode criar tasks naquele fluxo,
  nada além disso.
- Evento na mesma transação acopla o custo do disparo à escrita que o causou; por isso a avaliação é uma consulta
  indexada (`enabled`, `kind='event'`, empresa) e o excedente é descartado, não enfileirado.
- A mudança do cron para opcional é retrocompatível: fluxos existentes têm cron preenchido.
- Dependência Go nova: nenhuma.

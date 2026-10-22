# ADR-0046 — Canais externos: Telegram e GitHub
Data: 2026-10-21 · Status: aceita

## Contexto
Hoje a conversa e as decisões do Hanko só acontecem na UI, na CLI e na API. Quem aprova gates precisa abrir o
painel; um comentário num pull request não chega a nenhuma persona. O plano pede (item #42, M19) uma camada de **canais**
com entrada e saída, vínculo de identidade, anexos e estado de entrega, mais dois drivers: Telegram (conversar e decidir
gates) e GitHub (um comentário no PR ou na issue acorda a persona; ela publica a review). As **respostas rápidas** do
decision model (#27, ADR-0056), adiadas "para junto dos canais", entram aqui. Outros canais ficam como plugin (ADR-0038).

## Decisão

### A. Modelo (migration 00151)
- **`channels`**: `(id, company_id, kind telegram|github, name, enabled, persona_id, project_id, token_ref,
  webhook_secret_ref, config jsonb, inbound_token, created_at, updated_at)`. `token_ref` e `webhook_secret_ref` são
  referências `${secret:NOME}` (ADR-0050): o valor nunca é guardado no canal, nem aparece em atividade, log ou erro; as
  referências são validadas ao salvar e resolvidas a cada uso. `persona_id` é a persona que conversa/é acordada.
  `config` não tem segredo: Telegram `{api_base?}`; GitHub `{repos:["dono/repo","dono/*"], mention:"@hanko",
  api_base?}`. `inbound_token` é o identificador opaco da URL de entrada (não é segredo de autenticação).
- **`channel_identities`**: vínculo entre uma pessoa do canal (`external_id`: id do usuário no Telegram, login no GitHub)
  e um **usuário do Hanko** (`user_id`). Estados `pending|linked|revoked`. O vínculo nasce **no Hanko**: a pessoa
  logada pede um código (`hanko channel link`, botão na tela do canal; vale 15 min, guardado só como hash) e o prova no
  canal (`/vincular CÓDIGO` no Telegram; `/hanko vincular CÓDIGO` num comentário do GitHub). Sem vínculo, o canal **não faz
  nada**: nenhuma decisão, nenhuma task, nenhuma resposta. Uma pessoa tem no máximo um vínculo ativo por canal; revogar
  (`hanko channel unlink`) vale na hora.
- **`channel_threads`**: liga um interlocutor (`peer`: chat do Telegram, `dono/repo#N` do GitHub) a uma **conversa** (Telegram)
  ou a uma **task** (GitHub), para a continuidade e para saber para onde espelhar a resposta.
- **`channel_messages`**: toda mensagem que entra (`in`) ou sai (`out`) com `status`, `attempts`, `last_error`,
  `next_attempt_at`, `external_id` e o id do provedor. Entrada é idempotente por `(channel_id, direction, external_id)`
  (`update_id` do Telegram, `X-GitHub-Delivery`). **Estado de entrega** (saída): `pending → sent | failed`; `failed` é
  re-enviável (`hanko channel resend`, botão na tela). A entrega é **pelo menos uma vez** (job `channel_send` da fila,
  retry com backoff de 30 s a 2 h, 6 tentativas); um envio aceito cujo registro falhou pode duplicar e isso é aceito.

### B. Camada `internal/channel`
Interface `Driver` pura (sem banco): `Kind`, `Verify(headers, body, secret)` (autentica o que chega), `Parse(headers, body)`
(devolve `[]Inbound` normalizadas: texto, comando, clique de botão, anexos, interlocutor, remetente), `Send(ctx, cfg, token,
Outbound)`, `Fetch(ctx, cfg, token, ref)` (baixa um anexo) e `Register(ctx, cfg, token, url, secret)`. O `service` orquestra
(identidade, autorização, conversa, task, entrega); o driver só fala o protocolo. Os dois drivers falam HTTP com o cliente do
`netguard` (destino interno só com `integrations.allow_private`); `api_base` do canal permite espelho/GitHub Enterprise e é
o que os testes apontam para `httptest`. Dependência Go nova: nenhuma.

### C. Entrada
`POST /hooks/channels/{inbound_token}` (sob `/hooks/`, com o rate limit por IP). O handler só **autentica, deduplica e
grava**: Telegram confere `X-Telegram-Bot-Api-Secret-Token` (tempo constante); GitHub confere `X-Hub-Signature-256`
(HMAC-SHA256 do corpo). Grava `channel_messages` (`in`) e enfileira o job `channel_inbound`; responde 200 logo (o provedor não
espera o modelo nem a persona). Corpo até 2 MiB. Canal desligado ou inexistente: 404; autenticação falhou: 401.

### D. Telegram (conversar e decidir)
1. **Conectar**: `setWebhook` com a URL pública e o `secret_token` (`hanko channel connect`); sem `HANKO_PUBLIC_URL`
   a tela mostra a URL para registrar à mão.
2. **Comandos** (pt-BR): `/vincular CÓDIGO`, `/desvincular`, `/pendentes` (gates que a pessoa pode decidir), `/novo`
   (recomeça a conversa), `/ajuda`.
3. **Gates**: ao ser pedido, um gate vira uma mensagem para cada pessoa vinculada **que pode decidi-lo** (papel e
   audiência do ADR-0041), com botões Aprovar / Rejeitar / Abrir. O clique chega como `callback_query` e executa
   **`DecideGate` como o usuário vinculado**, com o mesmo `require`/audiência de qualquer superfície (um membro removido
   ou sem papel recebe "sem permissão"). Gate já decidido responde "já decidido". Gates `question`, `plan` e os que pedem
   nota (mudanças pedidas) mostram só o resumo e o link: decidir exige a UI.
4. **Conversar**: texto livre de uma pessoa vinculada abre (ou continua) a **conversa direta** com a persona do canal,
   como ela, e a resposta da persona é espelhada de volta ao chat. Anexos (foto/documento até a cota de anexos do
   ADR-0052) são baixados e viram **anexos da mensagem**; arquivos que a persona deixa na conversa saem como documento.
5. **Respostas rápidas (decision model, #27)**: nova feature `fast_response`. Antes de acordar a persona, se a feature está
   ligada, o decision model recebe a mensagem e um retrato curto da empresa (gates pendentes da pessoa, contagem de tasks)
   e devolve `{"action":"reply"|"escalate","reply":"…"}`. `reply` é enviado direto (custo no ledger, ator `system`);
   `escalate` (ou qualquer falha, desligado, sem decision model) segue para a persona. **O modelo nunca decide nada**:
   não existe ação de aprovar/rejeitar na saída; decisão só por botão ou UI.

### E. GitHub (acordar e publicar review)
1. **Entrada**: eventos `issue_comment` (`created`, em PR ou issue), `pull_request_review_comment` e `pull_request_review`
   (`submitted`). Só repositórios da lista `repos` do canal; o remetente precisa ter vínculo (login → usuário); bots e o
   próprio autor do token são ignorados (sem laço); o comentário precisa **citar** `mention` (padrão `@hanko`) ou começar
   com `/hanko`. Qualquer outra coisa é registrada como `ignored` (com o motivo) e some.
2. **Efeito**: o primeiro comentário de um PR/issue cria uma **task** atribuída à persona do canal (título
   `dono/repo#N: <título>`, descrição com o texto, a URL e o autor; autor da task = o usuário vinculado) e a acorda; os
   seguintes entram como **comentário humano** na mesma task (que acorda a persona). Task fechada: o próximo comentário abre
   outra e a thread passa a apontar para ela.
3. **Saída**: o resumo que a persona deixa na task é publicado como comentário no PR/issue
   (`POST /repos/{dono}/{repo}/issues/{n}/comments`). **Review**: `PublishChannelReview` (`POST …/pulls/{n}/reviews`,
   `event` ∈ `COMMENT|APPROVE|REQUEST_CHANGES`, corpo obrigatório) — `hanko channel review`, API e tool MCP
   `publish_review` para a persona do canal. Só em canal GitHub, só na thread do PR, nunca em repositório fora da lista.
   Aprovar um PR é ato de pessoa: `APPROVE` exige ator humano vinculado; a persona publica `COMMENT` ou `REQUEST_CHANGES`.
4. **Webhook do repositório**: o GitHub é configurado pelo admin (URL e segredo mostrados na tela); o Hanko não registra
   webhook sozinho (exigiria escopo de administração do repositório).

### F. Segurança e operação
- Toda escrita gera `activity_event` na mesma transação: `channel.created|updated|enabled|disabled|deleted`,
  `channel.identity_linked|identity_revoked`, `channel.message_received|message_sent|message_failed`, `channel.reply_fast`.
- Quem administra canais é **admin**; vincular é do próprio usuário (member). Persona nunca cria nem altera canais.
- Mensagens de entrada têm tamanho cortado (8000 caracteres) e entram como **dado**: nunca viram instrução do sistema;
  o texto do usuário vai à persona como mensagem de chat/comentário comum.
- OTel: spans `hanko.channel.ingest|send|process`, contadores `hanko.channel.inbound`/`outbound` por driver e resultado.
- Telas: Configurações › Canais (lista, criar, ligar/desligar, vincular, conectar, mensagens com estado de entrega e
  reenviar). CLI: `hanko channel …`. API: `/companies/{c}/channels…`.

## Consequências
- Telegram e GitHub passam a ser superfícies de decisão e de pedido com a mesma autorização do resto; nenhum atalho.
- Sem fila de saída própria: reaproveita a fila Postgres (River) com o mesmo padrão dos webhooks de saída.
- Anexos no GitHub não existem (só link); Telegram aceita arquivos até a cota. Limitação registrada.
- Outros canais (e-mail, Slack…) entram como plugin sobre os mesmos fatos de atividade (ADR-0038), não nesta camada.

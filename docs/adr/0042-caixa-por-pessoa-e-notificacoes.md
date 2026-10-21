# ADR-0042 — Caixa por pessoa, notificações, visões "Minhas" e metas em árvore
Data: 2026-10-17 · Status: aceita

## Contexto
A Caixa de entrada (ADR-0020, ADR-0028, ADR-0032, ADR-0040) é uma fotografia da **empresa**: todo humano vê os mesmos
itens e nada some até a condição acabar. Com várias pessoas na mesma empresa (ADR-0021) isso não serve: um gate que
a Ana já viu continua gritando para o Bruno, uma falha que alguém já entendeu não tem como sair da frente, e quem é
citado num comentário não recebe nada. Também faltam visões por pessoa ("o que é meu"), a busca única ainda não acha
metas, projetos, gates e artefatos, a atividade não filtra por ator, e as metas (ADR-0028) são uma lista plana. Este ADR
cobre os itens #30, #31 e #32 de `docs/PLANO-DECISOES.md` (M16a).

## Decisão

### A. Caixa por pessoa (#30)
1. **Os itens seguem derivados; o estado é da pessoa.** Nada muda na forma de montar a Caixa. A migration 00120 cria
   `inbox_state (company_id, user_id, item_kind, item_id, fingerprint, read_at, archived_at, updated_at)`. Tipos de item:
   `gate`, `task_blocked`, `task_held`, `run_failed`, `persona_error`, `alert`, `subscription`, `mention`,
   `goal_achieved`. `item_id` é o id da linha de origem, sem FK (a origem vive em tabelas diferentes e o item some quando a
   condição acaba). O estado nunca fecha, aprova ou muda a coisa de origem: **arquivar limpa a Caixa de quem arquivou, não a
   task** (o gate continua pendente para os outros, a task continua bloqueada).
2. **Reaparece se muda.** A impressão digital (`fingerprint`) é o "estado do item" no instante em que a pessoa o marcou
   (gate: quando foi pedido; task bloqueada/pausada: última mudança ou instante da pausa; run falha: status; persona
   com erro: nº de falhas seguidas + instante da última; alerta: severidade + abertura; assinatura: semana; meta:
   `achieved_at`). Se a impressão atual difere da guardada, o estado guardado é ignorado e o item volta como **não lido e
   não arquivado**. Um item novo (outro id) já nasce não lido.
3. **Visões.** `Inbox` ganha as visões `all` (padrão: tudo que não está arquivado), `unread` (não lidos e não arquivados)
   e `archived`. `Inbox.Count()` conta os itens **não arquivados** que exigem ação (o número do cabeçalho), e a nova
   `Inbox.Unread` conta os não lidos (o selo do menu lateral, carregado por htmx, para não pesar em toda página). Marcar:
   `read`, `unread`, `archive`, `unarchive` por item e `read-all` (todos os itens visíveis agora). Sem usuário
   autenticado (modo trusted) não há estado: tudo aparece como não lido e marcar é `ErrInvalid` ("exige uma pessoa
   autenticada"), como os segredos de usuário (ADR-0050).
4. **Fato de atividade.** `inbox.archived`, `inbox.unarchived` e `inbox.read_all` entram na mesma transação (ator = a
   pessoa). Marcar **um** item como lido/não lido **não** gera fato: é estado de navegação pessoal (como a sessão),
   frequente e sem valor para a trilha da empresa. A tabela é podada na escrita (linhas sem atividade há mais de 180 dias).
5. **Menções.** `@handle` num comentário de task (a análise é a `domain.ParseMentions` da sala, ADR-0033: ignora e-mail,
   caminho e trecho de código). Resolução: o handle é primeiro o **slug de uma persona** da empresa; senão o **handle de um
   membro**, que é a parte local do e-mail em minúsculas com tudo fora de `[a-z0-9]` virando `-` (`renato.bardi@…` →
   `@renato-bardi`). Handle de membro ambíguo (dois membros com o mesmo) não menciona ninguém. A própria pessoa não se
   menciona. No máximo 10 alvos por comentário. Tabela `comment_mentions`
   (uma linha por comentário e alvo, mesma transação do comentário, fato `comment.mentioned`).
   - **Humano citado → Caixa** dele (item `mention`, 30 dias) + aviso (toast e push, conforme preferência).
   - **Persona citada → wake** (gatilho `comment`) na task, mesmo que não seja a responsável. Só quando o **autor do
     comentário é humano**: persona citando persona não acorda ninguém (sem laço de ping-pong; delegação entre personas
     continua sendo criar/atribuir task, ADR-0040). Task de sistema, pausada ou bloqueada seguem as regras do wake.
     A persona citada **não precisa ser a responsável**: o despacho (`CheckoutRun`) aceita o wake de comentário de uma
     persona com menção humana nos últimos 15 minutos, abre uma run dela na task **sem mexer no status nem na
     atribuição** (o briefing diz que a task é de outra persona e que ela só responde ao pedido). Resumo de run de persona
     também passa pela análise de menções.
   - Persona que cita humano (comentário de persona) cria o item na Caixa do humano; é a forma de "pedir atenção".
6. **Avisos ao vivo (toast).** O broker ganha o tópico por usuário (`user:<empresa>:<usuário>`). A UI abre
   `GET /c/{empresa}/caixa/eventos` (SSE; o acesso à empresa é reverificado periodicamente e o stream acaba quando
   cai; sem pessoa autenticada responde 204) e mostra um toast no canto, atualiza o selo e, na Caixa, oferece recarregar. Só chega à pessoa o que é dela: cada aviso é publicado
   para os usuários-alvo, nunca no tópico da empresa.
7. **Web push no PWA.** Pacote novo `internal/webpush`, **sem dependência nova** (a regra do ADR-0011): VAPID (RFC 8292,
   JWT ES256 com `crypto/ecdsa`) e criptografia de mensagem `aes128gcm` (RFC 8188/8291, ECDH P-256 + HKDF com a
   stdlib). Motivo de não adotar uma biblioteca: são ~250 linhas, testáveis de ponta a ponta (o teste faz de serviço de push
   com `httptest`, decifra a mensagem com a chave da assinatura e confere o JWT), e a superfície de rede fica sob o
   `netguard` do projeto.
   - **Chaves VAPID geradas em runtime** na primeira necessidade e guardadas em `instance_secrets` (migration 00121),
     cifradas com o `secretbox` (`HANKO_SECRET_KEY`, AAD `instance_secret/vapid`); a chave pública vai à UI para
     `pushManager.subscribe`. **Sem `HANKO_SECRET_KEY` o web push fica desligado** (a tela explica; toast e Caixa seguem).
   - **Assinaturas** `push_subscriptions` (por usuário, não por empresa: valem para o aparelho; sem `company_id`, como
     `sessions`). `POST/DELETE` pela API; o endpoint é único (reassinar por outro usuário muda de dono). Resposta 404/410 do serviço
     de push apaga a assinatura; 5 falhas seguidas (rede, 5xx) também.
   - **Preferências** `notification_prefs` por empresa, usuário e tipo (`mention`, `gate`, `run_failed`, `alert`), cada um
     com `toast` e `push`. Padrões: toast ligado em todos; push ligado em `mention` e `gate`.
   - **Entrega** best-effort e assíncrona, **depois do commit** (fila em memória do processo, sem job durável: um push
     perdido nunca desfaz nem atrasa a escrita; o item continua na Caixa). O texto é curto e sem conteúdo sensível (título do
     aviso e a chave da task; trecho do comentário só em `mention`, cortado em 100 caracteres). Métrica
     `hanko.notifications` (por tipo, canal e resultado) e span `hanko.notify.dispatch`.
   - **Contato VAPID**: `HANKO_PUSH_SUBJECT` (`mailto:` ou URL https); vazio, vale o e-mail do dono
     (`HANKO_AUTH_OWNER_EMAIL`) ou a `HANKO_PUBLIC_URL` https. O endpoint de cada assinatura é validado como o de webhook
     (https, sem rede interna; `netguard`), e o envio usa o cliente com proteção SSRF.
   - **Interoperabilidade não testada contra serviço real:** a criptografia e o VAPID têm testes de ida e volta contra um
     serviço falso que decifra com a chave da assinatura (RFC 8291 §4), mas nenhum push foi enviado ao FCM/Mozilla/Apple
     (sem rede nesta entrega). Validar uma vez no hanko-prd com um aparelho real (docs/validacao/m16a.md).
   - **Service worker** ganha os handlers `push` e `notificationclick` (abre a URL do aviso, só caminho relativo).
   - **Testes sem rede:** o serviço de push é um `httptest.Server`; `internal/webpush.Sender` recebe o cliente HTTP.
8. **Superfícies.** API `/companies/{c}/inbox` (lista com `view`), `…/inbox/state` (POST `action` = read|unread|archive|
   unarchive|read_all), `/me/notifications` (preferências por empresa), `/push/vapid-key`, `/me/push-subscriptions`;
   CLI `hanko inbox list|read|unread|archive|unarchive|read-all` e `hanko notify prefs|set`; UI: abas **Não lidos** e
   **Arquivados** na Caixa, botões por item, selo no menu, Configurações › Notificações.

### B. Visões por participação (#31)
1. **"Minhas" = participação.** Uma task é minha quando eu: **criei**; **atribuí** (fato `task.assigned` com meu ator, é a
   "responsabilidade" que um humano tem sobre o trabalho delegado, já que quem executa é a persona); fui **mencionado** nela;
   **comentei**; ou sou da **audiência de um gate** dela (pedi o gate, ou decidi, ou ele está pendente numa task em que já
   participo por um dos motivos anteriores). A consulta devolve os motivos junto (`participation`), a lista de tasks mostra
   a razão em chips e `?minhas=1` / `--mine` / `mine=true` filtram. Sem usuário autenticado, "Minhas" é vazio.
   Gates têm o mesmo filtro (`mine`): os da audiência acima, mais os sem task que eu pedi ou decidi.
2. **Busca cobre metas, projetos, gates, artefatos e fluxos.** Fluxos já eram (ADR-0024). A migration 00123 acrescenta
   `search_tsv` GENERATED + GIN em `goals`, `projects`, `gates` e `artifacts` (mesma `hanko_fold`, configuração `simple`,
   texto cortado em 100 000). Os tipos novos (`goal`, `project`, `gate`, `artifact`) entram em `SearchTypes` e em todas
   as superfícies da busca (service, API `types=`, CLI `--type`, MCP, UI), com ranking e trecho como os demais. Artefato:
   só a versão mais recente da cadeia aparece. Nenhuma busca nova: é a mesma `Service.Search`.
3. **Atividade por ator.** `ActivityFilter` ganha `ActorType` e `ActorID` (migration 00124: índice
   `(company_id, actor_type, actor_id, occurred_at DESC)`). `Service.ResolveActor` converte a referência
   `system` | `persona:<slug|id>` | `human:<e-mail|id>` (ou só `persona`/`human`, para todos do tipo; pessoa só entre os membros
   da empresa). API `GET …/activity?actor=…`, CLI `hanko activity --actor …`, UI `/atividade?ator=…` (seletor com o
   sistema, as pessoas e as personas).

### C. Metas em árvore (#32)
1. **Modelo** (migration 00122). `goals.parent_id` com FK composta `(company_id, id)` (pai de outra empresa é impossível),
   `parent_id <> id` e o trigger `goals_parent_guard`, que recusa **ciclo** e profundidade acima de **4 níveis** (raiz = 1,
   somando a subárvore de quem muda de pai), sob lock por empresa. O service repete a regra em Go (`domain.GoalTree`) para dar
   a mensagem certa; o trigger é a garantia contra corrida (a violação é mapeada para erro de validação).
2. **Progresso do pai = agregado.** Meta com ao menos um filho **não abandonado** tem progresso = **média simples** do
   progresso dos filhos (cada um já agregado, recursivamente); filhos abandonados não contam, e uma meta cujos filhos
   estão todos abandonados volta a valer o próprio valor. Meta sem filhos vale (atual − inicial) / (alvo − inicial) como
   antes. Peso igual por filho é uma escolha deliberada: peso configurável seria outra decisão.
3. **Pai não recebe progresso direto** (422 "tem submetas: o progresso é a soma delas"). A meta-pai **vira atingida** na
   mesma transação em que o agregado chega a 100% (todos os filhos não abandonados atingidos; fatos `goal.achieved`) e
   **volta a ativa** se um filho é reaberto, ou um filho novo chega, e o agregado cai abaixo de 100%. A propagação sobe a
   cadeia até a raiz.
4. **Briefing** da run inclui, para cada meta listada, a **cadeia até a raiz** (`Visão › Meta › Submeta`) e o progresso
   agregado.
5. **UI** em árvore (Metas): raízes com submetas indentadas, progresso agregado, seletor de "meta-pai" no formulário
   (só candidatas válidas), cadeia no detalhe. API `parent` / `clear_parent` no create e no PATCH, `children` e `path`
   na view; CLI `hanko goal create --parent`, `goal update --parent|--clear-parent`, `goal list` com recuo por nível e `goal show`
   com a cadeia e as submetas; MCP `hanko_goal_list` mostra pai, cadeia e `rollup`.

## Consequências
- Cada pessoa tem a própria Caixa sem duplicar dados: o custo é uma tabela pequena (`inbox_state`) e um JOIN em memória.
- Push depende de `HANKO_SECRET_KEY` e de o navegador aceitar a permissão; sem eles o toast e a Caixa continuam
  funcionando. A fila de push é do processo (reiniciar perde avisos em voo): aceitável porque nunca é a única forma de saber.
- "Responsável" para um humano é "quem atribuiu"; se um dia tasks tiverem responsável humano explícito, entra como mais um
  motivo de participação.
- Pai de meta não recebe progresso manual; quem queria medir o "todo" direto cria uma submeta para isso.
- Fica para depois: menção em outros textos (descrição, chat, sala já tem a sua), digest por e-mail, pesos por submeta,
  desfazer "marcar tudo como lido".

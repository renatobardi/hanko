# ADR-0043 — Conexões OAuth para servidores MCP remotos
Data: 2026-10-17 · Status: aceita

## Contexto
As integrações MCP http (ADR-0029, ADR-0036) autenticam com um header fixo que o admin cola (`Authorization: Bearer ...`),
guardado como segredo (ADR-0050). Isso não serve aos servidores MCP remotos que seguem a especificação de autorização do MCP
(OAuth 2.1 com PKCE): o token expira, é emitido para uma pessoa depois de um consentimento no navegador e precisa ser renovado.
O item #38 do plano pede: OAuth 2.1 + PKCE com descoberta de metadados e registro dinâmico de cliente, token cifrado com
renovação, identidade da empresa ou pessoal, probe de autenticação, setup recuperável (rascunho, "concluir depois", "reconectar") e
um gate `kind=connection` que retoma a task. Catálogo de apps ("lojinha") fica fora.

## Decisão

### 1. Conexão é uma camada sobre a integração MCP
`connections` (migration 00130) é a linha de **autorização** de um servidor MCP http. Ao criar a conexão o service cria também a
**integração MCP** correspondente (ADR-0029), **desligada**, com o header `Authorization: Bearer ${secret:conn.<slug>.access}`. Tudo
o que já existe para integrações vale sem mudança: catálogo de tools e quarentena, perfis, política off/ask/allow (ADR-0053),
auditoria, gateway. A conexão só cuida de **obter, guardar e renovar** o token. Nada de duplicar tabelas de tools.

### 2. Estados (setup recuperável)
`draft` (criada, sem autorização — "concluir depois") → `pending_auth` (URL de autorização emitida, state e verifier guardados,
validade de 15 min) → `connected` → `expired` (renovação falhou por token vencido sem refresh válido) / `error` (probe ou
renovação com falha de rede ou servidor) / `revoked` (o servidor respondeu `invalid_grant`: só uma nova autorização resolve).
`draft`, `expired`, `error` e `revoked` reabrem com **Autorizar/Reconectar**; `Concluir depois` é simplesmente deixar em
`draft`/`pending_auth` (a tela lista o que está incompleto e o passo seguinte). Desconectar apaga os segredos de token, volta a
`draft` e desliga a integração.

### 3. Protocolo (internal/oauth, só stdlib)
- **Descoberta.** Requisição sem credencial ao servidor MCP; o `WWW-Authenticate: Bearer resource_metadata=` do 401 (ou
  `/.well-known/oauth-protected-resource`, RFC 9728) aponta o servidor de autorização; seus metadados vêm de
  `/.well-known/oauth-authorization-server` (RFC 8414) com `openid-configuration` como alternativa; sem metadados, os endpoints
  padrão da origem (`/authorize`, `/token`, `/register`), como manda a especificação do MCP.
- **PKCE S256 obrigatório.** Se o servidor anuncia métodos e não inclui `S256`, a conexão recusa (`ErrNoPKCE`). `code_verifier` de
  32 bytes aleatórios; `state` de 32 bytes, guardado só como sha256 (`auth_state_hash`, único) — o state é o que liga o retorno do
  navegador à conexão. O verifier fica cifrado (secretbox, AAD `conn/<id>`) e é apagado ao consumir.
- **Registro dinâmico (RFC 7591)** como cliente **público** (`token_endpoint_auth_method=none`); o `client_id` fica na conexão.
  Sem `registration_endpoint`, o admin informa `client_id` (e, se o servidor exigir, o segredo vai como segredo `${secret:...}`).
- **`resource` (RFC 8707)** no pedido de autorização, na troca e na renovação.
- **Troca e renovação** por `POST` form no `token_endpoint`. `invalid_grant` na renovação → `revoked`. Resposta de token com
  `token_type` diferente de Bearer é recusada. Respostas lidas com teto de 1 MiB.
- **SSRF.** Todo endpoint (MCP, metadados, registro, token) passa por `netguard.ValidateURL` e o cliente HTTP é o de `netguard`
  (checagem do IP na conexão). `HANKO_INTEGRATIONS_ALLOW_PRIVATE` libera destinos internos, como nas integrações. `authorization_endpoint`
  não é chamado pelo servidor: é para onde o navegador do humano vai.

### 4. Tokens são segredos (ADR-0050)
Access e refresh token ficam nos segredos `conn.<slug>.access` e `conn.<slug>.refresh`, cifrados (AES-256-GCM) e versionados; a
renovação rotaciona o segredo (versão sobe, `secret.rotated` na atividade). Eles nunca aparecem em API, UI, CLI, export ou log
(a conexão expõe só estado, validade e escopos). Sem `HANKO_SECRET_KEY` o servidor recusa conectar. A integração usa a referência
`${secret:...}`, então a renovação vale para o gateway na chamada seguinte, sem tocar na integração.

### 5. Identidade
`identity=company` (padrão, admin): o token é segredo da empresa e **personas o usam** através do gateway de tools.
`identity=user` (qualquer membro, só para si): o token é segredo **de usuário** (`${secret:user/...}`), que ninguém mais lê. Uma
run de persona não tem identidade humana, então não resolve segredo de usuário (ADR-0050): a conexão pessoal serve a **ações do
próprio dono** (probe, teste de tool) e não liga a integração para personas. Documentado como limite; usar a pessoal para
agentes fica para depois, quando houver delegação explícita.

### 6. Renovação
- **Sob demanda:** o gateway (`toolEndpoint`) renova antes de abrir a integração se o token vence em menos de 2 minutos.
- **Periódica:** o `skill_tick` (5 min) também varre conexões `connected` que vencem em menos de 10 minutos (`RefreshDueConnections`).
- Uma renovação por vez: a conexão é reservada com `refreshing_until` (30 s) antes da chamada de rede, que roda fora de transação.
- Sem `expires_at` (servidor não informou), não há renovação preventiva; um 401 no probe rebaixa para `expired`.

### 7. Probe
`POST .../connections/{c}/probe` faz o `initialize` MCP com o token: 2xx → `connected` (e `last_probe_*`); 401 → tenta renovar uma
vez, depois `expired`; 403 → continua `connected` com aviso de escopo; rede/5xx → `error` sem perder o token.

### 8. Gate `kind=connection`
Uma persona pede uma conexão (`hanko_connection_request` no MCP, ou a API com run token) citando uma conexão existente (slug) ou
propondo nome e URL (nasce `draft`, criada pela persona; só um humano autoriza). Se a conexão já está `connected` e o probe passa,
nada é aberto. Senão abre-se **um** gate por conexão (idempotente): task em `blocked`, comentário e a persona livre para outras
tasks (o gate **não** é bloqueante da persona). O humano abre o gate, vê a URL e os escopos e escolhe **Autorizar** (OAuth no
navegador). Quando o retorno do navegador grava o token, o service **aprova o gate** e a task retoma (`in_progress`, persona
acordada com o gatilho `gate`). Aprovar o gate à mão só vale com a conexão `connected`; rejeitar funciona sempre (task volta a `todo`).
Migration 00130 recria `gates_kind_check` com a lista completa vigente mais `connection`.

### 9. Retorno do navegador
`GET /oauth/callback?code&state` (rota fixa da UI, fora de `/api`): exige **sessão** de quem iniciou (em modo trusted, sem
identidade, vale o state); procura a conexão pelo hash do state, confere validade e consome o state (uso único) antes de tocar a
rede. Erros do servidor (`error=access_denied`) viram `draft` com `last_error`. A resposta tem `Referrer-Policy: no-referrer` e o
`code` e o `state` nunca vão para log (o caminho é mascarado como o de `/pedir-acesso`).

### 10. Superfícies e papéis
API: `GET/POST /companies/{c}/connections`, `GET/PATCH/DELETE .../{id}`, `POST .../{id}/authorize` (devolve `authorization_url`),
`/probe`, `/disconnect`, `/refresh`; `POST /companies/{c}/connections/request` (persona). CLI: `hanko connection list|add|authorize|probe|refresh|disconnect|rm`. UI: aba
**Conexões** em Integrações (estado, validade, Autorizar/Reconectar, probe, passo seguinte do que está incompleto) e o botão
Autorizar no gate. Admin cria, autoriza (empresa), apaga; membro autoriza as suas pessoais; todos leem. Atividade:
`connection.created|authorize_started|connected|refreshed|refresh_failed|probed|disconnected|deleted`.
OTel: span `hanko.connection.*` e contador `hanko.connection.events` por `result`.

## Consequências
- Sem dependência Go nova. Tudo testado contra um servidor falso (`internal/oauth/oauthtest`) — nenhuma chamada real.
- O token vive em memória só durante a chamada; quem tem acesso ao banco e à `HANKO_SECRET_KEY` o lê, como qualquer segredo.
- Mudar o `client_id`/servidor exige reconectar (o token antigo não vale em outro cliente).
- Servidores que não suportam PKCE S256 ou registro dinâmico (sem `client_id` informado) não conectam; a mensagem diz qual passo falhou.
- A conexão pessoal não alimenta agentes (item 5). O gate cobre só OAuth; credencial estática segue sendo segredo + integração.

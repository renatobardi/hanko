# ADR-0036 — Apps, gateway de tools e perfis de ferramentas
Data: 2026-10-16 · Status: aceita · Alterada por: ADR-0067 (integração nova nasce com a escrita em quarentena)

## Contexto
As integrações MCP da empresa (ADR-0029) chegavam ao agente inteiras: a URL do servidor e os headers com segredo iam
no `mcpServers` da run, e a persona podia chamar qualquer tool que o servidor expusesse. Não havia como dizer "esta
persona só lê", "apagar repositório pede aprovação", nem saber depois o que foi chamado. Um servidor MCP também pode
mudar as tools que expõe sem aviso: uma tool nova que escreve passava a valer na próxima run. E clientes de fora da
instalação (uma IDE, um script) não tinham como usar as mesmas integrações com as mesmas regras.

## Decisão
1. **App = integração MCP.** Não há entidade nova: o "app" é a `integration` kind `mcp` que já existe. Migration 00032
   cria só o que falta em volta dela.
2. **Catálogo.** `integration_tool_catalogs` (estado da descoberta) e `integration_tools` (nome, título, descrição,
   schema de entrada, anotações, `schema_sha256`, risco, estado, primeira e última vez visto, quem revisou). O servidor
   descobre as tools com `tools/list` (cliente MCP do SDK em `internal/mcpupstream`, cada operação com a própria sessão),
   passando pelo `netguard` na conexão e respeitando `HANKO_INTEGRATIONS_ALLOW_PRIVATE`. O **risco** vem das anotações
   do protocolo: `readOnlyHint` → `read`; `destructiveHint=false` → `write`; `destructiveHint=true` (ou anotações sem
   ele, que pelo protocolo valem como destrutivas) → `destructive`; sem anotações → `unknown`. O hash cobre nome,
   título, descrição, schema e anotações. Schema que não é objeto `type: object`, ou maior que 128 KiB, vira o schema
   genérico; no máximo 500 tools por integração; nomes fora de `[A-Za-z0-9_.-]{1,128}` são ignorados.
3. **Quarentena.** A **primeira** descoberta é a linha de base: tudo entra `active` (o admin já confiou na integração ao
   criá-la, e assim nada muda para quem já usa). Depois dela, tool **nova** que não declara que só lê (`write`,
   `destructive` ou `unknown`) entra `quarantined`; tool existente cuja definição mudou (hash) e não é só leitura volta
   para `quarantined`; tool que sumiu vira `removed`; `disabled` (por admin) fica desligada. Revisar/liberar e desligar
   são de admin, com quem e quando registrados. A descoberta roda por admin (botão, API, CLI) e, pelo gateway, quando o
   catálogo nunca foi descoberto ou tem mais de 1 h (melhor esforço; uma falha mantém o catálogo anterior e não tenta de
   novo por 1 min).
4. **Perfis.** `tool_profiles` (nome, ação padrão `deny|allow` — padrão `deny` —, teto de chamadas por minuto, padrão
   120) e `tool_profile_rules`: alvo `integration` (a integração inteira), `tool` (uma tool) ou `risk` (um nível de
   risco, de todas as integrações ou de uma); efeito `include|exclude`; na inclusão, modo `allow` ou `ask`. **Excluir
   vence incluir.** Entre inclusões vale a mais específica: tool > risco de uma integração > integração > risco global;
   empatando, `ask` vence `allow`. Sem regra que alcance a tool, vale a ação padrão. Até 100 perfis por empresa e 500
   regras por perfil. O assistente da UI escreve, por integração, a regra do grupo e as regras por tool (Desligado =
   exclui; Perguntar = inclui com `ask`; Permitido = inclui com `allow`; Padrão = sem regra).
5. **Vínculos e resolução.** `tool_profile_bindings` liga um perfil a `company`, `project` ou `persona`, com prioridade
   (-1000..1000). Vale **um** perfil: o do escopo mais estreito (persona > projeto > empresa); no mesmo escopo, a maior
   prioridade e, empatando, o vínculo mais novo. O projeto é o da task da run (chat não tem projeto). **Sem perfil
   algum, o comportamento anterior continua:** todas as tools ativas das integrações da persona.
6. **Gateway da run.** As integrações MCP **http** de uma run não vão mais direto para o agente: o `mcpServers` aponta
   para `/mcp/run/{integração}` no Hanko, com o run token no `Authorization` (`RunExtras.ToolGateway`; o executor monta
   a entrada). Escolhemos um endpoint por integração em vez de um agregado: os nomes das tools ficam os originais e o
   agente vê cada servidor como antes. Fica sob `/mcp/` para valer a regra de autenticação que o guard já aplica ao
   MCP. O endpoint (JSON-RPC sem sessão, respostas JSON; `initialize`, `ping`, `tools/list`, `tools/call`) aceita só
   run token de run ativa e recusa as runs de sistema (revisão, curadoria, card de status, teste de skill), que já não
   recebem MCP da empresa. `tools/list` mostra as tools `active` permitidas pelo perfil. `tools/call` decide:
   - tool fora do catálogo, desligada, removida ou excluída pelo perfil → erro de parâmetro do protocolo (`-32602`);
   - em quarentena → resultado com `isError` explicando que um admin precisa revisar;
   - `ask` → **gate `tool`** (abaixo) e resultado com `isError`: "aguardando aprovação: gate X; repita a chamada com os
     mesmos argumentos depois da decisão";
   - teto de taxa por (persona, integração) atingido → `isError`;
   - senão, repassa ao servidor com os segredos guardados (secretbox), pelo `netguard`, e devolve o `CallToolResult`.
     A URL e os headers do servidor nunca chegam ao agente.
   Uma integração http cujo destino a política bloqueia gera um aviso no log da run (as chamadas vão falhar);
   quem integra com MCP interno precisa de `HANKO_INTEGRATIONS_ALLOW_PRIVATE=true`, como os webhooks.
7. **MCP stdio** roda no harness e não passa pelo gateway: o perfil só inclui ou exclui a integração inteira (regras de
   tool e de risco não valem para ela, e `ask` não tem como valer — uma inclusão com `ask` é recusada no service e, se
   houver, deixa o stdio de fora). Um stdio que o perfil não inclui sai do `mcpServers` com aviso no log da run.
8. **Gate `tool` e aprovação de uso único.** `GateTool` entra em `domain` (Valid; não é bloqueante: a persona não fica
   em `awaiting_gate` e a task não muda) e na Caixa de entrada (aba "Tools") e em Gates (filtro "Tools"). O gate nasce
   sem task (as rotinas de gates de task não o tocam) e com a run, a persona (ou nenhuma, no gateway nomeado), o risco,
   o sha256 e os argumentos **redigidos** (padrões de credencial e os segredos da integração mascarados, até 8 KiB) para
   o humano decidir o que está aprovando. `tool_call_approvals` guarda a chave: integração, tool, **sha256 de
   integração + tool + argumentos em JSON canônico** e o chamador (persona ou gateway). Um advisory lock por chave
   serializa pedidos concorrentes. Com a chamada repetida: gate pendente → a mesma resposta, sem gate novo; aprovado e
   não consumido → **passa uma vez** (consome); rejeitado ou com mudanças pedidas → recusado; consumido ou expirado →
   gate novo. Argumentos diferentes = sha256 diferente = nova aprovação. A aprovação vale por 7 dias. Decidir é como
   qualquer gate (member; trilha de auditoria; vira memória), e aprovar acorda a persona na task da run que pediu
   (gatilho `gate`) para ela repetir a chamada.
9. **Gateway nomeado.** `tool_gateways` (slug, nome, perfil obrigatório, ligado) e `tool_gateway_tokens` (sha256 do
   token, prefixo `hkgw_` + 6 visível, rótulo, ações ⊆ `tools/list|tools/call`, expiração obrigatória — padrão 90 dias,
   de 1 h a 366 dias —, último uso com resolução de 1 min, revogação). Endpoint `/mcp/gw/{empresa}/{gateway}` com
   `Authorization: Bearer hkgw_…`: agrega as integrações MCP http ligadas da empresa com o nome
   `<integração>__<tool>`, aplica o perfil do gateway (a mesma decisão da run) e respeita as ações do token. `ask` abre
   o gate em nome do gateway (ator sistema, sem persona). Qualquer problema de credencial (token de outro gateway ou
   empresa, expirado, revogado, gateway desligado) é 401 sem dizer qual. O token é mostrado **uma vez** (API, CLI e
   UI). No máximo 50 gateways por empresa e 20 tokens ativos por gateway; um perfil usado por gateway não pode ser
   apagado.
10. **Auditoria.** `tool_invocations`: empresa, run ou gateway, persona, integração, tool, sha256 e **tamanho** dos
    argumentos (nunca os argumentos), decisão `allow|deny|ask|quarantined|error`, gate, resultado
    `ok|failed|blocked|pending`, latência, erro curto (sem segredos), data. O teto de taxa conta as chamadas `allow` do
    último minuto. Retenção: 30 dias e 10 mil por empresa, aplicada no `memory_sweep` (a cada 5 min).
11. **Papéis (ADR-0021).** Admin configura catálogo, quarentena, perfis, regras, vínculos, gateways e tokens; member
    decide o gate `tool`; qualquer membro lê catálogo, perfis, vínculos, gateways (sem tokens) e as tools efetivas;
    **só admin lê a auditoria**. Run tokens não configuram nada pela API.
12. **Superfícies.** API: `/integrations/{i}/tools` (GET; POST `/refresh`; PATCH `/{tool}`; POST `/review`),
    `/tool-profiles` (CRUD, `/rules`, `/integrations/{i}` para as escolhas do assistente, `/bindings`,
    `/effective?persona=&project=`), `/tool-gateways` (CRUD, `/tokens`), `/tool-invocations` (filtros). CLI:
    `hanko integration tools <i> [--refresh|--review|--release|--disable|--enable]`,
    `hanko tool-profile list|show|create|rule|bind|effective|delete`, `hanko tool-gateway list|show|create|token|revoke|delete`.
    UI: abas em Integrações (Integrações, Tools, Perfis, Gateways, Auditoria) sob `/c/{empresa}/ferramentas` (para não
    disputar rota com `/integracoes/{integração}`), assistente de perfil em 3 passos, detalhe do gateway com conexão e
    tokens, e o painel "Tools efetivas" na página da persona (carregado por htmx). Não há tools `hanko_*` novas: a
    persona não configura nada disso, e o próprio gateway é o MCP que ela usa.
13. **Export/import (ADR-0022).** Fica de fora nesta versão: perfis e vínculos referenciam integrações, tools e
    personas que o import recria desligadas e sem catálogo, e o catálogo, as aprovações, os gateways (com tokens) e a
    auditoria são estado de operação. Um arquivo `hanko.company.v1` continua igual.

## Consequências
- O agente deixa de receber as credenciais dos servidores MCP http; elas ficam no Hanko e só saem na conexão do
  servidor. Em compensação, toda chamada passa pelo Hanko (duas idas ao servidor por chamada: `initialize` e o pedido),
  com a latência e a disponibilidade do Hanko no caminho.
- MCP http interno passa a depender de `HANKO_INTEGRATIONS_ALLOW_PRIVATE=true`, porque quem conecta agora é o servidor
  (antes era o agente, sem a proteção SSRF).
- Nada muda para quem não cria perfil: as mesmas tools continuam disponíveis (com a quarentena valendo só para o que
  surgir depois da primeira descoberta).
- A quarentena depende de o servidor declarar anotações honestas; um servidor que declara `readOnlyHint` em uma tool
  que escreve escapa dela. As anotações são dicas do protocolo, não garantia — por isso o padrão de um perfil é `deny`.
- A aprovação de uso único é por sha256 dos argumentos canônicos: uma chamada "igual" com um campo a mais pede nova
  aprovação, e uma aprovação consumida por uma chamada que falhou no servidor não volta.
- Sem SSE nem sessão no gateway: notificações do servidor remoto (`listChanged`, progresso) não chegam ao agente.

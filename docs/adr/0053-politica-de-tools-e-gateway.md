# ADR-0053 — Motor de política de tools e gateway único
Data: 2026-10-17 · Status: aceita

## Contexto
O plano pede (itens #20, #39 e #40) uma política `off/ask/allow` por persona × tool, com padrão da empresa, `ask` virando
gate com prazo, "sempre permitir" gravando regra e o mesmo motor valendo para as tools nativas do harness e para as de
integração; quarentena de tool nova com aviso na Caixa, teste "como a persona X" e log de chamadas por conexão; e o
`/mcp` como gateway com perfis reutilizáveis, um endpoint para o harness e token por cliente externo.

Boa parte disso **já existe** desde o ADR-0036 (migration 00032, `internal/service/tools_*.go`, abas Tools/Perfis/Gateways/
Auditoria): catálogo por integração com `tools/list`, risco derivado das anotações, **quarentena** de tool nova ou alterada
que escreve, perfis reutilizáveis (`tool_profiles` + regras `include/exclude` com modo `allow|ask`), vínculos
empresa/projeto/persona, gateway da run (`/mcp/run/{integração}`), gateway nomeado com token `hkgw_` por cliente externo,
o gate `tool` com aprovação de uso único por sha256 dos argumentos e a auditoria `tool_invocations`. Este ADR **não repete**
nada disso; registra o que o ADR-0036 já cobre e acrescenta só a diferença.

### Já coberto por ADR-0036 (nada a fazer)
- **#39** tool nova entra em quarentena (equivale a `off` até um admin revisar); descoberta por `tools/list`; auditoria de
  cada chamada (decisão, resultado, latência, sha256 e tamanho dos argumentos).
- **#40** perfis de tools reutilizáveis ligados a persona, projeto ou empresa; gateway nomeado para cliente de fora com
  token opcional, escopo de ações e expiração; o harness não recebe a URL nem os segredos do servidor.
- **#20** gate `ask` com aprovação de uso único para chamadas de integração (o gate se chama `kind=tool`, não `tool_call`;
  mantivemos o nome do ADR-0036 e a constraint `gates_kind_check` não muda).

## Decisão

### 1. Política por cima do perfil
Migration **00050** cria `tool_policies`: uma linha diz "neste **escopo**, para este **alvo**, o **modo** é `off`, `ask` ou
`allow`".
- Escopo: `company` (o padrão da empresa) ou `persona` (a exceção de uma persona).
- Alvo: `all` (qualquer tool de integração e do harness), `integration` (a integração inteira), `tool` (uma tool de
  integração) ou `native` (uma tool do harness — `Bash`, `Edit`... — ou de plugin, pelo nome).
- Única por (empresa, escopo, persona, alvo, integração, tool). Até 2000 por empresa. Só admin grava e apaga; qualquer
  membro lê.

O perfil de ferramentas (ADR-0036) continua sendo o **padrão reutilizável**; a política é a exceção explícita por cima
dele. Funções puras em `internal/domain/toolpolicy.go`.

**Precedência** (`ResolveToolPolicy`, total e independente da ordem de entrada):
1. escopo mais estreito: **persona vence empresa**;
2. alvo mais específico: **tool/nativa vence integração vence curinga**;
3. empate: o modo mais restritivo — **deny vence** (`off` > `ask` > `allow`);
4. só para a ordem ser total: o id maior.
Sem política que alcance a chamada, vale o perfil (tools de integração) ou o que o harness já fazia (tools nativas).
O estado no catálogo vem antes de tudo: **quarentena, desligada ou removida nunca são abertas por política**.

O curinga `all` cobre tools de integração e do harness, mas **não** tools de plugin nem as `hanko_*`: uma política "tudo
desligado" não pode derrubar o protocolo do Hanko. Plugin só por nome (`native` com `plugin_<id>_<tool>`).

**Gateway nomeado (cliente de fora): as políticas só apertam.** O perfil do gateway é a fronteira que o admin desenhou
para aquele cliente; um `allow` da empresa não a abre. Já na run da persona a política vence o perfil (é a exceção
explícita).

### 2. `ask` com prazo e "sempre permitir"
- O gate `tool` ganha prazo (`gates.expires_at`): 24 h para a chamada de integração/plugin (a persona repete a chamada
  depois da decisão) e **10 min** para a chamada nativa (a execução está parada esperando). A varredura periódica
  (`memory_sweep`, a cada 5 min) vira `expired` os pendentes vencidos, com `gate.expired` na atividade; uma decisão tardia
  dá conflito e a próxima chamada igual abre um gate novo. Constantes no service (`toolGateTTL`, `nativeGateTTL`).
- **Sempre permitir**: `DecideGateInput.Always` (API `always`, CLI `gate approve --always`, botão "Aprovar e sempre
  permitir"). Só vale ao **aprovar**, só em gate `tool`, só para chamada de **persona** (gateway nomeado não tem persona) e
  **só admin** — grava uma política permanente. Na mesma transação da decisão: política `persona/allow` para a tool
  (`origin=always`), trocando a de `ask` de mesma chave, e atividade `tool_policy.set`. Daí em diante a chamada passa
  direto, com a decisão registrada como `policy:persona`.

### 3. Tools nativas do harness (mesmo motor)
`NativeToolCheck` aplica a política a uma tool nativa; sem política que a alcance devolve `none` (nada muda, nada é
auditado). Em `ask` abre o gate (`tool_call_approvals` agora aceita `native_tool` no lugar da integração), com a entrada
**redigida** no corpo, e a execução espera:
- **claude — hook `PreToolUse`.** Com política nativa para a persona (`RunExtras.NativeTools`), o adapter grava um settings
  temporário (0600) e roda `claude --settings <arquivo>` com o hook `hanko hook pretooluse` (o próprio executável, prazo de
  11 min). O hook lê o JSON do claude, ignora `mcp__*` (essas já passam pelo gateway/servidor MCP, que aplicam a mesma
  política), consulta `POST /companies/{c}/tool-permission` com o **run token** e, em `ask`, consulta
  `GET …/tool-permission/{gate}` até sair de `pending`. Resposta `permissionDecision` allow/deny; sem opinião não imprime
  nada. **Falha fechada**: se o Hanko não responde (3 tentativas), nega. Revisões (somente leitura) não levam hook; em
  ambiente remoto o executável não está no alvo, então o hook não vale (aviso no log da run).
- **ACP — permission request nativo.** `adapter.Spec.ToolGuard` é consultado a cada `session/request_permission`, antes do
  `permission` do `adapter_config`; o nome da tool é o tipo da chamada do protocolo (`execute`, `edit`, `read`...; `other`
  se ausente). Em `ask` o guarda espera o gate dentro da run (cancelar a run encerra a espera com negação). Sem opinião,
  vale o `permission` de sempre; revisões ignoram o guarda.
- Outros harnesses (codex) não expõem gancho: a política vale só para integração e plugin, e a run avisa no log.
- **Tools de plugin** (servidor MCP do Hanko) passam pelo mesmo motor em `CallPluginTool`: `off` recusa, `ask` abre o gate
  e a persona repete a chamada (modelo da integração).

### 4. #39: o que faltava
- **Aviso na Caixa.** Tool em quarentena aparece na aba Tools da Caixa (e em "Tudo") para admin, com o risco e "Revisar" que
  leva ao catálogo; entra no contador (`Inbox.QuarantinedTools`).
- **Testar como a persona X.** `TestToolCall` (admin; API `POST /integrations/{i}/tools/{tool}/test`; CLI
  `integration tools --test … --as …`; aba Política): mostra o estado no catálogo, o veredito (perfil + políticas) e a
  origem. Sem `execute` é só simulação (nada é gravado nem chamado). Com `execute` repassa de verdade, abre o gate em nome da
  persona (sem run) e audita com origem `test` e o humano como ator.
- **Log por conexão.** A auditoria ganha `source` (run, gateway, test, hook, acp, plugin), `actor` (persona, humano ou
  sistema), `result_preview` (início do resultado, **redigido**: segredos da integração e padrões de credencial, até 500
  caracteres) e `policy_source` (include/exclude/default ou `policy:persona|company`). Chamada nativa não tem integração:
  `integration_id` nulo. Os argumentos continuam nunca gravados (só sha256 e tamanho). Filtro por origem na API.

### 5. #40: o que faltava
- **Endpoint único da run.** `POST /mcp/run` (run token): as tools de **todas** as integrações MCP http da persona, com o
  nome `<integração>__<tool>`, sob o mesmo perfil, as mesmas políticas, o mesmo teto de taxa e a mesma auditoria do endpoint
  por integração (que continua existindo). Opt-in por `HANKO_TOOLS_SINGLE_ENDPOINT=true` (o executor passa a montar um
  servidor `hanko-tools` em vez de um por integração; muda o nome das tools que o harness vê, por isso não é o padrão).
- **Limite assumido.** As tools `hanko_*` e as de plugin seguem em `/mcp` (o contrato de nomes que os prompts das personas
  usam), então o harness recebe `hanko` (Hanko + plugins), `hanko-tools` (integrações) e o servidor de memória. Unir tudo
  num único servidor exigiria proxiar o servidor MCP do Hanko por dentro do gateway; fica para um ADR próprio.

## Consequências
- Uma persona com política `ask` numa tool nativa do claude espera até 10 min por um humano; passado o prazo a tool é negada
  (o claude segue o turno com a negação). Quem quer o contrário usa `allow` ou `off`.
- O hook só existe quando há política nativa para a persona, então runs sem política não pagam o processo extra por tool.
- `allow` em `all` na empresa abre também o que o perfil da persona excluiria (política vence perfil nas runs). É poder de
  admin e fica visível em "Tools efetivas" (origem `policy:company`).
- O nome de gate continua `tool`; `gates_kind_check` não foi tocada (lista vigente: approval, task_review, hire, memory, tool).
- Dependência Go nova: nenhuma. Config nova: `HANKO_TOOLS_SINGLE_ENDPOINT`. Migration aditiva 00050 (tabela, colunas
  nuláveis ou com padrão, `tool_call_approvals.integration_id` passa a aceitar nulo); o `Down` desfaz.
- Export/import (ADR-0022): políticas não entram (referenciam integrações e personas que o import recria desligadas),
  como já era com perfis e vínculos (ADR-0036).
- Testes de propriedade do motor (#56): independência da ordem, persona > empresa, deny vence no mesmo nível, UPSERT
  idempotente e "sempre permitir" para de perguntar.

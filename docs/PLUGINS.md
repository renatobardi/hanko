# Plugins de terceiros — protocolo (api_version 1)

Decisão e limites: [ADR-0038](adr/0038-plugins-de-terceiros.md) e [ADR-0045](adr/0045-plugins-contribuicoes-e-sdk.md)
(abas, gatilhos de fluxo, provider de ambiente, fonte de skills, concessão de capacidades e SDK). Exemplos:
[`examples/plugins/hello`](../examples/plugins/hello) (só biblioteca padrão, protocolo cru) e
[`examples/plugins/showcase`](../examples/plugins/showcase) (SDK Go: tool, aba com ponte, gatilho e fonte de skills).

**Começo rápido:** `hanko plugin new meu-plugin` gera o manifesto, um `main.go` com o SDK e um README (seção 9).

## 1. Instalar um plugin (operador)

1. Escolha um diretório local e defina `HANKO_PLUGINS_DIR` (por exemplo `/srv/hanko/plugins`). Opcional:
   `HANKO_PLUGINS_DATA_DIR` para os diretórios de trabalho (padrão `$HOME/hanko-data`). Reinicie o servidor.
2. Coloque cada plugin num subdiretório com o **mesmo nome do `id`** do manifesto:
   ```
   /srv/hanko/plugins/hello/
     hanko-plugin.json
     hello              # o executável (command), relativo a este diretório
   ```
   Para o exemplo: `cd examples/plugins/hello && go build -o hello . && cp -r . /srv/hanko/plugins/hello`.
3. Como dono da instalação: Configurações › Plugins › Disponíveis › **Instalar** (ou `hanko plugin install hello`).
   A tela mostra as capacidades pedidas; instalar as concede.
4. Em cada empresa, um admin liga o plugin e preenche a configuração (`hanko plugin enable hello --company acme`,
   `hanko plugin config hello --company acme --set greeting=Oi`).

O Hanko nunca baixa plugin. Atualizar é trocar os arquivos do diretório por uma versão maior e clicar em
**Atualizar** (`hanko plugin upgrade hello`); se a versão nova pedir capacidades a mais, ela espera
**Aprovar capacidades** (`hanko plugin upgrade hello --approve`). Troque o executável por renomeação
(`mv hello.new hello`), não sobrescrevendo o arquivo em uso.

## 2. Manifesto (`hanko-plugin.json`)

```json
{
  "id": "hello",
  "api_version": 1,
  "version": "1.0.0",
  "name": "Hello",
  "description": "O que o plugin faz.",
  "author": "Quem mantém",
  "homepage": "https://exemplo.com",
  "command": "hello",
  "args": [],
  "capabilities": ["tasks.read", "tasks.write", "events.subscribe", "agent.tools", "widgets", "jobs", "state"],
  "config_schema": {
    "type": "object",
    "required": ["greeting"],
    "properties": {
      "greeting": {"type": "string", "title": "Saudação", "default": "Olá"},
      "mode": {"type": "string", "enum": ["curto", "longo"]},
      "limit": {"type": "number"},
      "loud": {"type": "boolean"},
      "api_token": {"type": "string", "title": "Token", "secret": true}
    }
  },
  "tools": [{"name": "hello_echo", "description": "Ecoa o texto.", "input_schema": {"type": "object", "properties": {"text": {"type": "string"}}}, "timeout_sec": 30}],
  "events": ["task.created"],
  "widgets": [{"key": "resumo", "title": "Resumo", "placement": "dashboard"}],
  "jobs": [{"key": "tick", "every_minutes": 5}],
  "pages": [{"key": "painel", "title": "Painel"}],
  "flow_triggers": [{"key": "novo-pedido", "title": "Novo pedido"}],
  "environment_providers": [{"key": "nuvem", "title": "Nuvem X", "known_hosts": "gw.nuvem.example ssh-ed25519 AAAA…"}],
  "skill_sources": [{"key": "catalogo", "title": "Catálogo"}]
}
```

| Campo | Regra |
|---|---|
| `id` | `^[a-z][a-z0-9-]{1,62}$`, igual ao nome do diretório |
| `api_version` | `1` |
| `version` | semver (`1.2.3`, `1.2.3-rc.1`) |
| `command` / `args` | caminho relativo ao diretório do plugin (sem `..`, sem caminho absoluto; links simbólicos não podem sair do diretório); até 32 argumentos, sem shell |
| `capabilities` | lista fechada (abaixo); tools exigem `agent.tools`, `events` exige `events.subscribe`, `widgets` exige `widgets`, `jobs` exige `jobs` |
| `config_schema` | object; campos `string` (com `enum` opcional), `number`, `boolean`; `default`; `required`; `secret: true` só em `string` livre, sem `default` |
| `tools[]` | `name` `^[a-z][a-z0-9_]{0,47}$`, `description`, `input_schema` (JSON Schema com `type: "object"`, até 16 KiB), `timeout_sec` 1..300 (padrão 30) |
| `events[]` | verbos da atividade (`task.created`, `task.closed`, `comment.human`, `gate.decided`, `run.finished`…) |
| `widgets[]` | `key`, `title`, `placement`: `dashboard`, `task` ou `persona` |
| `jobs[]` | `key`, `every_minutes` 5..1440 |
| `pages[]` | `key`, `title`, `icon?`; exige `pages`; até 8 (seção 10) |
| `flow_triggers[]` | `key`, `title`, `description?`; exige `flows.trigger`; até 16 (seção 11) |
| `environment_providers[]` | `key`, `title`, `known_hosts` (obrigatório, sem coringa de host); exige `environments.provider`; até 4 (seção 12) |
| `skill_sources[]` | `key`, `title`, `description?`; exige `skills.source`; até 8 (seção 13) |

Campos desconhecidos são erro. Tetos: manifesto 256 KiB, 32 tools, 32 eventos, 16 widgets, 16 jobs, 32 campos.

## 3. Capacidades

| Capacidade | Libera |
|---|---|
| `tasks.read` | `tasks.list`, `tasks.get` |
| `tasks.write` | `tasks.create`, `tasks.comment`, `tasks.update_status` (nunca decidir gate) |
| `personas.read` | `personas.list` (id, slug, nome, cargo, status; nunca instruções nem configuração do harness) |
| `projects.read` | `projects.list` |
| `activity.read` | `activity.list` |
| `events.subscribe` | receber `on_event` dos verbos em `events[]` |
| `agent.tools` | expor `tools[]` às personas no MCP |
| `widgets` | blocos `get_widget` no Painel, na task e na persona |
| `jobs` | `run_job` periódico |
| `state` | `state.get`, `state.set`, `state.delete`, `state.list` (chave ≤ 200 bytes, valor JSON ≤ 64 KiB, até 1000 chaves por empresa) |
| `http.outbound` | declarativa: avisa o operador e repassa as variáveis de proxy. O Hanko não impede rede de um processo |
| `pages` | abas na barra lateral da empresa: `get_page` e `page_call` (seção 10) |
| `flows.trigger` | `flows.fire`: dispara os fluxos que o admin da empresa ligou ao gatilho (seção 11) |
| `environments.provider` | `env.ping|create|ssh|delete`: provider de sandbox em nuvem; **recebe a chave de API do ambiente** (seção 12) |
| `skills.source` | `skills.snapshot`: fonte de skills (seção 13) |

`log` e `config.get` só exigem o plugin ligado na empresa. Fora da lista não existe nada: decidir gate, orçamento,
autenticação, segredos do Hanko, banco.

## 4. Transporte

- JSON-RPC 2.0, **uma mensagem por linha** (UTF-8, terminada em `\n`) em stdin (Hanko → plugin) e stdout
  (plugin → Hanko). stderr vira o log do plugin.
- Mensagem até **1 MiB** em cada direção; maior é descartada e registrada.
- As duas pontas fazem requisições: o plugin pode chamar o Hanko enquanto atende uma chamada (ex.: dentro de
  `on_event`). Atenda cada requisição numa goroutine/thread se for chamar o host no meio dela.
- Até 8 chamadas simultâneas e 600 por minuto do plugin ao host (`-32002` acima disso).
- Timeout por chamada do Hanko: 30 s (tools: `timeout_sec`, até 300 s; widgets: 3 s; jobs: 5 min).
- Ambiente: `PATH`, `LANG`, `LC_*`, `TZ`, `SSL_CERT_*` (e proxies com `http.outbound`); `HOME` e `TMPDIR` no
  diretório de trabalho do plugin, que é também o diretório corrente.

Erros do host: `-32601` método desconhecido, `-32602` parâmetros inválidos, `-32001` capacidade não concedida ou
plugin não ligado na empresa, `-32002` limite, `-32004` não encontrado, `-32005` dados recusados, `-32603` interno.

## 5. Do Hanko para o plugin

`company` é sempre `{"id", "slug", "name"}`.

| Método | Params | Resposta |
|---|---|---|
| `initialize` | `{api_version, host_version, plugin_id}` | `{api_version: 1}` |
| `health` | `{}` | `{ok: true}` (ou `{ok: false, message}`) |
| `shutdown` | `{}` | `{}` e sair; depois de 10 s o Hanko manda SIGTERM, e SIGKILL 5 s depois |
| `validate_config` | `{company, config}` (com segredos) | `{ok: true}` ou `{ok: false, error, errors: {campo: motivo}}` |
| `on_event` | `{company, event: {id, verb, occurred_at, actor: {type, id}, subject: {type, id}, data}}` | `{}`; erro = nova tentativa |
| `execute_tool` | `{company, persona: {id, slug, name}, run_id, tool, arguments}` | `{text, data?, is_error?}` |
| `get_widget` | `{company, key, context: {type, id?, key?, slug?}}` | `{title?, markdown?, table?: {columns, rows}}` |
| `run_job` | `{company, key}` | `{}`; erro = rodada falhou |

Eventos chegam **pelo menos uma vez** (5 tentativas: 5 s, 15 s, 45 s, 2 min 15 s): deduplique pelo `event.id`.
Fatos causados pelo próprio plugin não voltam para ele. O widget é **dado**: markdown renderizado e sanitizado pelo
Hanko (sem HTML, sem imagem externa) e tabela simples (até 20 colunas e 200 linhas); fica 60 s em cache.

## 6. Do plugin para o Hanko

Todas recebem `company` (id ou slug) e valem só para empresas que ligaram o plugin.

| Método | Params | Resposta |
|---|---|---|
| `tasks.list` | `{status?: [..], persona?, project?, limit?}` | `{tasks: [...]}` |
| `tasks.get` | `{key}` (chave ou id) | `{task}` |
| `tasks.create` | `{title, description?, priority?, assignee?, project?, event_id?}` | `{task}` |
| `tasks.comment` | `{key, body, event_id?}` | `{comment: {id, task, created_at}}` |
| `tasks.update_status` | `{key, status, event_id?}` | `{task}` |
| `personas.list` | `{}` | `{personas: [...]}` |
| `projects.list` | `{}` | `{projects: [...]}` |
| `activity.list` | `{limit?, before?}` | `{events: [...]}` |
| `state.get` | `{key}` | `{found, value?}` |
| `state.set` | `{key, value}` | `{ok}` |
| `state.delete` | `{key}` | `{deleted}` |
| `state.list` | `{prefix?, limit?}` | `{items: [{key, value, updated_at}]}` |
| `config.get` | `{}` | `{config}` (padrões, valores e segredos) |
| `log` | `{level: debug\|info\|warn\|error, message, company?}` | `{ok}` |

`event_id` torna a escrita idempotente (o mesmo `event_id` não duplica a task nem o comentário). O plugin aparece
na atividade como ator `system` com o id da instalação do plugin.

## 7. Tools para as personas

Cada tool vira `plugin_<id>_<nome>` no servidor MCP do Hanko (`/mcp`), só para as runs de empresas com o plugin
ativo e ligado. A descrição ganha o prefixo `[plugin <nome>]`. `is_error: true` ou um erro JSON-RPC aparecem para o
agente como erro da tool; cada chamada fica na atividade da empresa (`plugin.tool_called`).

## 8. Conceder e revogar capacidades

Quem instala (dono da instalação) concede e revoga capacidades **uma a uma**, só as que o manifesto vigente declara:

```sh
hanko plugin cap revoke showcase pages      # a aba some na próxima página
hanko plugin cap grant  showcase pages
```

Na tela: Configurações › Plugins › (plugin) › **Capacidades concedidas** (visível só ao dono). Revogar vale na hora, porque o
host confere a concessão a cada uso: a aba e a ponte, a lista de tools, os widgets, `flows.fire`, a resolução do provider de
ambiente e a sincronização da fonte de skills. Cada mudança vai para `admin_audit` e para a atividade das empresas que ligaram
o plugin (`plugin.capability_granted|revoked`).

## 9. SDK Go e `hanko plugin new`

`github.com/renatobardi/hanko/sdk/plugin` (só biblioteca padrão) cuida do protocolo; o plugin registra o que contribui:

```go
p := plugin.New("meu-plugin")
p.Tool("ping", func(ctx context.Context, h *plugin.Host, c plugin.ToolCall) (plugin.ToolResult, error) {
	return plugin.ToolResult{Text: "pong"}, nil
})
p.Page("painel", func(ctx context.Context, h *plugin.Host, r plugin.PageRequest) (string, error) { return "<h1>Oi</h1>", nil })
p.Bridge(func(ctx context.Context, h *plugin.Host, c plugin.BridgeCall) (any, error) { return map[string]any{"ok": true}, nil })
p.SkillSource("catalogo", func(ctx context.Context, h *plugin.Host, co plugin.Company) (plugin.SkillSnapshot, error) { /* … */ })
p.Environment("nuvem", meuProvider{}) // implementa plugin.EnvProvider
log.Fatal(p.Run(context.Background()))
```

Também: `Widget`, `Job`, `OnEvent`, `ValidateConfig` e o cliente do host (`h.Call`, `h.StateGet/StateSet`, `h.CommentTask`,
`h.FireTrigger`, `h.Config`, `h.Log`). `hanko plugin new <id> [--dir D] [--author A]` gera `hanko-plugin.json`, `main.go`
(tool + aba com ponte + gatilho), `go.mod`, `.gitignore` e `README.md`; recusa diretório que já tem arquivos. Depois:
`go mod tidy && go build -o <id> .`, copie para `HANKO_PLUGINS_DIR/<id>` e `hanko plugin install <id>`.

## 10. Abas (`pages`)

Cada `pages[]` vira um item do grupo "Plugins" da barra lateral (`hanko plugin tabs`). Hanko → plugin:

| Método | Params | Resposta |
|---|---|---|
| `get_page` | `{company, page, user: {id, name, role}}` | `{html}` (até 512 KiB) |
| `page_call` | `{company, page, method, params, user}` | qualquer JSON (até 256 KiB); erro = a página recebe a mensagem |

**Isolamento.** O HTML roda num `<iframe sandbox="allow-scripts">` **sem** `allow-same-origin` (origem opaca: sem cookies, sem
`localStorage`, sem acesso ao DOM do Hanko), servido com `Content-Security-Policy: default-src 'none'; script-src
'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; font-src data:; sandbox allow-scripts` (**sem rede**, sem
frames, sem formulários), `nosniff` e `no-referrer`. Imagens e fontes só `data:`. Estilo e script só inline.

**Ponte.** O Hanko injeta antes do seu HTML `window.hanko.call(método, params) → Promise` (rejeita com `Error(mensagem)`),
`window.hanko.resize(altura)` e `window.hanko.context` (`{company, plugin, page}`). A chamada vira `page_call` no seu plugin;
`user.role` (`viewer|member|admin|owner`) diz quem está na página: **o plugin decide o que cada papel pode** (o Hanko só
entrega). Método: `^[a-z][a-z0-9_.]{0,63}$`; até 8 chamadas simultâneas por página; timeout de 30 s. O e-mail da pessoa nunca
vai ao plugin. API equivalente: `POST /api/v1/companies/{c}/plugins/{p}/pages/{page}/call`.

## 11. Gatilhos de fluxo (`flows.trigger`)

O plugin declara `flow_triggers[]`; o **admin da empresa** liga cada gatilho a fluxos existentes
(`hanko plugin trigger link <plugin> <gatilho> <fluxo> --company acme`, ou na página do plugin). Plugin → Hanko:

| Método | Params | Resposta |
|---|---|---|
| `flows.fire` | `{company, trigger}` | `{fired, flows: [{flow, task}], skipped_disabled}` |

Cada fluxo ligado **e ativo** dispara como um disparo manual (cria a task do fluxo; ator `system` com o id do plugin); fluxos
desligados são pulados; sem ligação, `fired = 0`. O plugin não escolhe fluxo, persona nem projeto. Não há deduplicação:
cada chamada é um disparo (use `state` se precisar de idempotência).

## 12. Provider de ambiente (`environments.provider`)

O plugin implementa um provider de sandbox em nuvem; o ambiente da empresa usa `cloud.provider = plugin:<id>/<key>`
(`hanko plugin list` mostra o nome; `hanko env create "Nuvem X" --driver cloud --provider plugin:<id>/<key> --api-key-file -`). O Hanko chama o plugin com `{provider: <key>, api_key, …}` (a chave de API do ambiente,
cifrada no Hanko):

| Método | Params extras | Resposta |
|---|---|---|
| `env.ping` | — | `{ok: true}` ou erro |
| `env.create` | `spec: {name, labels, snapshot, cpus, memory_gib, disk_gib, block_network, idle_stop_sec, reap_sec}` | `{id}` (sandbox pronto para SSH) |
| `env.ssh` | `id`, `ttl_sec` | `{host, port, user}` — `user` é o token de acesso do gateway SSH |
| `env.delete` | `id` | `{ok: true}` (apagar um que já não existe não é erro) |

O sandbox é alcançado pelo **mesmo** caminho SSH endurecido dos outros drivers (`known_hosts` do manifesto, obrigatório e sem
coringa; o workspace vai e volta por tar). `idle_stop_sec` e `reap_sec` são a rede de segurança contra vazamento: o provider
deve parar o sandbox ocioso e apagá-lo depois. Revogar `environments.provider` corta a próxima run.

## 13. Fonte de skills (`skills.source`)

A fonte da empresa tem a URL `plugin://<id>/<key>` (`hanko plugin skill-sources`; conecte com `hanko skill source add
plugin://<id>/<key>` ou `POST /skill-sources`). Não depende de `HANKO_SKILL_SOURCES` (nada é clonado). Hanko → plugin:

| Método | Params | Resposta |
|---|---|---|
| `skills.snapshot` | `{company, source}` | `{revision, skills: [{path, skill_md}]}` (até 200; `skill_md` é o SKILL.md completo) |

`revision` é qualquer texto que mude quando o conteúdo muda. O resto é igual às fontes de repositório: seleção por caminho,
edição local marcada como "modificada", fork e pin por persona. Revogar `skills.source` faz a próxima sincronização falhar
com erro claro; as skills já importadas ficam.

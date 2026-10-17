# Plugins de terceiros — protocolo (api_version 1)

Decisão e limites: [ADR-0038](adr/0038-plugins-de-terceiros.md). Exemplo completo, só com a biblioteca padrão de Go:
[`examples/plugins/hello`](../examples/plugins/hello).

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
  "jobs": [{"key": "tick", "every_minutes": 5}]
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

# ADR-0045 — Plugins: abas em iframe, gatilhos de fluxo, provider de ambiente, fonte de skills, concessão de capacidades e SDK
Data: 2026-10-22 · Status: aceita

## Contexto
O plano pede, no M18, o **#37**: plugin = executável + manifesto com capacidades, JSON-RPC por stdio, contribuindo **tools,
gatilhos de fluxo, provider de ambiente (#35), fonte de skills (#36) e páginas/abas em iframe sandboxed com ponte
postMessage**; o admin concede capacidades; enable/disable/remove; SDK Go e `hanko plugin new`. Demo: um plugin de exemplo
instalado adiciona uma tool e uma aba; revogar a capacidade tira as duas.

O ADR-0038 (v0.3) já entrega a base: processo isolado, manifesto validado, ambiente mínimo, capacidades fechadas conferidas a
cada chamada, ciclo de vida (instalar, atualizar com aprovação de capacidade, desligar, desinstalar), tools para as personas,
eventos, jobs, widgets (só dados), estado, configuração com segredos, API/CLI/UI e o plugin `hello`. Este ADR **não repete**
nada disso; acrescenta só a diferença.

### Já coberto por ADR-0038 (nada a fazer)
- Executável + manifesto + JSON-RPC por stdio, capacidades pedidas no manifesto e aceitas pelo dono ao instalar.
- Tools, eventos, jobs, widgets, estado, `http.outbound` declarativa, segredos cifrados, saúde, logs.
- enable/disable por empresa e na instalação, upgrade com aprovação, desinstalação.

## Decisão

### 1. Quatro capacidades novas e quatro contribuições no manifesto
Capacidades: `pages`, `flows.trigger`, `environments.provider`, `skills.source`. Cada contribuição exige a sua capacidade (como
`tools` exige `agent.tools`); campos desconhecidos continuam recusados; um upgrade que pede capacidade nova do M18 cai em
`upgrade_pending` como qualquer outra.

| Manifesto | Capacidade | Teto |
|---|---|---|
| `pages[]` `{key, title, icon?}` | `pages` | 8 |
| `flow_triggers[]` `{key, title, description?}` | `flows.trigger` | 16 |
| `environment_providers[]` `{key, title, known_hosts}` | `environments.provider` | 4 |
| `skill_sources[]` `{key, title, description?}` | `skills.source` | 8 |

### 2. Concessão e revogação de capacidades (admin da instalação)
Quem concede é o **dono da instalação** (o papel que já instala o plugin, ADR-0038 item 6): "admin" do plano é o administrador da
instância, porque o plugin é código na máquina do servidor; o admin de uma empresa decide só **ligar** o plugin nela. Novo
`SetPluginCapability` (`POST /plugins/{p}/capabilities/{cap}/grant|revoke`, `hanko plugin cap grant|revoke`): concede apenas o
que o manifesto vigente declara; revogar vale **na hora** para tudo que a capacidade sustenta, porque o host confere a
concessão a cada uso (lista de tools, aba, widgets, `flows.fire`, resolução do provider, sincronização da fonte). Cada mudança
vai para `admin_audit` e vira `plugin.capability_granted|revoked` na atividade de cada empresa que ligou o plugin. Repetir é
no-op. Revogar `http.outbound` reinicia o processo (o ambiente muda).

### 3. Abas (`pages`): iframe sandboxed + ponte postMessage
- **Quem serve o HTML.** O plugin responde `get_page {company, page, user}` com `{html}` (até 512 KiB); o Hanko serve a página em
  `/c/{co}/plugins/{plugin}/{page}/frame`. O HTML do plugin **só** existe dentro desse documento.
- **Isolamento (substitui, só para páginas, a regra "nenhum HTML de plugin chega ao navegador" do ADR-0038 item 10).** A página
  hospedeira (`/c/{co}/plugins/{plugin}/{page}`) embute `<iframe sandbox="allow-scripts" referrerpolicy="no-referrer">` **sem
  `allow-same-origin`**: origem opaca, sem cookies, sem `localStorage`, sem acesso ao DOM do Hanko. O documento do iframe sai com
  `Content-Security-Policy: default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:;
  font-src data:; sandbox allow-scripts`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`,
  `Cache-Control: no-store`: **sem rede** (nem `connect-src`, nem frames, nem formulários, nem `navigate-to` por links externos
  efetivos). A única saída da página é a ponte. Widgets seguem como dados (markdown/tabela), inalterados.
- **Ponte.** O Hanko injeta `window.hanko.call(method, params) → Promise` e `window.hanko.context` (empresa, tema). A chamada
  vira `postMessage` para o pai; o script da hospedeira (`/static/plugin-frame.js`, arquivo próprio, sem inline) só aceita mensagem
  cujo `event.source` seja o `contentWindow` do **seu** iframe, valida o formato e encaminha por `fetch` same-origin (cookie de
  sessão + verificação de origem das rotas de escrita) para `POST /api/v1/companies/{c}/plugins/{p}/pages/{page}/call`
  (`{method, params}`), que chama `page_call {company, page, method, params, user}` no plugin (timeout 30 s, params e resposta
  até 256 KiB). O `user` é `{id, name, role}` (**nunca** o e-mail): o plugin decide o que cada papel pode; o Hanko só entrega.
  Resposta pelo mesmo caminho, casada por id. Run tokens (personas) não usam a ponte.
- **Menu.** Cada aba vira item do grupo "Plugins" da barra lateral da empresa, só para plugins ativos, ligados na empresa **e com
  `pages` concedida**; revogar a capacidade (ou desligar o plugin) tira a aba na próxima página.
- **Quem abre.** Qualquer membro (viewer+). O papel vai ao plugin na ponte.

### 4. Gatilhos de fluxo (`flows.trigger`)
O manifesto declara gatilhos; o **admin da empresa** liga cada gatilho a fluxos existentes (`plugin_flow_triggers`, migration
00140; `PUT|DELETE /companies/{c}/plugins/{p}/flow-triggers/{t}/flows/{f}`, `hanko plugin trigger link|unlink`, seção na página do
plugin). O plugin chama o novo método do host `flows.fire {company, trigger}`; o host dispara **cada fluxo ligado e ativo**
como um disparo manual (cria a task do fluxo, ator `system` com o id do plugin), pula os desligados e devolve
`{fired, flows[], skipped_disabled}`. Sem ligação, nada acontece. `plugin.flow_triggered` na atividade. O disparo **não** é
deduplicado pelo host (cada chamada é um disparo); quem precisa de idempotência usa `state` no plugin. O plugin não escolhe
fluxo, persona nem projeto: só o admin liga.

### 5. Provider de ambiente (`environments.provider`, #35)
O driver `cloud` (ADR-0055) já tem a interface `CloudProvider` (ping/criar/ssh/apagar) e o caminho SSH endurecido. O plugin entra
como **mais um provider**: `cloud.provider = plugin:<id>/<key>`. O Hanko chama `env.ping|create|ssh|delete` no plugin com
`{provider, api_key, …}`; depois de criado, o sandbox é alcançado pelo **mesmo** gateway SSH (`known_hosts` obrigatório, declarado
no manifesto e validado: sem coringa de host), tar por stdin/stdout, comando com aspas POSIX. `execenv.Config.CloudPlugins`
resolve o driver a cada uso e o service confere então que o plugin está ativo **com `environments.provider` concedida** (e de
novo a cada chamada ao plugin); revogar corta a próxima run. Criar/editar/testar o ambiente exige o plugin ligado na empresa e o
provider declarado. A chave de API do ambiente (cifrada, `${secret:}` aceito) vai ao plugin a cada chamada e é removida de
qualquer mensagem de erro. Desligado sem `HANKO_PLUGINS_DIR`.

### 6. Fonte de skills (`skills.source`, #36)
Reaproveita `skill_sources`/`skill_source_entries`/sincronização/revisões do ADR-0035 sem tabela nova: a fonte de plugin tem URL
`plugin://<id>/<key>` (como `workspace://` já faz). `skills.snapshot {company, source}` devolve `{revision, skills:[{path,
skill_md}]}` (até 200 skills); o service converte no mesmo `Snapshot` do clone git (nome/descrição do frontmatter, hash do
conteúdo, `last_commit` = sha1 da revisão, só identificador) e o resto é igual: seleção por caminho, `source_modified`, fork
(ADR-0044), pin por persona. **Não depende de `HANKO_SKILL_SOURCES`** (nada é clonado). Sincronização confere a cada rodada que
o plugin está ativo, ligado e com `skills.source`; revogar faz a sincronização falhar com erro claro e as skills ficam.
`GET /companies/{c}/plugin-skill-sources` lista o que os plugins oferecem; conectar usa o `POST /skill-sources` de sempre.

### 7. SDK Go e `hanko plugin new`
`sdk/plugin` (`github.com/renatobardi/hanko/sdk/plugin`, só stdlib) implementa o protocolo (initialize, health, shutdown,
despacho concorrente, cliente do host com timeout) e deixa ao autor `Tool`, `Widget`, `Job`, `OnEvent`, `Page`, `Bridge`,
`SkillSource`, `Environment` e `ValidateConfig`. `examples/plugins/showcase` usa o SDK (tool, aba com ponte, gatilho, fonte de
skills) e é o plugin do teste de demo. `hanko plugin new <id> [--dir D]` gera `hanko-plugin.json` válido, `main.go` com o SDK,
`.gitignore` e `README`, recusando diretório não vazio. **Nenhuma dependência Go nova**: o SDK é pacote do próprio módulo.

## Alternativas descartadas
- **iframe `allow-same-origin`** ou página servida de um subdomínio por plugin: precisaria de CSP/CORS por plugin e de DNS; a
  origem opaca dá o isolamento sem infraestrutura nova.
- **Plugin criando fluxos/gatilhos livremente:** o plugin só dispara o que o admin ligou.
- **Escolher o provider de ambiente por plugin no nível da instalação:** manter por ambiente reaproveita a tela e a API de
  ambientes e deixa cada empresa escolher.
- **Tabela própria de fontes de plugin:** duplicaria seleção, revisões e fork.

## Consequências
- Um plugin com `pages` executa **JavaScript do plugin no navegador de quem abre a aba**, mas isolado: sem cookies, sem rede, sem
  DOM do Hanko; o pior que a página faz é pedir coisas ao próprio plugin pela ponte (que o plugin já poderia fazer sozinho) e
  desenhar o que quiser dentro do quadro. A tela de instalação avisa na capacidade `pages`.
- `environments.provider` entrega a chave de API do provider ao plugin; a tela avisa (capacidade destacada). Quem não confia no
  plugin não a concede.
- O dono da instalação pode revogar capacidade de plugin em uso: ambientes que dependiam do provider passam a falhar na próxima
  run, com a causa na mensagem.
- Sem migration destrutiva: uma tabela nova (`plugin_flow_triggers`, 00140). `gates_kind_check` não muda.

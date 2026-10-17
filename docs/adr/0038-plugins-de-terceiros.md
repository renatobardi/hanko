# ADR-0038 — Plugins de terceiros: processo isolado, capacidades e dados
Data: 2026-10-16 · Status: aceita

## Contexto
Configurações › Plugins mostrava só os pontos de extensão embutidos (ADR-0031, item 7) e dizia que plugins de
terceiros não existiam. Quem opera o Hanko quer ligar o time a sistemas próprios sem esperar uma integração no
núcleo: uma tool que consulta o ERP da empresa, um comentário automático em toda task nova, um resumo no Painel,
uma sincronização de hora em hora. Os pedidos têm o mesmo risco das integrações stdio (ADR-0029): código de
terceiros rodando na máquina do servidor, perto do banco e dos segredos. A decisão do dono foi pelo isolamento
conservador: nada de código de plugin dentro do processo do Hanko, nada de script de plugin no navegador, e o
plugin só faz no Hanko o que uma lista fechada de capacidades permite.

## Decisão
1. **Plugin é um processo externo.** Cada plugin é um diretório local com `hanko-plugin.json` e um executável; o
   Hanko sobe o executável como processo filho e conversa **JSON-RPC 2.0 por stdin/stdout, uma mensagem por
   linha** (protocolo em `docs/PLUGINS.md`, gerenciador em `internal/plugin`, só stdlib). Código de plugin nunca é
   carregado no processo do Hanko.
2. **Desligado por padrão e local.** A funcionalidade só existe com `HANKO_PLUGINS_DIR` (diretório do operador,
   um subdiretório por plugin com o nome igual ao `id` do manifesto). O Hanko **nunca baixa** plugin. O diretório
   de cada plugin é resolvido (links simbólicos inclusive) e precisa ficar dentro de `HANKO_PLUGINS_DIR`; o
   `command` do manifesto é relativo ao diretório do plugin e também não pode sair dele.
3. **Manifesto validado inteiro.** `id` (`^[a-z][a-z0-9-]{1,62}$`), `api_version: 1`, `version` semver, `name`,
   `description`, `author`, `homepage`, `command` + `args`, `capabilities[]`, `config_schema` (object com campos
   `string`/`number`/`boolean`, `enum`, `default`, `required` e `secret: true`), `tools[]` (nome, descrição,
   `input_schema` objeto, `timeout_sec` até 300), `events[]`, `widgets[]` (`dashboard|task|persona`), `jobs[]`
   (`every_minutes` 5..1440). Campos desconhecidos são recusados (uma capacidade com erro de digitação não vira
   capacidade ignorada); há tetos de tamanho e quantidade em tudo (256 KiB de manifesto, 32 tools, 16 jobs…).
   Declarar tools, eventos, widgets ou jobs exige a capacidade correspondente.
4. **Capacidades fechadas, checadas pelo host a cada chamada.** `tasks.read`, `tasks.write` (criar, comentar e
   mudar status; nunca decidir gate), `personas.read` (sem instruções nem `adapter_config`), `projects.read`,
   `activity.read`, `events.subscribe`, `agent.tools`, `widgets`, `jobs`, `state` e `http.outbound`. O host recusa
   (`-32001`) método fora das capacidades concedidas e empresa que não ligou o plugin (com a mesma resposta de
   empresa inexistente). Não existem, por desenho: decidir gate, mexer em orçamento, autenticação, ler segredos do
   Hanko, acesso ao banco. `http.outbound` é **declarativa**: avisa o operador (e libera as variáveis de proxy), mas
   o Hanko não tem como impedir rede de um processo; quem precisa isola o processo (container, firewall). O plugin
   age como ator `system` com o próprio id, e as chaves de idempotência dele são prefixadas para não colidir.
5. **Ambiente mínimo.** O processo recebe só `PATH`, `LANG`, `LC_*`, `TZ`, os certificados e, com `http.outbound`,
   as variáveis de proxy; `HOME` e `TMPDIR` apontam para o diretório de trabalho próprio
   (`HANKO_PLUGINS_DATA_DIR/plugins/<id>`, padrão `$HOME/hanko-data`). Nada de `HANKO_*`, de credenciais de
   provedores de modelo, de token de run nem de acesso ao banco.
6. **Quem faz o quê (ADR-0021).** Instalar, atualizar, aprovar capacidades, desligar na instalação, desinstalar,
   ver logs e saúde são do **dono da instalação** (papel conferido no banco; a API recusa run tokens). Ligar,
   desligar e configurar por empresa são do **admin** dela; qualquer membro vê a lista e os widgets.
7. **Ciclo de vida.** `installed → ready|error|uninstalled`; `ready → disabled|error|upgrade_pending|uninstalled`;
   `disabled → ready|uninstalled`; `error → ready|uninstalled`; `upgrade_pending → ready|error|uninstalled`;
   reinstalar reaproveita a linha (`uninstalled → installed`). Instalar valida o manifesto, grava as capacidades
   pedidas (o dono as vê e aceita ao instalar), sobe o processo e faz `initialize` + `health`. **Atualizar** lê a
   versão nova do diretório (semver maior): sem capacidade a mais, aplica e reinicia; pedindo capacidade a mais, o
   plugin fica `upgrade_pending` e continua atendendo com o manifesto e as capacidades já aprovados até o dono
   aprovar. Desinstalar para o processo e as tools e apaga configuração (com segredos) e estado de todas as
   empresas; logs e histórico de jobs ficam. Instalação e operações do dono vão para `admin_audit`; o que muda o
   plugin de uma empresa gera `activity_event` nela (`plugin.enabled|disabled|configured|suspended|resumed|
   upgraded|uninstalled|tool_called`).
8. **Gerenciador de processos.** Um processo por plugin com estado `ready`, `upgrade_pending` ou `error` **e** ao
   menos uma empresa ligada (o tick reconcilia a cada minuto). Queda reinicia com backoff exponencial (1 s
   dobrando até 5 min; zera depois de um minuto de pé); saúde periódica (`health` a cada minuto; falha reinicia);
   timeout por chamada (30 s; tools até 300 s); mensagens até 1 MiB nas duas direções (a maior é descartada e
   registrada); até 8 chamadas simultâneas do plugin ao host e 600 por minuto; desligamento com `shutdown`, até
   10 s, `SIGTERM` ao grupo, 5 s, `SIGKILL`. O stderr vira log do plugin (`plugin_logs`, últimas 1000 linhas, até
   120 por minuto, com os segredos conhecidos do plugin mascarados). Uma instância do servidor (ADR-0008): o estado
   do processo fica em memória; o banco guarda o estado do plugin e o último erro.
9. **Configuração e segredos.** O formulário da empresa é gerado do `config_schema`; o service valida tipos, `enum`
   e `required` (ao ligar) e, com o plugin no ar, pergunta ao próprio plugin (`validate_config`). Campos `secret`
   ficam cifrados com o cofre das integrações (AES-256-GCM, `HANKO_SECRET_KEY`, AAD = plugin + empresa), entram na
   rotação de chave (`hanko integration rekey`) e **nunca voltam** pela API, UI, CLI, atividade ou log: só a lista
   dos campos definidos. O plugin lê a própria configuração completa por `config.get`.
10. **Integração.**
    - **Tools para as personas**: aparecem no MCP do Hanko como `plugin_<id>_<tool>` só para a empresa do token de
      run com o plugin ativo e ligado (cada empresa com tools de plugin ganha um servidor MCP próprio, em cache pela
      impressão digital do conjunto). A chamada vai para `execute_tool` com empresa, persona e run; erro ou timeout
      viram erro da tool; cada chamada vira `plugin.tool_called`.
    - **Eventos**: o fato de atividade de uma empresa que ligou o plugin, com o verbo em `events[]`, entra na fila
      (river) **na mesma transação** do fato; entrega pelo menos uma vez, 5 tentativas com espera de 5 s × 3; o
      plugin deduplica pelo `id` do evento. Fatos causados pelo próprio plugin não voltam para ele.
    - **Jobs**: o tick agenda cada job vencido por (plugin, empresa) com um índice único parcial que impede
      sobreposição; rodada perdida (processo caiu) expira em 10 min.
    - **Widgets**: Painel, task e persona ganham um bloco "De plugins" carregado depois da página (htmx), que chama
      `get_widget` com timeout de 3 s e cache de 60 s e renderiza **dados**: markdown pelo renderizador interno
      (sanitizado, sem imagem externa) e tabela simples. **Nenhum HTML ou script de plugin chega ao navegador**: é a
      decisão de segurança que dispensa sandbox de UI e CSP por plugin.
11. **Superfícies.** API `/api/v1/plugins` (disponíveis + instalados, `install`, `/{p}/enable|disable|upgrade|
    approve-upgrade|uninstall`, `/{p}/logs`, `/{p}/health`) e `/api/v1/companies/{c}/plugins` (lista, `GET|PATCH
    /{p}`, `/{p}/state`, `/{p}/widgets`); CLI `hanko plugin list|install|enable|disable|upgrade|uninstall|logs|
    config`; UI Configurações › Plugins (instalados, disponíveis, capacidades com aviso, ligar por empresa,
    configurar, aprovação de capacidades, saúde e log para o dono). Exemplo em `examples/plugins/hello` (Go, só
    stdlib), usado nos testes de integração.
12. **Export/import (ADR-0022).** Plugins **não** entram no `hanko.company.v1`: a instalação é do operador e pode não
    existir no destino, e a configuração por empresa tem segredos. Quem importa liga e configura de novo.

## Consequências
- Um plugin malicioso continua podendo fazer o que um processo do usuário do servidor faz na máquina (ler arquivos,
  abrir conexões): as capacidades limitam o que ele faz **no Hanko**, não no sistema operacional. O operador que
  instala um plugin confia nele como confiaria num binário qualquer; para mais isolamento, rode o Hanko (ou o
  plugin, via um executável que o embrulhe) num container sem acesso ao `.env` e ao banco.
- `http.outbound` é um aviso, não uma barreira; a tela diz isso ao instalar.
- O estado do processo é por instância: com mais de uma instância do servidor (fora do ADR-0008), cada uma subiria
  os seus processos.
- Plugins que sabem pouco de UI ficam limitados a markdown e tabela; em troca, um plugin nunca injeta script na
  sessão de quem lê o Painel.
- O teste dos fluxos depende de compilar o plugin de exemplo (`go build`) nos testes de integração do service.

# ADR-0023 — ACP como harness genérico (v1)
Data: 2026-10-15 · Status: aceita

## Contexto
O ADR-0004 deixou o ACP (Agent Client Protocol) para a v0.2: um protocolo aberto em que o cliente (o Hanko) inicia o
agente como processo filho e conversa JSON-RPC 2.0 por stdio. Os adapters `claude` e `codex` falam a saída própria de
cada CLI; cada agente novo exigiria um parser novo. Com o ACP basta um adapter para qualquer agente que o fale: o
que muda de um agente para outro é só o comando.

## Decisão
1. **Um harness `acp`, versão 1 do protocolo** (`protocolVersion` 1), em `internal/adapter/acp`, ao lado de `claude`,
   `codex` e `fake` (interface `adapter.Adapter`, reaproveitando `procrun`: ambiente por allowlist, grupo de processo,
   término em duas etapas). A v2 do ACP (em rascunho) não é implementada; um agente que responda outra versão no
   `initialize` falha com mensagem clara. Cliente JSON-RPC próprio, só com a stdlib (sem dependência nova): linhas
   delimitadas por `\n`, ids, requisições nos dois sentidos, notificações, erros, `-32601` para método desconhecido,
   linha de entrada limitada a 16 MiB e linhas que não são JSON-RPC registradas no log sem derrubar a run.
2. **O comando mora na persona.** `agent = "acp"` e `adapter_config` com `command` (obrigatório, só o executável),
   `args`, `env` (variáveis extras; nomes `HANKO_*` são reservados e o valor de nomes com cara de credencial é
   redigido nos eventos), `permission`, `model` e `init_timeout_sec` (padrão 30 s). Chaves desconhecidas são recusadas.
   A configuração é validada ao contratar a persona e ao importar um arquivo (erro 422 apontando `adapter_config`); a
   migration 00014 acrescenta `'acp'` ao CHECK de `personas.agent`. O ambiente do agente é o da allowlist do servidor
   (`HANKO_AGENT_ENV_ALLOW` vale) mais `adapter_config.env`; o ambiente da run (`HANKO_RUN_TOKEN`, OTel) sempre vence.
3. **Ciclo da run.** `initialize` (cliente `hanko` + versão; `fs.readTextFile` e `fs.writeTextFile` anunciados;
   `terminal` **não**) → `session/load` se há `SessionRef` e o agente anuncia `loadSession` (os updates de replay são
   ignorados no log; o custo acumulado do replay é a base de que o custo da run é descontado), senão `session/new` com
   `cwd` absoluto = diretório do workspace e `mcpServers` convertidos de `Spec.MCPConfig` (stdio sempre; `http`/`sse` só
   se o agente anuncia `mcpCapabilities` correspondente, senão o servidor é pulado com um evento de aviso) → um
   `session/prompt` → coleta até o `stopReason`. O modelo (`Spec.Model`, senão `adapter_config.model`) é aplicado em
   melhor esforço pelo que o agente anuncia (opção de configuração de categoria `model`, ou `session/set_model`); sem
   essa escolha, ou com um modelo que o agente não lista, vai um aviso no log e o agente usa o padrão dele.
4. **Prompt.** O ACP não tem campo de prompt de sistema. O prompt são dois blocos de texto, nesta ordem: as instruções
   da persona mais o protocolo do Hanko (`Spec.System`), depois o briefing da task (`Spec.Prompt`). Um bloco vazio é
   omitido.
5. **Eventos.** `session/update` vira `adapter.Event` com os `RunEventKind` existentes: `agent_message_chunk` →
   `assistant` e `agent_thought_chunk` → `thinking`, agregados por `messageId` (um evento por mensagem, não por token;
   mensagens muito longas saem em blocos de 16 KiB); `tool_call` → `tool_use`; `tool_call_update` para `completed` ou
   `failed` → `tool_result` (texto do conteúdo ou da saída, truncado em 4 KiB); `plan`, `notice` e as decisões de
   permissão → `system`; `usage_update` e os demais updates não geram evento (o uso vai no `result` final). O resumo
   da run é a **última mensagem do agente**; `Result.SessionRef` é o `sessionId`. Custo: o `cost` do `usage_update` só
   vale em USD (outra moeda é ignorada com um aviso); `Result.Usage` (tokens) só vem do `usage` da resposta do prompt
   quando o agente o envia, porque o `used` do `usage_update` é o tamanho do contexto, não tokens cobrados.
   `stopReason` ≠ `end_turn` (`max_tokens`, `max_turn_requests`, `refusal`, `cancelled`) vira `IsError` com uma mensagem
   que diz o motivo, mantendo o resumo parcial.
6. **Política de permissão** (`session/request_permission`), decidida pelo Hanko sem humano no meio:
   `allow` aprova tudo; `reject` recusa tudo; `read_only` (padrão) aprova só `read`, `search`, `think` e `fetch` e
   recusa `edit`, `delete`, `move`, `execute`, `switch_mode`, `other` e kinds ausentes ou desconhecidos. Responde-se a
   opção `*_once` (a decisão não sobrevive ao pedido); se o agente não oferece opção compatível, a resposta é
   `cancelled`. Toda decisão vira um evento `system` (auditoria). **Runs de revisão (`ReadOnly`) forçam `read_only`.**
7. **Arquivos confinados.** `fs/read_text_file` e `fs/write_text_file` só alcançam o diretório da run: caminho
   absoluto obrigatório, `os.Root` do kernel impede `..` e symlink que saia (inclusive em corrida), leitura até 2 MiB
   de texto (binário é recusado), escrita até 8 MiB. Escrita só com a política `allow` e fora de run de revisão; caso
   contrário é recusada (`-32000`) e registrada. Acesso fora do diretório é recusado (`-32602`) e registrado. O
   confinamento vale para os métodos `fs/*`: o processo do agente roda com os privilégios do Hanko e, se tiver
   ferramentas próprias, elas só são limitadas pelo pedido de permissão, não por esta camada. O sandbox real é o
   container/usuário em que o Hanko roda.
8. **Cancelamento e fim.** Cancelar ou estourar o prazo da run envia `session/cancel`, passa a responder pedidos de
   permissão com `cancelled` e espera até 3 s o `stopReason cancelled`; depois fecha o stdin e, em 2 s, mata o grupo
   de processos. Fim normal: stdin fechado, o agente tem 2 s para sair, depois é morto.
9. **Erros claros.** Sem resposta ao `initialize` em N s: "não respondeu ao initialize em N s; confirme que o
   comando fala ACP v1 em stdio", com as linhas inválidas e o stderr recentes. Versão diferente de 1, resposta sem
   `protocolVersion`, processo que sai antes de responder, comando que não inicia e agente que exige autenticação
   (`-32000`/"auth" em `session/new`, citando os métodos anunciados) têm mensagem própria.
10. **Revisão.** `acp` **não** pode ser o harness alternativo de revisão (`reviewer_agent`): esse harness não carrega
    `adapter_config`, sem o qual não há comando. O CHECK de `personas.reviewer_agent` não muda. Uma persona ACP
    pode ser a persona revisora (ela tem a própria configuração).
11. **Fora de escopo (v1 do adapter):** `terminal/*` (não anunciado; o agente recebe `-32601`), autenticação
    interativa (`authenticate`; o agente deve estar autenticado fora do Hanko, p. ex. chave na `env`), ACP v2, imagens
    e áudio no prompt, modos/comandos/forks de sessão, `compaction_*`, `$/cancel_request` e permissão decidida por
    humano durante a run (a política é fixa por persona).

12. **Adendo (revisão independente): o comando é decisão do operador.** O `adapter_config.command` é escrito por um
    admin de empresa e roda na máquina do servidor; sem trava, um admin escolheria `/bin/sh` com argumentos livres. Como
    no MCP stdio do ADR-0029: `HANKO_ACP_ALLOW` (padrão `false`) liga o harness e `HANKO_ACP_COMMANDS` (opcional, lista de
    nomes ou caminhos absolutos) o restringe. A checagem vale na contratação e na importação (422 com o motivo) e de novo
    em `acp.Adapter.Run` (persona antiga ou config alterada: a run falha sem iniciar o processo). Nome simples só casa com
    comando simples igual; caminho absoluto só casa com o mesmo caminho.
13. **Adendo: `env` não volta em claro.** A API (e, por ela, a CLI `persona show`) devolve `adapter_config` com os
    **nomes** das variáveis de `env` e os valores mascarados (`***`), assim como valores de chaves com cara de credencial
    e o valor que segue uma flag como `--api-key` (`export.MaskConfig`); a UI não exibe o `adapter_config`. A contratação
    segue aceitando valores. **Limite conhecido:** o `adapter_config` ainda é guardado em claro no banco (o runner precisa
    dele inteiro); cifrá-lo com `internal/secretbox` exige migrar a coluna e o caminho de leitura do runner e fica para
    outra decisão. Quem precisa de uma credencial fora do banco usa `HANKO_AGENT_ENV_ALLOW` (variável do servidor).

## Consequências
- Qualquer agente que fale ACP v1 roda sem código novo: contratar a persona com o comando basta, e o mapeamento dos
  eventos fica num lugar só.
- O Hanko anuncia o próprio MCP (`/mcp`) por HTTP; um agente sem `mcpCapabilities.http` roda, mas sem as tools
  `hanko_*` (comentar, delegar), e a run registra um aviso. O resumo continua sendo postado na task pelo Hanko.
- `read_only` como padrão é seguro e pode surpreender: a persona não edita nem executa nada até alguém trocar a
  política para `allow` na contratação. Não há meio-termo por ferramenta nesta versão.
- A escrita de `adapter_config` só acontece na contratação/importação (não existe edição de `adapter_config`); trocar o
  comando exige uma nova persona, como para os outros harnesses.
- Exportar/importar uma persona ACP mantém `command`, `args` e `env` não sensíveis; chaves e flags com credencial são
  removidas (ADR-0022) e precisam ser repostas antes de a persona rodar.
- A instalação do agente ACP é do operador (o `Dockerfile` não muda); ver `docs/RUNBOOK.md`.
- O ADR-0004 previa `internal/acp`; o pacote ficou em `internal/adapter/acp`, ao lado dos outros adapters.

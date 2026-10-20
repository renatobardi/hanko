# ADR-0056 — Decision model por empresa e novos harnesses
Data: 2026-10-17 · Status: aceita

## Contexto
Duas lacunas no M12 do plano (itens #27 e #43):

1. Decisões pequenas do próprio Hanko (para quem mandar uma task nova, em que etapa um texto cai, que nome dar a
   algo longo) hoje exigiriam acordar uma persona: uma run inteira, com harness, workspace e custo de agente.
   Falta um serviço interno, barato, que classifique e responda em uma chamada.
2. Os harnesses são `claude`, `codex`, `acp` (genérico, ADR-0023) e `fake`. Faltam o Devin CLI, o Pi, o Cursor CLI e
   um harness que não dependa de CLI nenhuma (loop de tool-use direto sobre um endpoint OpenAI-compatível, que
   também reabre o teste de modelo local registrado no plano).

## Decisão

### A. Decision model ("decidir")
1. **Provider.** Um único provedor na v1, `openrouter`: qualquer endpoint OpenAI-compatível (`POST {base}/chat/completions`,
   catálogo em `GET {base}/models`). O cliente é o pacote `internal/provider` (só stdlib), compartilhado com o harness
   `http`. A `base_url` é configurável por empresa (padrão `https://openrouter.ai/api/v1`) e passa pelo `netguard`
   como as integrações: destino interno só com `integrations.allow_private` (ADR-0050).
2. **Chave como segredo.** A configuração guarda `api_key_ref = ${secret:NOME}` (segredo local ou de cofre da empresa,
   ADR-0050), nunca o valor. A referência é validada ao salvar (o segredo precisa existir; `user/` é recusado) e
   resolvida **a cada invocação** por `ResolveSecretText`. O valor não entra em atividade, log, ledger nem erro (o
   cliente mascara a chave em mensagens). Apagar o segredo é recusado enquanto o decision model o usa (aparece em
   `used_by` como `decision`).
3. **Modelo do catálogo.** `GET /decision/catalog` busca o catálogo no endpoint da empresa (HTTP configurável; os
   testes usam `httptest`, nenhuma chamada real) e devolve id, nome, contexto e preço por token. Ao salvar a
   configuração o modelo precisa estar no catálogo e o preço por token daquele instante é guardado (estima o custo
   quando a resposta não o traz). Catálogo indisponível: o modelo é aceito sem preço, com custo 0 estimado e aviso.
4. **Features registradas** (código, não dado): `triage`, `phase_classifier`, `name_summary`. Cada empresa liga ou
   desliga cada uma (padrão desligada: nada chama o modelo sem o admin ligar) e pode dar um modelo próprio à feature.
   - `triage`: dado uma task, sugere persona (entre as ativas da empresa) e prioridade; o resultado é **comentário
     do sistema** na task. Não atribui: a decisão continua humana.
   - `phase_classifier`: dado um pipeline e um texto, devolve a chave da etapa mais adequada.
   - `name_summary`: dado um texto, devolve nome (≤ 60) e resumo (≤ 280).
   A resposta é pedida em objeto JSON e validada contra o universo permitido (persona/etapa que não existe é erro, não
   sugestão); texto de entrada é limitado e tratado como dado, nunca como instrução ao sistema.
5. **Ledger.** Cada invocação (ok ou erro) é uma linha de `decision_invocations` com `actor_type = 'system'`,
   feature, modelo, tokens, custo (`reported` quando a resposta traz `usage.cost`, senão `estimated` pelo preço por
   token) e latência, mais um `activity_event` `decision.invoked` (ator system) na mesma transação. O Custos mostra
   um bloco "Decision model" (por feature e modelo) e o `GET /costs` devolve `decision`. O custo do decision model
   **soma ao gasto mensal da empresa** (orçamento da empresa) e **não** a nenhuma persona nem a nenhum grupo de runs.
6. **Papéis (ADR-0021).** Configurar, ligar features: admin. Ver configuração, catálogo e histórico: member. Invocar
   uma feature: member. Nenhuma feature roda sozinha em segundo plano nesta entrega (triagem é acionada por API/CLI/UI);
   fast responses seguem com canais externos (#42).
7. **Telemetria.** Span `hanko.decision.invoke` e métricas `hanko.decision.calls` (feature, resultado) e
   `hanko.decision.cost_usd`.

### B. Harnesses
8. **Devin (1ª classe).** `agent = "devin"`: o motor ACP (ADR-0023) com comando **fixo pelo operador**
   (`HANKO_DEVIN_BIN`, padrão `devin`; argumentos `acp`). Como o operador escolhe o executável e a persona não, o
   harness **não** passa pelo gate `HANKO_ACP_ALLOW`/`HANKO_ACP_COMMANDS`. `adapter_config` aceita `permission`,
   `model`, `env`, `init_timeout_sec` (nunca `command`/`args`). Contratação: preset "Devin" na tela e
   `hanko persona hire --agent devin`.
9. **Probe de autenticação.** `Probe` roda o handshake ACP sem enviar prompt (initialize + `session/new` em diretório
   temporário) e devolve passos nomeados: `binário`, `initialize`, `autenticação`/`sessão`. Agente que exige
   autenticação (`-32000`/"auth") falha no passo `autenticação` com os métodos anunciados. Exposto em
   `POST /companies/{c}/personas/{p}/probe` e `hanko persona probe`; falha = erro claro, sem custo (nenhum prompt).
   O probe periódico e o bloqueio de despacho por readiness (#10) pertencem a outro marco; este entrega a primitiva.
10. **Presets ACP.** `adapter_config: {"preset": "gemini"}` (gemini: `gemini --experimental-acp`; opencode:
    `opencode acp`) preenche `command`/`args`. O gate do operador continua valendo (são `acp`). Conflito entre
    `preset` e `command` é erro. **Teste de contrato** (`internal/adapter/acp/contract_test.go`): um agente ACP falso
    verifica o que o cliente envia no handshake (versão 1, `clientInfo`, capacidades, `cwd` absoluto, `mcpServers`,
    blocos do prompt) e roda cada preset (devin, gemini, opencode) contra ele, também para o probe.
11. **Pi nativo.** `agent = "pi"`: `pi --mode json -p <prompt>`. Pi não fala MCP: as tools do Hanko são a CLI `hanko`
    (já no PATH do servidor), autenticada pelo **run token** (`HANKO_URL`/`HANKO_TOKEN` no ambiente da run; o
    protocolo no system prompt lista os comandos). O parser lê o stream JSONL (`session`, `message_end`,
    `tool_execution_*`, `agent_end`, `usage`) e é tolerante a evento desconhecido. **Honestidade:** os fixtures
    (`internal/adapter/pi/testdata`) foram escritos a partir da documentação dos eventos, não gravados de um binário
    real (não há binário nem rede aqui); regravar com um Pi real é parte da validação no hanko-prd.
12. **Cursor CLI.** `agent = "cursor"`: `cursor-agent -p --output-format stream-json`; parser de `system/init`,
    `assistant`, `tool_call` (`started`/`completed`) e `result` (com `session_id`, `duration_ms`, `is_error`). Mesma
    ressalva de fixtures e mesma regra de tools (CLI `hanko` + run token; Cursor tem MCP próprio, mas configurá-lo é
    do operador).
13. **Harness HTTP próprio.** `agent = "http"`: o Hanko é o harness. Loop de tool-use sobre `chat/completions`:
    system + prompt → o modelo devolve `tool_calls` → o Hanko executa → devolve `role: tool` → repete até a resposta
    final ou o teto de passos (`max_steps`, padrão 20, máx. 50). `adapter_config`: `base_url`, `api_key_ref`
    (`${secret:X}`, resolvido no despacho pelo caminho existente de `adapter_config`), `model`, `max_steps`,
    `temperature`, `timeout_sec`, `tools`. As **tools** são as do servidor MCP do Hanko (`hanko_*`), chamadas por
    JSON-RPC em `Spec.MCPConfig` (mesma URL e bearer do run token): o modelo escolhe entre elas. Sem execução de
    comando nem escrita em disco no harness (v1): é o harness para persona operacional (comentar, delegar, decidir).
    Reusa `internal/provider`; mesma proteção `netguard` do decision model. Custo: `usage.cost` quando há, senão o
    preço de lista (`model_prices`) já existente. Destrava o teste com modelo local (oMLX): basta `base_url` apontando
    para o endpoint OpenAI-compatível dele e `integrations.allow_private`.
14. **Revisão.** `devin`, `pi`, `cursor` e `http` **não** são `reviewer_agent` (mesma razão do `acp`: o harness
    alternativo não carrega `adapter_config`); uma persona dessas pode ser a persona revisora.

## Consequências
- Migration 00080: `personas.agent` ganha `devin|pi|cursor|http`; tabelas `decision_models`, `decision_features`,
  `decision_invocations`. Aditiva; o Down restaura a constraint antiga (falha se já houver persona dos novos harnesses).
- Nenhuma dependência Go nova (cliente HTTP e JSON-RPC são stdlib).
- Env nova: `HANKO_DEVIN_BIN`, `HANKO_PI_BIN`, `HANKO_CURSOR_BIN` (padrões `devin`, `pi`, `cursor-agent`).
- O custo do decision model não é atribuível a persona nem projeto: aparece como linha própria no Custos.
- Sem rede real nos testes: o contrato dos formatos de Pi/Cursor/Devin é o das fixtures; divergência com o binário real
  aparece no teste de aceitação do operador, e o parser ignora eventos que não conhece (degrada para "sem resposta"
  em vez de quebrar).
- Fora de escopo: fast responses (canais, #42), execução automática da triagem na criação da task, fallback entre
  modelos, streaming de tokens do decision model, outros provedores além de OpenAI-compatível.

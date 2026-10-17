# ADR-0055 — Ambientes por projeto, sandbox cloud, readiness da persona e catálogo de modelos
Data: 2026-10-17 · Status: aceita · Estende: ADR-0037

## Contexto
O ADR-0037 já entregou ambientes de execução (`environments`, drivers `ssh` e `sandbox` docker, probe, atribuição por
persona e padrão da empresa, `execenv`). O M11 do plano (#35, #10, #17) pede a diferença:

1. **#35** o ambiente local explícito; o sandbox docker testável sem daemon; **um sandbox em nuvem** atrás da mesma
   interface; **projeto** como nível da precedência, com a **origem gravada na run**; probe com **capacidades**.
2. **#10** saber *antes* do run que o harness da persona não roda (binário, login, modelo), com botão "Testar agora",
   checagem periódica, alerta e bloqueio de despacho com motivo.
3. **#17** um catálogo de modelos que una a tabela de preços ao que os testes e as runs relatam, com aviso quando
   o modelo não tem preço (cai no preço de reserva do ADR-0019).

## Decisão

### A. Ambientes (#35)
1. **Drivers.** `local` (agora também como linha explícita), `ssh`, `sandbox` e `cloud`. O driver **docker do plano é o
   `sandbox` existente**: o nome fica (API, banco e CLI estáveis, ADR-0037); a UI o rotula "Docker". A migration 00070
   só alarga `environments_driver_check` com `cloud`.
2. **Local explícito.** Uma linha `driver=local` (sem config; só variáveis de `env`, com `${secret:X}` do ADR-0050)
   permite *forçar* o local numa persona ou projeto quando o padrão da empresa é remoto, e dar variáveis ao harness
   local. O local implícito (sem linha) continua sendo o último nível. O slug `local` segue reservado (rotas).
3. **Precedência (função pura, `domain.ChooseEnvironment`).** persona > projeto > padrão da empresa > local implícito.
   Um nível que aponta para ambiente arquivado é ignorado (aviso no log da run) e a decisão cai no seguinte. Propriedade
   testada em `internal/domain` sobre todas as combinações de presença/atividade dos três níveis.
4. **Origem gravada na run.** `runs.environment_id` + `runs.environment_source` (`persona|project|default|local`),
   decididos na criação da run (mesma transação do `run.created`). O executor usa o que a run guardou; se aquele
   ambiente foi arquivado entre a fila e a execução, resolve de novo, atualiza a run e avisa no log. Runs anteriores
   (`source=''`) são resolvidas na hora, como antes. A tela da execução mostra "Ambiente · origem".
5. **Projeto.** `projects.environment_id` (FK `ON DELETE SET NULL`); `PATCH /projects/{p}` não muda: o ambiente do projeto tem
   endpoint próprio `PUT /companies/{c}/projects/{p}/environment` (admin), `hanko project env`, e arquivar um ambiente leva
   também os projetos dele (junto com as personas).
6. **Docker testável sem daemon.** `execenv.DockerClient` (Available, Command, Do) é a única fronteira com o motor; o
   padrão chama o binário `docker` e os testes injetam um fake que executa o "contêiner" como processo local. As
   garantias de endurecimento (sem capabilities, `no-new-privileges`, raiz somente leitura, usuário não-root do servidor,
   rede `none` por padrão, limites de memória/CPU/processos, variáveis só por nome no argv) são afirmadas sobre o argv
   que o cliente recebe.
7. **Provider cloud: Daytona (decisão por critério).** Comparados Daytona e E2B contra os critérios abaixo (pesos na ordem):

   | Critério | Daytona | E2B |
   |---|---|---|
   | Zero dependência Go nova (regra do projeto: Postgres é a única dependência de runtime; Go novo só com justificativa) | API REST + gateway SSH: só `net/http` | execução via Connect/gRPC streaming no `envd` de cada sandbox: exige cliente protobuf/connect (dependência nova e pesada) |
   | Reaproveitar o caminho já endurecido e testado do `ssh` (tar por stdin/stdout, `known_hosts` obrigatório, cancelamento) | sim: o gateway SSH recebe um token de curta duração como usuário | não há SSH nativo; seria um segundo caminho de execução e de sincronização de arquivos |
   | Fronteira de teste sem rede real | REST simples ⇒ `httptest` + o `ssh` fake que a suíte já usa | stream binário ⇒ fake bem mais caro |
   | Rede de segurança contra vazamento de custo | `autoStopInterval` / `autoDeleteInterval` no pedido de criação | tempo de vida (`timeout`) renovável, semântica diferente |
   | Autohospedável / saída de emergência | código aberto, pode ser autohospedado (a URL da API é do operador) | idem parcial |

   Daytona vence nos três primeiros critérios, que são os que custam código e risco aqui. **Limite honesto:** as formas
   das chamadas (`POST /sandbox`, `GET /sandbox/{id}`, `POST /sandbox/{id}/ssh-access`, `DELETE /sandbox/{id}`, Bearer) seguem
   a documentação pública conhecida na data e foram exercitadas só contra `httptest`; **precisam ser conferidas com uma
   conta real** antes de produção (`docs/validacao/m11.md`). Trocar de provider é implementar `CloudDriver` (4 métodos).
8. **Como o cloud roda.** `cloudSession` cria um sandbox por run no primeiro `Prepare` (nome `hanko-run-<run>`, recursos e
   rede da config, `public=false`, `autoStopInterval=15min`, `autoDeleteInterval=60min` como rede contra vazamento
   se o servidor cair), espera `started`, pede um token SSH e delega o resto a uma `sshSession` apontada para o gateway
   (sem chave privada; `known_hosts` do gateway obrigatório; o ssh recusa chave desconhecida). Run ok e sincronização ok ⇒
   o sandbox é apagado. Qualquer falha ⇒ o sandbox é **mantido** (o provider o para e apaga sozinho) para recuperar o
   trabalho, e o id fica no motivo do uso. O registro de uso grava `daytona:<id>:<pasta>`.
9. **Interruptores do operador (padrão desligado).** `HANKO_ENV_CLOUD=daytona` liga o driver; `HANKO_ENV_CLOUD_API_URL`
   (padrão `https://app.daytona.io/api`), `HANKO_ENV_CLOUD_SSH_HOST` (padrão `ssh.app.daytona.io`) e
   `HANKO_ENV_CLOUD_SSH_KNOWN_HOSTS` (obrigatória; saída de `ssh-keyscan` do gateway) são do operador: quem administra uma
   empresa não escolhe destino de rede. A **chave de API** do provider é segredo do ambiente (cifrada como a chave SSH),
   aceita `${secret:NOME}` (ADR-0050) e nunca volta por API, UI, evento ou log.
10. **Probe real com capacidades.** Todo driver, inclusive o local, roda um script POSIX no alvo (`RunScript`, por stdin
    nos alvos remotos) e o relatório ganha `capabilities`: `sync` (tar), `git`, `claude`, `codex`, `hanko-reachable` (o alvo
    alcança `HANKO_PUBLIC_URL/healthz` — medido com curl/wget, é por ali que o harness chega ao MCP), `network`,
    `isolated` (alvo descartável) e `persistent`. `network`, `isolated` e `persistent` vêm da construção do driver; o resto é
    medido.
11. **Segredos de ambiente (`${secret:X}`)** já estavam cobertos pelo ADR-0050 para `env`; passam a valer também para a
    chave de API cloud. Resolvidos só no uso (`service.target`), a falha nomeia o segredo.
12. **Plugins.** O "provider de ambiente via plugin" (#37/M18) fica fora: `CloudDriver` é a fronteira que o M18 pode
    reaproveitar, mas nenhum plugin é carregado como driver aqui.

### B. Readiness da persona (#10)
13. **Probe por harness com passos nomeados**, em ordem: `environment` (o ambiente da persona responde — o próprio
    script já é o teste de conexão), `binary` (o binário do harness existe no alvo; `claude`, `codex`, o comando do `acp`
    ou nada para `fake`), `auth` (chave de API no env da persona/ambiente ou login do harness: `claude auth status`,
    `codex login status` ou arquivo de credenciais; `acp` e `fake` ⇒ `skip`), `model` (formato válido; `warn`
    quando não há preço próprio — o custo cai no preço de reserva). Status por passo `ok|warn|fail|skip`; `fail` bloqueia.
    Executa pelo `execenv.RunScript` no ambiente resolvido (persona > padrão), com o env da persona (segredos resolvidos
    só em memória).
14. **`persona_readiness`** (uma linha por persona, sobrescrita; migration 00070): veredito, harness, modelo, ambiente e
    origem testados, passos, motivo (primeiro passo que falhou), gatilho (`manual|periodic|hire|update`), duração e
    `failing_since`. Mudar harness, modelo, `adapter_config` ou ambiente da persona **apaga** a linha (o resultado antigo não vale);
    sem linha a persona não é bloqueada ("nunca testada" não é falha).
15. **Gatilhos.** (a) "Testar agora" (`POST /personas/{p}/readiness`, UI na persona e depois da contratação, `hanko persona
    test`); (b) job periódico river `readiness_tick` testa, por tick, até 20 personas sem teste ou com teste mais velho que
    `HANKO_READINESS_INTERVAL` (padrão 15 min); (c) após a contratação o tick pega a persona na rodada seguinte.
16. **Alerta e bloqueio.** `not_ready` abre o alerta `persona_not_ready` (critical) pelo `alert_tick` já existente, com o motivo, e
    `CheckoutRun`/revisão/chat recusam abrir run com `ErrPersonaNotReady` (HTTP 409, motivo na mensagem; o wake a trata como
    `ErrBudgetExceeded`: não abre run e tenta de novo quando algo mudar). Um teste que passa fecha o alerta e libera o
    despacho. O teste só bloqueia se o harness e o modelo gravados são os de hoje.
17. **Limites.** O probe não gasta tokens: não faz chamada ao modelo (validar o modelo de verdade custaria dinheiro e
    exigiria rede). O ambiente testado é o da persona (persona > padrão); uma persona que roda em ambientes diferentes por
    projeto só é testada no dela.

### C. Catálogo de modelos (#17)
18. **Catálogo híbrido** (`domain.BuildModelCatalog`, função pura): aliases do harness (`opus|sonnet|haiku` no claude) ∪
    linhas de `model_prices` que sejam modelos (prefixos de família terminados em `-` ficam de fora) ∪ `model_reports` (o que
    um teste de readiness viu: o modelo da persona que passou) ∪ modelos que as runs da empresa usaram (90 dias). Cada
    entrada carrega as origens e `priced`/`price_model` (casamento por prefixo mais longo, como o `GetModelPrice`).
19. **Superfícies.** `GET /companies/{c}/models?agent=` e `hanko model list`; nos formulários de contratação e edição da persona
    o campo de modelo vira um select do harness escolhido com a opção **"Outro…"** (campo livre) e aviso inline, sem
    JavaScript além do htmx, quando o modelo não tem preço ("custo estimado com o preço de reserva").
20. **Fora do M11:** buscar a lista de modelos de uma API de provider (sem rede e sem credencial aqui) e o teste de adapter
    com chamada real (o #17 diz que o teste de adapter é o #10).

## Consequências
- Migration 00070 (aditiva, `Down` reversível): `projects.environment_id`, `runs.environment_id|environment_source`,
  `persona_readiness`, `model_reports`, e recria `environments_driver_check` e `alerts_kind_check` com a lista vigente + o
  valor novo. Outras migrations que estenderem `alerts_kind_check` precisam recriar a constraint com `persona_not_ready`.
- Variáveis novas: `HANKO_ENV_CLOUD`, `HANKO_ENV_CLOUD_API_URL`, `HANKO_ENV_CLOUD_SSH_HOST`, `HANKO_ENV_CLOUD_SSH_KNOWN_HOSTS`,
  `HANKO_READINESS_INTERVAL`.
- O alvo cloud depende do gateway do provider estar acessível por SSH a partir do servidor e do `known_hosts` correto.
- O readiness periódico executa `sh` e (se existir) `claude auth status`/`codex login status` no host do servidor a cada
  intervalo por persona não pronta/antiga: custo baixo, mas é um processo filho a mais; personas `fake` são puladas.
- Um bloqueio por readiness é persistente até o próximo teste: se o login for consertado, o tick seguinte (≤ intervalo) ou o
  "Testar agora" libera.

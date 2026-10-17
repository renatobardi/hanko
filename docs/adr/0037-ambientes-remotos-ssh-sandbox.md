# ADR-0037 — Ambientes de execução remotos: SSH e sandbox
Data: 2026-10-16 · Status: aceita

## Contexto
Toda run era um processo filho do servidor do Hanko, no mesmo host e com o mesmo usuário (ADR-0017). A recomendação desde
então é isolar os agentes do servidor, mas não havia como: a tela Configurações › Ambientes (ADR-0031) só mostrava o local e
dizia que ambientes remotos estavam no roadmap. Duas necessidades aparecem juntas: tirar as runs da máquina do servidor (um host
de build com as ferramentas da equipe, uma máquina com GPU) e confinar uma run num contêiner descartável, sem acesso ao `.env`,
ao banco nem à rede do servidor.

## Decisão
1. **Modelo.** `environments` (por empresa, migration 00033): nome, slug, descrição, driver `local|ssh|sandbox`, estado
   `active|archived`, `config` jsonb sem segredos, `secret_enc` (chave privada e valores das variáveis, AES-256-GCM pelo cofre das
   integrações, AAD = id do ambiente), `is_default` (no máximo um por empresa, índice único parcial; só ativo), relatório do
   último teste (`last_probe_at/ok/report`), autor e datas. O ambiente **local é implícito** (sem linha) e é o padrão quando
   nenhum outro está marcado; cadastrar `local` é recusado. `personas.environment_id` (FK `ON DELETE SET NULL`).
   `environment_leases` (empresa, ambiente, run, `active|released|failed`, diretório remoto, motivo da falha) registra o uso.
2. **Precedência.** O ambiente de uma run é o da persona; senão o padrão da empresa; senão o local. Vale para todas as runs da
   persona (task, chat, revisão, curadoria). Arquivar um ambiente leva, na mesma transação, as personas dele para outro ambiente
   ativo ou para o padrão; uma persona que ainda aponte para um arquivado cai no padrão com aviso no log.
3. **Interruptores do operador** (padrão desligado; quem administra uma empresa não liga sozinho):
   - `HANKO_ENV_SSH_ALLOW_HOSTS`: hosts exatos, sufixos (`.exemplo.com`, `*.exemplo.com`), IPs, redes CIDR (só para hosts
     informados como IP; nome nunca é resolvido na checagem, porque o ssh resolveria de novo) ou `*`. Vazio = SSH desligado.
     Criar, editar, testar e rodar fora da lista é recusado com a variável no texto do erro.
   - `HANKO_ENV_SANDBOX=docker` e o binário `docker` no PATH. Sem isso o driver aparece como indisponível, com o motivo.
   - `HANKO_ENV_SANDBOX_HOST_ROOT`: quando o Hanko roda dentro de um contêiner que fala com o docker do host, o caminho no host do
     diretório que aqui é `HANKO_WORKSPACES_ROOT` (os volumes do sandbox usam caminhos do host). Com ele, runs sem workspace usam
     um diretório temporário sob `HANKO_WORKSPACES_ROOT/.hanko-sandbox`, e um workspace fora da raiz é recusado.
4. **Alvo de execução.** `adapter.Spec.Exec` (interface `adapter.ExecTarget`: `procrun.Target` + diretório de trabalho no alvo +
   diretório auxiliar local/remoto + descrição); nil = local, e nada muda. `procrun.Options.Target` pede ao alvo o comando local
   que embrulha o harness (`Prepare`) e, num cancelamento, chama `Abort` depois de matar o grupo local. No alvo só vão as variáveis
   da run, da persona e do ambiente (`TargetEnv`); o ambiente do servidor nunca vai. Os adapters gravam a config MCP e as skills
   no diretório auxiliar e passam o caminho remoto: `claude` (`--mcp-config`, `--add-dir`), `codex` (`-C` no diretório remoto),
   `acp` (`cwd` remoto; o cliente deixa de oferecer `fs/*`, porque os arquivos estão no alvo, e o agente usa o sistema de arquivos
   de lá) e `fake` (roda `pwd` de verdade no alvo, e `touch`/`remote_sleep_sec` no `adapter_config` exercitam a sincronização e o
   cancelamento sem rede nem modelo). O código de execução remota fica em `internal/execenv`.
5. **SSH.** Binário `ssh` do sistema, por argv (sem shell local), com `-F none` (nada da config do usuário), `BatchMode=yes`,
   `StrictHostKeyChecking=yes` com `UserKnownHostsFile` só da sessão (o `known_hosts` é obrigatório; chave desconhecida nunca é
   aceita), `GlobalKnownHostsFile=/dev/null`, `IdentitiesOnly=yes`, `IdentityAgent=none`, chave num arquivo temporário 0600
   apagado no fim, `ConnectTimeout=10`, `ServerAliveInterval=15`, sem encaminhamentos, `-T`, e `HOME` descartável. Chaves com
   senha não são aceitas (modo batch). O comando remoto é montado com aspas simples POSIX: `cd <work> && echo $$ > .hanko/pid &&
   set -a && . .hanko/env.sh && set +a && rm -f .hanko/env.sh && exec <bin> <args>`. Os valores das variáveis (run token,
   segredos do ambiente) vão num arquivo lido por `.` e apagado em seguida, nunca na linha de comando remota. O shell de login do
   usuário remoto precisa ser POSIX (sh, bash, dash, zsh).
6. **Workspace remoto.** `<pasta remota>/<empresa>/<workspace|_sem-workspace>/<run>/work` (0700 na pasta da run) recebe a cópia
   do diretório da run, `.git` inclusive, num tar gerado pelo Hanko e extraído no host com `tar -xpf -` pelo stdin do ssh; os
   auxiliares vão em `.hanko/`. No fim, `tar -cf - .` no host volta pelo stdout e é extraído aqui com `os.Root` (nenhum caminho
   absoluto, `..` ou link para fora sai do diretório); o que subiu e não voltou é apagado localmente (o agente apagou lá), e
   arquivos criados localmente durante a run ficam. Tetos de 2 GiB e 200 mil entradas por sentido. Falha ao subir = a run falha
   antes do harness; falha ao trazer de volta = a run falha com o motivo. Política de limpeza: run ok apaga a pasta da run no host;
   run com falha (ou sincronização com falha) mantém `work/` para diagnóstico e apaga só `.hanko/` (onde estava o token).
7. **Cancelamento.** Sem terminal, o processo remoto pode sobreviver ao fim do canal ssh. O cancelamento mata o processo ssh local
   e depois roda, por outra conexão, `kill -TERM` (2 s depois `-KILL`) no grupo do pid guardado em `.hanko/pid`. O `-tt` não é
   usado: ele misturaria stdout e stderr e quebraria o stream JSON dos harnesses.
8. **Sandbox (docker).** `docker run --rm -i --name hanko-run-<run> --network none|bridge --memory/--memory-swap --cpus
   --pids-limit --cap-drop ALL --security-opt no-new-privileges --read-only --tmpfs /tmp --user <uid:gid do servidor>` com o
   workspace montado em `/work` (leitura e escrita) e os auxiliares em `/hanko` (só leitura), `HOME=/tmp`, e as variáveis só por
   nome (`-e NOME`, o valor no ambiente do processo docker). Rede padrão `none`; imagem sem digest `@sha256:` é aceita com aviso.
   Cancelar faz `docker kill`; o fim garante `docker rm -f`. O timeout é o da run.
9. **MCP no ambiente remoto.** O harness remoto alcança o Hanko pela URL pública da instalação (`HANKO_PUBLIC_URL`), não pela
   `HANKO_AGENT_URL` (que costuma ser loopback): o executor troca a base dos servidores MCP do Hanko e da memória e o `HANKO_URL`.
   Sem URL pública (ou com loopback) um ambiente SSH, ou um sandbox com rede, recusa rodar com mensagem clara; o sandbox sem rede
   roda com aviso de que não alcança o MCP nem a API do modelo. Servidores MCP `stdio` da empresa não vão para o ambiente remoto
   (o comando e os segredos dele são desta máquina), com aviso no log. As credenciais do harness (login do claude/codex, chaves de
   API) são as do host remoto, ou as variáveis do ambiente/persona no sandbox.
10. **Teste de conexão.** SSH: conecta, `uname -sm`, `mkdir -p` e escrita na pasta remota, `command -v` de `tar` (obrigatório),
    `git`, `claude` e `codex`. Sandbox: `docker version` e um `docker run --rm --network none` da imagem com o mesmo script. O
    relatório (passos, sistema, binários, avisos, duração) é gravado no ambiente; o rascunho do formulário pode ser testado sem
    gravar (`POST /environments/probe`).
11. **Segredos.** A chave privada e os valores das variáveis só entram (API, UI, CLI por arquivo ou stdin, nunca por flag); a
    API, a UI, os eventos e a atividade mostram só `key_set` e os nomes. Os valores entram na redação do log da run. A rotação de
    `HANKO_SECRET_KEY` (`hanko integration rekey`) também reescreve os ambientes.
12. **Papéis (ADR-0021).** Todo membro lê ambientes, usos e relatórios; criar, editar, testar, arquivar, escolher o padrão e o
    ambiente da persona é de admin. Run tokens não usam os endpoints de ambiente.
13. **Superfícies.** API `/companies/{c}/environments` (CRUD, `DELETE` arquiva com `move_personas_to`), `…/{e}/probe`,
    `…/{e}/leases`, `…/probe` (rascunho), `PUT …/default`; `PATCH /personas/{p}` aceita `environment`. CLI `hanko env
    list|show|create|update|probe|leases|default|rm` e `hanko persona update --environment`. UI em Configurações › Ambientes (o
    local com as verificações reais, a escolha do padrão, a lista com Testar, os formulários de SSH e sandbox com teste de
    rascunho, a página do ambiente com relatório, execuções recentes, edição e arquivamento com "Mover as personas para o
    ambiente…"), o cartão "Ambiente de execução" na página da persona e a linha "Ambiente" na página da execução. Cada run remota
    também registra no log "executando no ambiente …".
14. **Export/import (ADR-0022).** Ambientes não viajam no `hanko.company.v1`: são infraestrutura da instalação (hosts, chaves,
    o docker do operador) e dependem dos interruptores dela; a persona importada usa o padrão da empresa de destino.

## Consequências
- Um ambiente remoto tira a run do host do servidor, mas o harness remoto tem o run token e alcança a API/MCP do Hanko pela URL
  pública: a fronteira de confiança do ADR-0017 passa a ser o host remoto, que precisa ser de confiança da empresa.
- A sincronização por tar copia o workspace inteiro a cada run (sem incremental); workspaces grandes pagam o tempo de cópia e o
  teto de 2 GiB. Duas runs remotas no mesmo workspace trabalham em cópias separadas; a última a voltar vence nos arquivos que as
  duas mudaram.
- O sandbox depende do docker local (ou do socket do host); podman e outros motores ficam para depois. Não há fila nem limite de
  runs simultâneas por ambiente além dos workers do Hanko.
- Um uso que fica `active` porque o servidor caiu no meio aparece como órfão na tela; o diretório remoto dele fica no host até
  alguém apagar.
- O `Dockerfile.agents` passa a trazer `openssh-client`; a imagem distroless não tem `ssh` nem `docker`, então ambientes remotos
  exigem a imagem com agentes ou o binário no host.

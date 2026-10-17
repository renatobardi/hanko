# RUNBOOK — Hanko

> English: [docs/en/RUNBOOK.md](en/RUNBOOK.md)

Operação do dia a dia. Decisões em `docs/adr/` (0007/0014 auth, 0008 deploy, 0011 Postgres, 0013 OTel, 0015 memória).

## 1. Rodar local em 5 minutos

Pré-requisitos: Go 1.27, Docker com Compose v2.

```bash
cp .env.example .env
# edite .env: POSTGRES_PASSWORD, AI_MEMORY_IMAGE (digest fixado), AI_MEMORY_AUTH_TOKEN (= HANKO_MEMORY_TOKEN)
# ajuste HANKO_DATABASE_URL com a mesma senha

docker compose -f deploy/compose.yaml --env-file .env up -d postgres ai-memory otel-lgtm
make dev          # sobe o Hanko local em http://127.0.0.1:7420 (migrations rodam no boot)
```

- UI: <http://127.0.0.1:7420> · API/docs: `/docs` · métricas: `/metrics`
- Grafana (traces, logs, métricas): <http://127.0.0.1:3000> (LGTM; login inicial `admin`/`admin`)
- Tudo em container (inclui o Hanko): `make up`; logs: `make logs`; parar: `make down`.
- Sem ai-memory: `HANKO_MEMORY_PROVIDER=none`.
- Os CLIs `claude`/`codex` precisam estar no PATH da máquina onde o Hanko roda (ou em `HANKO_CLAUDE_BIN`/`HANKO_CODEX_BIN`).
  A imagem padrão (distroless) não os inclui; use `Dockerfile.agents` para rodar agentes dentro do container.

Testes: `export HANKO_TEST_DATABASE_URL=postgres://postgres:...@127.0.0.1:5432/hanko_test?sslmode=disable; make test`
(testes de integração fazem skip sem a variável).

**Idioma da UI.** A interface web fala pt-BR (padrão) e inglês. O idioma vem do cookie `hanko_lang` (gravado pelo seletor no rodapé da barra lateral e na tela de entrada, via `POST /idioma`) e, sem ele, do `Accept-Language` (`en*` é inglês, qualquer outro é pt-BR). Dados do usuário, logs de execução e erros da API não são traduzidos.

**Idioma da CLI.** A CLI (`hanko …`) também fala pt-BR (padrão) e inglês, incluindo o `--help`, tabelas, mensagens e prompts. O idioma é resolvido uma vez, na partida: `HANKO_LANG` (`pt-BR` ou `en`) vence; sem ele valem `LC_ALL`, `LC_MESSAGES` e `LANG`, nessa ordem (o primeiro que estiver definido decide; `en*` é inglês, qualquer outro valor é pt-BR); sem nenhuma, pt-BR. Ex.: `HANKO_LANG=en hanko task list`. Em inglês, datas saem como `aaaa-mm-dd hh:mm` e valores como `$1.00`. Mensagens vindas da API ou do domínio, a saída `--json` e as mensagens de erro de parsing do próprio cobra (flag desconhecida etc.) não são traduzidas. Novos textos da CLI entram em `internal/i18n/en_cli.go` (mesma regra do catálogo da UI: a chave é o texto em pt-BR) e o teste `TestCatalogCoversSources` cobre UI e CLI.

## 2. Deploy em LXC (oute-server)

**Produção (caminho atual):** `deploy/oute-server/deploy-prd.sh` — roda no Mac, segue as convenções do repo `lab`
(`docs/oute-server-app-setup.md`): LXC `hanko-prd` não privilegiado com nesting em `10.173.117.43`, `limits.memory` 4GiB,
app em `/opt/app` com o `docker-compose.yml` da raiz (porta `3810`), `.env` gerado dentro do LXC, borda pública por
nginx + certbot no host (`--edge`, mesmo padrão de `lab/scripts/ops/deploy-vault-vhost.sh`; o vhost escuta também em
`100.66.254.24:443` por causa do split DNS da tailnet). O container e o vhost estão no `inventory.yaml` do lab (#280).
ai-memory sobe no profile `memory` (`COMPOSE_PROFILES=memory` no `.env`); o digest fica fixado em `AI_MEMORY_IMAGE` no
primeiro deploy — para atualizar, troque essa linha no `.env` do LXC e rode o deploy de novo.
O texto abaixo descreve a variante genérica com `deploy/compose.yaml`.


Dois containers: `hanko-stg` (branch `develop`) e `hanko-prd` (tag `v*`). Acesso via Tailscale (split-horizon, como no runbook do kura-dev).

1. No oute-server: `deploy/lxc/setup.sh hanko-stg` (cria o container Ubuntu 24.04 com Docker, restic e psql).
2. Copie a stack: `lxc file push -r deploy .env hanko-stg/opt/hanko/` e ajuste o dono (`chown -R hanko:hanko /opt/hanko`).
3. Preencha `/opt/hanko/.env`:
   - `HANKO_VERSION` = tag (prd) ou `develop` (stg); `POSTGRES_PASSWORD`; `HANKO_SECRET` (`openssl rand -hex 32`);
   - `AI_MEMORY_IMAGE` por digest; `AI_MEMORY_AUTH_TOKEN`; `HANKO_PUBLIC_URL=https://hanko.oute.pro` (prd);
   - `HANKO_DOMAIN`;
   - borda (prd): no modo `trusted`, `HANKO_EDGE_USER` e `HANKO_EDGE_PASSWORD_HASH` (veja "Borda com basic_auth" abaixo); no modo
     `authenticated`, `HANKO_CADDYFILE=Caddyfile.authenticated` e as variáveis do §5 (sem basic_auth: quem autentica é o Hanko).
4. Suba: `cd /opt/hanko && docker compose -f deploy/compose.yaml --env-file .env up -d` (prd com borda TLS: `--profile edge`).
5. Verifique: `curl -fsS http://127.0.0.1:7420/healthz` e `/readyz`; abra o Grafana por túnel (porta 3000 só em loopback).
6. Atualizar versão: ajuste `HANKO_VERSION`, `docker compose pull hanko && docker compose up -d hanko`. Migrations rodam no boot
   (`HANKO_MIGRATE_ON_BOOT=true`); para rodar à parte: `docker compose run --rm --entrypoint /hanko hanko migrate`.
7. Rollback: volte `HANKO_VERSION` e suba de novo. Migrations são append-only; rollback de schema = restore do backup.

Segredos nunca entram no git: `.env` fica só no container.

### Borda com basic_auth (modo trusted)

No modo `trusted` o Hanko não autentica humanos: quem alcança a porta age como operador. Por isso o
`deploy/caddy/Caddyfile` exige `basic_auth` e remove o `Authorization` antes de repassar ao Hanko. Gere o hash bcrypt:

```bash
docker run --rm caddy:2 caddy hash-password --plaintext 'SENHA-FORTE'   # ou: caddy hash-password
```

No `.env`, `HANKO_EDGE_USER=operador` e `HANKO_EDGE_PASSWORD_HASH=...` com **cada `$` do hash escrito como `$$`** (o compose
interpola `$`). Troque a senha regerando o hash e executando `docker compose up -d caddy`. Sem as duas variáveis o Caddy recusa
subir (config inválida), de propósito. Com o modo `authenticated`, use o §5 e dispense o basic_auth.

### Endurecimento do modo trusted

- `HANKO_SECRET` com menos de 32 bytes é recusado no boot; vazio gera um por boot.
- O cabeçalho `Host` só vale para `localhost`, `127.0.0.1`, `[::1]`, o host de `HANKO_PUBLIC_URL` e `HANKO_ALLOWED_HOSTS`
  (csv); outros recebem `421`. Atrás de proxy/túnel com outro nome, acrescente-o em `HANKO_ALLOWED_HOSTS`.
- Agentes não herdam o ambiente do servidor: só `PATH, HOME, USER, LANG, LC_*, TERM, TMPDIR, TZ, SHELL, SSL_CERT_*`,
  proxies, prefixos `ANTHROPIC_ CLAUDE_ OPENAI_ CODEX_ OTEL_ NODE_ XDG_ GIT_ GH_` e o que `HANKO_AGENT_ENV_ALLOW` (csv, aceita
  prefixo com `*`) liberar. `HANKO_*` do servidor nunca passa.
- Saída dos agentes é varrida para segredos (run token, Bearer, JWT, chaves de API, senhas em URL de banco) antes de virar log de run.
- Run tokens só valem enquanto o run está `queued`/`running`.
- Memória: o agente fala com `<HANKO_AGENT_URL>/memory-mcp` (padrão = `HANKO_PUBLIC_URL`; no prd, loopback interno `http://127.0.0.1:7420`, sem depender de DNS/TLS público) usando o run token; o Hanko injeta `HANKO_MEMORY_TOKEN` e o escopo
  (empresa/workspace) do lado do servidor. O token do ai-memory nunca vai ao agente.
- Workspaces só podem apontar para dentro da subpasta da empresa na raiz, `HANKO_WORKSPACES_ROOT/<slug-da-empresa>/...` (raiz padrão `$HOME/hanko-workspaces`), com caminho absoluto e limpo; a raiz é compartilhada entre empresas e a subpasta as isola. Na execução o caminho é revalidado (symlinks resolvidos): fora da raiz, a run usa um diretório temporário e deixa um aviso.
- Orçamento: antes de abrir uma run, o Hanko soma o gasto do mês, a reserva de cada run `queued`/`running` e a reserva da nova;
  se passar do teto da persona ou da empresa, a run não abre. A reserva de uma run é o maior entre a média de custo das últimas
  10 runs da persona e `HANKO_RUN_RESERVE_USD` (padrão `1.00`). Persona pausada por orçamento volta sozinha quando o teto sobe
  ou o mês vira. Uma recusa só pela reserva (nada foi pausado) tira a persona da rede de segurança do heartbeat por 10 min, para não
  gerar uma tempestade de wakes; "Rodar agora" (wake manual) continua tentando. Quem define o status da persona explicitamente (ex.:
  Pausar) assume a pausa: o motivo de orçamento é apagado e a retomada automática não a desfaz.
- Limites e recomendação de isolamento: `docs/adr/0017-modo-trusted-e-limites.md`. O modo `authenticated` (§5) remove o limite principal.

## 5. Modo authenticated (login, sessões e API keys)

`HANKO_MODE=authenticated` exige login para tudo, exceto `/healthz`, `/readyz` e `/static`:

| Rota | Quem entra |
|---|---|
| UI (`/`, `/c/...`) | sessão (cookie `hanko_session`); sem sessão, redireciona para `/entrar` |
| `/api/v1/*` | sessão (métodos que mudam estado exigem mesma origem), **API key** (`Authorization: Bearer hk_…`) ou run token de persona |
| `/mcp` | run token ou API key (cookie não vale) |
| `/memory-mcp` | run token |
| `/metrics` | conexão loopback direta (sem `X-Forwarded-For`) ou API key |

Recusa de boot: `HANKO_SECRET` com menos de 32 bytes (ou vazio) e `HANKO_PUBLIC_URL` que não seja `https` (exceto `localhost`/loopback).
O cookie leva `Secure` quando `HANKO_PUBLIC_URL` é https. Sessão: token aleatório de 32 bytes (só o hash SHA-256 vai ao banco),
validade de 30 dias **deslizante** (renovada no máximo 1×/min) com **teto absoluto de 90 dias** desde o login (depois disso, entrar de novo
mesmo em uso contínuo), `HttpOnly`, `SameSite=Lax`; trocada a cada login. Sair: menu do rodapé da sidebar.

**IP do cliente e proxies.** O rate limit de login e o IP gravado na sessão usam o IP do cliente. `X-Forwarded-For` só é aceito quando o
par da conexão está em `HANKO_TRUSTED_PROXIES` (CIDRs ou IPs, separados por vírgula; padrão `127.0.0.1/32,::1/128`); a cadeia é lida da
direita para a esquerda e vale o primeiro endereço que não é de proxy confiável. Fora da lista, ou com a lista vazia, vale o `RemoteAddr`.
**Atrás do Caddy do compose** (que fala com o hanko pela rede docker) acrescente a sub-rede do compose, senão todos os clientes parecem
ter o IP do Caddy: `docker network inspect <projeto>_hanko -f '{{(index .IPAM.Config 0).Subnet}}'` e então, por exemplo,
`HANKO_TRUSTED_PROXIES=127.0.0.1/32,::1/128,172.18.0.0/16` (o `deploy/compose.yaml` repassa a variável). Não liste redes que clientes
não confiáveis alcancem.

**Custo de senha.** Hash e verificação argon2id (64 MiB cada) passam por um semáforo de `max(2, nº de CPUs)` vagas; sem vaga em 2 s o
login responde 503 com `Retry-After` ("tente de novo") e não conta como falha.

### 5.1 Provider `local` (padrão, `HANKO_AUTH_PROVIDER=local`)

E-mail e senha (argon2id, 64 MiB, 3 passadas), com limites de falhas a cada 15 min (em memória; reiniciam com o processo): 5 por IP+e-mail,
25 por IP e **20 por e-mail somando todos os IPs** (força bruta distribuída), este com backoff progressivo (a partir da 6ª falha do e-mail,
espera de 1 s, 2 s, 4 s… até 5 min).

```bash
export HANKO_MODE=authenticated HANKO_SECRET=$(openssl rand -hex 32) HANKO_PUBLIC_URL=https://hanko.exemplo.com
hanko migrate
read -rs HANKO_OWNER_PASSWORD && export HANKO_OWNER_PASSWORD      # mínimo de 10 caracteres
hanko auth create-owner --email voce@exemplo.com --name "Seu Nome"
hanko serve
```

**Crie o dono pela CLI antes de expor o servidor.** O formulário web "Criar conta de dono" (quem chegar primeiro vira dono) só existe quando
`HANKO_PUBLIC_URL` é loopback (`localhost`/`127.0.0.1`) ou `HANKO_AUTH_WEB_BOOTSTRAP=true`. Em URL pública, sem usuários, `/entrar` só orienta
a rodar `hanko auth create-owner` e `POST /entrar/dono` responde 404. Só existe um dono no v0.1 (multi-humano é v0.2); todos os usuários podem tudo.

### 5.2 Provider `firebase` (`HANKO_AUTH_PROVIDER=firebase`)

Login com Google e e-mail/senha pelo Firebase Auth. O servidor **não usa o Admin SDK**: o navegador autentica com o SDK web, envia o
ID token para `POST /auth/firebase` (só mesma origem) e o Hanko valida o JWT RS256 com as chaves públicas do Google
(`securetoken@system.gserviceaccount.com`, cache conforme `Cache-Control`, refresh quando o `kid` é novo; se o refresh falhar, as chaves em cache
valem por até 1 h depois de vencidas, com aviso no log, e depois disso o login falha fechado com 503), `iss`/`aud` do projeto,
`exp`/`iat`/`auth_time`, `sub` e `email_verified`; então abre a sessão própria. Revogar = expirar a sessão do Hanko.

1. Console do Firebase: crie o projeto (ou reuse um) e registre um **app Web**; anote `projectId`, `apiKey` e `authDomain`
   (são valores públicos, não segredos).
2. Authentication → Sign-in method: habilite **Google** e **E-mail/senha**.
3. Authentication → Settings → **Authorized domains**: acrescente o domínio do Hanko (ex.: `hanko.oute.pro`); `localhost` já vem.
4. No `.env` do Hanko:
   ```bash
   HANKO_MODE=authenticated
   HANKO_PUBLIC_URL=https://hanko.oute.pro
   HANKO_SECRET=...                       # openssl rand -hex 32
   HANKO_AUTH_PROVIDER=firebase
   HANKO_FIREBASE_PROJECT_ID=meu-projeto
   HANKO_FIREBASE_API_KEY=AIza...
   HANKO_FIREBASE_AUTH_DOMAIN=meu-projeto.firebaseapp.com
   HANKO_AUTH_ALLOWED_EMAILS=voce@exemplo.com,outra@exemplo.com   # e/ou
   HANKO_AUTH_ALLOWED_DOMAINS=exemplo.com                         # e/ou
   HANKO_AUTH_OWNER_EMAIL=voce@exemplo.com                        # pelo menos um dos três é obrigatório em URL pública
   ```
5. Borda: `HANKO_CADDYFILE=Caddyfile.authenticated` (sem basic_auth, repassa o `Authorization`) e `docker compose --profile edge up -d`.

**Política de admissão.** Com `HANKO_AUTH_ALLOWED_EMAILS` e/ou `HANKO_AUTH_ALLOWED_DOMAINS`, só entra quem estiver nas listas, e sair da lista
**derruba na hora** as sessões e API keys desse usuário (checado a cada requisição). O primeiro a entrar vira `owner`; os demais, `member`.
`HANKO_AUTH_OWNER_EMAIL` nomeia o dono: só esse e-mail vira `owner` (e passa mesmo fora das listas); os demais só entram se estiverem nas
listas, como `member`.

**Recusa de boot.** Com `HANKO_PUBLIC_URL` fora de loopback, o provider `firebase` exige lista de admissão **ou** `HANKO_AUTH_OWNER_EMAIL`:
sem nenhum dos dois o primeiro visitante que fizesse login viraria dono, então o servidor não sobe. Só em loopback, sem listas nem dono
nomeado, vale o modo de desenvolvimento: o primeiro a entrar vira dono e os demais são recusados (o boot avisa).
Exige `email_verified`: quem cria conta por e-mail/senha recebe o link de verificação e precisa confirmá-lo antes de entrar.
Um e-mail já cadastrado por outro método (ex.: dono local) não é vinculado automaticamente: é recusado (409).
`hanko auth create-owner` não se aplica ao provider firebase.

### 5.3 API keys e CLI

Crie pela tela **Configurações** (menu do rodapé da sidebar) ou, direto no banco, sem o servidor no ar:

```bash
hanko auth token create --name notebook      # imprime a chave uma única vez (hk_...); --email se houver mais de um usuário
hanko auth token list
hanko auth token revoke <id>
export HANKO_URL=https://hanko.exemplo.com HANKO_TOKEN=hk_...
hanko task list -c acme                       # a CLI envia HANKO_TOKEN como Bearer
```

Só o hash SHA-256 da chave fica no banco (mais um prefixo para identificá-la). Revogada, a chave deixa de valer na hora.
Os comandos `hanko auth` usam `HANKO_DATABASE_URL` (e o restante da configuração, incluindo `HANKO_SECRET` no modo authenticated).

### 5.4 Agentes e o modo authenticated

Run tokens continuam funcionando (API, `/mcp`, `/memory-mcp`). Agentes locais que **omitiam o token** e falavam com o loopback como se
fossem o humano (ADR-0017) passam a receber **401**; use o run token (já injetado pelo Hanko) ou uma API key dedicada.

### 5.5 PWA instalável

A UI é instalável (manifest em `/manifest.webmanifest`, service worker em `/sw.js`, ícones em `/static/icons/`) e mostra uma página
`/offline` quando a rede cai. O service worker só guarda assets estáticos versionados (`/static/…?v=`): HTML autenticado, API, SSE,
`/auth/*`, `/convite/*` e qualquer POST nunca passam pelo cache. Exige https (ou `localhost`); manifest, `sw.js` e `/offline` são públicos
(sem dado algum). Cada deploy muda o `sw.js` (versão do binário), o que atualiza o worker e descarta o cache antigo. Se o app instalado
parecer desatualizado: recarregue duas vezes ou, nas ferramentas do navegador, "Unregister" do service worker. Regerar os PNG a partir do
selo: `python3 scripts/gen-pwa-icons.py` (Pillow).

## 3. Backup e restore

### 3.1 Produção (`hanko-prd`, oute-server)

Duas camadas, que falham de formas diferentes:

| Camada | O quê | Quando | Onde |
|---|---|---|---|
| Local (LXC) | `deploy/oute-server/backup.sh`: `pg_dump -Fc` do banco `hanko` + tar do volume do ai-memory (container pausado durante o tar, para o índice SQLite sair consistente) | diário ~01:30 UTC (timer `hanko-backup`, instalado pelo `deploy-prd.sh`) | `/var/backups/hanko` no LXC, 3 gerações + `SHA256SUMS` |
| Lab (restic) | `pg_dump` do banco feito pelo próprio lab + o diretório acima (`backup_paths`) | semanal, sábado 02:00 (`oute-backup-weekly`) | restic em `/var/backups/restic-oute` no host + sync offsite; alerta no Kuma |

O timer roda antes do sábado 02:00, então o restic leva uma cópia local fresca. O `agent-home` (login do claude/codex) e o
`lgtm` (telemetria) **não** entram no backup: login se refaz (§ "Login do Claude" ao fim do deploy) e a telemetria é descartável.
A fonte da verdade de governança está toda no Postgres (ADR-0015, regra 5); o ai-memory é recall (não é fonte de verdade),
mas o tar dele é guardado para não perder o histórico de sessões.

Verificar (no Mac):

```bash
ssh oute-server "lxc exec hanko-prd -- systemctl list-timers hanko-backup.timer --no-pager"
ssh oute-server "lxc exec hanko-prd -- bash -lc 'systemctl start hanko-backup.service && ls -l /var/backups/hanko && cd /var/backups/hanko && sha256sum -c SHA256SUMS'"
```

**Drill mensal (não toca no prd):** restaura o dump mais novo num banco descartável `hanko_restore_check`, imprime o nº de
tabelas e a última migration aplicada, e apaga o banco.

```bash
ssh oute-server "lxc exec hanko-prd -- /opt/app/deploy/oute-server/restore.sh --check"
# saída esperada: tabelas=<N> / migration=<versão> / "drill ok (prd intocado)"
```

**Restore a partir da cópia local (mesmo LXC):** para o hanko e o ai-memory, renomeia o banco atual para `hanko_pre_restore_<ts>`
(nunca o dropa), restaura o dump num banco novo `hanko`, repõe o volume do ai-memory, sobe a stack e espera o `/readyz`. Se o
`pg_restore` falhar, o nome antigo volta e a stack sobe sozinha. Só depois do readyz o script mostra o comando para descartar
o banco antigo (`DROP DATABASE hanko_pre_restore_<ts>`). Pede confirmação (`--yes` dispensa).

```bash
ssh -t oute-server "lxc exec hanko-prd -- /opt/app/deploy/oute-server/restore.sh --apply"
ssh oute-server "lxc exec hanko-prd -- curl -fsS http://127.0.0.1:3810/readyz"
# uma geração específica:
#   restore.sh --apply --pgdump /var/backups/hanko/hanko-<UTC>.pgdump --aimemory /var/backups/hanko/aimemory-<UTC>.tar.gz
```

**Voltar um deploy que trouxe migrations (código + banco):** o `restore.sh --apply` sobe a stack com o código que estiver em
`/opt/app` e com o `HANKO_VERSION` do `.env`. Se isso ainda for a versão nova, o boot migra o banco restaurado para a frente de
novo e nada volta. Para voltar de verdade, primeiro o código e a tag, depois o banco (o dump pré-deploy é o mais novo de `/var/backups/hanko`):

```bash
ssh oute-server "lxc exec hanko-prd -- bash -lc 'cd /opt/app && git reset --hard <rev-anterior> && sed -i \"s/^HANKO_VERSION=.*/HANKO_VERSION=<rev-anterior>/\" .env && docker image ls hanko'"
ssh -t oute-server "lxc exec hanko-prd -- /opt/app/deploy/oute-server/restore.sh --apply"
```

Se a imagem `hanko:<rev-anterior>` ainda existe no LXC, o `up -d` do restore a usa sem recompilar; senão ele compila (alguns minutos).
Atalho sem restaurar dados: o binário anterior sobe sobre o schema novo (as migrations 00008–00013 só acrescentam tabelas, colunas
com valor padrão e índices) — é o caminho mais rápido, mas o banco fica na versão nova e os dados criados depois do deploy continuam lá.

**Restore a partir do restic do lab (LXC perdido ou cópia local corrompida):** no oute-server, como root (precisa do
`/etc/oute/backup.env` do lab para o repositório e a senha do restic). Recrie o LXC com `deploy-prd.sh` primeiro (ele sobe a stack
vazia), depois:

```bash
. /etc/oute/backup.env; export RESTIC_REPOSITORY RESTIC_PASSWORD
restic snapshots --tag hanko-prd | tail
restic dump latest containers/hanko-prd/hanko.pgdump > /tmp/hanko.pgdump       # dump do próprio lab
restic dump latest containers/hanko-prd/files/hanko.tar > /tmp/hanko-local.tar  # cópia local + memória
mkdir -p /tmp/hanko-local && tar xf /tmp/hanko-local.tar -C /tmp/hanko-local && ls /tmp/hanko-local
lxc file push /tmp/hanko.pgdump hanko-prd/root/hanko.pgdump
lxc file push /tmp/hanko-local/aimemory-*.tar.gz hanko-prd/root/aimemory.tar.gz   # se existir
lxc exec hanko-prd -- /opt/app/deploy/oute-server/restore.sh --apply --yes \
  --pgdump /root/hanko.pgdump --aimemory /root/aimemory.tar.gz
rm -f /tmp/hanko.pgdump /tmp/hanko-local.tar; rm -rf /tmp/hanko-local
```

(`--aimemory` é opcional; sem ele só o banco é restaurado. O nome exato do arquivo no restic está em `restic ls latest --tag hanko-prd`.)
Depois do restore: `/readyz` ok, login pelo Firebase, e registre data e resultado em `docs/SESSION.md`. O `.env` (segredos) **não**
está em nenhum backup: ele é gerado dentro do LXC na primeira vez; ao recriar o LXC, o `POSTGRES_PASSWORD` novo vale porque o banco
restaurado é recriado pelo próprio postgres do compose, mas `HANKO_SECRET` novo invalida sessões e API keys emitidas — guarde uma
cópia do `.env` no cofre depois do primeiro deploy.

### 3.2 Variante genérica (compose de dev/stg)

Backup diário (cron do usuário `hanko` no container): `pg_dump` + `restic` para o bucket R2.

```bash
# /opt/hanko/backup.sh  (exporte RESTIC_REPOSITORY=s3:https://<conta>.r2.cloudflarestorage.com/<bucket>,
# RESTIC_PASSWORD, AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY no ambiente do cron)
set -euo pipefail
cd /opt/hanko
docker compose -f deploy/compose.yaml exec -T postgres pg_dump -U "$POSTGRES_USER" -Fc "$POSTGRES_DB" > /tmp/hanko.dump
restic backup /tmp/hanko.dump --tag hanko-db
# ai-memory: volume com wiki em git + índice SQLite
docker run --rm -v hanko_aimemory:/data:ro -v /tmp:/out alpine tar czf /out/aimemory.tgz -C /data .
restic backup /tmp/aimemory.tgz --tag hanko-memory
rm -f /tmp/hanko.dump /tmp/aimemory.tgz
restic forget --tag hanko-db --tag hanko-memory --keep-daily 7 --keep-weekly 4 --keep-monthly 6 --prune
```

Cron: `17 3 * * * /opt/hanko/backup.sh >> /var/log/hanko-backup.log 2>&1`.

**Teste de restore (1x por mês, em `hanko-stg`):**

```bash
restic restore latest --tag hanko-db --target /tmp/restore
docker compose -f deploy/compose.yaml exec -T postgres createdb -U "$POSTGRES_USER" hanko_restore
docker compose -f deploy/compose.yaml exec -T postgres pg_restore -U "$POSTGRES_USER" -d hanko_restore --no-owner < /tmp/restore/tmp/hanko.dump
docker compose -f deploy/compose.yaml exec -T postgres psql -U "$POSTGRES_USER" -d hanko_restore -c "select count(*) from companies; select max(version_id) from goose_db_version;"
docker compose -f deploy/compose.yaml exec -T postgres dropdb -U "$POSTGRES_USER" hanko_restore
```

Registre data e resultado em `docs/SESSION.md` ou no issue de operação.

## 4. Troubleshooting

| Sintoma | Causa provável | O que fazer |
|---|---|---|
| Hanko não sobe: `HANKO_DATABASE_URL é obrigatório` | env/yaml sem URL | defina `HANKO_DATABASE_URL`; confira `HANKO_CONFIG` |
| `db: ping após 5 tentativa(s)` | Postgres fora/senha errada | `docker compose ps postgres`, healthcheck, senha no `.env` |
| Aviso `falha de exportação OpenTelemetry` | collector (LGTM) fora | **não derruba o app**: dados de telemetria ficam sem destino até voltar. `docker compose up -d otel-lgtm`; confira `OTEL_EXPORTER_OTLP_ENDPOINT`. `/metrics` segue funcionando (pull) |
| Aviso `ai-memory indisponível no boot` | ai-memory fora ou token errado | app **degrada**: runs abrem sem briefing, fatos ficam em `memory_outbox` e drenam quando voltar. `docker compose logs ai-memory`; confira `HANKO_MEMORY_URL`/`HANKO_MEMORY_TOKEN` |
| Aviso `HANKO_SECRET não definido` | secret gerado por boot | defina `HANKO_SECRET`; sem isso, run tokens de runs em andamento invalidam a cada restart |
| Runs ficam `queued` | heartbeat parado, `Workers` baixo ou CLI do agente ausente | log do hanko; `HANKO_WORKERS`; `claude`/`codex` no PATH (ou `Dockerfile.agents`) |
| `/mcp` responde 401 de fora | modo trusted só aceita sem token em loopback | normal: agentes usam run token; humanos usam a API/UI |
| `421 host não permitido` | cabeçalho `Host` fora da lista | acrescente o nome em `HANKO_ALLOWED_HOSTS` ou ajuste `HANKO_PUBLIC_URL` |
| `HANKO_SECRET tem N bytes` | secret curto | `openssl rand -hex 32` |
| `path: must be inside the workspaces root` ao criar workspace | caminho fora de `HANKO_WORKSPACES_ROOT/<slug-da-empresa>` | use um caminho dentro da pasta da empresa na raiz ou mude a raiz |
| Agente não enxerga uma variável de ambiente | env filtrado (allowlist) | `HANKO_AGENT_ENV_ALLOW=NOME` (ou `adapter_config.env` numa persona `acp`) |
| Run `acp` falha com "não respondeu ao initialize" | o comando não fala ACP v1 por stdio (ou demora mais que `init_timeout_sec`) | rode o comando à mão; o erro traz as últimas linhas de stdout/stderr (§ Agentes ACP) |
| Run `acp` termina em segundos sem editar nada | política `read_only` (padrão) recusou a edição | veja os eventos "permissão negada" no log; contrate com `permission: allow` se for o caso |
| `HANKO_SECRET exige…` / `HANKO_PUBLIC_URL exige https` no boot | modo authenticated sem secret de 32 bytes ou com URL http fora de loopback | `openssl rand -hex 32`; use `https://…` (ou loopback para teste local) |
| `/entrar` mostra "Criar conta de dono" | banco sem usuários | `hanko auth create-owner` (§5.1) antes de expor; em URL pública o formulário só existe com `HANKO_AUTH_WEB_BOOTSTRAP=true` |
| Boot recusado: `HANKO_AUTH_PROVIDER=firebase com HANKO_PUBLIC_URL público exige…` | firebase sem listas nem dono nomeado | defina `HANKO_AUTH_ALLOWED_EMAILS/DOMAINS` ou `HANKO_AUTH_OWNER_EMAIL` (§5.2) |
| Login 503 "Muitos logins ao mesmo tempo" | todas as vagas de argon2id ocupadas por 2 s | tente de novo; se persistir, há rajada de logins (confira o rate limit na borda) |
| Rate limit de login bloqueia todo mundo junto atrás do Caddy | `HANKO_TRUSTED_PROXIES` sem a sub-rede do compose | inclua a sub-rede docker (§5) |
| Firebase: "Este e-mail não tem acesso" | fora de `HANKO_AUTH_ALLOWED_EMAILS/DOMAINS` (ou lista vazia e já há um dono) | ajuste as listas (§5.2) |
| Firebase: "domínio não autorizado" no popup | domínio fora de Authorized domains | Console → Authentication → Settings |
| Firebase: 503 `Não foi possível validar o login` | servidor sem acesso a `www.googleapis.com` | libere a saída HTTPS do servidor para o JWKS do Google |
| Agente local recebe 401 depois de ligar o modo authenticated | omitia o token (ADR-0017) | use o run token injetado ou uma API key (§5.4) |
| Porta 7420 ocupada | outra instância | `HANKO_ADDR` |

Logs são JSON no stderr com `trace_id`/`span_id`: copie o `trace_id` e busque no Tempo (Grafana) para ver o trace completo da requisição/run.

## Memória consolidada (curadoria governada)

Requer `HANKO_MEMORY_PROVIDER=ai-memory` (ADR-0030). Com `none` a seção "Memória" das Configurações mostra "Memória desligada" e a API responde 422 com o motivo.

- **Ligar:** Configurações da empresa → Memória (ou `hanko memory config --enable --interval 24 --curator <persona>`). O intervalo vai de 1 a 720 h; desligada por padrão. Prefira um curador com modelo barato.
- **Rodar agora:** botão "Consolidar agora" ou `hanko memory consolidate [workspace]`. Cria uma task de sistema por workspace; o custo entra no ledger e no orçamento como qualquer run. Uma rodada aberta por workspace; a que nunca rodou expira em 6 h.
- **Gate `memory`:** remoções, fusões e reescritas da curadoria não chegam ao ai-memory; viram proposta e um gate na aba "Memória" do Inbox (diff e sha256). Aprovar enfileira `apply_memory_proposal`; rejeitar descarta. CLI: `hanko memory proposals|approve|reject <id>`.
- **Falha ao aplicar:** a proposta fica `failed` com o erro e é tentada de novo (1 min a 6 h, 20 tentativas). Depois disso: `hanko memory retry <id>` ou o botão "Tentar de novo". Nada se perde.
- **Concessões:** uma persona só alcança a memória do próprio workspace. Para outro workspace da empresa: `hanko memory grant <persona> <workspace> --access read|write` (`grants`, `revoke` para listar e remover).
- **Diff com conteúdo atual:** o padrão é `memory_read_page` (tool real do ai-memory); `HANKO_MEMORY_READ_TOOL` (`memory.read_tool` no YAML) troca o nome; sem nenhuma o diff mostra só o proposto.

| Sintoma | Causa provável | Ação |
|---|---|---|
| "Memória desligada" / 422 ao ligar | `HANKO_MEMORY_PROVIDER=none` | configurar o ai-memory e reiniciar |
| Proposta `failed` | ai-memory fora do ar ou recusou | ver o erro na seção Memória; o retry é automático, ou `hanko memory retry` |
| Muitas propostas | tool de nome desconhecido é retida por segurança | aprovar/rejeitar; ver ADR-0030 |
| Consolidação não roda | curador pausado ou sem orçamento | ver Inbox/alertas; expira em 6 h |

## Alertas operacionais

O job periódico `alert_tick` (mesmo período do `heartbeat_tick`, `HANKO_HEARTBEAT_INTERVAL`) compara as condições de agora com os
alertas abertos: abre o que apareceu, atualiza o que continua e **fecha sozinho** quando a condição some. Há no máximo um alerta
aberto por (empresa, tipo, sujeito), então avaliar de novo nunca duplica. Cada abertura e fechamento é um fato de atividade
(`alert.opened`, `alert.closed`). Os alertas abertos aparecem na Caixa de entrada (aba Alertas) e no gauge
`hanko_alerts_open{kind=...}` de `/metrics` (um ponto por tipo, zero incluído).

| Tipo (`kind`) | Condição | Limite (padrão) | Severidade |
|---|---|---|---|
| `persona_silent` | persona `active`/`idle` com `heartbeat_interval_sec > 0` e ao menos uma task trabalhável (`todo`/`in_progress`) sem run ativa nem gate pendente, cuja última atividade (criação, fim da última run ou última mudança humana numa task dela) passou de N× o intervalo dela. Persona barrada por reserva de orçamento fica de fora (o alerta de orçamento a cobre) | `HANKO_ALERT_SILENT_FACTOR` (3) | warning |
| `run_stuck` | run em `running` sem nenhum evento no log (ou, sem eventos, desde o início) há mais que o limite | `HANKO_ALERT_RUN_STUCK_AFTER` (15m) | critical |
| `budget_near_limit` | gasto do mês em 80% ou mais do teto da empresa ou da persona (mesma conta do painel de custos, no fuso da empresa); subir o teto ou virar o mês fecha | fixo em 80% | warning; critical a partir de 100% |

O critério é sempre estado do banco (runs, eventos, tasks, gastos), nunca texto de log. O livro `budget_alerts` segue como o
registro de "já avisei neste mês" que gera `budget.warning`/`budget.exceeded`; o alerta aberto é o estado atual, que o livro não guarda.

## Revisão por outro modelo antes do gate

Persona com `require_review` pode ter um **revisor automático**: uma outra persona (`--reviewer <persona>`) ou um harness/modelo
alternativo no mesmo papel (`--reviewer-agent codex --reviewer-model <modelo>`; ex.: autora `claude`, revisor `codex`). São exclusivos
entre si e a persona não revisa a si mesma. Sem revisor configurado nada muda: o gate `task_review` abre direto.

Com revisor, quando a persona pede para fechar a task, o Hanko registra uma revisão e o job `start_review` espera a run da autora
terminar (uma run ativa por task), então abre uma run headless `trigger=review`, somente leitura e sem credenciais do Hanko, que recebe
a task, o histórico e a entrega. A resposta deve ter a linha `VEREDITO: APROVADO` ou `VEREDITO: REPROVADO` seguida da justificativa.
O gate nasce com o parecer (veredito, justificativa, sha256 do pacote revisado e a run). Reprovado aparece marcado na UI, na CLI e na
API, mas **a decisão continua humana**. Revisão que falha, expira, é interrompida ou não traz veredito válido abre o gate sem veredito,
com o motivo; revisão pendente há mais de 2 h é resolvida da mesma forma. Se a task sai de `in_review` no meio, a revisão é cancelada.
O sha256 cobre o pacote task + histórico + entrega mostrado ao revisor, não o estado git do workspace.

## Coordenação de tasks (ADR-0040)

- **Dependências.** `hanko task dep add HNK-12 HNK-7` faz a 12 depender da 7: ela só é despachada quando todas as dependências fecham (done **ou**
  cancelled) e o responsável é acordado na mesma transação em que a última fecha. Sem ciclo (409 com a cadeia) e sem cruzar empresas; no
  máximo 50 por task. "Bloqueada" é calculado; o status `blocked` segue sendo o parado-esperando-alguém. A persona usa `hanko_task_dependency`
  e `depends_on` em `hanko_task_create`.
- **Árvore.** Subtask = `--parent`. O pai acorda quando o último filho fecha e não conclui com filho aberto (422); um humano conclui mesmo assim com
  `hanko task status HNK-1 done --force --reason "…"` (o motivo fica no fato `task.close_override`). Teto de 10 níveis.
- **Pausa.** `hanko task hold HNK-1 --reason "…"` congela a task e toda a subárvore: o despacho recusa, a rede de segurança do heartbeat ignora,
  as runs ainda na fila são canceladas e a que já roda termina sozinha. `hanko task resume HNK-1` acorda os wakes que foram recusados. A Caixa tem a
  aba **Pausadas**. Quem retoma é a raiz da pausa (uma filha que só herda aponta a origem).
- **Política de revisão por task/projeto.** `hanko task review-policy HNK-1 --require --reviewer-agent codex` (admin); `--no-reviewer` manda direto
  ao gate humano; `--clear` volta a herdar. `hanko project review-policy <projeto>` vale para as tasks sem sobreposição. A tela da execução mostra
  a origem (task, projeto ou persona) da política que valeu.
- **Operação.** Migration 00100 aditiva (tabelas novas, 2 triggers de integridade, 1 função); o `Down` desfaz. Contador OTel `hanko.task.coordination`
  (`kind`, `op`) e spans `service.hold_task`, `service.resume_task`, `service.add_task_dependency`, `service.remove_task_dependency`, `service.checkout_run`.
  Limitações: o export/import (`hanko.company.v1`) leva a árvore (e recusa mais de 10 níveis), mas não as dependências, pausas nem políticas por task.

## Skills e integrações (ADR-0029)

Configuração do operador (a empresa não consegue ligar sozinha):

| Variável | Padrão | Efeito |
|---|---|---|
| `HANKO_SECRET_KEY` | vazio | Chave (mínimo 32 caracteres) que cifra os segredos das integrações (AES-256-GCM). Vazia: o Hanko sobe, mas recusa guardar segredos de integração. |
| `HANKO_SECRET_KEY_PREVIOUS` | vazio | Chave anterior, só durante a rotação. |
| `HANKO_INTEGRATIONS_ALLOW_STDIO` | `false` | Libera servidores MCP `stdio`, que executam um comando **na máquina do servidor**. |
| `HANKO_INTEGRATIONS_ALLOW_PRIVATE` | `false` | Libera webhooks e testes de conexão para loopback, link-local e redes privadas (sem isso, bloqueio SSRF). |
| `HANKO_SKILL_SOURCES` | `false` | Liga as fontes git de skills (ADR-0035): o servidor clona repositórios públicos por https com o `git` do PATH. Hosts internos só com `HANKO_INTEGRATIONS_ALLOW_PRIVATE`. |

**Gerar a chave:** `openssl rand -hex 32`. Guarde-a junto com o `HANKO_SECRET` e no backup do operador; perder a chave perde os
segredos guardados (as integrações ficam, sem credenciais, e é só informá-los de novo).

**Rotacionar a chave:**
1. Gere a nova. Ponha a nova em `HANKO_SECRET_KEY` e a antiga em `HANKO_SECRET_KEY_PREVIOUS`; reinicie.
2. `hanko integration rekey` (ou `POST /api/v1/integrations/rekey-secrets`; só o dono da instalação): reescreve todos os
   segredos com a chave atual e imprime quantos.
3. Tire `HANKO_SECRET_KEY_PREVIOUS` e reinicie. Enquanto ela estiver definida, segredos antigos continuam abrindo.

**Webhooks.** Cada entrega leva `X-Hanko-Signature: t=<unix>,v1=<hex>`, `X-Hanko-Event` e `X-Hanko-Delivery`. O receptor calcula
`HMAC-SHA256(segredo, "<t>.<corpo>")`, compara em tempo constante e descarta `t` muito antigo. Falhou: 7 tentativas
(30 s, 2 min, 8 min, 32 min, ~2 h, 6 h). É entrega ao menos uma vez; deduplique por `X-Hanko-Delivery`. Histórico em
`/integracoes/<slug>` ou `hanko integration deliveries <slug>`.

**Problemas comuns.** "destino não permitido": o endereço resolve para rede interna (ligue `HANKO_INTEGRATIONS_ALLOW_PRIVATE`
se for proposital). "HANKO_SECRET_KEY não está configurada": defina a chave. MCP stdio ignorado na run: falta
`HANKO_INTEGRATIONS_ALLOW_STDIO=true`.
## Agentes ACP (harness `acp`)

O harness `acp` (ADR-0023) roda qualquer agente que fale o Agent Client Protocol v1 por stdio. O Hanko não traz agente algum: o
operador instala o programa e a persona aponta para ele.

**Ligar o harness (decisão do operador).** Quem escreve o comando da persona é o admin de uma empresa, e o comando roda na máquina do
servidor com os privilégios do Hanko. Por isso o harness vem **desligado**: com `HANKO_ACP_ALLOW=false` (padrão), contratar ou importar
uma persona `acp` dá 422 ("o harness acp está desligado neste servidor") e uma persona antiga falha ao rodar com o mesmo motivo. Ligue
com `HANKO_ACP_ALLOW=true` e, de preferência, restrinja com `HANKO_ACP_COMMANDS=meu-agente,/opt/agentes/outro` (nome simples, resolvido
pelo `PATH`, ou caminho absoluto; com a lista definida, só esses executáveis valem). A checagem acontece na contratação/importação e de
novo no início de cada run (uma config que mudou depois não escapa).

**Instalar o agente.** O executável precisa existir no `PATH` do processo do Hanko (ou ser usado por caminho absoluto) e estar
autenticado fora do Hanko (autenticação interativa não é suportada). O `Dockerfile` oficial (distroless) não tem como rodar agentes;
use a variante `Dockerfile.agents` ou uma imagem sua derivada dela, por exemplo (**exemplo ilustrativo**, o pacote é fictício):

```dockerfile
FROM minha-imagem-hanko-agents
USER root
RUN npm install -g pacote-do-agente-acp && npm cache clean --force
USER node
```

Sem container, instale o agente no host/LXC do servidor (npm global, binário em `/usr/local/bin`, etc.) com o mesmo usuário que roda o Hanko.
Credenciais do agente (chave de API etc.) entram por `HANKO_AGENT_ENV_ALLOW` (variáveis do servidor) ou por `adapter_config.env` (na persona; vale também para personas `claude`, e é o que permite apontar só uma persona a outro endpoint compatível, ex.: `{"env":{"ANTHROPIC_BASE_URL":"http://host:8000","ANTHROPIC_AUTH_TOKEN":"…","ANTHROPIC_MODEL":"…"}}`; as variáveis da run vencem e os valores saem mascarados na API/CLI).

**Contratar uma persona ACP.** Na UI, escolha o harness ACP e preencha comando, argumentos e permissões. Pela CLI (listas e objetos
precisam de `--config-json`) ou pela API (`adapter_config`):

```sh
# EXEMPLO: troque "meu-agente-acp" pelo comando real do seu agente
hanko persona hire "Externo" --agent acp \
  --config-json '{"command":"meu-agente-acp","args":["--stdio"],"permission":"read_only"}'
```

`permission`: `read_only` (padrão) aprova leitura, busca, raciocínio e consulta externa (`read`, `search`, `think`, `fetch`) e recusa
edição, remoção, mover, executar e o resto; `allow` aprova tudo (inclusive executar comandos: use só com agentes e workspaces em que você
confia); `reject` recusa tudo. A escrita por `fs/write_text_file` só é aceita com `allow`. Runs de revisão ignoram a configuração e usam `read_only`.
Cada decisão aparece no log da run como evento "permissão concedida/negada".

**Limites conhecidos.**
- O confinamento ao diretório do workspace vale para o que passa pelo Hanko (`fs/*`). O processo do agente tem os privilégios do usuário que
  roda o Hanko; para isolar de verdade, rode o Hanko num container/usuário sem acesso ao que o agente não deve ver.
- O servidor MCP do próprio Hanko é http; um agente que não anuncia `mcpCapabilities.http` roda sem as tools `hanko_*` (há um aviso no log).
- Sem terminal (`terminal/*`), sem autenticação interativa, sem ACP v2. Mudar o comando de uma persona exige contratar outra.
- Cancelar uma run envia `session/cancel` e espera até 3 s; o agente que não obedece é morto (grupo de processos inteiro).
- Uma persona ACP pode ser **persona revisora**, mas `acp` não pode ser o harness alternativo de revisão (`--reviewer-agent`).
- Exportar uma empresa remove de `adapter_config` chaves e flags com credencial; depois de importar, reponha a chave do agente.

**Diagnóstico.** Erros comuns da run: "não respondeu ao initialize em N s" (o comando não fala ACP por stdio; teste-o à mão e veja as linhas
de stdout/stderr no erro), "versão do ACP incompatível" (o agente só fala outra versão do protocolo), "o agente exige autenticação"
(autentique-o fora do Hanko) e "não foi possível iniciar o agente ACP" (comando fora do `PATH` do Hanko).

## Projetos e workspaces

Um **workspace** é um diretório (opcionalmente com `repo_url` e branch padrão, só metadado) e um **projeto** agrupa tasks, aponta
para um workspace e pode ter orçamento mensal (ADR-0025). Telas `/projetos` e `/workspaces`; CLI `hanko project …`, `hanko workspace …`,
`hanko task create --project`, `hanko cost --project`, `hanko budget set --project`.

- **Pasta da run:** workspace da task > workspace do projeto > workspace da persona > diretório temporário. A origem fica em
  `runs.workspace_source` (`task|project|persona|none`).
- **Caminho:** absoluto, limpo e dentro de `HANKO_WORKSPACES_ROOT/<slug-da-empresa>` (workspaces antigos fora da pasta da empresa seguem funcionando e a UI avisa). Se o diretório não existe na máquina a UI mostra "diretório ausente"
  e as runs usam um temporário; crie a pasta (o Hanko não faz `git clone`).
- **Excluir workspace em uso** dá 409; `--unlink` (CLI), `?unlink=true` (API) ou a caixa na UI desvinculam personas, projetos e tasks.
- **Orçamento do projeto:** estourou, a run é recusada com o motivo, sem pausar a persona; ≥ 80% abre alerta na Caixa de entrada. Suba o
  teto (`hanko budget set --project <slug> <usd>`) para liberar. O custo pago (mensalidade) não é rateado por projeto.
- **Importar de outra máquina:** caminho que esta instalação não aceita entra vazio e a contagem `workspace_paths_cleared` avisa;
  ajuste com `hanko workspace update <slug> --path …`.

## Chat com uma persona

Conversa direta de um humano com uma persona, fora de uma task (ADR-0027). Na UI: **Chat** no menu (`/c/{empresa}/chat`). Na CLI:

```
hanko chat cto "Qual o risco da migração de sexta?"          # abre a conversa, espera e imprime a resposta
hanko chat cto "E o plano B?" --conversation 01a13f51          # continua uma conversa (id ou prefixo)
hanko chat cto "Resuma o dia" --no-wait                        # só envia; acompanhe com `hanko chat show <id>`
hanko chat list --persona cto
```

- Cada mensagem humana dispara uma **run** com o gatilho `chat`: aparece em Execuções (identificada como Chat, ligada à conversa)
  e em Custos (linha "Chat"), e conta no orçamento da persona e da empresa e na trava semanal da assinatura.
- Persona pausada, em erro, sem orçamento ou com a trava semanal atingida **não recebe a mensagem**: a UI, a API (409) e a CLI mostram o motivo.
  Reative a persona ou suba o limite e envie de novo.
- Mensagens enviadas enquanto a persona responde esperam (até 20) e a run seguinte as responde juntas. Uma run de chat que falha,
  é cancelada ou interrompida deixa um aviso de sistema na conversa; as mensagens em espera seguem na fila até a próxima mensagem.
- Com `claude` (e `fake`) a sessão do harness é retomada entre as mensagens; harness sem essa capacidade recebe o histórico recente
  (até 24 KiB) no prompt a cada mensagem.
- A persona pode abrir uma task a partir da conversa (`hanko_task_create`); a descrição cita a conversa. Não há task na run de chat,
  então `hanko_gate_request` exige o argumento `task`.
- Ver o raciocínio de uma resposta: abra o link "execução" da mensagem (log ao vivo da run).

## Sala com várias personas

Uma conversa com várias personas ao mesmo tempo (ADR-0033). Na UI: **Sala** no menu (`/c/{empresa}/sala`). Na CLI:

```
hanko room create --persona cto,pm,design "Planejar a semana"   # abre a sala (2 a 12 personas)
hanko room say 01a14700 "@cto qual o risco da migração?"          # envia, espera as respostas e as imprime
hanko room set 01a14700 --mode all --max-hops 1                   # todas respondem; uma persona chama no máximo uma outra
hanko room add 01a14700 cfo; hanko room remove 01a14700 design
```

- Quem responde: as personas citadas com `@slug`; sem citação, a **moderadora** (modo `mentions`, o padrão) ou todas (modo `all`).
  Cada resposta é uma run com o gatilho `chat`, no orçamento da persona e da empresa e na trava semanal.
- Uma persona pausada, sem orçamento ou com a trava atingida não derruba as outras: a sala mostra "Fulana não vai responder: motivo".
  Se ninguém pode responder, a mensagem é recusada com o motivo (409 na API).
- Uma persona que cita outra na resposta a acorda. O freio: `max_hops` (padrão 2 saltos a partir da sua mensagem) e
  `max_runs_per_message` (padrão = número de personas). Passou do limite, a sala avisa e ninguém mais roda; cite de novo numa
  mensagem sua para continuar.
- Mensagens para uma persona que está respondendo esperam a run dela terminar (até 20) e entram juntas na próxima.
- O painel **Feed do time** ao lado mostra a atividade recente da empresa (atualiza a cada 10 s).

## Artefatos e metas

**Artefatos** (ADR-0028) são arquivos com versões imutáveis: o mesmo nome na mesma task cria a versão seguinte; publicar o mesmo
conteúdo de novo não cria versão. Tela `/artefatos` (filtros por projeto, task, persona, tipo e nome; envio; detalhe com prévia,
versões e diff de texto), seções nas páginas da task, da execução e do projeto. Papéis: viewer lê, member publica, admin exclui
(a cadeia inteira, sem lixeira).

| Variável | Padrão | Efeito |
|---|---|---|
| `HANKO_ARTIFACT_MAX_BYTES` | 5 MiB (de 1 a 16 MiB) | Tamanho máximo de um arquivo. |
| `HANKO_ARTIFACT_QUOTA_BYTES` | 512 MiB | Cota da empresa, somando todas as versões. |

- **Publicar:** UI (envio na tela), `hanko artifact put <arquivo|-> [--task HNK-1]` (também `list`, `get <id> [-o arquivo] [--info]`,
  `diff <id> [--against <id>]`, `rm <id>`, `usage`), API `…/artifacts` e a tool MCP `hanko_artifact_put` (texto em `content`, binário em
  `content_base64`). A persona publica só na própria task e execução.
- **Entrega segura:** o conteúdo nunca vira página. Só texto puro, Markdown e imagem raster (png, jpeg, gif, webp) abrem no
  navegador; HTML, SVG, PDF e o resto baixam. A resposta leva `nosniff`, CSP com `sandbox` e `Cross-Origin-Resource-Policy`.
- **Exportar com artefatos:** `hanko export --with-artifacts` (API `with_artifacts=true`); o conteúdo dos artefatos tem teto de
  20 MiB no arquivo. Sem a opção o arquivo sai como sempre; arquivos antigos importam normalmente. Metas não são exportadas.
- **Backup:** os arquivos estão no Postgres, então crescem o dump; confira `hanko artifact usage` antes de subir a cota.

**Metas** (ADR-0028): valor inicial, alvo (pode ser menor, para metas de redução) e valor atual; progresso = (atual − inicial) /
(alvo − inicial), de 0 a 100%. Registrar um valor que alcança o alvo marca a meta como atingida, grava a atividade `goal.achieved` e
mostra um item informativo na Caixa de entrada por 7 dias. Tela `/metas` (abas ativas, atingidas, abandonadas), página da meta com o
histórico, tasks ligadas (uma meta ativa por task) e o quadro "Metas" no projeto. CLI: `hanko goal create|list|show|update|progress`,
`hanko task create --goal`. As metas ativas entram no briefing da run, e a persona registra o valor medido com `hanko_goal_update`
(`hanko_goal_list` lista). Papéis: viewer lê; member e persona registram progresso e ligam tasks; admin cria, edita, abandona e reabre.

| Sintoma | Causa | Ação |
|---|---|---|
| Envio recusado: "passa do limite de N MiB por arquivo" | arquivo acima de `HANKO_ARTIFACT_MAX_BYTES` | divida o arquivo ou suba o limite (até 16 MiB) |
| Envio recusado: "cota de artefatos da empresa" | soma das versões acima de `HANKO_ARTIFACT_QUOTA_BYTES` | apague artefatos antigos (admin) ou suba a cota |
| Exportação com `--with-artifacts` recusada | conteúdo acima de 20 MiB | exporte sem a opção |
| HTML ou PDF baixa em vez de abrir | proposital: só texto, Markdown e imagem raster abrem | abra o arquivo baixado |
| Meta atingida não aceita progresso | status `achieved` | reabra (admin) trocando para ativa; recusado enquanto o valor atual já alcança o alvo |

## Busca (ADR-0024)

A busca usa full-text do Postgres sobre colunas `search_tsv` geradas a partir de `hanko_fold(texto)` (caixa, acentos, pontuação ASCII e
caracteres de formatação Unicode). Pontos de operação:

- **Locale do banco.** O banco **não pode estar em `lc_ctype=C`/`POSIX`**: nele `lower()` e o tokenizador só entendem ASCII, e a
  busca deixa de achar maiúsculas e letras fora do ASCII. Use um locale UTF-8 (`C.UTF-8`, `en_US.UTF-8`, `pt_BR.UTF-8`). O `hanko serve`
  consulta `pg_database.datctype` no boot e registra um aviso (`o banco está com lc_ctype=C`) se for o caso. Confira com
  `SELECT datctype FROM pg_database WHERE datname = current_database();`. Trocar o locale exige recriar o banco (dump/restore).
- **Alterar `hanko_fold` exige migration que recrie as colunas.** A função é usada por colunas geradas (`STORED`) e por índices GIN:
  `CREATE OR REPLACE FUNCTION` não recalcula as linhas existentes nem reindexa. Toda mudança na dobra (nova letra na tabela, outra
  regra) precisa derrubar índices e colunas `search_tsv`, trocar a função e recriar tudo, como faz a migration 00024; e a tabela
  equivalente em Go (`internal/search`) muda junto (o teste de paridade confere os dois lados).
- **Instalações grandes.** As migrations 00015 e 00024 reescrevem `tasks`, `task_comments`, `personas`, `runs` e `flows` e constroem
  os GIN com bloqueio de escrita (~25 s com 80 mil runs, 60 mil comentários e 30 mil tasks). Se a janela não couber, aplique a mão, antes
  de subir a versão, a variante concorrente: adicione a coluna gerada e crie cada índice com `CREATE INDEX CONCURRENTLY` (fora de
  transação) e só então marque a migration como aplicada. O `ADD COLUMN … GENERATED … STORED` ainda reescreve a tabela; o ganho é
  não segurar o bloqueio durante a construção do GIN.
- **Teto por consulta.** Cada grupo da busca roda numa transação somente leitura com `statement_timeout` de 2 s; estourar devolve 422
  ("a busca demorou demais; use palavras mais específicas"). A última palavra só casa por prefixo com 2+ letras (3+ em comentários).
- **Memória na busca.** O grupo "Memória" só aparece se o servidor de memória lista a tool `memory_query` (lido de `tools/list`, com
  cache de 5 min). Sem ela o grupo some e o log registra um aviso uma única vez; a consolidação (ADR-0030) não depende dessa tool.

## Plugins de terceiros (ADR-0038)

- Desligados até `HANKO_PLUGINS_DIR` apontar para um diretório local (um subdiretório por plugin, com o nome do
  `id`). Diretórios de trabalho em `HANKO_PLUGINS_DATA_DIR` (padrão `$HOME/hanko-data`); inclua-o no backup se os
  plugins guardarem arquivos lá. Protocolo e exemplo: `docs/PLUGINS.md`.
- Instalar, atualizar, aprovar capacidades e desinstalar: dono da instalação (Configurações › Plugins ou
  `hanko plugin …`). Cada plugin roda como processo filho, com ambiente mínimo e sem acesso ao banco; o Hanko não
  impede rede (`http.outbound` é só declarado). Para isolar de verdade, rode o Hanko num container sem o `.env`.
- Plugin com erro: `hanko plugin logs <id>` mostra estado, PID, reinícios, último erro e o stderr. O gerenciador
  reinicia sozinho com backoff (até 5 min entre tentativas); `hanko plugin disable <id> --global` para o processo
  sem perder a configuração das empresas.
- Segredos de plugin usam `HANKO_SECRET_KEY` e entram na rotação (`hanko integration rekey`).
## Ambientes de execução remotos (ADR-0037)

Por padrão toda execução é um processo filho deste servidor (o ambiente **local**). Um admin pode cadastrar, em
Configurações › Ambientes (ou `hanko env create`), ambientes **SSH** (um host remoto) e **sandbox** (um contêiner por execução),
escolher o padrão da empresa e o ambiente de cada persona. Cada driver só existe se o operador o ligar:

| Variável | Padrão | Efeito |
|---|---|---|
| `HANKO_ENV_SSH_ALLOW_HOSTS` | vazio | Hosts liberados para SSH: exatos (`build.exemplo.com`), sufixos (`.exemplo.com`, `*.exemplo.com`), IPs, CIDR (só para hosts informados como IP) ou `*`. Vazio = SSH desligado. |
| `HANKO_ENV_SANDBOX` | vazio | `docker` liga o sandbox (precisa do binário `docker` no PATH e acesso ao daemon). |
| `HANKO_ENV_SANDBOX_HOST_ROOT` | vazio | Hanko dentro de contêiner com o socket do docker do host: o caminho, **no host**, de `HANKO_WORKSPACES_ROOT`. |
| `HANKO_PUBLIC_URL` | — | Obrigatória e fora de loopback para SSH e sandbox com rede: é por ela que o harness remoto chama o MCP do Hanko. |
| `HANKO_SECRET_KEY` | vazio | Sem ela não há como guardar a chave privada nem as variáveis de um ambiente. |

**SSH, passo a passo.**
1. No host remoto: um usuário com shell de login POSIX (sh, bash, dash, zsh), `tar`, `git` e o harness instalado e já logado
   (`claude`, `codex` ou o agente ACP). As credenciais do harness são **as do host remoto**: o Hanko não leva as deste servidor.
   Confira que `command -v claude` funciona num `ssh usuario@host 'command -v claude'` (shell não interativo).
2. Gere uma chave **sem senha** só para o Hanko (`ssh-keygen -t ed25519 -N '' -f hanko_id`) e ponha a pública em
   `~/.ssh/authorized_keys` do host.
3. Pegue a chave do host: `ssh-keyscan -H build.exemplo.com` (confira a impressão digital por outro canal). O `known_hosts` é
   obrigatório; o Hanko roda com `StrictHostKeyChecking=yes` e nunca aceita uma chave desconhecida.
4. `HANKO_ENV_SSH_ALLOW_HOSTS=build.exemplo.com` no `.env` e reinicie. A imagem precisa do `ssh`: use `Dockerfile.agents` (traz
   `openssh-client`); a distroless não tem.
5. `hanko env create build --driver ssh --host build.exemplo.com --user hanko --root /srv/hanko --known-hosts-file kh
   --key-file hanko_id --probe` (ou pela tela, com "Testar conexão"), depois `hanko env default build` ou
   `hanko persona update <persona> --environment build`.

Cada execução copia o diretório da run (o workspace, `.git` inclusive) para `/srv/hanko/<empresa>/<workspace>/<execução>/work`,
roda o harness lá e traz o resultado de volta (arquivos apagados lá são apagados aqui). Execução ok apaga a pasta remota;
execução com falha mantém `work/` para diagnóstico (a tela do ambiente mostra o diretório e o motivo) e apaga só `.hanko/`. Limpe
pastas antigas no host quando quiser: `find /srv/hanko -mindepth 3 -maxdepth 3 -mtime +7 -exec rm -rf {} +`.

**Sandbox.** `HANKO_ENV_SANDBOX=docker`, a imagem com o harness (de preferência fixada por `@sha256:`) e, se o harness precisa da API
do modelo ou do MCP do Hanko, rede `bridge` (o padrão `none` isola por completo). O contêiner roda sem capacidades, com
`no-new-privileges`, raiz só leitura, `/tmp` em memória, `HOME=/tmp`, o usuário deste servidor e limites de memória, CPU e
processos; o workspace entra em `/work`. Chaves de API do harness vão nas variáveis do ambiente ou da persona (cifradas).
Com o Hanko em contêiner falando com o docker do host, monte o socket e defina `HANKO_ENV_SANDBOX_HOST_ROOT` com o caminho do host
de `HANKO_WORKSPACES_ROOT` (o mesmo volume montado nos dois lados).

**Problemas comuns.** "SSH está desligado neste servidor": falta `HANKO_ENV_SSH_ALLOW_HOSTS`. "não está na lista de hosts
permitidos": o host do ambiente não casa com a lista. `Host key verification failed`: o `known_hosts` do ambiente não tem a chave
atual do host (refaça o `ssh-keyscan` e edite o ambiente). `Permission denied (publickey)`: a pública não está no
`authorized_keys` ou a chave tem senha. "sem URL pública configurada": defina `HANKO_PUBLIC_URL` com um endereço que o host remoto
alcance. Uso "órfão" na tela: o servidor caiu no meio da execução; a pasta remota pode ter ficado no host.

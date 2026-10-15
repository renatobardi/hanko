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
- Memória: o agente fala com `<HANKO_PUBLIC_URL>/memory-mcp` usando o run token; o Hanko injeta `HANKO_MEMORY_TOKEN` e o escopo
  (empresa/workspace) do lado do servidor. O token do ai-memory nunca vai ao agente.
- Workspaces só podem apontar para dentro de `HANKO_WORKSPACES_ROOT` (padrão `$HOME/hanko-workspaces`), com caminho absoluto e limpo.
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
| `path: must be inside the workspaces root` ao criar workspace | caminho fora de `HANKO_WORKSPACES_ROOT` | use um caminho dentro da raiz ou mude a raiz |
| Agente não enxerga uma variável de ambiente | env filtrado (allowlist) | `HANKO_AGENT_ENV_ALLOW=NOME` |
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
- **Diff com conteúdo atual:** defina `HANKO_MEMORY_READ_TOOL` (`memory.read_tool` no YAML) com o nome da tool de leitura de página do ai-memory; sem ela o diff mostra só o proposto.

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

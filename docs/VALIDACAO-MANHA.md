# Validação da manhã

Roteiro único para o dono validar o que entrou durante a execução autônoma do plano de evolução (M7–M20 e trilha contínua). Cada marco aponta para o roteiro completo em `docs/validacao/`. Andamento por item em `docs/PROGRESSO.md`.

## 1. Estado geral
- 62 dos 70 itens `feito`, 5 `parcial`, 3 `fora` (decisão do plano). Nenhum `pendente` ou `bloqueado`.
- M7 a M20, trilha Já, O1 e Onda 2A estão mergeados na `main` (o ruleset exige `check`, `release-dry-run` e `gitleaks`). O atraso anterior do Actions foi a cota de minutos em repositório privado, resolvida quando o repo virou público.
- Ondas 2B (PR #34) e 3 (PR #37) estão em PR aberto; PR #37 aguarda CI. Os roteiros delas valem depois do merge.
- Nada foi implantado no `hanko-prd` (o `oute-server` não é acessível do ambiente da execução) e nenhuma tag nova foi publicada: ver seção 4.
- Quase tudo que toca o mundo externo foi testado contra fakes; a seção 3 lista exatamente o quê. Testes de integração rodaram em Postgres 16 local (o CI usa 17). As migrations (00035 a 00200) são aditivas e aplicam no boot.

## 2. O que validar, por marco
Pré-requisito comum: `HANKO_URL`/`HANKO_TOKEN` na CLI e uma empresa com uma persona (`ana`). Os comandos supõem `hanko -c <empresa>`.

**M7 — segredos, settings, BlobStore** (`docs/validacao/m7.md`)
- Boot com `HANKO_SECRET_KEY`: log `segredos: env das personas movido para segredos cifrados`; `hanko secret ls` lista `persona.*`.
- `printf '%s' "$T" | hanko secret create gw --value-stdin`; persona com `${secret:gw}` no env roda e o log da run não contém o valor. `hanko secret reveal gw` mostra uma vez e depois dá 409.
- Com um S3 de teste (`HANKO_BLOB_DRIVER=s3`): `hanko artifact put x.md --task HNK-1` cria o objeto no bucket; `hanko blobs migrate --from postgres --to s3 --dry-run`.

**M8 — revisões, anexos, rascunhos, chat** (`m8.md`)
- `hanko persona update ada --instructions "..." --reason t`, `persona history ada`, `persona restore ada 1`; a página da run mostra «Instruções rN · sha».
- Arrastar um arquivo no cartão Anexos da task; `hanko attach arq.pdf --task HNK-1`. Rascunho de comentário aparece em outro navegador. `/new` no chat mostra «Contexto reiniciado».

**M9 — política de tools e gateway** (`m9.md`)
- `hanko tool-policy set ask --persona ana --integration docs --tool apagar`; a task abre um gate `tool`; `hanko gate approve <id> --always`; a segunda task passa direto e a auditoria (`/c/<empresa>/ferramentas/auditoria`) mostra `policy:persona`.
- Tool nativa do claude em `ask` (`--native Bash`) espera o gate (10 min) e nega por prazo.

**M10 — workspace com Git** (`m10.md`; precisa de `HANKO_GIT_WORKSPACES=true`, `HANKO_WORKSPACES_ROOT`)
- `hanko workspace git enable loja --credential '${secret:repo-token}' --sync` → `Espelho: ready`.
- Task num projeto do workspace: o log mostra `worktree Git: branch hnk-<n>`; `hanko task git status|diff|log HNK-<n>`; ao concluir, o worktree some em até 5 min. `hanko task new-session HNK-<n>`.

**M11 — ambientes, readiness, modelos** (`m11.md`)
- Mesma persona em `local`, `sandbox` (docker) e `ssh` via `hanko persona update ana --environment <env>`; `hanko run show` traz «Ambiente (origem)». `hanko env probe <env>` mostra as capacidades.
- Persona `claude` em host sem login: `hanko persona test ana` → NÃO PRONTA e a task não despacha; depois do login, pronta. `hanko model list --agent claude`.

**M12 — decision model e harnesses** (`m12.md`)
- Segredo `openrouter`; Configurações › Decision model; `hanko decision triage HNK-N` comenta a sugestão e `hanko decision invocations` mostra o custo; bloco «Decision model» em Custos.
- `hanko persona hire "Dev" --agent devin` + `persona probe dev`; mesma para `--agent pi`, `cursor` e `http` (`--config-json` com `base_url`/`api_key_ref`).

**M13 — resiliência de run** (`m13.md`)
- `kill -9` do servidor com uma run ativa: sobe uma run `continuation_of_run_id` sozinha (máx. 2). Com `Bash` em curso ao morrer, vira «desfecho incerto» na Caixa e exige `hanko run continue <run> --note ...`.
- `hanko run steer <run> "foque nos testes"` (claude: «injeta ao vivo»; codex: «interrompe e continua»). `hanko followup add|list|edit|discard`.

**M14 — coordenação de tasks** (`m14.md`)
- Pai HNK-1 com filhos B e C usando `--parent` e `--depends-on`: só a primeira roda; fechar uma destrava a seguinte; `hanko task status HNK-1 done` falha (422) e passa com `--force --reason`.
- `hanko task hold HNK-1`: filho novo não abre run; banner «Pausada · herdada»; `hanko task resume HNK-1`.

**M15 — gates, contratação, incidentes** (`m15.md`)
- `hanko plan-policy set ana --scope persona --on`; a persona pergunta (`gate answer <id> --set k=v`), publica o plano, `gate request-changes`, reenvia; `hanko gate history <id>` mostra o diff e `gate approve` libera o run com «Plano aprovado (revisão 2)».
- `hanko persona hire Yuki --agent claude --gate`; `hanko persona primary ana`; `hanko budget incidents` / `budget resolve <id> --once|--raise 20|--keep`.

**M16a — Caixa por pessoa, visões, metas** (`m16a.md`; duas contas e `HANKO_SECRET_KEY`)
- Em Configurações › Notificações, «Ativar neste aparelho» e «Enviar aviso de teste» (o push real é o ponto não provado). `@bruno` num comentário gera item Menção na Caixa dele.
- `hanko inbox archive gate:<id>` limpa a Caixa sem decidir o gate. `hanko goal create ... --parent` e `goal show` com rollup.

**M16b — acesso, ciclo de vida, import** (`m16b.md`)
- `hanko -c <empresa> company archive` bloqueia escrita (409) e `company restore` devolve. `hanko import arq.json --dry-run --into <empresa>`.
- `hanko --url https://<instância> login` abre o navegador; `hanko whoami`; link de entrada em Configurações › Membros; `/admin/usuarios`.

**M17 — conexões OAuth, skills, pacotes de time** (`m17.md`)
- `hanko connection add "Docs" https://<mcp>/mcp` + `connection authorize docs`; gate «Conexão» quando a persona pede.
- `hanko skill fork <skill> --move-personas`; `hanko run skills <run>`. `hanko teams catalog|preview|install catalog:squad-de-produto` (personas entram pausadas).

**M18 — plugins** (`m18.md`)
- Instalar `examples/plugins/showcase`; `hanko plugin enable showcase --company <empresa>` mostra a aba e a tool `plugin_showcase_ping`; `hanko plugin cap revoke showcase pages|agent.tools` tira cada uma. `hanko plugin new meu-plugin` gera o esqueleto.

**M19 — canais, variáveis e gatilhos de fluxo** (`m19.md`)
- Fluxo com `--var repo!` e `hanko flow trigger add ... --kind webhook`: entrega assinada devolve 202, repetida 200 `duplicate`, assinatura errada 401, sem variável 422. Gatilho por evento `gate.*`.
- Telegram e GitHub reais: roteiro completo em `m19.md` (passos 4 a 6).

**M20 — etapas do projeto e cards de status** (`m20.md`)
- `hanko stage set site --stage ...`: a task anda triagem → dev → review → QA e só para nos gates (`hanko gate list --status pending`).
- `hanko status-card create "..." --engine decision --cron "0 7 * * 1-5" --dashboard` e `status-card refresh <id>`: o Painel mostra o card e `hanko artifact list` ganha uma versão por atualização.

**Trilha Já** (`trilha-ja.md`) — `scripts/test-release-notes.sh` → `release-notes: ok`; `scripts/release-notes.sh cut 0.3.0 --dry-run`; no GitHub, `ci`, `e2e`, `security` verdes e `docker pull ghcr.io/renatobardi/hanko:edge`.

**Trilha O1** (`trilha-o1.md`)
- Banco sem usuários: o link `/claim/<token>` cria o dono (reabrir dá 404); `hanko auth claim-link` recusa com usuários.
- Rate limit: baixar «Escritas por minuto» para 10 e disparar 12 POSTs: do 11º em diante 429 com `Retry-After`. Erro de navegador (`setTimeout(()=>{throw new TypeError("t")})`) aparece no painel «Hanko · Erros» (reiniciar o `otel-lgtm`).
- `/design-guide`, `/dev/ux/thread-longo`, `/dev/ux/run-10k` como dono; 404 para membro. `cd e2e && npm ci && npx playwright test`.

**Trilha Onda 2A** (`trilha-w2a.md`)
- Depois do deploy: `docker compose -f /opt/app/docker-compose.yml exec -T hanko /hanko doctor` (aviso esperado de `pg_dump`); `systemctl list-timers hanko-backup.timer`.
- Em host com PostgreSQL: `hanko backup create|list|verify`, `hanko backup restore latest --yes` com o servidor parado; `hanko serve --embedded-pg`; `hanko service install --dry-run`.

**Trilha Onda 2B** (`trilha-w2b.md`, depois do merge do PR #34)
- `scripts/test-install.sh`; `go run ./cmd/hanko-evals` verde e `--bad` vermelho (código 1); `hanko mcp` com o handshake do roteiro lista as tools `hanko_*`. Tap e evals reais dependem da seção 4.

**Trilha Onda 3** (`trilha-w3.md`, depois do merge do PR #37)
- Empresa nova cai em `/c/<slug>/primeiros-passos` (fechar a aba no meio e reabrir retoma); `hanko onboard --company-name Acme --harness fake --persona Ada --task "..."`.
- `hanko context add prd --url ... --token-stdin`, `hanko sandbox up|list|down` por branch; estrela de favorito e Recentes na sidebar; `/c/<slug>/perfil`; `/c/<slug>/novidades`; `make docs-site`.

**Varredura de segredos do repo público** (`2026-10-repo-publico.md`) — nenhum segredo verificado no histórico; nada a rotacionar; `.gitleaks.toml` e o job `gitleaks` fixos no `security.yml`.

## 3. Pulado / Falhou / Ressalvas
Nenhuma fatia foi revertida e nenhuma falhou duas vezes. Itens `parcial` e limites de verificação:

**Parciais**
- **#19 plan-review:** a fase só-leitura é imposta em claude e codex e nas tools de escrita do Hanko; em ACP ainda é possível escrever arquivo local (ADR-0041).
- **#40 gateway:** o endpoint único (`HANKO_TOOLS_SINGLE_ENDPOINT`) cobre só integrações MCP http; `hanko_*` e plugins seguem em `/mcp` (ADR-0053 §5). O codex não tem gancho de permissão para tool nativa.
- **#58 evals:** cenários de comportamento e chaos só rodaram contra roteiro de mentira; nenhuma chamada a modelo real. Só `HANKO_EVALS_LIVE=1` mede a qualidade do modelo.
- **#60 release:** tudo pronto, falta publicar as tags (seção 4).
- **#62 docs:** gerador e feature-map prontos; a hospedagem em `docs.hanko.oute.pro` ficou fora do escopo.

**Só testado contra fakes (precisa de validação real)**
- Telegram e GitHub reais (formato de payload em produção, limites de taxa) — M19; gates `question`, `plan`, contratação e instrução não têm botão no Telegram.
- Daytona (provider cloud) — só contra servidor de teste, o contrato pode divergir (`internal/execenv/daytona.go`); docker e SSH reais também não foram exercitados — M11.
- OAuth/MCP de terceiros (consentimento no navegador) — M17. Web push FCM/Mozilla/Apple — M16a.
- Binários reais de Pi, Cursor, Devin, gemini e opencode (fixtures e flags vieram da documentação) — M12; steer contra o binário real do claude — M13.
- Cofre externo (KV v2) e S3 reais (apenas fakes) — M7; o `pg_dump` não cobre objetos no bucket, que precisa de backup próprio.
- Navegador real: drag-and-drop de anexos (M8), telas de M10, M11, M14 (só testes de handler/HTML), iframe de plugins (M18), jornada Playwright de onboarding e perfil (Onda 3). O painel do Grafana foi validado como JSON/YAML, não num Grafana rodando (O1).
- Restore real e `install.sh`/cask contra release real: o restore foi exercitado só em Postgres 16 local (Onda 2A); `install.sh`, cask e `hanko mcp` contra um Hanko completo não foram (Onda 2B).
- Segredo de usuário não é resolvido em run de persona (a run não registra o solicitante) — M7.

**Limites conhecidos que valem uma olhada**
- No prd a imagem distroless não tem `pg_dump`: Configurações › Backups fica desligada e `hanko update` recusa contêiner. O backup do prd continua sendo `backup.sh` + restic.
- Sem push no Git do M10: o trabalho fica na branch do espelho. `HANKO_GIT_WORKSPACES` precisa ficar ligado para a limpeza rodar.
- Export/import não leva políticas de tools, configuração Git, dependências/pausas de task, etapas de projeto nem gatilhos de fluxo.
- Inconsistência de fonte: o plano marcou Cases como `fora` (#44), mas Pipelines e cases já existiam desde a paridade v0.3 (ADR-0034).

## 4. Ações do dono

### 4.1 Publicar as tags
O `v0.2.0` já está no remoto (`34978d2`). Do ambiente da execução, `git push` de tag dá HTTP 403; no Mac, na `main` atualizada (`git switch main && git pull --ff-only`):

```bash
git tag -a v0.1.0 9a5d13e -m "Hanko v0.1.0" && git push origin v0.1.0   # marco interno; opcional (o CHANGELOG diz «sem tag»)
git tag -a v0.3.0 2a9bbd8 -m "Hanko v0.3.0 — Onda 1 (M7–M9)" && git push origin v0.3.0
git tag -a v0.4.0 8801e28 -m "Hanko v0.4.0 — Onda 2 (M10–M13)" && git push origin v0.4.0
git tag -a v0.5.0 bc620ad -m "Hanko v0.5.0 — Onda 3 (M14–M16)" && git push origin v0.5.0
git tag -a v0.6.0 <HEAD da main> -m "Hanko v0.6.0 — Onda 4 (M17–M20)" && git push origin v0.6.0   # só depois do merge dos PRs #34 e #37
```

Alvos: o merge do último PR de cada onda na `main` (`2a9bbd8` = trilha O1 (#15), depois do M9; `8801e28` = M10 (#17); `bc620ad` = M15 (#25)). Se a tag local `v0.1.0` já existir (criada neste ambiente, aponta para `9a5d13e`), basta `git push origin v0.1.0`.

Atenção: o `release.yml` falha cedo se o `CHANGELOG.md` do commit tagueado não tiver a seção `## [X.Y.Z]`, e hoje só existe `[Unreleased]` com os fragmentos de `changelog.d/`. Os alvos acima (commits antigos) não têm seção. Duas saídas, escolher antes de taguear:
- **Uma release só (mais simples):** `scripts/release-notes.sh cut 0.6.0` num ramo `release/v0.6.0`, PR, merge e tag no HEAD; `v0.3.0`–`v0.5.0` ficam como marcos sem release.
- **Uma release por onda:** para cada alvo, `git switch -c release/vX.Y.Z <commit>`, `scripts/release-notes.sh cut X.Y.Z` (consome só os fragmentos que existem naquele commit), commit `-s` e tag nesse commit. Fluxo completo em `docs/RELEASING.md`.

Antes da primeira tag depois do merge do PR #34: criar o repositório `renatobardi/homebrew-tap` (vazio, com branch padrão) e o secret `HOMEBREW_TAP_GITHUB_TOKEN` (PAT com escrita nesse repo). Sem o secret a release segue e o cask é pulado. Opcional para evals reais: secret `ANTHROPIC_API_KEY` e variáveis `EVALS_LIVE`, `EVALS_MODEL`, `EVALS_MAX_USD`.

### 4.2 Deploy no hanko-prd (nunca stg)
No Mac, com `ssh oute-server` funcionando, na `main` (ou na tag) que será implantada e com a árvore limpa. Para ensaiar: `deploy/oute-server/deploy-prd.sh --dry-run`.

```bash
# 1) backup (dentro do LXC; o deploy-prd.sh também roda um backup pré-deploy)
ssh oute-server "lxc exec hanko-prd -- bash /opt/app/deploy/oute-server/backup.sh"
# 2) deploy (envia HEAD por git bundle; sem --edge, a borda já existe)
deploy/oute-server/deploy-prd.sh
# 3) smoke (o deploy-prd.sh também o roda no fim)
ssh oute-server "lxc exec hanko-prd -- env BASE=http://127.0.0.1:3810 PUBLIC_HOST=hanko.oute.pro bash /opt/app/deploy/oute-server/smoke.sh"
```

Depois: conferir `/healthz`, `/readyz`, as migrations aplicadas (00035 a 00200; as da Onda 3 só se o PR #37 estiver na `main`) e um run de persona de ponta a ponta (RUNBOOK). Rollback: `deploy/oute-server/restore.sh --apply` com o dump do passo 1. Login do Claude para as personas, se ainda faltar: `ssh -t oute-server lxc exec hanko-prd -- docker compose -f /opt/app/docker-compose.yml exec hanko claude`.

### 4.3 Settings do GitHub
Já ligados em 2026-10-20 (só conferir): push protection, Dependency graph, Dependabot alerts, malware alerts, security updates, CodeQL e o ruleset da `main` (PR obrigatório, sem force push, checks `check`, `release-dry-run`, `gitleaks`). Falta:
- Ruleset da `main`: marcar **Require branches to be up to date before merging**.
- **Secret scanning** (sempre ativo em repo público; confirmar).
- **Private vulnerability reporting** (o `SECURITY.md` depende dele).
- Settings › General › **Social preview**: enviar `docs/brand/social-card.png`.

### 4.4 Telegram e GitHub (canais)
Seguir `docs/validacao/m19.md`, passos 4 a 6: segredos (`tg-token`, `tg-hook`, `gh-token`, `gh-hook`), `hanko channel add/connect/link`, webhook do repositório para `<HANKO_PUBLIC_URL>/hooks/channels/<identificador>`. Exige `HANKO_PUBLIC_URL` https e `HANKO_SECRET_KEY`.

### 4.5 Alertas de CodeQL
Decidir os dois alertas e, sugestão, descartar ambos como «por design» na aba Security:
- `internal/gitrepo/inspect.go`: falso positivo, os caminhos passam por `safeJoin` (confinamento ao diretório do worktree).
- `internal/provider/provider.go`: o `base_url` é configuração do operador, com validação de esquema e host.

### 4.6 Opcional
- `trivy-fs` e `dependency-review` já passam e já são bloqueantes: o `security.yml` na `main` não tem mais `continue-on-error`. Nada a remover.

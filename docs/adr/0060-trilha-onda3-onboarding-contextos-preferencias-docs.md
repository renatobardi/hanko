# ADR-0060 — Trilha contínua, onda 3: onboarding, contextos, preferências, novidades e site de docs
Data: 2026-10-22 · Status: aceita

## Contexto
A onda 3 da trilha contínua (itens #67, #68, #69, #61, #62 de `docs/PLANO-DECISOES.md`) cuida do começo e do dia a dia de
quem usa o Hanko: levar uma empresa nova à primeira task rodando, alternar entre servidores na CLI, ter atalhos e perfil
próprios, saber o que mudou na versão instalada e ter uma documentação navegável. Não reabre nenhum ADR anterior; parte do
que existe (pacotes de time do ADR-0044, readiness do ADR-0055, `hanko login` do ADR-0057, contextos já gravados por ele).

## Decisão

### A. Onboarding (#67)
1. **Os passos são derivados, não gravados.** `domain.ComputeOnboarding` calcula o estado a partir do que a empresa já tem:
   empresa (sempre feito) → harness (probe `ready` ou passo pulado) → persona (qualquer persona não desligada, contratada ou
   vinda de um pacote) → task (a primeira task criada pelo assistente ou qualquer run). Por isso o assistente é retomável
   sem lógica de retomada: fechar a aba e voltar cai no passo certo, e quem contratou a persona pela tela antiga também avança.
2. **O banco guarda só o que não se deduz** (`onboarding_state`, migration 00200): o último probe do harness (feito antes de
   existir persona, então não cabe em `persona_readiness`), os passos pulados e a dispensa. Só o passo do harness pode ser
   pulado (quem já tem o login na máquina ou usa o adapter de teste); persona e task são o que o assistente existe para fazer.
3. **Probe sem persona.** `OnboardingProbe` reaproveita `readiness.Check` (ADR-0055) no ambiente padrão da empresa (ou o
   local), para `claude`, `codex` e `fake`; `acp` exige um comando por persona e fica para o "Testar agora" da persona. O
   resultado (ok ou não) vira fato `onboarding.harness_probed`. É o mesmo teste que depois protege o despacho.
4. **Pacote de time.** `OnboardingInstallTeam` instala um pacote do catálogo embutido com `Source=catalog` e **ativa a persona
   de topo** (o pacote instala pausado, ADR-0044; o assistente existe para chegar numa task rodando). As demais ficam pausadas.
5. **Primeira task.** `OnboardingStartTask` usa `CreateTask` com a persona escolhida (que acorda como em qualquer task
   atribuída) e grava `first_task_id` + fato `onboarding.first_task`.
6. **Superfícies.** UI `/c/{empresa}/primeiros-passos` (formulários simples, sem JS; o Painel mostra um cartão enquanto o
   assistente está ativo e a pessoa administra; criar a empresa em `/bem-vindo` já cai nele), API `/companies/{c}/onboarding[/probe|/skip|/dismiss|/team|/task]` e
   `hanko onboard`, que executa os passos que as flags permitem e para no primeiro que falta, dizendo o comando para continuar.
7. **Escrita gera fato** na mesma transação (`onboarding.harness_probed|step_skipped|dismissed|resumed|first_task`); métrica
   `hanko.onboarding{action,result}` e span `hanko.onboarding.probe`.

### B. Contextos da CLI e instância descartável (#68)
1. **`hanko context list|current|use|add|rm`** operam sobre o mesmo `contexts.json` do `hanko login` (0600). `add` guarda um
   servidor sem passar pelo navegador; a API key só entra por `--token-stdin` (nunca flag, para não ir ao histórico do shell) e,
   por padrão, o servidor é conferido com `whoami` antes de gravar. `list` nunca imprime a chave. Um contexto sem chave aponta um
   servidor em modo trusted (`currentContext` passou a aceitar URL sem token). `--context`/`HANKO_CONTEXT` escolhe o contexto de
   uma chamada só; nome inexistente é erro de uso.
2. **`hanko sandbox up|down|list`**: uma sandbox por branch ou worktree = um banco `hanko_sbx_<nome>` criado no Postgres que o
   desenvolvedor já usa (`--database-url`, `HANKO_SANDBOX_DATABASE_URL` ou `HANKO_DATABASE_URL`), porta determinística
   (18000–18999, hash do nome; a próxima livre se estiver ocupada), pasta de workspaces própria e o contexto `sandbox-<nome>`.
   `up` sobe o servidor (o próprio binário, `serve`, em modo trusted com `HANKO_MIGRATE_ON_BOOT=true`) em primeiro plano; `--no-serve`
   só prepara e imprime o ambiente. `down` apaga banco, pasta e contexto. Só bancos com o prefixo `hanko_sbx_` são listados ou
   apagados (`internal/sandbox`). Não depende do `--embedded-pg` da onda 2A: usa o Postgres que já existe.

### C. Favoritos, recentes e perfil (#69)
1. **Preferências no servidor**, por pessoa. `nav_items` (favoritos até 30 e recentes até 8 por empresa e pessoa) e `user_profiles`
   (avatar, fuso, idioma; o nome continua em `users.name`). `subject` = id do usuário, ou `local` no modo trusted (uma pessoa só);
   sem FK de propósito (o modo trusted não tem usuário), então `AdminRemoveUser` limpa à mão. O perfil exige conta.
2. **Caminhos relativos e fechados.** Um favorito/recente é um caminho relativo à empresa, só de seções conhecidas
   (`domain.NavSections`), sem esquema, host, `..`, query ou fragmento (`NormalizeNavPath`): nada de redirecionamento aberto.
   Recentes registram só páginas de detalhe (e não formulários `/nova`), pelo título da própria página, depois de renderizar.
3. **Fatos.** Favoritar/desfavoritar gera `nav.favorite_added|removed` (baixa frequência, na transação). Recentes, dispensas e
   perfil **não** geram fato: são estado de navegação pessoal (precedente: lido/não lido da Caixa, ADR-0042) e o perfil vale em
   todas as empresas (não há `company_id` onde gravar). Ficam em métrica `hanko.preferences{kind}`.
4. **Idioma e fuso do perfil.** Sem cookie, o idioma do perfil vale mais que o `Accept-Language`; salvar o perfil grava o cookie.
   O fuso do perfil substitui o da empresa nas telas da empresa (inclusive como padrão do fuso de um fluxo novo, que o formulário
   mostra e a pessoa pode trocar). A ordem da sidebar continua FORA do escopo (decisão do plano).
5. **Avatar sem upload:** glifo de até 3 caracteres + cor de uma paleta fechada de tokens; evita imagem externa (rastreador) e
   armazenamento de binário.

### D. "O que há de novo" (#61)
O `CHANGELOG.md` da raiz é embutido no binário (`hanko.Changelog`, `go:embed`; `.dockerignore` passou a deixá-lo entrar). `internal/changelog`
o separa em versões e escolhe o bloco da versão instalada (`v0.2.0`, `0.2.0-5-gabc` → `0.2.0`; versão sem bloco ou `dev` → `[Unreleased]`).
Sem rede. A dispensa é por pessoa e por versão (`user_dismissals`, chave `whats_new`, valor = versão normalizada): atualizar a instalação
mostra o cartão de novo. UI: cartão no Painel, `/c/{empresa}/novidades`; API `GET /whats-new` e `POST /whats-new/dismiss`; CLI `hanko whats-new`.

### E. Site de docs e feature-map (#62)
1. **Gerador, não publicação.** `internal/docsite` lê `docs/*.md` + `docs/adr` (pt-BR) e `docs/en` (inglês) e gera HTML estático
   autossuficiente (CSS inline, claro/escuro, sem script nem recurso externo) com índices, `sitemap.txt` e o feature-map. Arquivos de
   trabalho (`SESSION`, `PROGRESSO`, `VALIDACAO-MANHA`, `PLANO-DECISOES`) não são publicados. Links `.md` entre documentos viram `.html`; os que
   não resolvem são avisos (e o teste exige zero). `go run ./tools/docsite -out dist/docs` (ou `make docs-site`). A hospedagem em
   `docs.hanko.oute.pro` fica a cargo de quem publica.
2. **Feature-map por domínio** em `docs/feature-map.json`: cada funcionalidade lista as superfícies (rotas de UI, comandos da CLI,
   operações do OpenAPI, tools MCP), o roteiro em passos (base de e2e e evals), os arquivos e2e e os cenários de eval. O teste
   `TestFeatureMapMatchesTheCode` confere tudo contra o código real (rotas lidas das fontes da UI, `cli.CommandPaths()`, `api/openapi.json`, tools
   do MCP, ADRs, arquivos e2e) e a coerência de status (`covered` exige e2e; `gap` não pode listar). É o roteiro das ondas de e2e e evals.
3. **Sem dependência Go nova:** markdown pelo `goldmark` que a UI já usa (HTML cru nunca vira marcação).

## Consequências
- Uma migration aditiva (00200) com quatro tabelas; nenhuma CHECK de tabela compartilhada foi recriada.
- Env nova: `HANKO_SANDBOX_DATABASE_URL` (CLI) e `HANKO_CONTEXT` (CLI). Nenhuma env de servidor nova.
- Rotas novas na UI e API listadas em `docs/validacao/trilha-w3.md`. A UI de Perfil e Novidades fica no menu do usuário (modo authenticated) e no Painel.
- Pendências conhecidas: jornada Playwright do assistente e do perfil (não executável neste ambiente), cenários de eval reais (onda 2B).

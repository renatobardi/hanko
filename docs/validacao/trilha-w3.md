# Validação — Trilha contínua, onda 3 (ADR-0061)

Itens: #67 onboarding, #68 contextos + sandbox, #69 favoritos/recentes/perfil, #61 O que há de novo, #62 site de docs + feature-map.

## O que entrou
- **Migration:** 00200 `trilha_w3` (`user_profiles`, `nav_items`, `user_dismissals`, `onboarding_state`). Aditiva; não recria nenhuma CHECK de tabela compartilhada.
- **Env novas:** só da CLI: `HANKO_SANDBOX_DATABASE_URL`, `HANKO_CONTEXT`. Flag global nova: `--context`. Nada novo em `internal/config`.
- **Rotas de UI:** `GET /c/{empresa}/primeiros-passos` (+ POST `harness`, `pular`, `pacote`, `persona`, `task`, `dispensar`, `retomar`), `GET|POST /c/{empresa}/perfil`,
  `GET /c/{empresa}/novidades`, `POST /c/{empresa}/novidades/dispensar`, `POST /c/{empresa}/favoritos`. Criar empresa em `/bem-vindo` agora redireciona para o assistente.
- **API:** `GET /companies/{c}/onboarding`, `POST …/onboarding/{probe,skip,dismiss,team,task}`, `GET|PATCH /me/profile`, `GET /whats-new`, `POST /whats-new/dismiss`,
  `GET /companies/{c}/nav`, `PUT /companies/{c}/nav/favorites`.
- **CLI:** `onboard`, `context {list,current,use,add,rm}`, `sandbox {up,down,list}`, `profile {show,set}`, `favorite {list,add,rm}`, `whats-new`.
- **Ferramenta:** `go run ./tools/docsite -out dist/docs` / `make docs-site`.
- **Sem dependência Go nova.** `.dockerignore` passou a deixar `CHANGELOG.md` entrar no contexto (o binário o embute).

## Como validar no hanko-prd
1. **Assistente (#67).** Empresa nova (`hanko company create` ou `/bem-vindo`): cai em `/c/<slug>/primeiros-passos`. Testar o harness `claude` (precisa do login no host) ou `fake`;
   contratar uma persona ou instalar um pacote do catálogo (a persona de topo fica ativa, as outras pausadas); criar a primeira task e ver a execução abrir. Fechar a aba no meio e reabrir: continua no passo.
   Painel: cartão "Assistente de primeiros passos" enquanto incompleto (só admin); "Dispensar o assistente" some com ele; `hanko onboard --resume` devolve.
   CLI equivalente: `hanko onboard --company-name Acme --harness fake --persona Ada --task "Resumir o README"`; sem flags, mostra o estado e o próximo comando.
2. **Contextos (#68).** `hanko context add prd --url https://… --token-stdin <<< hk_…`; `hanko context list` (a chave nunca aparece); `hanko context use prd`; `hanko --context outro whoami`.
3. **Sandbox (#68).** Num worktree/branch: `export HANKO_DATABASE_URL=postgres://…/hanko`; `hanko sandbox up` (cria `hanko_sbx_<branch>`, sobe em `127.0.0.1:18xxx`, trusted); em outro terminal
   `hanko --context sandbox-<branch> company list`; `hanko sandbox list`; `hanko sandbox down` apaga banco, pasta e contexto. Duas branches = bancos e portas distintos.
4. **Favoritos/recentes/perfil (#69).** Em modo authenticated: abrir uma task, clicar na estrela (topo): aparece em *Favoritos* na sidebar, em outra aba e depois de reiniciar o servidor; abrir outra task: *Recentes*.
   Menu do usuário › Meu perfil: trocar nome, avatar (glifo + cor), fuso e idioma; com o idioma `en` e sem cookie a UI sai em inglês; datas passam a usar o fuso do perfil.
5. **O que há de novo (#61).** Painel mostra o cartão; `/c/<slug>/novidades` mostra o bloco do CHANGELOG da versão instalada (`dev` ou versão sem bloco = `[Unreleased]`); "Dispensar" some o cartão só para você; atualizar a versão o traz de volta.
6. **Docs (#62).** `make docs-site`; abrir `dist/docs/index.html` e `dist/docs/en/index.html`, e `feature-map.html`. Nada é publicado.

## Resultado local
- Unitários e integração (Postgres 16 local) verdes: domain (perfil, caminhos de navegação, passos do onboarding), changelog, sandbox (cria/lista/apaga banco real), service (favoritos por pessoa e limite,
  recentes, perfil, dispensa, onboarding de ponta a ponta com probe falho/ok, pacote que ativa a persona de topo, papéis), api (fluxo HTTP real), ui (assistente, favoritos/recentes na sidebar, perfil, idioma do perfil sem cookie, novidades),
  cli (contexto add/use/rm/list, `--context`, sandbox `--no-serve`, onboard, perfil/favoritos/novidades), docsite (site real gerado sem link quebrado, determinístico, feature-map conferido contra rotas, CLI, OpenAPI, tools MCP, ADRs e e2e).
- Gates: gofmt do Go 1.27.2, `go vet`, golangci-lint v2.15 (0 achados), sqlc/templ/OpenAPI sem diferença.

## Riscos
- O fuso do perfil substitui o da empresa em `withCompany` (inclusive o padrão do fuso de um fluxo novo, visível no formulário).
- A visita a uma página de detalhe grava uma linha (`nav_items`, `recent`) por GET completo; é best-effort e podada a 8 por pessoa/empresa.
- Recentes só aparecem na sidebar a partir da visita seguinte (o registro acontece depois de renderizar a página, para usar o título dela).
- `sandbox up` sobe o servidor com o próprio binário; em Windows o `serve` herda o terminal (não testado).

## Pulado / Falhou
- **Jornada Playwright** do assistente e do perfil: não escrita (não há navegador neste ambiente para provar que passa). O feature-map marca `onboarding.wizard` e as preferências como `partial`, com o roteiro pronto para a próxima onda de e2e.
- **Cenários de eval** (`evals` no feature-map) são identificadores; o runner é da onda 2B.
- **Hospedagem** de `docs.hanko.oute.pro`: fora do escopo (gerador + saída verificável apenas).
- `hanko sandbox up` em primeiro plano (serve de verdade) é coberto só por `--no-serve` nos testes; o caminho de exec do processo é injetável (`serveFn`) mas não há teste que o exerça.

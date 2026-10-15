# DEMOS — roteiros de gravação por milestone

> English: [docs/en/DEMOS.md](en/DEMOS.md)

Um roteiro por milestone do [ROADMAP](ROADMAP.md), cada um com ≤3 min. Os comandos são os da CLI real (`hanko --help`); as telas citadas estão em pt-BR (a UI também fala inglês: seletor no rodapé da barra lateral). Quem grava e quem cria as tags é o dono; este documento só prepara o caminho.

## Cenário rápido (vale para todas as demos)

O arquivo [`examples/demo-company.json`](../examples/demo-company.json) (formato `hanko.company.v1`, [ADR-0022](adr/0022-export-import.md)) cria a empresa **Demo** de uma vez: workspace, projeto, assinatura, orçamento da empresa (US$ 50) e da Ana (US$ 10), três personas (Carla, CTO, claude/sonnet; Ana, engenheira, claude/haiku, com revisão obrigatória pela Rui; Rui, revisora, codex), organograma, 4 tasks (`DEMO-1`…`DEMO-4`, a `DEMO-3` filha da `DEMO-2`) e o fluxo "Resumo diário". Sem segredos. Um teste de integração (`TestExemploDemoImporta`) garante que ele continua importável.

```bash
# 1. servidor local (veja o RUNBOOK §1 e §5 para banco, HANKO_SECRET e o dono)
mkdir -p /tmp/hanko-demo/demo/app && cd /tmp/hanko-demo/demo/app && git init -q   # workspace onde as personas trabalham
export HANKO_WORKSPACES_ROOT=/tmp/hanko-demo      # o workspace do exemplo fica em <raiz>/<slug-da-empresa>/app
hanko serve &                                     # http://127.0.0.1:7420

# 2. cliente (modo authenticated: uma API key do dono)
export HANKO_TOKEN=$(hanko auth token create --name demo | grep -oE 'hk_[A-Za-z0-9_-]+')

# 3. a empresa de exemplo
hanko import examples/demo-company.json --slug demo --active
export HANKO_COMPANY=demo
hanko persona list && hanko org && hanko task list
```

- `--active` liga personas e fluxo; sem ela a importação entra **pausada** (nada trabalha até você liberar na UI ou com `hanko import … --active`). Para gravar, use `--active`.
- Para repetir: o import cria sempre uma empresa nova; use outro `--slug` (`demo2`) ou apague o banco da demo.
- `claude` e `codex` precisam estar no PATH (ou `HANKO_CLAUDE_BIN`/`HANKO_CODEX_BIN`). Para ensaiar sem harness real, edite uma cópia do JSON trocando `"agent"` por `"fake"`.
- Antes de gravar, ensaie uma vez: a primeira run de cada persona é a mais lenta.

Em cada roteiro, "Passou quando" repete o critério de demo do ROADMAP.

---

## M1 — Loop mínimo com CLI e log ao vivo

**Duração alvo:** 2 min · **Pré-requisitos:** cenário rápido acima (Postgres no ar, `hanko serve`, empresa `demo` importada). Opcional: Grafana em <http://127.0.0.1:3000> (`otel-lgtm` do RUNBOOK §1) para o trace.

**Comandos** (terminal dividido: servidor à esquerda, CLI à direita)

> Os números `DEMO-5` abaixo supõem uma empresa recém-importada; se rodou outros roteiros antes, use a chave que `task create` imprimir.

```bash
hanko persona list                                  # Carla, Ana, Rui: contratadas
hanko task create "Dizer olá e descrever o repositório" -p ana --project demo
hanko run list                                      # a run nasce da atribuição (heartbeat/river)
hanko run logs -f <run>                             # log ao vivo (SSE); o nome amigável, ex. Veloz_Raposa, também vale
hanko task comment DEMO-5 "Obrigado, Ana. Pode detalhar o ponto 2?"
hanko run list --task DEMO-5                        # o comentário acorda a persona: segunda run
hanko task show DEMO-5                              # thread com o comentário da persona
```

**Mostrar na tela:** o `run logs -f` rolando enquanto a persona trabalha; a task mudando de status; a resposta da persona na thread. No Grafana, abrir *Explore → Tempo* e mostrar o trace do primeiro request (`hanko-*`).

**Passou quando:** `hanko serve` → contratar persona → criar task → run → log ao vivo → comentário, e o trace aparece no Grafana (ROADMAP, M1).

---

## M2 — UI, MCP e memória entre runs

**Duração alvo:** 3 min · **Pré-requisitos:** cenário rápido **com ai-memory ligado** (`HANKO_MEMORY_PROVIDER` diferente de `none`, RUNBOOK §1); entrar em <http://127.0.0.1:7420/entrar> com o dono.

**Cliques**

1. *Painel* da empresa Demo: cards de personas, tasks e execuções.
2. *Tasks*: lista agrupada por status. Abrir **DEMO-1** (Descrever o repositório) e clicar **Acordar Ana** (botão da task).
3. Abrir a execução (link *Inspecionar* na task, ou *Execuções*): log ao vivo. Apontar as chamadas `hanko_task_comment`/`hanko_task_get`: é a persona usando o servidor MCP do Hanko (`/mcp`, tools `hanko_*`).
4. Quando terminar, voltar à task e comentar na thread: "Agora liste os arquivos de nível superior." (acorda a persona; segunda run no **mesmo workspace**).
5. Abrir a segunda run: mostrar que ela parte do que a primeira fez (o despacho injeta o *Briefing* da memória e o handoff do workspace; a persona cita o trabalho anterior).
6. *Personas* → **Nova persona**: mostrar o formulário de contratação (sem enviar).

**Mostrar na tela:** lista de tasks, log ao vivo, tool calls MCP, a run 2 retomando o contexto da run 1.

**Passou quando:** a run 2 no mesmo workspace recebe o handoff da run 1 (ROADMAP, M2). O dogfood começa depois disto.

---

## M3 — Organograma, delegação e gate humano

**Duração alvo:** 3 min · **Pré-requisitos:** cenário rápido; ai-memory ligado (para mostrar a decisão virando memória).

**Comandos e cliques**

```bash
hanko org                                           # Carla na raiz; Ana e Rui reportam a ela
hanko task wake DEMO-2                              # a CTO planeja o MVP (instruída a delegar e pedir gate)
hanko task list                                     # novas tasks filhas atribuídas à Ana (checkout atômico: uma persona por task)
hanko gate list --status pending                    # gate pedido pela persona (hanko_gate_request)
hanko gate show <gate>
hanko gate approve <gate> -n "Plano aprovado. Pode seguir."
```

Na UI: *Organograma* (árvore Carla → Ana, Rui) → *Caixa de entrada* (gate pendente) → abrir o gate e clicar **Aprovar** (ou **Pedir mudanças**/**Rejeitar**, que exigem nota) → *Decisões* (a decisão registrada).

Extra, se sobrar tempo: concluir uma task da Ana mostra a **revisão automática** da Rui antes do gate humano (`require_review`, ADR-0020): `hanko gate list --kind task_review`.

**Mostrar na tela:** a árvore do organograma; a task filha criada pela Carla com a Ana como responsável; o gate com audit trail; a persona acordada após a decisão; a decisão em *Decisões*.

**Passou quando:** persona delega; humano decide o gate; a decisão vira memória (ROADMAP, M3).

---

## M4 — Orçamento bloqueia a run; fluxo cria task sozinho

**Duração alvo:** 3 min · **Pré-requisitos:** cenário rápido. A reserva por run é no mínimo US$ 1,00 (`HANKO_RUN_RESERVE_USD`), então um teto abaixo disso bloqueia a próxima run de forma determinística, sem esperar o gasto estourar de verdade.

**Parte 1: orçamento**

```bash
hanko budget show                                   # empresa US$ 50, Ana US$ 10
hanko budget set 0.50 --persona ana                 # teto da Ana abaixo da reserva de uma run
hanko task create "Esta run não deve abrir" -p ana --project demo   # anote a chave impressa (aqui, DEMO-5)
hanko task wake DEMO-5                              # a run é recusada pelo hard stop
hanko run list --persona ana                        # nenhuma run nova aberta
hanko cost --by persona                             # custos do mês por persona
hanko budget set 10 --persona ana                   # libera: a persona pausada por orçamento volta sozinha
```

Na UI: *Custos* (orçamentos e assinaturas, uso semanal) e *Caixa de entrada*/alertas mostrando o orçamento atingido.

**Parte 2: fluxo**

```bash
hanko flow list                                     # "Resumo diário" (todo dia 9h, fuso da empresa)
hanko flow show resumo-diario                       # próximo disparo
hanko flow run resumo-diario                        # dispara agora (o cron faria o mesmo às 9h)
hanko task list                                     # "Resumo diário <data>" criada sozinha, atribuída à Carla
```

Na UI: *Fluxos* → **Resumo diário** → **Rodar agora**; o disparo aparece com a task que gerou.

**Mostrar na tela:** a recusa por orçamento (sem run nova, persona/alerta), o gasto em *Custos*, o fluxo criando a task com título datado.

**Passou quando:** orçamento estoura → run bloqueada; fluxo diário cria task sozinho (ROADMAP, M4).

---

## M5 — Produção em hanko.oute.pro

**Duração alvo:** 3 min · **Pré-requisitos:** produção no ar (`deploy/oute-server/deploy-prd.sh --edge`); conta Google autorizada; uma API key sua para a CLI contra produção (`export HANKO_URL=https://hanko.oute.pro HANKO_TOKEN=hk_…`, criada em *Configurações*); um segundo e-mail para o convite. Nunca grave tela com tokens: gere e revogue a key da demo.

**Cliques e comandos**

1. Abrir <https://hanko.oute.pro> (anônimo): redireciona para `/entrar`. **Continuar com Google**, cair no painel.
2. *Configurações → Membros*: **Convidar** um e-mail como `viewer`; ou `hanko member invite fulano@exemplo.com --role viewer` (imprime o link; o Hanko não envia e-mail). Mostrar `hanko member list` e a troca de papel: `hanko member role fulano@exemplo.com member`. Abrir o link numa janela anônima: o viewer vê, mas os botões de ação não aparecem.
3. Export/import: 
   ```bash
   hanko export -o demo.json                         # empresa atual, sem segredos
   hanko import demo.json --slug demo-copia --active  # empresa nova, tudo ou nada
   ```
   Trocar de empresa pelo seletor na UI.
4. PWA: no navegador, ícone de instalar na barra de endereço (ou *Adicionar à tela inicial* no celular) e abrir o app instalado.
5. Bilíngue: seletor de idioma no rodapé da barra lateral (pt-BR ↔ en); na CLI, `HANKO_LANG=en hanko task list`.
6. Rodapé técnico (opcional, 15 s), no servidor: `BASE=http://127.0.0.1:3810 PUBLIC_HOST=hanko.oute.pro deploy/oute-server/smoke.sh`.

**Mostrar na tela:** redirecionamento ao login, o login Google, convite com papel, export/import, app instalado, alternância de idioma.

**Passou quando:** hanko.oute.pro com login Google (ROADMAP, M5). Também está entregue no v0.2 e vale mostrar: papéis, convite, export/import, PWA, UI bilíngue.

---

## Tags e release (só o dono cria)

Nada neste repositório cria ou empurra tags. Depois de cada demo aprovada, escolha o commit em que o milestone passou (`git log --oneline`; se tudo já está na `main`, use o `HEAD` dela) e crie a tag **anotada** (não assinada; se quiser assinar, `-s` no lugar de `-a`):

```bash
git tag -a v0.1.0-m1 <commit> -m "M1: loop mínimo (CLI, run, log ao vivo, comentário)"
git tag -a v0.1.0-m2 <commit> -m "M2: UI, MCP e memória entre runs"
git tag -a v0.1.0-m3 <commit> -m "M3: organograma, delegação e gates"
git tag -a v0.1.0-m4 <commit> -m "M4: orçamentos, hard stop e fluxos"
git tag -a v0.1.0-m5 <commit> -m "M5: produção em hanko.oute.pro"
git tag -n                                          # confira antes de publicar
```

Release `v0.1.0`, depois que as cinco demos passaram:

```bash
git tag -a v0.1.0 -m "Hanko v0.1.0" && git push --tags
```

Atenção: o workflow `release` dispara em **qualquer** tag `v*`. `git push --tags` empurra também as `v0.1.0-mN`, e cada uma gera uma release (marcada como pré-release, `prerelease: auto` no goreleaser) e imagens no registro. Para publicar só o que quer: `git push origin v0.1.0-m1`, … e, no fim, `git push origin v0.1.0`. Antes da primeira tag, o CI (`release-dry-run`) já valida a configuração.

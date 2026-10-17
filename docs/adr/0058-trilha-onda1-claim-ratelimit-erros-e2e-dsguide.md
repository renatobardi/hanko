# ADR-0058 — Trilha contínua, onda 1: claim do primeiro dono, rate limit, erros via OTel, e2e e guia do design system
Data: 2026-10-17 · Status: aceita

## Contexto
A trilha contínua do plano (itens #53, #51, #49, #57, #70) fecha cinco lacunas de operação e qualidade antes de a
instalação ter mais de um usuário: o primeiro dono ainda nasce por `hanko auth create-owner` (ou por um formulário que só
existe em loopback); não há limite de taxa além do login; erros de servidor e de navegador só aparecem se alguém olha o log;
o `e2e.yml` é um esqueleto; e o Kubo DS não tem catálogo vivo que sirva de referência visual. As decisões abaixo valem
sobre o `STACK.md` e não reabrem nenhum ADR anterior (ADR-0013 telemetria, ADR-0005 UI, ADR-0050 settings).

## Decisão

### A. Claim do primeiro dono (#53)
1. **Quando.** No boot, com `mode=authenticated`, provider `local` e **zero usuários**, o servidor emite um *claim*: token
   aleatório de 32 bytes (base64url), guardado **só como SHA-256** em `owner_claims` (migration 00170), com expiração
   (1 h). Provider `firebase` não usa claim (o primeiro login admitido já vira dono, ADR-0014); modo `trusted` não tem
   usuários.
2. **Onde aparece.** O link `<HANKO_PUBLIC_URL>/claim/<token>` é impresso **direto no stderr do processo** (bloco
   delimitado), e não pelo logger estruturado: o logger também exporta por OTLP e um token de posse não deve ir para o
   Loki. O log estruturado só registra que o claim foi emitido e quando expira. `hanko auth claim-link` emite um novo
   link sem reiniciar (invalida os anteriores); `hanko auth create-owner` continua como fallback documentado.
3. **Uso.** `GET /claim/<token>` mostra o formulário (nome, e-mail, senha); `POST` cria o dono, marca o claim como usado
   e grava `admin_audit` (`claim_owner`) **na mesma transação**, com `lock users` (a mesma serialização do `CreateOwner`):
   duas disputas pelo mesmo link produzem no máximo um dono. Depois de usado, expirado ou com qualquer usuário no banco,
   o link responde 404 genérico (não distingue "inexistente" de "usado"). Falhas contam no `LoginLimiter` por IP.
4. **Higiene.** `/claim/` é público no Guard, o token é mascarado em span/log (`auth.RedactPath`), a página tem
   `Cache-Control: no-store` e `Referrer-Policy: no-referrer`. Não há `activity_event`: o claim é anterior a qualquer
   empresa; a trilha é o `admin_audit`.

### B. Rate limit geral (#51)
1. **Algoritmo.** Token bucket guardado no Postgres (`rate_limit_buckets`, tabela UNLOGGED, migration 00171). Uma única
   instrução `INSERT … ON CONFLICT DO UPDATE` recarrega (`min(capacidade, tokens + Δt·taxa)`), decide e consome de forma
   atômica; a linha é o lock, então várias instâncias do Hanko compartilham o mesmo bucket. O modelo puro vive em
   `internal/ratelimit` (`Step`) e é o oráculo dos testes de propriedade (nunca excede a capacidade, nunca negativo,
   negar não consome, esperar nunca piora, o total admitido numa janela ≤ capacidade + taxa·janela) e do teste contra o SQL.
2. **Chaves e classes.** Identidade: hash da API key (`key:`), usuário de sessão (`user:`), run token (`run:`), senão o
   IP do cliente (mesmo `TrustedProxies` do login). Classes: `write` (POST/PUT/PATCH/DELETE em `/api/` e UI, exceto login e
   claim, que têm limitador próprio), `mcp` (`/mcp`, `/mcp/…`, `/memory-mcp`), `webhook` (`/hooks/…`, caminho reservado
   para os gatilhos de entrada do M19; hoje nenhum handler o usa) e `browser_error` (`/telemetry/errors`). Escritas somam
   um segundo bucket por IP (teto maior), para que muitas chaves de um mesmo IP também sejam contidas. `mcp` não tem
   bucket de IP: os agentes de uma instalação compartilham o loopback. Leituras (GET/HEAD/SSE), `/healthz`, `/readyz`,
   `/metrics` e `/static/` nunca consomem.
3. **Resposta.** `429` com `Retry-After` (segundos, arredondado para cima, ≥ 1), `RateLimit-Limit` e `RateLimit-Remaining`;
   corpo `application/problem+json` em `/api/`, texto simples nas demais rotas, `HX-Reswap` seguro para htmx. Falha do
   banco **abre** (fail-open) com aviso e métrica `outcome=error`: o limitador nunca derruba a instalação.
4. **Configuração.** Chaves registradas em `instance_settings` (grupo `ratelimit`, mudam sem reiniciar; env vence e trava):
   `ratelimit.enabled` (padrão ligado), `ratelimit.writes_per_min` (600), `ratelimit.ip_per_min` (1200),
   `ratelimit.mcp_per_min` (1500), `ratelimit.webhooks_per_min` (120), `ratelimit.browser_errors_per_min` (20). A
   capacidade do bucket é o valor por minuto (rajada de 1 min) e a recarga é valor/60 por segundo.
5. **Observabilidade.** Contador `hanko.ratelimit.decisions{class,outcome}` (allowed|limited|error) e atributos no span da
   requisição. Limpeza: um laço de fundo apaga buckets parados há mais de 1 h (equivalem a um bucket cheio).

### C. Erros via OTel (#49), sem SaaS
1. **Servidor.** Todo erro 5xx inesperado e todo panic de handler vira **span event `exception`** (com
   `exception.type/message/stacktrace`) no span da requisição e `Status=Error`, mais um log `ERROR` com
   `exception.stacktrace` (vai ao Loki pelo OTLP existente) e o contador `hanko.errors{source,kind}`. O ponto único é
   `telemetry.RecordException`, usado por `api.fail`, pelo recover da API, por `ui.fail` e pelo recover da UI.
2. **Navegador.** `hanko.js` ganha ~40 linhas: `window.onerror` e `unhandledrejection` enviam (no máximo 5 por página, sem
   repetir a mesma mensagem) um JSON pequeno por `sendBeacon` para `POST /telemetry/errors`. O handler exige sessão (o
   Guard trata a rota como UI) e mesma origem, limita o corpo a 8 KiB, trunca campos, remove query/fragmento da URL e passa
   o caminho por `RedactPath`; depois cria um span `browser.error` (kind=client) com o span event `exception`, um log
   `ERROR` com a pilha e o contador `hanko.errors{source=browser}`. Taxa: classe `browser_error`.
3. **Painel.** `deploy/grafana/dashboards/hanko-erros.json` + provisionamento `deploy/grafana/provisioning/dashboards/hanko.yaml`
   montados no container `otel-lgtm` do compose: taxa de erros por origem (Mimir), traces com `status=error` (Tempo) e logs
   de erro com pilha (Loki).

### D. E2E com Playwright (#57)
1. Pacote em `e2e/` (`@playwright/test` com versão exata e `package-lock.json`), **fora** do bundle de runtime e fora do
   `ui/islands`. É dependência de desenvolvimento/CI, não de Go; o binário continua sem Node.
2. O harness sobe `bin/hanko serve` contra um Postgres real (esquema limpo por execução), `mode=authenticated`, provider
   local, `HANKO_PUBLIC_URL` em loopback e **adapter `fake`** (já existe no registro) para as runs; o e2e lê o link do claim
   do stderr do processo, o que também testa o item A de ponta a ponta.
3. 19 jornadas críticas (claim, empresa, login/logout, persona, projeto, task→run→concluída, comentário, gate, chat,
   artefato, metas, busca, custos, rate limit com 429, erro de navegador → OTel, design-guide, ux-labs, idioma/tema, convite
   e papéis) e **screenshot diff** das telas do DS (`/design-guide` em claro e escuro, entrar, claim inválido) com baselines
   versionadas, geradas em Linux/Chromium (a fonte Inter é servida localmente, então o render é estável). Toda jornada
   falha se a página lançar erro de JavaScript não tratado (fixture `noPageErrors`). `e2e.yml` roda no PR que toca
   UI/handlers/migrations/`e2e/` e no `main`, com Postgres 17 como service, e sobe o relatório em falha.
4. **Fake com gate.** A jornada de gates precisa que o agente peça uma decisão humana, e o adapter `fake` só emite eventos.
   Ele ganha `adapter_config.gate {title, body}`: depois dos passos de sempre, chama a tool MCP `hanko_gate_request` do
   próprio Hanko com o token da run (JSON-RPC por HTTP, no modo stateless do `/mcp`), exatamente como um agente real
   faria. Sem processo, sem rede além do Hanko, sem custo; coberto por teste unitário com `httptest`.
5. **Achado do e2e.** O e2e pegou um erro de sintaxe herdado em `internal/ui/static/hanko.js` (o IIFE da sala perdeu o
   `})();` num merge): o navegador descartava o arquivo inteiro e nenhuma ilha, atalho, quadro arrastável nem o relato de
   erros rodava. Corrigido aqui; a fumaça das ilhas (`ui/islands/smoke.mjs`) passa a compilar `hanko.js`, para o `ci` barrar
   a regressão sem navegador.

### E. `/design-guide` e ux-labs (#70)
1. `/design-guide` é o catálogo vivo do Kubo DS: tokens (cores, tipografia, raios), botões, badges, formulários, tabela,
   cards, avisos, abas e estados vazios, **lado a lado em claro e escuro** (`?tema=claro|escuro|ambos`, sem depender do
   cookie, para a captura ser determinística).
2. `/dev/ux/…` (ux-labs) monta cenários sintéticos **sem tocar o banco**: `thread-longo` (500 comentários) e
   `run-10k` (10 000 linhas de log pelo componente real de log de run).
3. **Acesso.** Só no modo `trusted` (desenvolvimento local) ou para dono/admin da instância no `authenticated`; os demais
   recebem 404 (a rota não existe para eles). Nada disso aparece na navegação principal.

## Alternativas descartadas
- Imprimir o claim pelo logger estruturado: simples, mas o token iria para o armazenamento de logs do operador.
- Rate limit em memória: não vale com mais de uma instância e perde estado em restart; contador no Postgres custa um
  UPSERT por requisição de escrita, aceitável (ADR-0011: Postgres é a única dependência).
- Fail-closed no limitador: um banco lento viraria indisponibilidade total.
- Sentry/GlitchTip: o plano veta SaaS e o stack OTel já está sempre ligado.
- Cypress/Selenium: Playwright já tem Chromium empacotado, trace e comparação de screenshots nativa.
- Dependência Go nova: **nenhuma**. Dependência de desenvolvimento nova: `@playwright/test` (em `e2e/`, só CI/dev).

## Consequências
Migrations 00170 (`owner_claims`) e 00171 (`rate_limit_buckets`), ambas aditivas. A suíte e2e leva alguns minutos e não
bloqueia o `ci`. Os limites padrão são folgados para uso humano e para agentes normais; quem opera lotes grandes por
API key ajusta `ratelimit.writes_per_min`. `/telemetry/errors` é a única rota nova que aceita dado do navegador; ela é
mínima, autenticada, limitada e sem persistência (só telemetria).

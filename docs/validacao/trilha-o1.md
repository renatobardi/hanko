# Validação — Trilha contínua, onda 1 (ADR-0058)

Itens: #53 claim do primeiro dono, #51 rate limit, #49 erros via OTel, #57 e2e Playwright, #70 design-guide e ux-labs.

## O que entrou
- **Migrations:** 00170 `owner_claims`, 00171 `rate_limit_buckets` (UNLOGGED). Aditivas.
- **Env novas:** `HANKO_RATELIMIT_ENABLED`, `HANKO_RATELIMIT_WRITES_PER_MIN` (600), `HANKO_RATELIMIT_IP_PER_MIN` (1200),
  `HANKO_RATELIMIT_MCP_PER_MIN` (1500), `HANKO_RATELIMIT_WEBHOOKS_PER_MIN` (120), `HANKO_RATELIMIT_BROWSER_ERRORS_PER_MIN` (20).
  Viram as chaves `ratelimit.*` de Configurações › Instância (env vence e trava).
- **Rotas novas:** `GET/POST /claim/{token}`, `POST /telemetry/errors`, `GET /design-guide`, `GET /dev/ux[...]`.
- **CLI nova:** `hanko auth claim-link`.
- **Sem dependência Go nova.** Dev: `@playwright/test` 1.56.1, `typescript`, `@types/node` em `e2e/`.

## Como validar no hanko-prd
1. **Claim (#53).** Banco sem usuários, `HANKO_MODE=authenticated`, provider local: `docker compose logs hanko | grep -A8 claim` mostra o bloco com
   `<PUBLIC_URL>/claim/<token>`. Abrir o link, criar o dono (cai no onboarding). Reabrir o link: 404 "Link inválido ou expirado".
   `hanko auth claim-link` com usuários existentes recusa ("já existe um usuário"). `hanko auth create-owner` segue funcionando.
2. **Rate limit (#51).** Configurações › Instância › Limite de taxa: baixar "Escritas por minuto" para 10; com uma API key
   (`hanko auth token create`) disparar 12 `POST` inválidos em `/api/v1/companies/<slug>/tasks`: do 11º em diante `429` com `Retry-After`.
   Voltar ao padrão. `curl -H "Authorization: Bearer hk_…" /metrics | grep hanko_ratelimit_decisions`.
3. **Erros (#49).** No console do navegador, numa tela do Hanko: `setTimeout(()=>{throw new TypeError("teste")})`. No log do servidor aparece
   `exceção registrada` com `exception.source=browser` e a pilha; no Grafana (otel-lgtm) o painel **Hanko · Erros** (pasta *Hanko*) mostra o contador,
   o trace `browser.error` com o evento `exception` e o log. O painel é provisionado pelo `deploy/compose.yaml` (reiniciar o `otel-lgtm` após o deploy).
4. **Dev labs (#70).** Logado como dono: `/design-guide` (claro e escuro lado a lado, `?tema=`), `/dev/ux/thread-longo`, `/dev/ux/run-10k`.
   Como membro: 404.
5. **E2E (#57).** `make build && cd e2e && npm ci && HANKO_E2E_DATABASE_URL=… npx playwright test` (Postgres descartável; ver RUNBOOK).

## Resultado local
- e2e: 24 testes (2 de setup + 22 em 19 jornadas) verdes em 3 execuções seguidas, contra Postgres 16 local, Chromium 141; screenshot diff estável
  sem `--update-snapshots`.
- Testes Go novos: `internal/ratelimit` (propriedade do bucket, middleware, classes), `internal/service` (claim, SQL do bucket contra o modelo puro,
  concorrência), `internal/telemetry` (exceções, endpoint do navegador, painel), `internal/ui` (claim, dev labs, CSS), `internal/cli` (claim-link),
  `internal/adapter/fake` (gate pelo MCP).

## Achados no caminho (corrigidos aqui)
- `internal/ui/static/hanko.js` estava com erro de sintaxe herdado (o IIFE da sala sem `})();`): o navegador descartava o arquivo e nenhuma ilha,
  atalho nem quadro arrastável rodava. A fumaça das ilhas (`ui/islands/smoke.mjs`) agora compila `hanko.js`.
- `internal/ui/static/hanko.css` tinha um `@media (max-width: 1100px)` sem fechar: todas as regras seguintes (skills, plugins, tools, ambientes…)
  só valiam em tela estreita. Fechado; `TestStylesheetBracesBalanced` barra a volta. **Atenção ao mergear:** telas dessas áreas passam a ter o CSS em
  qualquer largura (era o desenho original).
- `ui.fail`/`api.fail` não registram como exceção a requisição cancelada pelo cliente (`context.Canceled`).

## Pulado / Falhou
- Nada falhou depois de duas correções. Limitações: o e2e não exercita um 5xx real do servidor (coberto por teste unitário de `telemetry.Record` e
  do recover); webhooks de entrada ainda não têm handler (classe `webhook` reservada ao M19); o painel do Grafana foi validado como JSON/YAML e
  contra as métricas emitidas, não contra um Grafana rodando (sem Docker aqui).
- A cobertura de `internal/ui/preview` e dos cenários ux-labs é só funcional (testes de rota e e2e).

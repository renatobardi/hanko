# ADR-0005 — UI: host templ+htmx + ilhas React via esbuild (Go)
Data: 2026-10-13 · Status: aceita

## Contexto
(A) React SPA trimado; (B) templ+htmx puro; híbrido "duas apps" = pior dos dois. Três peças pesadas (editor ancorado, org chart interativo, log viewer 10k+) favorecem React; 80% do produto (CRUD, board, threads, approvals, budget) favorece htmx. Histórico de projetos mortos por complexidade.

## Decisão
Host templ + htmx 2 (+sse) + Alpine + Tailwind standalone CLI. Ilhas React bundladas por esbuild via Go, deps em `ui/islands/package.json` (npm só dev/CI). Regras: (1) host decide rota/auth/layout/listas; (2) ilha é folha, props via data-*, sem estado global; (3) 1 CSS; (4) sem Vite/pnpm workspace/Router/TanStack; (5) teto 3 ilhas, a 4ª exige ADR. Peças pesadas = v0.2+. Scripts do host (ex.: Firebase Auth SDK) passam pelo mesmo bundler e não contam como ilha.

## Consequências
Visual utilitário no v0.1. Node volta só como ferramenta de build. Checkpoint pós-M2 (ROADMAP).

# AGENTS.md — protocolo de sessão

Este arquivo é a memória de trabalho do projeto. Toda sessão (humano, Claude Code, Cowork ou agente do próprio Hanko) começa aqui.

## Cold start (obrigatório, nesta ordem)
1. `docs/ROADMAP.md` — qual milestone está aberto e o critério de demo.
2. `docs/adr/` — decisões vigentes. ADR vence STACK.md em conflito. Não reabrir decisão sem ADR novo.
3. `docs/STACK.md` — stack e convenções.
4. `git log --oneline -20` e issues abertas do milestone.
5. `docs/SESSION.md` — último estado deixado pela sessão anterior (o que ficou pronto, o que travou, próximo passo).

## Fim de sessão (obrigatório)
- Toda sessão termina com algo executável + commit (`-s`). Sem exceção. Zero sessões "só refatorando".
- `docs/SESSION.md`: feito / travado / próximo passo em ≤10 linhas, só com o estado da sessão. Sobrescrever, não acumular. É o **último commit** da sessão, feito **depois** de `git merge origin/main` (ou rebase). Se outro PR entrar na `main` depois, atualize o branch e refaça só esse commit; não resolva o conflito somando o texto das duas sessões.
- Decisão de arquitetura tomada no meio → ADR novo em `docs/adr/NNNN-*.md`.

## Regras de trabalho
- Milestone tem contrato antes de código: migrations + tipos de domínio + OpenAPI. Trilhas paralelas só depois do contrato.
- Antes do push, rodar localmente os geradores que o job `gerados` confere (openapi, templ, sqlc e bundle das ilhas; `make openapi`, `make generate`, `make islands`) e commitar o resultado.
- Ao criar migration e ao atualizar o branch com a `origin/main`, conferir o maior número em `db/migrations/` da `origin/main` e usar o seguinte.
- Camadas handler → service → store. Handler não toca DB. Store não tem regra. Domain não importa infra.
- Toda escrita de domínio gera `activity_event` na mesma transação.
- Nunca citar produtos de terceiros como referência nos artefatos do projeto.
- Nunca `panic` fora de `main`; nunca `log.Fatal` fora de `cmd/`.

## Validar antes do PR
Cada item abaixo já reprovou PR no CI depois de aberto. Confira no diff antes do push.
- `ctx context.Context` é o primeiro parâmetro, inclusive em helper de teste (revive `context-as-argument`).
- Conversão entre tipos inteiros só com limite validado antes (gosec G115); não converta `int` para `int32`/`uint` sem checar a faixa.
- Nunca hash simples (SHA-256, MD5…) para senha ou dado sensível de baixa entropia (CodeQL `go/weak-sensitive-data-hashing`); use KDF (bcrypt, argon2 ou scrypt). Token de alta entropia usa HMAC com rótulo, como `internal/auth/secretbox.go`.
- Função com complexidade cognitiva até 15 (Sonar); extraia helpers antes de passar disso.
- Texto novo de CLI ou UI entra no catálogo de tradução; rode `go test ./internal/i18n ./internal/ui` (`TestCatalogCoversSources`).
- Depois do push, rode `oute-sonar pr <n> --json` (ou leia o check SonarCloud do PR) e só avise que o PR está pronto com o gate OK.

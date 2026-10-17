# ADR-0049 — Canais de release, CI por propósito e piso de cobertura
Data: 2026-10-17 · Status: aceita

## Contexto
O plano de evolução (itens #56, #59, #60, #63) pede qualidade e operação em paralelo a todas as ondas: um CI que
continue rápido enquanto ganha e2e/evals/segurança, um jeito previsível de publicar versões e um piso de cobertura que
não regrida. O ADR-0009 já fixava ≥70% de cobertura só em domain/heartbeat; faltava aplicar isso no CI.

## Decisão
1. **CI por propósito.** `ci.yml` (PR rápido: fmt, vet, lint, build, testes unit + integração, piso de cobertura, drift
   gates de openapi/templ/ilhas/sqlc, `release-dry-run`); `security.yml` (govulncheck, `trivy fs`, revisão de dependências;
   também semanal); `e2e.yml` e `evals.yml` (esqueletos, preenchidos por #57 e #58); `edge.yml`; `release.yml`.
   Os jobs `check` e `release-dry-run` mantêm o nome por serem os status exigidos pelo repo. O `govulncheck` saiu do
   `ci` para o `security`: um CVE novo no dia não trava o PR de ninguém, mas aparece no PR, no main e na semana.
2. **Piso de cobertura por pacote** (`scripts/coverage-gate.sh`, cobertura de declarações do próprio pacote, testes com
   `-tags integration`): `internal/domain` ≥ 80 %, `internal/heartbeat` ≥ 80 %, `internal/service` ≥ 70 %;
   `internal/api` só é relatado (a cobertura vem dos testes de integração). Medido em 2026-10-17 (antes → depois dos
   testes novos): domain 60,1 % → 100 %; heartbeat 88,4 %; service 80,9 %; api 81,8 %. Como todos já cumprem a meta,
   o piso é a meta (80/80/70), com folga para variações de ordem/tempo; o piso só sobe (variáveis
   `COVERAGE_FLOOR_*` ou edição do script), nunca desce para passar PR. Regra do repo: toda feature entra com teste.
3. **Testes de propriedade** com `testing/quick` e semente fixa (sem dependência nova) nos motores puros; o primeiro é o
   de orçamento (`internal/service/budget_property_test.go`). Os motores #1 e #20 do plano entram com os próprios marcos.
4. **Canais.** `edge` = push no `main` → imagem `ghcr.io/renatobardi/hanko:edge` (+ `:edge-<sha8>`) e binários snapshot
   (artefato do job); `stable` = tag `vX.Y.Z` → `release.yml` (goreleaser). SemVer; no `0.x` o minor pode quebrar.
   Notas de versão vêm de `changelog.d/*.md` (um fragmento por marco, sem conflito no CHANGELOG) por
   `scripts/release-notes.sh`: `cut` funde fragmentos + `[Unreleased]` na seção da versão; o `release.yml` publica a seção
   da tag e falha cedo se ela não existir. `hanko update --channel` fica para a onda 2 (#64). Rollback = tag anterior +
   restore. Fluxo em `docs/RELEASING.md`.
5. **Higiene.** `SECURITY.md` (+ pt-BR) com relato privado pelo GitHub e `.github/dependabot.yml` semanal (gomod, npm em
   `ui/islands`, github-actions). CODEOWNERS e templates ficam fora (decisão #63).

## Alternativas descartadas
- Piso único global: esconde pacote fraco atrás de pacote forte; o domínio é o que mais precisa de piso alto.
- Codecov/serviço externo: dependência de terceiro para algo que um script de 80 linhas resolve.
- Dependência de property testing (`gopter`, `rapid`): `testing/quick` com semente fixa basta e não adiciona dependência Go.
- Imagem edge via goreleaser: exigiria um segundo `.goreleaser` ou flags de snapshot para docker; o `Dockerfile` existente
  já compila multi-arch.

## Consequências
O PR segue rápido (o custo novo é só o perfil de cobertura na etapa de integração que já existia). Release vira um comando
(`cut`) + uma tag. O workflow `edge` consome minutos a cada push no main. Workflows não são testáveis localmente: a
primeira execução real é o teste (ver `docs/validacao/trilha-ja.md`).

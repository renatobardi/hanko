# RELEASING — canais, versões e notas

Decisão em `docs/adr/0049-canais-ci-e-cobertura.md`. Item #60 do plano.

## Canais

| Canal | Gatilho | O que sai | Para quem |
|---|---|---|---|
| **edge** | push no `main` (`.github/workflows/edge.yml`) | imagem `ghcr.io/renatobardi/hanko:edge` (e `:edge-<sha8>`), multi-arch; binários snapshot como artefato do job (14 dias) | quem quer o que acabou de entrar; instalação de teste |
| **stable** | tag `vX.Y.Z` (`.github/workflows/release.yml`) | binários linux/darwin amd64/arm64, `checksums.txt`, SBOM, imagem `:X.Y.Z` e `:latest`, release no GitHub com as notas da versão | produção (`hanko-prd`) |

Versões seguem [SemVer](https://semver.org/lang/pt-BR/): enquanto for `0.x`, minor (`0.3.0`) pode quebrar
compatibilidade e patch (`0.3.1`) só corrige. Pré-release (`v0.3.0-rc.1`) sai como pré-release no GitHub
(`release.prerelease: auto`) e não move `:latest`.

`hanko update --channel edge|stable` (respeita o canal e faz backup antes) chega na onda 2 (#64); até lá a troca
de canal é trocar a tag da imagem.

## Notas de versão: `changelog.d/`

Cada marco/PR que muda comportamento visível acrescenta **um arquivo** `changelog.d/<nome>.md` (nunca edita o
`CHANGELOG.md` direto; evita conflito entre trilhas paralelas). Formato: marcadores `- ...` em pt-BR e uma linha
final `- English summary: ...`. Título opcional com `### ...`.

`scripts/release-notes.sh` faz duas coisas:

```bash
scripts/release-notes.sh notes            # prévia do próximo corte: [Unreleased] + changelog.d/*.md
scripts/release-notes.sh notes 0.2.0      # seção "## [0.2.0]" do CHANGELOG.md (é o que o release.yml publica)
scripts/release-notes.sh cut 0.3.0        # funde [Unreleased] + fragmentos em "## [0.3.0] — <hoje>", esvazia [Unreleased], apaga os fragmentos
scripts/release-notes.sh cut 0.3.0 --date 2026-11-01 --dry-run   # só mostra a seção
```

`scripts/test-release-notes.sh` testa o script contra um CHANGELOG de mentira e roda no `ci`.

## Cortar uma versão estável (passo a passo)

1. `main` verde (jobs `check` e `release-dry-run` do `ci`, `security`).
2. Num ramo `release/vX.Y.Z`: `scripts/release-notes.sh cut X.Y.Z`; revise o `CHANGELOG.md`; atualize a versão
   citada em README/ROADMAP se for o caso.
3. Commit assinado: `git commit -s -am "release: vX.Y.Z"`; PR para `main`; merge.
4. Tag **no commit mergeado** e push (o ambiente de automação não cria tags; é um passo humano):

   ```bash
   git switch main && git pull --ff-only
   git tag -s vX.Y.Z -m "vX.Y.Z"      # ou -a, se não houver chave de assinatura
   git push origin vX.Y.Z
   ```

5. O `release.yml` roda: falha cedo se o `CHANGELOG.md` não tiver a seção da tag (passo 2 esquecido), publica
   binários, imagem e release com as notas.
6. Confira no GitHub (release, assets, `docker pull ghcr.io/renatobardi/hanko:X.Y.Z`) e faça o deploy no `hanko-prd`
   com a tag nova.

Primeiras tags pendentes: `v0.1.0` e `v0.2.0` (já em produção; o `CHANGELOG.md` tem a seção `0.2.0`). Para a
`v0.1.0`, que não tem seção própria, crie-a à mão no `CHANGELOG.md` antes de taguear. Marcos por onda: `v0.3.0` ao
fechar a Onda 1, `v0.4.0` na 2, `v0.5.0` na 3, `v0.6.0` na 4 (ver ROADMAP).

## Rollback

Rollback = voltar para a tag anterior e restaurar o backup:

1. `hanko-prd`: subir a imagem da tag anterior (`ghcr.io/renatobardi/hanko:<anterior>`).
2. Se a versão nova aplicou migrations, restaurar o backup feito antes do deploy (RUNBOOK, seção de backup/restore);
   as migrations são aditivas, então só é preciso restaurar se os dados novos atrapalham a versão antiga.
3. Se a tag nova estava ruim, **não** reaproveite o número: corrija e solte `X.Y.Z+1`.

## Outros workflows (para saber onde as coisas rodam)

| Workflow | Propósito |
|---|---|
| `ci.yml` | PR rápido: fmt, vet, lint, build, testes (unit + integração), piso de cobertura, drift gates (openapi, templ, sqlc, bundle das ilhas) e `release-dry-run`. Os jobs `check` e `release-dry-run` são os status exigidos; não renomear. |
| `security.yml` | `govulncheck`, `trivy fs` e revisão de dependências no PR; semanal também. |
| `e2e.yml` | jornadas no navegador (esqueleto; #57, onda 1). |
| `evals.yml` | comportamento das personas, nightly + manual (esqueleto; #58, onda 2). |
| `edge.yml` | canal edge (acima). |
| `release.yml` | canal stable (acima). |

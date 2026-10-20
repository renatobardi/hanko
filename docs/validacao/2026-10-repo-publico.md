# Varredura após tornar o repo público (#23)

Data: 2026-10-19/20. Premissa da issue: o que aparecer é tratado como vazado. Este relatório não copia valores de segredos.

## O que foi varrido
- **Histórico git completo:** 23 ramos remotos + tag `v0.2.0` (inclui `evolucao/*`, `claude/*`, `ci/*`, `dependabot/*`). `gitleaks detect --log-opts=--all` (regras padrão) e `trufflehog git` (verificado e não verificado). Busca extra por nomes de arquivo sensíveis (`.env`, `.pem`, `.key`, dumps, bundles), DSNs, IPs e e-mails em todos os commits.
- **Config e deploy:** `deploy/` (incl. `oute-server/`), `hanko.example.yaml`, `.env.example`, `docker-compose.yml`, `deploy/compose.yaml`, `docs/RUNBOOK.md`.
- **Fixtures:** `internal/adapter/*/testdata`, `e2e/`.
- **GitHub:** issue #23 e PRs #1–#22 (corpo, 45 comentários, comentários de revisão); 7 workflows; ~40 runs (inclusive falhos) com log completo de 8 jobs (`check`, `edge`, `release`, `trivy-fs`, `e2e`, `evals`); artefatos `cobertura`, `hanko-edge-snapshot` e o registro de build da imagem.
- **Regra de conteúdo:** busca textual por nomes de produtos de terceiros na `main`.

## Achados
| # | Achado | Severidade | Ação |
|---|---|---|---|
| 1 | Nenhum segredo verificado no histórico. gitleaks: 44 alertas; trufflehog: 0 verificados, 12 não verificados. Todos são fixtures sintéticas de teste (PEM de mentira, DSNs locais `postgres:postgres`/`hanko:hanko`, tokens de exemplo do redactor) | nenhuma | `.gitleaks.toml` com allowlist desses casos |
| 2 | API key Web do Firebase em `deploy/oute-server/deploy-prd.sh`, `.env.example`, `hanko.example.yaml` | baixa | É pública por desenho (vai ao navegador). **Pendente (dono):** conferir no console que a chave tem restrição de referrer HTTP (`hanko.oute.pro`, localhost) e de APIs (só Identity Toolkit/Token Service) |
| 3 | Topologia da infra: domínio do prd, sub-rede e IP do LXC, gateway, IP Tailscale do host, nome do item do Keychain com a senha sudo, em `deploy-prd.sh` e `docs/RUNBOOK.md` (pt e en) | baixa | IPs privados/CGNAT, não roteáveis da internet; nenhum segredo. **Decisão do dono:** manter (o deploy depende deles) ou mover para variáveis sem default |
| 4 | E-mail pessoal do dono como `HANKO_AUTH_OWNER_EMAIL` em `deploy-prd.sh`, `.env.example`, `hanko.example.yaml`; e-mail do autor em 194 commits (`Signed-off-by`) e no log/artefato do build da imagem | baixa (PII) | **Decidido (2026-10-20): manter como está**, junto com a topologia do item 3. O dado já é público no histórico e os IPs não são roteáveis |
| 5 | Segredos do prd (`POSTGRES_PASSWORD`, `HANKO_SECRET`, token do serviço de memória) são gerados dentro do LXC e nunca entram no git | nenhuma | Confirmado. `HANKO_SECRET_KEY` **não** precisa rotacionar |
| 6 | Workflows: sem `pull_request_target`, sem `secrets: inherit`; único segredo usado é o `GITHUB_TOKEN` com permissões mínimas; logs mascaram credenciais (`***`); nenhum passo imprime o ambiente | nenhuma | — |
| 7 | Actions fixadas por tag (`@v7`, `@v0.33.1`), não por SHA | baixa (cadeia de suprimentos) | **Feito:** todas as actions fixadas por SHA com a versão em comentário; o dependabot de `github-actions` (agrupado, semanal) atualiza |
| 8 | Artefato `cobertura` contém só `cover.out` (caminhos do módulo e contagens); snapshot e build sem nada além da versão | nenhuma | — |
| 9 | PRs/issues: nenhum token, URL privada ou dado de cliente. Há links de sessão de agente e IDs de escopo/cobrança postados por bot de revisão (não são credenciais) | informativa | — |
| 10 | `.gitignore` só cobria `.env`; o deploy cria `.env.secrets`/`.env.tmp` transitórios | baixa | `.env.*` (exceto `.env.example`) passou a ser ignorado |
| 11 | Regra de terceiros: só nomes de integrações/harnesses suportados (ex.: CLIs de agentes, chat connectors no fora-de-escopo). Nada citado como referência de produto | nenhuma | — |

## Ações tomadas (este PR)
- Job `gitleaks` em `.github/workflows/security.yml` varrendo o histórico inteiro a cada PR, push no `main` e semanalmente. Com `.gitleaks.toml` o histórico atual passa limpo.
- `.gitignore`: `.env.*`.

## Pendente (Settings do GitHub, só o dono)
- Ativar **Secret scanning** + **Push protection**, **Dependency graph** + **Dependabot alerts** e **Private vulnerability reporting** (Settings › Code security). A ferramenta de varredura do GitHub recusou rodar por falta disso.
- `trivy-fs`: falha em todo run **na instalação do binário** (o script de instalação sai 1), nunca chega a varrer. Fixar uma versão do binário instalável ou trocar a instalação; depois remover `continue-on-error` dele e do `dependency-review`.
- Branch protection da `main`: exigir `check`, `release-dry-run` e `gitleaks`.
- Item 2 acima resolvido pelo dono (key já restrita); 3 e 4 decididos: manter.

## Feito pelo dono em Settings (2026-10-20)
- Push protection ligado (secret scanning é sempre ativo em repo público). Alerta #1: a API key Web do Firebase, esperado.
- Dependency graph, Dependabot alerts, malware alerts, security updates (agrupados) e CodeQL default setup ligados.
- Ruleset `main`: PR obrigatório, sem force push nem deleção, checks `check`, `release-dry-run` e `gitleaks`, resultado do CodeQL; admin no bypass.

## Conclusão
Nada a rotacionar e nenhuma reescrita de histórico necessária. Do ponto de vista de segredos, o repo pode seguir público; a exposição restante é de topologia e PII, a decidir pelo dono.

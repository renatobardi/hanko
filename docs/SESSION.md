# Estado da última sessão (card de prévia social + varredura do repo público)
Data: 2026-10-22 · PR #26 (`claude/dreamy-johnson-btv0kl`) → `main`

## Feito
- Card de prévia social: `docs/brand/social-card.html` (1280×640, tokens do Kubo DS, selo sakura, margem de 80px) e `scripts/gen-social-card.mjs` gerando 5 PNG (EN/PT, claro/escuro, guias).
- Varredura do repo público (#23): nenhum segredo vazado (`docs/validacao/2026-10-repo-publico.md`); job `gitleaks` no `security.yml` + `.gitleaks.toml`.
## Travado
- Dono liga no GitHub: secret scanning, push protection, Dependabot alerts, branch protection; publicar o PNG é manual (Settings → Social preview).
## Próximo passo
- Subir `docs/brand/social-card.png`; dono decide PII/topologia nos exemplos e no deploy, chave Web do Firebase, actions por SHA, `trivy-fs`. Deploy do `main` segue pendente.

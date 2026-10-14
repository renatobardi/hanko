# SESSION

- M5 (autenticação) pronto na branch `feat/m5`: migration 00005, `internal/auth` (Guard, argon2id, rate limit, Firebase JWKS), sessões, API keys, CLI `hanko auth`.
- UI: /entrar (local e firebase), /sair, rodapé da sidebar com usuário, Configurações > chaves de API.
- Deploy: `Caddyfile.authenticated` + `HANKO_CADDYFILE`; RUNBOOK §5 documenta o modo authenticated.
- Desvios: sem activity_event para escritas de auth; sem vínculo de e-mail entre providers; MCP com DisableLocalhostProtection.
- Não testado: login real Google/Firebase, JWKS real, Docker/Caddy.
- Próximo: M6; no provider local crie o dono pela CLI antes de expor.

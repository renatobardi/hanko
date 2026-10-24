### Cofre para agentes: contrato (ADR-0068, 0069, 0070)

- ADRs do cofre para agentes (segredos com campos, coleções, `hanko_secret_get`, TOTP, campo arquivo, histórico, delegação de segredo pessoal, saúde, import de `.env` e export cifrado), do envio com chave no fragmento do link e da reautenticação recente com passkey.
- Migration 00203: tabelas novas do cofre, do envio e das passkeys; colunas `secrets.rotate_after_days` e `sessions.reauth_at`/`reauth_method`; alerta `secret_rotation_due`. Nenhum dado existente é movido e nada muda no comportamento atual.
- API: 54 operações novas no OpenAPI (tag `vault`, `auth` e `public`), que respondem 501 até cada fatia ser entregue.
- O pacote do cofre externo passou de `internal/vault` para `internal/extvault`; `provider = "vault"` na API e no banco não muda.
- English summary: vault-for-agents contract — ADRs 0068–0070, migration 00203 (schema only, no data moved), 54 new API operations that return 501 until each slice ships, and `internal/vault` renamed to `internal/extvault`.

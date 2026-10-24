[English](CONTRIBUTING.md) | **Português**

# Contribuindo

- Licença de entrada = licença de saída: Apache-2.0.
- Todo commit exige DCO: `git commit -s` (Signed-off-by). Sem CLA.
- `ee/` é reservado para features enterprise sob licença comercial; não aceita contribuição externa.
- Decisão de arquitetura = ADR em `docs/adr/` (`NNNN-titulo.md`). ADR vence `docs/STACK.md` em conflito.
- Gates de CI bloqueiam merge: fmt, vet, lint, test, piso de cobertura, drift de openapi/templ/sqlc/ilhas, build; o `security` (govulncheck, trivy) roda junto. Eles rodam em jobs paralelos, e o job exigido `check` só passa se todos passarem.
- **Toda feature entra com teste.** Mudança que acrescenta comportamento não é mergeável sem um teste que falhe sem ela. Regra pura de domínio leva teste em tabela (e teste de propriedade com `testing/quick` e semente fixa para motores como o de orçamento); regra de service leva teste de integração contra Postgres.
- O piso de cobertura é aplicado no CI por `scripts/coverage-gate.sh`: `internal/domain` e `internal/heartbeat` ≥ 80%, `internal/service` ≥ 70%; `internal/api` é coberto por testes de integração (relatado, sem piso). Nunca baixe o piso para passar um PR; suba o piso quando a cobertura subir (ADR-0049).
- Mudança visível ao usuário acrescenta um fragmento `changelog.d/<nome>.md` em vez de editar o `CHANGELOG.md` (ver [docs/RELEASING.md](docs/RELEASING.md)).
- Problema de segurança se reporta em privado, ver [SECURITY.pt-BR.md](SECURITY.pt-BR.md).

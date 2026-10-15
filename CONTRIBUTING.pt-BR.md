[English](CONTRIBUTING.md) | **Português**

# Contribuindo

- Licença de entrada = licença de saída: Apache-2.0.
- Todo commit exige DCO: `git commit -s` (Signed-off-by). Sem CLA.
- `ee/` é reservado para features enterprise sob licença comercial; não aceita contribuição externa.
- Decisão de arquitetura = ADR em `docs/adr/` (`NNNN-titulo.md`). ADR vence `docs/STACK.md` em conflito.
- Gates de CI bloqueiam merge: lint, vet, govulncheck, test, openapi diff, build.

**English** | [Português](CONTRIBUTING.pt-BR.md)

# Contributing

- Inbound license = outbound license: Apache-2.0.
- Every commit requires a DCO sign-off: `git commit -s` (adds `Signed-off-by`). No CLA.
- `ee/` is reserved for enterprise features under a commercial license; it does not accept external contributions.
- An architecture decision is an ADR in `docs/adr/` (`NNNN-title.md`). An ADR wins over `docs/STACK.md` when they conflict. See the [ADR index](docs/en/adr/README.md).
- CI gates block merge: lint, vet, govulncheck, test, openapi diff, build.
- Use the domain vocabulary from the [glossary](docs/en/GLOSSARY.md): code, API, MCP and database use the English terms.
- Project documentation is written in Portuguese first (`docs/`); the English version in `docs/en/` is a translation kept in parallel.

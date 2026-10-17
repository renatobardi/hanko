**English** | [Português](CONTRIBUTING.pt-BR.md)

# Contributing

- Inbound license = outbound license: Apache-2.0.
- Every commit requires a DCO sign-off: `git commit -s` (adds `Signed-off-by`). No CLA.
- `ee/` is reserved for enterprise features under a commercial license; it does not accept external contributions.
- An architecture decision is an ADR in `docs/adr/` (`NNNN-title.md`). An ADR wins over `docs/STACK.md` when they conflict. See the [ADR index](docs/en/adr/README.md).
- CI gates block merge: fmt, vet, lint, test, coverage floor, openapi/templ/sqlc/islands drift, build; `security` (govulncheck, trivy) runs alongside.
- Use the domain vocabulary from the [glossary](docs/en/GLOSSARY.md): code, API, MCP and database use the English terms.
- Project documentation is written in Portuguese first (`docs/`); the English version in `docs/en/` is a translation kept in parallel.
- **Every feature comes with tests.** A change that adds behavior is not mergeable without a test that fails without it. Pure domain rules get table tests (add `testing/quick` property tests with a fixed seed for engines such as the budget); service rules get integration tests against Postgres.
- Coverage floors are enforced in CI by `scripts/coverage-gate.sh`: `internal/domain` and `internal/heartbeat` ≥ 80%, `internal/service` ≥ 70%; `internal/api` is covered through integration tests (reported, no floor). Never lower a floor to get a PR through; raise the floor when coverage rises (ADR-0049).
- User-visible changes add a fragment `changelog.d/<name>.md` instead of editing `CHANGELOG.md` (see [docs/en/RELEASING.md](docs/en/RELEASING.md)).
- Report security problems privately, see [SECURITY.md](SECURITY.md).

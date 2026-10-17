**English** | [Português](SECURITY.pt-BR.md)

# Security policy

## Reporting a vulnerability

Please **do not** open a public issue for a security problem.

Report it privately through GitHub: **Security → Report a vulnerability** on
<https://github.com/renatobardi/hanko/security/advisories/new> (private vulnerability reporting).

Include what you can: affected version or commit, how to reproduce, the impact you see, and whether it needs a
logged-in user. Do not include real credentials, tokens or customer data.

What to expect:

- acknowledgement within **3 business days**;
- a first assessment (accepted, needs more information, or not a vulnerability) within **7 days**;
- a fix and a coordinated disclosure date agreed with you; credit in the release notes if you want it.

## Supported versions

Hanko is pre-1.0. Fixes go to the latest stable release (the most recent `vX.Y.Z` tag) and to `main`
(the `edge` channel). Older releases do not get backports; upgrade to the latest stable one.
Channels are described in [docs/RELEASING.md](docs/RELEASING.md).

## Scope

In scope: the `hanko` binary and its HTTP API, UI, CLI and MCP server; authentication and sessions; company
isolation (every read and write is scoped to a company); secret handling (integration secrets are encrypted at rest
and never returned); the artifact and webhook delivery paths; the container images and release artifacts.

Out of scope: vulnerabilities in third-party agents (harnesses) that Hanko only launches; findings that need
the `trusted` mode on an exposed network (it is documented as local-only, see ADR-0017/0018); denial of service by
volume against a deployment without a reverse proxy.

## How we keep dependencies safe

- `govulncheck` and `trivy fs` run on every pull request, on `main` and weekly (`.github/workflows/security.yml`).
- Dependabot opens weekly updates for Go modules, the build tooling of the React islands (npm) and GitHub Actions
  (`.github/dependabot.yml`).
- Postgres is the only runtime dependency; a new Go dependency needs a justified line in an ADR.

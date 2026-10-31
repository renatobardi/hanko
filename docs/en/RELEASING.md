> Português: [docs/RELEASING.md](../RELEASING.md) (source; this is a translation of it)

# RELEASING — channels, versions and notes

Decision in `docs/adr/0049-canais-ci-e-cobertura.md`. Item #60 of the plan.

## Channels

| Channel | Trigger | Output | For whom |
|---|---|---|---|
| **edge** | push to `main` (`.github/workflows/edge.yml`) | image `ghcr.io/renatobardi/hanko:edge` (and `:edge-<sha8>`), multi-arch; snapshot binaries as a job artifact (14 days) | people who want what just landed; trial installs |
| **stable** | tag `vX.Y.Z` (`.github/workflows/release.yml`) | linux/darwin amd64/arm64 binaries, `checksums.txt`, SBOM, image `:X.Y.Z` and `:latest`, a GitHub release with the version notes | production (`hanko-prd`) |

Versions follow [SemVer](https://semver.org/): while on `0.x`, a minor (`0.3.0`) may break compatibility and a
patch (`0.3.1`) only fixes. A pre-release (`v0.3.0-rc.1`) is published as a GitHub pre-release
(`release.prerelease: auto`) and does not move `:latest`.

`hanko update --channel edge|stable` (channel-aware, backs up first) arrives in wave 2 (#64); until then,
switching channels means switching the image tag.

## Release notes: `changelog.d/`

Every milestone/PR that changes visible behavior adds **one file** `changelog.d/<name>.md` (and never edits
`CHANGELOG.md` directly, which avoids conflicts between parallel tracks). Format: `- ...` bullets in Portuguese and a
final `- English summary: ...` line. An optional `### ...` title is allowed.

`scripts/release-notes.sh` does two things:

```bash
scripts/release-notes.sh notes            # preview of the next cut: [Unreleased] + changelog.d/*.md
scripts/release-notes.sh notes 0.2.0      # the "## [0.2.0]" section of CHANGELOG.md (what release.yml publishes)
scripts/release-notes.sh cut 0.3.0        # merges [Unreleased] + fragments into "## [0.3.0] — <today>", empties [Unreleased], deletes the fragments
scripts/release-notes.sh cut 0.3.0 --date 2026-11-01 --dry-run   # only prints the section
```

`scripts/test-release-notes.sh` tests the script against a fake CHANGELOG and runs in `ci`.

## Cutting a stable version (step by step)

1. `main` is green (`check` and `release-dry-run` jobs of `ci`, plus `security`).
2. On a `release/vX.Y.Z` branch: `scripts/release-notes.sh cut X.Y.Z`; review `CHANGELOG.md`.
3. Signed-off commit: `git commit -s -am "release: vX.Y.Z"`; PR to `main`; merge.
4. Tag **the merged commit** and push (automation environments do not create tags; this is a human step):

   ```bash
   git switch main && git pull --ff-only
   git tag -s vX.Y.Z -m "vX.Y.Z"      # or -a, if there is no signing key
   git push origin vX.Y.Z
   ```

5. `release.yml` runs: it fails early if `CHANGELOG.md` has no section for the tag (step 2 forgotten), then publishes
   binaries, the image and the release with the notes.
6. Check GitHub (release, assets, `docker pull ghcr.io/renatobardi/hanko:X.Y.Z`) and deploy the new tag to `hanko-prd`.
   With `deploy/oute-server/deploy-prd.sh --build-local` the image is built on the machine that runs the deploy and sent
   to the LXC, which starts it with `compose up --no-build`: the server does not compile. Use it when the build inside
   the LXC (4 GiB memory cap) saturates the host disk, as it did on `v0.8.0` (99% IO pressure).

Pending first tags: `v0.1.0` and `v0.2.0` (already in production; `CHANGELOG.md` has the `0.2.0` section). `v0.1.0`
has no section of its own: write it by hand in `CHANGELOG.md` before tagging. Wave releases: `v0.3.0` closing wave 1,
`v0.4.0` for wave 2, `v0.5.0` for wave 3, `v0.6.0` for wave 4 (see the ROADMAP).

## Rollback

Rollback = previous tag + restore:

1. `hanko-prd`: bring up the image of the previous tag (`ghcr.io/renatobardi/hanko:<previous>`).
2. If the new version applied migrations, restore the backup taken before the deploy (RUNBOOK, backup/restore);
   migrations are additive, so a restore is only needed when the new data gets in the old version's way.
3. If the new tag was bad, do **not** reuse the number: fix it and ship `X.Y.Z+1`.

## Other workflows

| Workflow | Purpose |
|---|---|
| `ci.yml` | Fast PR checks in parallel jobs: `estatico` (fmt, vet, lint), `gerados` (build and drift gates for openapi, templ, sqlc and the islands bundle), `test` (unit + integration in a single run, sharded into `service` and `resto`, each with its own Postgres) and `release-dry-run`. `check` aggregates them: it passes only if all of them pass, verifies that the shards covered every package and applies the coverage floor to the merged profile. The `check` and `release-dry-run` jobs are the required statuses; do not rename them. |
| `security.yml` | `govulncheck`, `trivy fs` and dependency review on PRs; weekly too. |
| `e2e.yml` | browser journeys (skeleton; #57, wave 1). |
| `evals.yml` | persona behavior, nightly + manual (skeleton; #58, wave 2). |
| `edge.yml` | the edge channel (above). |
| `release.yml` | the stable channel (above). |

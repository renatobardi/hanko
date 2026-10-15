> Português: [docs/DEMOS.md](../DEMOS.md) (source; this is a translation of it)

# DEMOS — recording scripts per milestone

One script per milestone of the [ROADMAP](../ROADMAP.md) (Portuguese), each ≤3 min. Commands are the real CLI (`hanko --help`). Screen names are given in English; the UI speaks pt-BR by default (language selector in the sidebar footer), and the CLI follows `HANKO_LANG=en`. The owner records and creates the tags; this file only paves the way.

## Quick scenario (applies to every demo)

[`examples/demo-company.json`](../../examples/demo-company.json) (format `hanko.company.v1`, [ADR-0022](../adr/0022-export-import.md), in Portuguese) creates the **Demo** company in one go: workspace, project, subscription, company budget (US$ 50) and Ana's budget (US$ 10), three personas (Carla, CTO, claude/sonnet; Ana, engineer, claude/haiku, with mandatory review by Rui; Rui, reviewer, codex), org chart, 4 tasks (`DEMO-1`…`DEMO-4`, `DEMO-3` a child of `DEMO-2`) and the "Daily summary" flow. No secrets. The persona instructions and task texts are in pt-BR. An integration test (`TestExemploDemoImporta`) keeps it importable.

```bash
# 1. local server (RUNBOOK §1 and §5 cover the database, HANKO_SECRET and the owner)
mkdir -p /tmp/hanko-demo/demo/app && cd /tmp/hanko-demo/demo/app && git init -q   # where the personas work
export HANKO_WORKSPACES_ROOT=/tmp/hanko-demo      # the example workspace lives at <root>/<company-slug>/app
hanko serve &                                     # http://127.0.0.1:7420

# 2. client (authenticated mode: an owner API key)
export HANKO_TOKEN=$(hanko auth token create --name demo | grep -oE 'hk_[A-Za-z0-9_-]+')

# 3. the example company
hanko import examples/demo-company.json --slug demo --active
export HANKO_COMPANY=demo
hanko persona list && hanko org && hanko task list
```

- `--active` turns personas and the flow on; without it the import comes in **paused** (nothing works until you release it in the UI). Use `--active` to record.
- To repeat: import always creates a new company; use another `--slug` (`demo2`) or wipe the demo database.
- `claude` and `codex` must be on the PATH (or `HANKO_CLAUDE_BIN`/`HANKO_CODEX_BIN`). To rehearse without a real harness, edit a copy of the JSON and set `"agent"` to `"fake"`.
- Rehearse once before recording: each persona's first run is the slowest.

In each script, "Passes when" repeats the ROADMAP demo criterion.

---

## M1 — Minimal loop with CLI and live log

**Target:** 2 min · **Prerequisites:** the quick scenario (Postgres up, `hanko serve`, `demo` imported). Optional: Grafana at <http://127.0.0.1:3000> (`otel-lgtm`, RUNBOOK §1) for the trace.

> The `DEMO-5` keys below assume a freshly imported company; if you ran other scripts first, use the key `task create` prints.

```bash
hanko persona list                                  # Carla, Ana, Rui: hired
hanko task create "Say hello and describe the repository" -p ana --project demo
hanko run list                                      # the run starts from the assignment (heartbeat/river)
hanko run logs -f <run>                             # live log (SSE); the friendly name, e.g. Veloz_Raposa, works too
hanko task comment DEMO-5 "Thanks, Ana. Can you expand on point 2?"
hanko run list --task DEMO-5                        # the comment wakes the persona: a second run
hanko task show DEMO-5                              # thread with the persona's comment
```

**Show on screen:** `run logs -f` scrolling while the persona works; the task status changing; the persona's reply in the thread. In Grafana, *Explore → Tempo*, show the trace of the first request (`hanko-*`).

**Passes when:** `hanko serve` → hire persona → create task → run → live log → comment, and the trace shows up in Grafana (ROADMAP, M1).

---

## M2 — UI, MCP and memory across runs

**Target:** 3 min · **Prerequisites:** quick scenario **with ai-memory on** (`HANKO_MEMORY_PROVIDER` not `none`, RUNBOOK §1); sign in at <http://127.0.0.1:7420/entrar> as the owner.

**Clicks**

1. Company *Dashboard* (Demo): persona, task and run cards.
2. *Tasks*: list grouped by status. Open **DEMO-1** (Descrever o repositório) and click **Wake Ana** (the task button).
3. Open the run (*Inspect* link on the task, or *Runs*): live log. Point at the `hanko_task_comment`/`hanko_task_get` calls: the persona is using Hanko's MCP server (`/mcp`, `hanko_*` tools).
4. When it finishes, go back to the task and comment: "Now list the top-level files." (wakes the persona; second run in the **same workspace**).
5. Open the second run: show that it picks up what the first did (dispatch injects the memory *Briefing* and the workspace handoff; the persona refers to the earlier work).
6. *Personas* → **Hire persona**: show the hiring form (do not submit).

**Show on screen:** task list, live log, MCP tool calls, run 2 resuming run 1's context.

**Passes when:** run 2 in the same workspace receives run 1's handoff (ROADMAP, M2). Dogfooding starts after this.

---

## M3 — Org chart, delegation and human gate

**Target:** 3 min · **Prerequisites:** quick scenario; ai-memory on (to show the decision becoming memory).

**Commands and clicks**

```bash
hanko org                                           # Carla at the root; Ana and Rui report to her
hanko task wake DEMO-2                              # the CTO plans the MVP (instructed to delegate and ask for a gate)
hanko task list                                     # new child tasks assigned to Ana (atomic checkout: one persona per task)
hanko gate list --status pending                    # gate requested by the persona (hanko_gate_request)
hanko gate show <gate>
hanko gate approve <gate> -n "Plan approved. Go ahead."
```

In the UI: *Org chart* (tree Carla → Ana, Rui) → *Inbox* (pending gate) → open the gate and click **Approve** (or **Request changes**/**Reject**, which require a note) → *Decisions* (the recorded decision).

Extra, if time allows: when one of Ana's tasks closes, Rui's **automatic review** runs before the human gate (`require_review`, ADR-0020): `hanko gate list --kind task_review`.

**Show on screen:** the org tree; the child task created by Carla with Ana as assignee; the gate with its audit trail; the persona woken after the decision; the decision under *Decisions*.

**Passes when:** a persona delegates; a human decides the gate; the decision becomes memory (ROADMAP, M3).

---

## M4 — Budget blocks the run; a flow creates a task by itself

**Target:** 3 min · **Prerequisites:** quick scenario. The per-run reservation is at least US$ 1.00 (`HANKO_RUN_RESERVE_USD`), so a cap below that blocks the next run deterministically, without waiting for real spend to blow through it.

**Part 1: budget**

```bash
hanko budget show                                   # company US$ 50, Ana US$ 10
hanko budget set 0.50 --persona ana                 # Ana's cap below one run's reservation
hanko task create "This run must not open" -p ana --project demo   # note the printed key (here, DEMO-5)
hanko task wake DEMO-5                              # the run is refused by the hard stop
hanko run list --persona ana                        # no new run opened
hanko cost --by persona                             # month's cost per persona
hanko budget set 10 --persona ana                   # releases: a persona paused by budget comes back by itself
```

In the UI: *Costs* (budgets and subscriptions, weekly usage) and *Inbox*/alerts showing the budget reached.

**Part 2: flow**

```bash
hanko flow list                                     # "Resumo diário" (every day at 9:00, company time zone)
hanko flow show resumo-diario                       # next fire
hanko flow run resumo-diario                        # fire now (cron would do the same at 9:00)
hanko task list                                     # "Resumo diário <date>" created by itself, assigned to Carla
```

In the UI: *Flows* → **Resumo diário** → **Run now**; the firing appears with the task it generated.

**Show on screen:** the budget refusal (no new run, persona/alert), the spend under *Costs*, the flow creating the dated task.

**Passes when:** budget blows → run blocked; the daily flow creates a task by itself (ROADMAP, M4).

---

## M5 — Production at hanko.oute.pro

**Target:** 3 min · **Prerequisites:** production up (`deploy/oute-server/deploy-prd.sh --edge`); an authorized Google account; your own API key for the CLI against production (`export HANKO_URL=https://hanko.oute.pro HANKO_TOKEN=hk_…`, created under *Settings*); a second e-mail for the invite. Never record a screen showing tokens: create the demo key and revoke it afterwards.

**Clicks and commands**

1. Open <https://hanko.oute.pro> (signed out): it redirects to `/entrar`. **Continue with Google**, land on the dashboard.
2. *Settings → Members*: **Invite** an e-mail as `viewer`; or `hanko member invite someone@example.com --role viewer` (prints the link; Hanko sends no e-mail). Show `hanko member list` and a role change: `hanko member role someone@example.com member`. Open the link in a private window: the viewer sees everything but no action buttons.
3. Export/import:
   ```bash
   hanko export -o demo.json                          # current company, no secrets
   hanko import demo.json --slug demo-copy --active    # new company, all or nothing
   ```
   Switch company with the selector in the UI.
4. PWA: install icon in the browser address bar (or *Add to Home Screen* on a phone), then open the installed app.
5. Bilingual: language selector in the sidebar footer (pt-BR ↔ en); in the CLI, `HANKO_LANG=en hanko task list`.
6. Technical footer (optional, 15 s), on the server: `BASE=http://127.0.0.1:3810 PUBLIC_HOST=hanko.oute.pro deploy/oute-server/smoke.sh`.

**Show on screen:** redirect to login, the Google sign-in, an invite with a role, export/import, the installed app, the language switch.

**Passes when:** hanko.oute.pro with Google login (ROADMAP, M5). Also shipped in v0.2 and worth showing: roles, invites, export/import, PWA, bilingual UI.

---

## Tags and release (the owner creates them)

Nothing in this repository creates or pushes tags. After each approved demo, pick the commit where the milestone passed (`git log --oneline`; if everything is already on `main`, use its `HEAD`) and create an **annotated** tag (not signed; use `-s` instead of `-a` to sign):

```bash
git tag -a v0.1.0-m1 <commit> -m "M1: minimal loop (CLI, run, live log, comment)"
git tag -a v0.1.0-m2 <commit> -m "M2: UI, MCP and memory across runs"
git tag -a v0.1.0-m3 <commit> -m "M3: org chart, delegation and gates"
git tag -a v0.1.0-m4 <commit> -m "M4: budgets, hard stop and flows"
git tag -a v0.1.0-m5 <commit> -m "M5: production at hanko.oute.pro"
git tag -n                                          # check before publishing
```

The `v0.1.0` release, once all five demos passed:

```bash
git tag -a v0.1.0 -m "Hanko v0.1.0" && git push --tags
```

Careful: the `release` workflow fires on **any** `v*` tag. `git push --tags` also pushes the `v0.1.0-mN` tags, and each one produces a release (marked pre-release, `prerelease: auto` in goreleaser) and registry images. To publish only what you want: `git push origin v0.1.0-m1`, … and, last, `git push origin v0.1.0`. CI's `release-dry-run` already validates the configuration before the first tag.

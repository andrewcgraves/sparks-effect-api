# Ticket template — Sparks Effect

The shape a Linear issue takes in team `Sparks Effect` (`SPA-`). One spine, six
variants. Canonical copy; the other three repositories link here.

The audience is two readers at once. A **human** skims the first six lines and
knows what this is. An **agent** — Claude, Cursor, whatever picks the ticket up
next — reads the whole body and starts work without asking a question, because
every fact it would have had to guess is already written down.

Those are not in tension. The tickets in this workspace that agents shipped
cleanly on the first pass ([SPA-302](https://linear.app/sparks-effect-personal/issue/SPA-302),
[SPA-289](https://linear.app/sparks-effect-personal/issue/SPA-289),
[SPA-293](https://linear.app/sparks-effect-personal/issue/SPA-293),
[SPA-308](https://linear.app/sparks-effect-personal/issue/SPA-308)) are also the
most readable ones. They share a shape. This file is that shape, written down.

## Contents

- [The spine](#the-spine) — the six blocks every ticket has
- [At a glance](#at-a-glance) — the front block, and why it is the whole trick
- [Definition of ready](#definition-of-ready) — the gate for `ready-for-agent`
- [Fields, not body](#fields-not-body) — labels, parents, dependencies
- [Templates](#templates) — six paste-ready bodies
- [Reference](#reference) — repo matrix, verification, seams, human-only actions, vocabulary traps

## Installing these in Linear

Linear's API cannot create issue templates, so this is a paste job, done once:

**Settings → Team `Sparks Effect` → Templates → New issue template** — one per
variant below, named `Bug`, `Change`, `Rollout`, `Spike`, `Ops`, `Seed`. Paste
the body from [Templates](#templates). Set the default label in the template
where the variant names one.

---

## The spine

Six blocks. Every variant is these, renamed or dropped:

| Block | Answers | Drop it when |
| --- | --- | --- |
| **Lede + at a glance** | What is this, where does it land, who has to touch a console | never |
| **Evidence** | How do we know, measured when | never — "I think" is a spike, not a ticket |
| **Already true** | What must the agent *not* re-derive | the ticket touches one file |
| **What to do** | The change, per repo, in order | it is a spike (the whole point is not to) |
| **Not this ticket** | The nearby thing it is not | nothing nearby is open |
| **Acceptance** | What is checked, by whom, with which command | never |

The two that get skipped and shouldn't are **Already true** and **Not this
ticket**. They are the two that cost an agent a wasted PR when they're missing:
one sends it re-measuring facts you already have, the other sends it fixing three
bugs in a diff you wanted to review as one.

## At a glance

The front block. Five bold labels, one line each, immediately under the lede:

```markdown
- **Type:** bug | change | rollout | spike | ops | seed
- **Repos:** `sparks-effect-api` → `sparks-effect-routing-worker`
- **Human steps:** none
- **Deploy impact:** migration `000NN`; no tile cycle
- **Verify:** `make dev-workflow` in both; `make check-contract` in the worker
```

A human reads five lines and knows the blast radius. An agent greps five labels
and knows which repos to clone, what it is not allowed to attempt, and which
command proves it is finished. Keep every value to one line; the detail goes in
the body.

**`Type:`** is in the body on purpose. The type labels (`Bug`, `Feature`,
`Improvement`) were retired on 2026-08-19 and there is no active replacement, so
the body is the only place the type reliably survives. It is also what tells an
agent whether it is allowed to write code at all — a `spike` that comes back with
a PR has failed.

**`Repos:`** uses `→` for *must land in this order*, `+` for *any order*. This is
the one field that has actually cost this project a production incident:
[SPA-273](https://linear.app/sparks-effect-personal/issue/SPA-273) was a
three-repo rollout that merged out of dependency order — the API and
`kustomize-config` shipped their halves while the worker's stayed on a branch —
and nothing in the system detected it. See
[cross-repo seams](#cross-repo-seams) for which pairs are actually coupled.

**`Human steps:`** is `none` or a pointer to the checklist. `none` is a promise:
an agent can take this ticket from `Todo` to a mergeable PR with nothing but repo
access. Anything in [human-only actions](#human-only-actions) breaks that promise
and belongs on the list, where the ticket cannot silently stall waiting for it.

**`Deploy impact:`** is where the operational tail goes: a migration number, a
`make cycle-tiles` run, a production pin bump, a cache that has to be invalidated,
a `vX.Y.Z` tag. `none` is a fine and common answer. What is not fine is silence —
[SPA-269](https://linear.app/sparks-effect-personal/issue/SPA-269) (stale cached
polygons) and [SPA-303](https://linear.app/sparks-effect-personal/issue/SPA-303)
(egress-cache invalidation on promotion) are both this line, unwritten.

**`Verify:`** names the command, not the intent. `make dev-workflow` in each repo
touched, plus whatever else the change earns —
[verification](#verification-per-repo) has the table.

## Definition of ready

`ready-for-agent` means: *a self-contained vertical slice, scoped and specified
well enough for an agent to pick up and implement without further clarification.*
That is testable. Apply the label only when all seven hold:

- [ ] The **front block** is filled in, with `Human steps:` honestly answered.
- [ ] Every claim in **Evidence** is measured and dated, with a `path:line`, a
      command, or a number — not a recollection.
- [ ] **What to do** names files or seams, not outcomes. "Extract one module in
      `api/`" is actionable; "make error handling consistent" is not.
- [ ] **Acceptance** is falsifiable by someone who was not in the conversation,
      and at least one item is a command or a test that fails today.
- [ ] Every repo the change touches is in `Repos:`, in landing order.
- [ ] Anything nearby and out of scope is listed and linked.
- [ ] Nothing in the body says *decide*, *figure out*, *investigate*, or *TBD*.
      Those are a [spike](#4--spike), which is a different ticket.

Fails any of them → `seed-ticket` (an idea worth keeping) or `needs-refinement`
(a ticket that is nearly there), and say in one line what is missing. Both are
respectable states. `ready-for-agent` on a ticket that isn't is how you get a
confident PR that solves the wrong problem.

The backlog already shows the failure mode: *"Beautify the route map"*,
*"Create a header bar."*, *"allow users to create route"* have sat unlabelled and
untouched for months, because there is nothing in them to start on.

## Fields, not body

Some things belong in Linear's fields, where they are queryable, and must not be
duplicated in prose.

| Field | Use |
| --- | --- |
| **Labels** | Active set: `ready-for-agent`, `seed-ticket`, `needs-refinement`, `research-needed`, `Agent: low-cost`, `Agent: high-cost`. Everything else in the picker is retired. |
| **Cost labels** | `Agent: low-cost` for a mechanical change with a named file and a passing test as the oracle. `Agent: high-cost` for anything that needs judgement about the domain. Omitted means unrouted. |
| **Parent / sub-issue** | Per-repo children of a rollout. The parent holds the model and the order; children hold one repo's diff each. |
| **Dependencies** | Linear's native blocking relations, never a `Blocked by:` line in the body. `docs/agents/issue-tracker.md` treats the native relation as canonical. |
| **Project** | `Isochrone Polishing`, `Tech Debt Resolution`, `Create testing env`, `UI/UX Polish…`. |
| **Priority** | Urgent means production is wrong right now, as in [SPA-302](https://linear.app/sparks-effect-personal/issue/SPA-302). |

**Title:** the outcome or the defect, in a sentence, specific enough to be
unambiguous in a list of forty — *"Send `denoise=0` on Valhalla isochrone requests
so transbay origin islands are not dropped"*, not *"Fix isochrones"*. No
`[BUG]` / `[FEATURE]` / `[API]` prefixes: the front block carries the type and the
repos, and the prefixes have drifted into five inconsistent styles. The one worth
keeping is a leading `SPIKE:`, because it changes what an agent may do.

**Branch and commit:** commits and PR titles lead with the identifier —
`SPA-84: add authoring API client, domain types, and job-polling helper`. Linear's
generated branch name is fine for humans; agent sessions use whatever branch the
session was given.

> **Label gap, worth fixing separately.** `sparks-effect-website/docs/agents/triage-labels.md`
> maps five canonical triage roles, but only `ready-for-agent` exists in the
> workspace — `needs-triage`, `needs-info`, `ready-for-human` and `wontfix` were
> never created. Either create them or repoint that table at
> `needs-refinement` / `research-needed`. Until then `/triage` will mint labels on
> first use.

---

## Templates

Six bodies. Paste one, delete what does not apply. A section you cannot fill in
is a signal, not an inconvenience — usually that this is a spike.

### 1 · Bug

Reference: [SPA-302](https://linear.app/sparks-effect-personal/issue/SPA-302),
[SPA-289](https://linear.app/sparks-effect-personal/issue/SPA-289),
[SPA-297](https://linear.app/sparks-effect-personal/issue/SPA-297).

```markdown
**One sentence: what is wrong, and whether it is live in production.**

- **Type:** bug
- **Repos:** `<repo>`
- **Human steps:** none
- **Deploy impact:** none
- **Verify:** `make dev-workflow` in `<repo>`

## Symptom

What a user or operator sees, with the exact inputs that produce it: pin
coordinates, mode, budget, scenario slug, endpoint, overlay. Someone who has
never seen this must be able to reproduce it from this paragraph alone.

## Evidence

Measured, and dated. Code as `path/file.ts:70-74`, commands with their output,
counts, timestamps, screenshots.

Say what came back **clean** as well. "All four error codes checked against the Go
handlers, no drift" is the sentence that stops an agent spending its first hour
re-deriving what you already ruled out.

## Cause

The mechanism, in a paragraph. If this is a guess, say so — or file a spike
instead and link it here.

## Fix

Repo, file, and the change. Name every call site; two of them is the usual reason
a fix ships to one surface and not the other.

## Not this ticket

Nearby symptoms with a different cause. One line each on *why* it is separate,
and a link to its own issue. Include the ones that look like this bug and aren't.

## Acceptance

- [ ] A test that fails without this fix and passes with it. Name it.
- [ ] <exact reproduction from Symptom> now yields <exact expected result>.
- [ ] `make dev-workflow` passes in `<repo>`.
```

### 2 · Change

A vertical slice: a feature, a refactor with a named seam, a tech-debt item.
Reference: [SPA-289](https://linear.app/sparks-effect-personal/issue/SPA-289),
[SPA-293](https://linear.app/sparks-effect-personal/issue/SPA-293),
[SPA-236](https://linear.app/sparks-effect-personal/issue/SPA-236).

```markdown
**One sentence: what will be true afterwards that is not true now.**

- **Type:** change
- **Repos:** `<repo>` → `<repo>`
- **Human steps:** none
- **Deploy impact:** none
- **Verify:** `make dev-workflow` in each

## Why now

What this unblocks, what it stops recurring, or which incident it came out of.
Two to four sentences. If the honest answer is "it would be tidier", say that —
it is a real reason and it sets the priority correctly.

## What is already true

The load-bearing facts, verified, so nothing here gets re-derived or re-litigated:
which helper already exists and is correct, which invariant already holds, which
constant is already plumbed through. Cite `path:line`.

This is the highest-value section in the whole ticket and the one most often
skipped.

## What to do

Numbered. Per repo, in landing order. Name files, functions, endpoints, columns —
in the vocabulary of [CONTEXT.md](../../CONTEXT.md).

1. …
2. …

## Out of scope

The adjacent work this deliberately does not do, linked. An agent with spare
context will otherwise do it and hand you a diff twice the size you can review.

## Done when

- [ ] <behaviour, stated so someone else can check it>
- [ ] <the invariant this establishes, e.g. "`JobFailedError` is referenced in
      exactly one place">
- [ ] `make dev-workflow` passes in each repo touched.
```

### 3 · Rollout

A change that lands in more than one repository. The parent holds the model and
the order; per-repo children hold the diffs. Reference:
[SPA-310](https://linear.app/sparks-effect-personal/issue/SPA-310),
[SPA-262](https://linear.app/sparks-effect-personal/issue/SPA-262),
[SPA-247…250](https://linear.app/sparks-effect-personal/issue/SPA-247).

```markdown
**One sentence: the end state, across all repos.**

- **Type:** rollout
- **Repos:** `sparks-effect-api` → `sparks-effect-routing-worker` → `sparks-effect-website` → `kustomize-config`
- **Human steps:** see checklist below
- **Deploy impact:** <migration / pin bump / tile cycle / tag>
- **Verify:** `make dev-workflow` per repo; system check in Acceptance

## Goal

The end state in three or four sentences, in the vocabulary of
[CONTEXT.md](../../CONTEXT.md). What is true when every child is closed.

## Why

The defect in the current arrangement. Where the two halves disagree today, and
what breaks while they do.

## Per repo

| Repo | Change | Lands | Blocked by |
| --- | --- | --- | --- |
| `sparks-effect-api` | … | SPA-… | — |
| `sparks-effect-routing-worker` | … | SPA-… | the API child |
| `sparks-effect-website` | … | SPA-… | the worker child |
| `kustomize-config` | … | SPA-… | — |

## Landing order, and why

Not just the sequence — the failure it prevents. "Do not land the workflow-string
PR before the default-branch rename: CI would start watching a ref that does not
exist yet and publishes stop silently."

Where a seam is versioned by a fixture or a shared constant, say which side moves
first and whether the other side tolerates both shapes in between.

## What this is not

The plausible-looking alternative model that is *not* what we are doing, and the
decision it would re-litigate. Link the ADR or the earlier issue.

## Human checklist (cannot be done from a PR)

- [ ] <console change: GitHub setting, Vercel, Railway, secret, `make apply`, tag>

## Children

- [ ] SPA-… `<repo>` — …
- [ ] SPA-… `<repo>` — …

Each child is a self-contained **Change** ticket with its own acceptance, linked
to this parent as a Linear sub-issue and blocked by its predecessor via Linear's
native dependencies.

## Acceptance (system-level)

Things no single repo's tests can prove. The end-to-end behaviour, the two sides
agreeing, the contract check passing symmetrically.

- [ ] …
```

### 4 · Spike

Reference: [SPA-100](https://linear.app/sparks-effect-personal/issue/SPA-100),
[SPA-251](https://linear.app/sparks-effect-personal/issue/SPA-251),
[SPA-246](https://linear.app/sparks-effect-personal/issue/SPA-246).

```markdown
**The question, as a question.**

- **Type:** spike — **no production code changes**
- **Repos:** read-only: `<repos>`
- **Human steps:** none
- **Deploy impact:** none
- **Verify:** n/a — output is a comment on this ticket

## Why we are asking now

The decision this blocks, or the recurring symptom that has outgrown guessing.
Name the ticket that is waiting on the answer.

## Constraints

- **No production code changes.** A throwaway branch, a script, a `curl`, a
  scratch overlay is fine — none of it merges.
- Do not reorganise the repo, do not fix bugs found along the way. File them.
- Time box: <hours / a session>. Report what you have when it runs out.

## Where to look

Files, endpoints, dashboards, upstream issues, prior art. Everything already
known, so the spike starts where the last one stopped.

## Output contract

A comment on this ticket containing:

1. **The answer**, in one paragraph, stated as a recommendation.
2. **The evidence** it rests on — commands run, numbers measured, dated.
3. **What was ruled out**, and why. As valuable as the recommendation.
4. **Follow-up tickets filed**, linked. A spike that recommends work and files
   nothing has not landed.
5. **Confidence**, and what would change the answer.

Then set this issue to `Review` — do not close it. Closing is the reader's call
once the follow-ups exist.
```

### 5 · Ops

Cluster, manifests, tiles, feeds — anything whose blast radius is the running
system rather than a test suite. Reference:
[SPA-308](https://linear.app/sparks-effect-personal/issue/SPA-308),
[SPA-303](https://linear.app/sparks-effect-personal/issue/SPA-303),
[SPA-296](https://linear.app/sparks-effect-personal/issue/SPA-296).

```markdown
**One sentence: what the operator can do afterwards that they cannot do now.**

- **Type:** ops
- **Repos:** `kustomize-config`
- **Human steps:** `make apply` / `make cycle-tiles GEN=<date>` — an agent cannot reach the cluster
- **Deploy impact:** <rollout / generation / pin bump / cache invalidation>
- **Verify:** `make dev-workflow` (validate + shellcheck), then `make verify-cycle` after apply

## Problem

The current procedure and what it costs — the outage, the manual step that is
load-bearing, the arm/disarm that gets forgotten. Include the way it fails
*while looking like success*, if it has one.

## Measured state

Dated, from the live cluster. File counts, sizes, timestamps, free disk, the
output of `/status`, which pod is serving what. This is what separates an ops
ticket from a hunch, and it is what an agent cannot obtain for itself.

## What is already true in the repo

The mechanisms that already exist and do not need inventing: the env var already
plumbed through every script, the probe budget already set, the guardrail that
already takes the right branch. Cite files.

## What to do

Numbered, ending in a single operator entry point. One `make` target the operator
types, with no arm/disarm to forget.

1. …

## Blast radius and rollback

What is serving during the change, what happens if it fails half-way, and how to
get back. Rollback should be the same mechanism run backwards, not a rebuild.

## Human steps

- [ ] `make validate` locally, PR, then `make apply` — the ritual is validate, PR, apply
- [ ] <secret, dashboard, or console change>

## Acceptance

- [ ] `make validate` and `make shellcheck` pass; every overlay renders.
- [ ] A request loop against the affected Service records **zero failed requests**
      for the duration.
- [ ] <a query only the new state can answer, with the expected answer>
- [ ] Rollback is a single apply and restores the prior answers.
- [ ] The README section is updated; any superseded procedure survives only as a
      labelled escape hatch.
```

### 6 · Seed

For an idea worth keeping that is not a ticket yet. Label `seed-ticket`. Keep it
short — the point is to stop pretending it is ready.

```markdown
**The idea, in one or two sentences.**

- **Type:** seed
- **Repos:** probably `<repos>`
- **Human steps:** unknown
- **Deploy impact:** unknown

## Why it matters

What is worse today because this does not exist.

## What I do not know yet

The open questions, plainly. These are what promotion to `ready-for-agent` has to
answer — see [Definition of ready](#definition-of-ready).

- …

## First thing to settle

The one question that unblocks the rest. If it needs measuring rather than
deciding, this becomes a [spike](#4--spike).
```

---

## Reference

### Repo matrix

| Repo | Stack | Trunk | Staging | Production |
| --- | --- | --- | --- | --- |
| `sparks-effect-api` | Go — Railway + GHCR | `main` ([SPA-312](https://linear.app/sparks-effect-personal/issue/SPA-312) renames to `trunk`) | every trunk commit publishes `sha-<commit>` + `:staging` | `vX.Y.Z` tag re-tags that image `:prd` |
| `sparks-effect-routing-worker` | Go — GHCR → cluster | `main` ([SPA-313](https://linear.app/sparks-effect-personal/issue/SPA-313)) | same | the `kustomize-config` production pin |
| `sparks-effect-website` | Vue + Vite — Vercel | `trunk` | every `trunk` commit | `vX.Y.Z` tag promotes the build that commit already produced |
| `kustomize-config` | kustomize manifests | `main` ([SPA-314](https://linear.app/sparks-effect-personal/issue/SPA-314)) | `overlays/staging` chases `:staging`; needs `kubectl rollout restart` | pin bump in `overlays/production/sparks-effect` + apply |

Production is never a second long-lived branch and never a branch merge — it is a
promoted artifact. Do not propose a `prd` branch; that decision is
[SPA-254](https://linear.app/sparks-effect-personal/issue/SPA-254).

### Verification per repo

`make dev-workflow` is the single pre-push step everywhere. What it covers, and
what it deliberately does not:

| Repo | `dev-workflow` | Also available | Not in `dev-workflow` |
| --- | --- | --- | --- |
| `sparks-effect-api` | test, vet, lint, build | `make test-race` (concurrency changes), `make itest` (throwaway Postgres + RabbitMQ — what CI gates on) | the raced integration suite |
| `sparks-effect-routing-worker` | test (raced), vet, lint, build | `make itest` (throwaway RabbitMQ), `make acceptance TEST_VALHALLA_URL=…` (needs real transit tiles; refuses rather than skipping) | `make check-contract` — needs network, CI runs it |
| `sparks-effect-website` | lint, test, build | — | `make dev` / `make run` are long-running servers; never in automation |
| `kustomize-config` | validate + shellcheck | `make preview`, `make verify-cycle`, `make probe`, `make generations` | anything that contacts a cluster |

A ticket whose acceptance cannot be checked by one of these commands needs to say
who checks it and how.

### Cross-repo seams

Where a change in one repo silently breaks another. If a ticket touches one side
of a row, `Repos:` names both.

| Seam | Coupling | Tripwire |
| --- | --- | --- |
| Queue message | `internal/routing/testdata/message.golden.json` exists in the API **and** the worker | `make check-contract` in the worker, in CI |
| Job write-back | The worker holds no database — writes go through `/api/internal/routing-jobs/…`. Schema is owned by the API's migrations ([ADR-0001](../adr/0001-api-and-worker-share-no-go-code.md)) | none — the API's handler tests are the only guard |
| Error codes & job statuses | Matched literally by the website and the worker. Changing one is a breaking change ([CONTEXT.md](../../CONTEXT.md#machine-contract)) | none |
| Isochrone ceiling | Worker `VALHALLA_MAX_ISO_CONTOUR_MINS` (320) vs `base/valhalla/valhalla.env` `ISOCHRONE_MAX_TIME_MINUTES` (320). Divergence = permanent 400s | none — prose only |
| Queue name | `ROUTING_QUEUE` is hardcoded as `routing.jobs` in the worker and in kustomize | they agree by default only |
| `geo` reach constants | The worker's copy must never be the smaller of the two vs the API's origin-range check | a comment, in one repo, about a constant in another |
| Chain metadata | The worker's `metadata` block arrives verbatim in the website (`src/fixtures/isochrone.ts`) | the website's fixture |
| Worker image | `overlays/production/sparks-effect` pins `sha-<commit>` of a worker build | bumping the pin is the release |
| Tiles vs cache | A tile rebuild changes every polygon without changing any compile job | nothing detects it — accepted, documented risk |

### Human-only actions

An agent cannot do these. Any of them in scope means `Human steps:` is not `none`,
and the item goes on a checklist where it can block explicitly instead of stalling
the ticket quietly.

- GitHub repository settings — default branch, rulesets, secrets
- Vercel project settings, including the production branch
- Railway service configuration and environment
- `kubectl` against the cluster: `make apply`, `make cycle-tiles`,
  `make rollback-tiles`, `rollout restart`
- Pushing a `vX.Y.Z` tag — production promotion
- Third-party credentials: the 511 token, GTFS sources, Grafana Cloud
- Anything requiring a look at production data or a live dashboard

### Vocabulary traps

Use [CONTEXT.md](../../CONTEXT.md) — the canonical glossary for the whole project;
the other three repos keep one for terms only they own. Do not mint synonyms. The
four that cause the most damage in tickets:

- **Scenario vs UserScenario, Service vs UserService** — different models, not
  variants. `/api/services` returns **UserServices**; `/api/me/services` returns
  **Services**. Say which one the ticket means.
- **Mode vs costing** — `walk`/`bike`/`drive`/`transit` is the domain's word and
  the only one on the wire. `pedestrian`/`bicycle`/`auto`/`multimodal` is
  Valhalla's, and stays behind the worker's Valhalla client. `transit` maps to
  `multimodal`, never to Valhalla's own `transit` costing.
- **Stale vs outdated** — *stale* is a compiled graph that no longer matches its
  inputs (409, `stale_graph`). *Outdated* is a prerendered isochrone, still
  served, flagged on the read. Neither word takes the other's meaning.
- **Compile job vs routing job** — different tables, different owning processes.
  A "job" with no qualifier is ambiguous; always say which.

And one house rule that keeps getting re-broken: **do not ask for
declaration-level doc comments.** They were removed deliberately in
[SPA-307](https://linear.app/sparks-effect-personal/issue/SPA-307) because they
restated their declarations. Domain meaning belongs in `CONTEXT.md`; rationale
belongs in ADRs, in-function comments, migration headers, and the README.

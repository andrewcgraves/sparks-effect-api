# Branching and releases

One trunk: `main`.

- Branch from `main`. Open the pull request into `main`. Merge on green CI.
- No direct pushes to `main`.
- Staging follows `main` automatically. Production runs a build that someone
  tagged — never a branch merge, and never a rebuild.

## Images

Every commit on `main` publishes `sha-<commit>` (immutable — this is what gets
promoted) and `staging` (moving). There is no `latest`.

`main` publishes only after test, vet, lint and build pass, so a `sha-` tag
existing is itself the proof that the commit went through the trunk green.

## Releasing

A release is a tag. Tag a commit that is already on `main`:

```sh
git fetch origin main
git tag -a v1.4.0 -m "v1.4.0" origin/main   # or an older SHA on main
git push origin v1.4.0
```

That fires the **Release** workflow, which re-tags the image built from that
commit as `:prd` and as `:v1.4.0`, then redeploys the production Railway
service so it pulls the new `:prd`. Nothing rebuilds and the suite does not
re-run — that commit passed on its way into `main`.

The workflow refuses to promote:

- a tag that is not `vMAJOR.MINOR.PATCH`
- a tag pointing at a commit that is not an ancestor of `main` — this is what
  stops a feature branch reaching production
- a commit with no published image — i.e. one that never went through `main`

### Rolling back

Re-promote an earlier tag: Actions → **Release** → *Run workflow*, pass the tag
(`v1.3.0`). It moves `:prd` back to that image and redeploys. Roll forward the
same way, or with a new tag.

### What production is running

There is no `prd` branch. What production runs is the `:prd` tag, and each
release keeps its own immutable tag:

```sh
docker buildx imagetools inspect ghcr.io/andrewcgraves/sparks-effect-api:prd
docker buildx imagetools inspect ghcr.io/andrewcgraves/sparks-effect-api:v1.4.0
```

## What has to be configured outside the repo

Railway must deploy the **image**, not a branch. Pointed at a branch it rebuilds
from source and production stops matching what staging ran.

- production service → `ghcr.io/andrewcgraves/sparks-effect-api:prd`
- staging service → `ghcr.io/andrewcgraves/sparks-effect-api:staging`
- prd credentials (`DATABASE_URL`, AMQP URL, bootstrap admin password) live only
  in the production environment — the staging service and its build logs must
  not be able to read them

GitHub side, on a repository environment named `production` (Settings →
Environments), so the credential is scoped to releases rather than to every
workflow run:

| name | kind | what it is |
| --- | --- | --- |
| `RAILWAY_TOKEN` | secret | Railway **project token** scoped to the production environment. The token is what makes the redeploy hit production and nothing else. |
| `RAILWAY_SERVICE` | variable | Name (or id) of the production API service. Optional if the token's project has a single service. |
| `LINEAR_ACCESS_KEY_PRODUCTION` | secret | Access key for the **API production** Linear release pipeline. Lives on this environment so only a promotion can write a production release. |

And at the repository level (Settings → Secrets and variables → Actions), not
on the `production` environment — staging records every trunk publish:

| name | kind | what it is |
| --- | --- | --- |
| `LINEAR_ACCESS_KEY_STAGING` | secret | Access key for the **API staging** Linear release pipeline. |

Without `RAILWAY_TOKEN` the release still re-tags `:prd`; it just leaves the
redeploy to be clicked in Railway. `GITHUB_TOKEN` covers GHCR — nothing to set.
Without either Linear key the matching sync step is skipped rather than failed,
so a missing pipeline does not block a publish or a promotion.

Adding required reviewers to that `production` environment is what puts a human
approval in front of a promotion, if that is wanted later.

## Linear

Linear Releases answer "which issues are on staging, and which made it to
production?" — not by reading a branch, but by scanning the commits this repo
already ships.

Two **continuous** pipelines, not one pipeline with stages. Staging auto-deploys
every commit on `main` while production lags on a tagged SHA, so the two
environments hold different commits at the same time. Linear's rule for that
shape is two pipelines:

| Pipeline | Created when | Version |
| --- | --- | --- |
| API staging | `main` publishes `sha-<commit>` + `:staging` | short SHA |
| API production | a `vMAJOR.MINOR.PATCH` tag promotes that image as `:prd` | the git tag |

The action scans commits since the last release in **that** pipeline, pulls
`SPA-123` identifiers out of subjects and squash messages (`SPA-258: … (#69)`),
and attaches those issues to the new release. An issue that merged to `main`
shows up on API staging immediately; it only appears on API production when a
tag that contains it is promoted.

Create both pipelines in Linear (Settings → Releases), generate an access key
per pipeline, and paste them into the secrets in the table above. The first
sync in each pipeline only sees the current commit — there is no previous SHA
to bound the range from. To backfill, re-run with an explicit `--base-ref`
pointing at the last commit that should count as already released.

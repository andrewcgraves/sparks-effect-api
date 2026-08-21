# Branching and releases

One trunk: `main`.

- Branch from `main`. Open the pull request into `main`. Merge on green CI.
- No direct pushes to `main`.
- Staging follows `main` automatically. Production runs a build that someone
  promoted — never a branch merge, and never a rebuild.

## Images

Every commit on `main` publishes `sha-<commit>` (immutable — this is what gets
promoted) and `staging` (moving). There is no `latest`.

## Releasing

Promotion re-tags an existing image as `prd`. Nothing rebuilds and the suite
does not re-run — that commit passed on its way into `main`.

Run the **CI** workflow from the Actions tab with *Run workflow*:

- leave `commit` blank to promote the head of `main`
- pass an older SHA to roll back to it

Only a commit whose image exists can be promoted, and images are published from
`main` alone — so that check is what stops anything reaching production without
going through the trunk.

There is no `prd` branch. What production runs is the `:prd` tag:

```sh
docker buildx imagetools inspect ghcr.io/andrewcgraves/sparks-effect-api:prd
```

Railway has to deploy the image (`:prd`, `:staging`), not a branch. Pointed at
a branch it rebuilds from source and production stops matching what staging
ran. Not yet wired up — SPA-255 / SPA-253.

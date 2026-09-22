# ADR-0005: What publishing an authored service means, and where it lives

*Recorded 2026-09-22 for SPA-350, ahead of the SPA-242 publishing epic.*

## Status

Accepted. Every later ticket in SPA-242 names a column, endpoint, handler or
TypeScript type after the concept settled here, so this is decided before any
migration exists.

## Context

A `UserService` can be created, compiled and previewed by its owner. It cannot
be shown to anyone else. The only public/private axis in the system is
`owner_id IS NULL` — *curated* — and `user_services.owner_id` is `NOT NULL`, so
that axis cannot carry visibility for an authored service at all.

Five things were open:

1. **The word.** *Publication* is already defined in
   `sparks-effect-routing-worker/CONTEXT.md` as "the act of the API publishing
   the queue message, and by extension the frozen inputs it carries". This
   repository also uses the verb at the AMQP boundary: `routing.Publisher`, and
   the `publish_failed` error code in the machine contract.
2. **The public URL space**, on the website and on the API.
3. **Pinned or live.** Whether the public sees a frozen compile job or whatever
   the owner last compiled.
4. **`scenarios.status`.** Stored, returned by `GET /api/scenarios`
   (`internal/handler/scenario.go:56`), holds `"published"` for `ca-hsr`
   (`internal/transit/data/scenarios/ca-hsr/scenario.yaml:8`) — and filtered on
   by nothing. `ListCuratedScenarios` filters on `owner_id IS NULL` alone
   (`internal/persistence/postgres/postgres.go:110`), and the website never reads
   the field.
5. **Slug on rename.** `applyTo` never touches `Slug`
   (`internal/handler/service.go:152-154`), so a renamed service keeps its
   original URL.

## Decision

### 1. The word is *publish*, deliberately overloaded

An authored service is **published**; the act is **publishing**; the frozen
public copy is its **publication**; undoing it is **unpublishing**. The
unpublished, editable row the owner works on is the **draft**.

Not *public*, *listed* or *release*:

- **The product already says *publish*.** The epic, every child ticket, the
  control SPA-360 puts on the authoring page and the cover page's "Published
  routes" copy all use it. A code word that differs from the UI word is itself
  the synonym every `CLAUDE.md` forbids; it would just be a synonym split across
  a layer boundary instead of within one.
- ***Release*** collides harder than *publish* does: `docs/releases.md`,
  `release.yml`, `vX.Y.Z` tags and the Linear release pipelines all mean
  shipping software.
- ***Listed*** means "appears in an index", which is SPA-358's concern and not
  the only thing publishing grants — a detail page is reachable by URL whether or
  not it is listed. Taking the word now would foreclose "published but unlisted"
  as a later, distinct state.
- ***Public*** names the visibility, not the act or the frozen copy. It stays in
  use as the adjective it already is: curated rows and publications are both
  *public*.

**Why the overload is reconciled rather than accidental.** Both senses are the
same idea — freezing inputs against a compile job id — applied at two levels,
and the levels nest. Publishing a service pins a compile job; every isochrone
over that publication then puts exactly that job's graph into a queue message,
which is the worker's *publication*. The worker's entry already says "anything
keyed on its compile job id can never go stale"; publishing a service extends
that guarantee from one message to every public reader.

**The rules that keep the two senses apart:**

- Unqualified *publish* / *publication* / *published* in this repository and the
  website means the **service** sense.
- In the worker, *publication* keeps its queue-message meaning — the worker never
  sees a service's publication, only the graph it pins. Its `CONTEXT.md` gains a
  sentence pointing here.
- In this repository the queue sense is spelled **enqueue** (a routing job is
  *enqueued*, as `enqueueIsochrone` already says). The AMQP-boundary names that
  predate this ADR — `routing.Publisher`, `PublishFailedErrorCode` and the
  `publish_failed` code — keep their names: the code is a machine contract, and
  renaming it would break clients for a vocabulary change. New code does not add
  to them.
- Publishing never answers `publish_failed`. That code continues to mean only
  "the routing job row exists but the queue message was not confirmed".

### 2. Pinned, and pinned to what the author previewed

A publication pins **a specific succeeded compile job**, never "the latest".

A live graph would break every anonymous reader the first time the owner edited
the draft: the authored isochrone endpoints answer 409 `stale_graph`
(`internal/handler/userisochrone.go:88-93`), the website's recovery is to
recompile, and compiling requires auth (`internal/server/server.go:270`). An
anonymous visitor cannot recover. Pinning makes staleness structurally
impossible for public readers, and delivers the draft / publication split for
free: the owner keeps working on the draft while the world sees the last thing
they published.

**Publishing does not compile.** It pins the owner's **latest succeeded compile
job, provided that job is not stale** against the draft — the same
`transit.GraphStale` test the authored isochrone endpoints apply. If there is no
such job (never compiled, last compile failed, or stale), publishing refuses
with 409 `stale_graph`, whose existing remedy — "recompile and retry" — is
exactly right. The website already knows how to compile and poll.

This replaces SPA-355's "compile, then pin the job that compile produced":

- *Compile-then-pin* publishes a graph the author has, by construction, never
  seen. Pinning the non-stale job they previewed is the only reading of "never
  pinned to a graph the author has not seen" that holds literally.
- Publishing becomes **synchronous** and single-transaction. There is no async
  window in which an edit, a second publish, an unpublish or a delete can race a
  compile, and no compile-failure mode to choose between — the questions SPA-242
  gap 3 raised mostly dissolve.
- Staleness already compares against the job's `CreatedAt`
  (`internal/transit/staleness.go:6`), not its completion time, so an edit made
  while a compile was running marks that compile stale rather than being missed.
  The publish transaction must lock the draft row (`SELECT … FOR UPDATE`) across
  the staleness check and the write, so no edit lands between them.

The cost: any draft edit bumps `updated_at`, prose included, so editing only the
description makes the graph stale and requires a recompile before republishing.
A compile of one service is cheap and the website already handles the flow, so
this is accepted rather than special-cased.

### 3. A publication is a snapshot of everything the public page shows

Pinning the graph alone does not freeze what a reader sees: name, subtext and
description live on the mutable draft, and `authoredTargetGraph` reads route
geometry **live** by the draft's current `route_id`
(`internal/handler/usercompile.go:63-70`). An owner who edited the draft's title,
or re-pointed it at another alignment, would change the public page without
republishing — which SPA-360 promises never happens.

So a publication freezes, at publish time and in the same transaction:

| Part | Frozen as |
| --- | --- |
| The graph | The pinned compile job id |
| Name, subtext, description | Copies of the draft's values |
| Route geometry | A copy of each route the pinned graph's edges name (`Edge.RouteID`), not the draft's `route_id` |
| When | The publish timestamp |

**A public read never reads the draft row.** That is the property the design is
built around: the public half of any handler can then not leak an unpublished
edit, however it is written.

Where it is stored is SPA-355's call, with one recommendation: a
`service_publications` table keyed on the service id, one row per published
service, rather than a set of nullable `published_*` columns on `user_services`.
Published then means "a row exists", unpublishing is a delete, the columns cannot
drift out of step with each other, and the public read path selects from a table
that holds nothing unpublished. The detail read (SPA-356) and the index
(SPA-358) both read these rows, so they cannot disagree.

**The pinned job must outlive retention.** Retention (SPA-333) must never delete
a compile job a publication pins. Leave the foreign key from the publication to
`jobs` at the default `NO ACTION`, so a retention sweep that tries fails loudly
instead of silently unpublishing. Deleting the service itself removes its jobs
and its publication together (both cascade from `user_services`); what that
means for a URL someone bookmarked is SPA-240's question.

**Unpublishing deletes the publication**, pin and snapshot with it. It is
reversible in the only way that matters: republishing takes a fresh snapshot of
the draft, subject to the same no-stale-graph rule, so a stale snapshot is never
resurrected.

**The prose field is `subtext`** — `subtext` in the column, the JSON and the
TypeScript type — for the one-line descriptor the ca-hsr page hard-codes as
`Electrified · High-speed rail · Greenfield` (`src/views/ScenarioView.vue:103`
in the website). It is the word every ticket in the epic already uses, and
nothing in any repository uses it for anything else. Not *subtitle* or
*tagline*.

### 4. The public page lives at `/services/:slug`

On the **website**, a published service's page is `/services/:slug` — a sibling
of `/scenario/:slug` and `/routes/:slug`, outside `/authoring`, and without
`meta: { requiresAuth: true }`. The plural follows `/routes/:slug`, the other
public page for an authored-and-curated noun.

Folding it into `/scenario/:slug` was rejected. That page renders a seeded
`Scenario` from `transit.Store`; a published service is a different model with
different data (embedded stops, inline vehicle, no segment run times). One URL
serving two models would need the page to probe which kind a slug is, and it
would call a *service* a *scenario* — the exact confusion
[CONTEXT.md](../../CONTEXT.md#the-one-that-catches-everyone-two-scenarios-two-services)
exists to warn about. Slugs are globally unique, so the separation costs nothing.

**The public URL shows the publication to everyone, owner included.** The draft
lives at `/authoring/services/:slug`. An owner who follows their own public link
sees what the world sees; that is the point of following it.

On the **API**, the same rule: **a public read's response depends on the slug
alone, never on the caller.** That settles the owner-at-the-public-URL question
and makes a public response safe to cache. It rules out a single
`GET /api/services/{slug}` that returns the draft to its owner and the
publication to everyone else. The publication is its own resource:

| Method and path | Who | Does |
| --- | --- | --- |
| `GET /api/services/{slug}/publication` | Anyone | The snapshot: prose, routes, graph, publish time. 404 if unpublished |
| `PUT /api/services/{slug}/publication` | Owner | Publish or republish from the current draft. Idempotent in effect: repeating it re-pins the same job |
| `DELETE /api/services/{slug}/publication` | Owner | Unpublish. Idempotent: 204 whether or not it was published |

The existing `/api/services/{slug}` draft endpoints keep their `authenticated`
wrapper and their behaviour unchanged. Isochrones over a publication (SPA-357)
belong under the same resource, are plotted over the pinned graph only, and
create **ownerless** routing jobs whatever the caller — which is what makes them
pollable by an anonymous reader under `GET /api/routing-jobs/{id}`'s existing
rule. The index's path and pagination are SPA-358's.

### 5. A slug is never re-minted — not on rename, not on publish

The slug is minted once, at create, and is the service's permanent address. A
published URL is a promise to whoever holds it; re-minting on rename would break
every link to the page at the moment the author is most likely to be sharing it.
Publishing does not re-mint either.

The title and the URL will visibly diverge after a rename. That is intended,
and is recorded in [CONTEXT.md](../../CONTEXT.md#reach-and-routing) under
**Slug** so it is not filed as a bug. Choosing a new slug, and redirecting the
old one, is a separate feature if it is ever wanted.

### 6. `scenarios.status` is retired, not reused

It carries no semantics: nothing filters on it, nothing on the website reads it,
and the one value in it is a YAML literal. Once *published* is a defined term, a
column holding `"published"` that means nothing is a trap for the next reader.

It is **not** renamed into, or reused as, publication state. Curated scenarios
are public by virtue of `owner_id IS NULL`, not by being published, and seeded
scenarios are not published — they are curated. The column, the `status` field
on `transit.Scenario` and the seed YAML, and the `status` key on
`GET /api/scenarios` are removed in a follow-up ticket. Until then it is dead
data and must not be read.

## Consequences

- **SPA-355** changes shape: publish is synchronous and pins the latest
  non-stale succeeded job instead of compiling, a stale or missing compile
  answers 409 `stale_graph`, and it stores the snapshot in section 3, not two
  columns. Its compile-failure question no longer arises.
- **SPA-351** names the new column `subtext`.
- **SPA-356** serves `GET /api/services/{slug}/publication` from the
  snapshot alone. It is not a public half bolted onto the draft read.
- **SPA-357** plots only over the pinned graph, with ownerless routing jobs.
- **SPA-359** routes `/services/:slug` without `requiresAuth`.
- **SPA-360**'s "edits stay invisible until republish" holds by construction,
  and its publish flow is: compile if stale, poll, then `PUT` the publication.
- **SPA-333** must exclude pinned compile jobs from retention.
- **No new error code.** Publishing reuses `stale_graph`; `publish_failed`
  keeps its narrow meaning.
- A published service's drawn alignment is a snapshot, so an admin correcting a
  curated alignment does not reach existing publications until their owners
  republish. That is the same trade the pinned graph already makes, and the two
  must not disagree.

## Considered and rejected

- **A live graph for public readers.** Rejected: an anonymous reader cannot
  recover from `stale_graph`, and every draft edit would break the public page.
- **Compile on publish, then pin the result.** Rejected in section 2: it pins a
  graph nobody has seen, and it makes publishing asynchronous with a
  compile-failure mode and edit races to design around.
- **Pin the graph but serve prose and geometry live.** Rejected in section 3:
  the public page would change without a republish, and the public read would
  have to read the draft row.
- **One `GET /api/services/{slug}` with a public half and an owner half.**
  Rejected in section 4: the response would depend on who asks, which leaves an
  owner no way to see their own publication and makes the response unsafe for a
  shared cache.
- **Reuse `scenarios.status` as publication state.** Rejected in section 6: it
  is on the wrong table (`scenarios`, not `user_services`), its one value is
  wrong under the new definition, and publication needs a pin and a snapshot, not
  a string.
- ***Release*, *listed* or *public* as the word.** Rejected in section 1.

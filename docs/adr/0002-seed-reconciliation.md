# ADR-0002: Seeded rows reconcile from YAML at boot

*Recorded 2026-09-10 for SPA-285.*

## Status

Accepted.

## Context

Seeded scenario data has two copies: the YAML under
`internal/transit/data/scenarios/`, which `SeedIfEmpty` writes into a fresh
database, and SQL literals in goose migrations, which patch a database that is
already populated. `SeedIfEmpty` returns as soon as it finds one scenario row,
so a correction to the YAML reaches a developer database that has never been
seeded and nowhere else. Every deployed database needed a migration carrying
the same correction as SQL. Tests then pinned the copies together with regular
expressions over the migration source, and each new migration had to edit
every earlier rewind test's goose-version list.

`CompileSeededIfNeeded` already recompiles when stored graph JSON no longer
matches a fresh compile — a content comparison, not a version stamp. The row
level never got that treatment.

## Decision

YAML is the source of truth for **seeded** rows (the subset embedded in the
binary). On every boot, `ReconcileSeed` upserts those rows when
`sameSeedContent` is false, the row-level twin of `sameCompiledGraph`.
`CompileSeededIfNeeded` then recompiles if the graph drifted.

Scope is seeded scenarios only: a curated scenario whose slug is in the
embedded YAML, with a matching id. An authored scenario (`owner_id` set) with
the same slug is skipped. Authored children of a curated scenario are skipped.
Rows the YAML does not name are left alone.

Schema changes still go through goose migrations. Data corrections to seeded
rows do not.

## Consequences

- A YAML edit reaches a deployed database on the next boot, with no migration.
- Historical data-correction migrations stay in the tree as a record of what
  already-applied databases were told to do; they are not rewritten, and they
  are no longer pinned to the current YAML.
- Adding a schema migration must not require editing earlier migrations'
  tests. Rewind helpers forget goose versions with `version_id >= n` rather
  than a hard-coded list.
- Reconciliation runs against production at boot, so it must stay idempotent.
  The no-op path is a JSON equality check; a matching database writes nothing.

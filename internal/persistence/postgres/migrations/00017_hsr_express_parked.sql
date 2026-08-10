-- +goose Up
-- Park HSR Express on a deployed database (SPA-223).
--
-- ## What was wrong
--
-- SPA-223 set HSR Express's `active` to false in services.yaml so Phase 1
-- stops presenting as two lines (see the comment on that entry). That is
-- enough for a database that has never been seeded. It is not enough for a
-- deployed one: SeedIfEmpty returns early on a populated store (seed.go) and
-- there is no reseed path, so a database seeded before SPA-223 landed kept
-- serving HSR Express with `active = true` forever, which is exactly what
-- production is still doing. This is the same split 00015 and 00016 called
-- out — the seed serves fresh databases, a migration serves the deployed one.
--
-- ## Why this is inert on a fresh database
--
-- Migrations run before SeedIfEmpty, so on an empty database the UPDATE below
-- touches zero rows and the seed inserts the row with `active = false` from
-- YAML a moment later. On a deployed database the row already exists and the
-- UPDATE does the work instead.
--
-- CompileSeededIfNeeded (compile_seeded.go) notices the resulting drift
-- between the stored graph and what the rows now compile to and recompiles on
-- the next boot, exactly as it did for 00015 and 00016, so the compiled graph
-- and the scenario API drop HSR Express together without a separate step.
--
-- Re-running is safe: assigning a value that is already there is a no-op.

UPDATE services
SET active = false
WHERE id = '00000000-0000-4004-8001-000000000001'::uuid;

-- +goose Down
-- Deliberately empty.
--
-- The inverse of this migration is HSR Express serving again, which is a
-- product decision (flip services.yaml's `active` back to true), not data to
-- restore. Rolling the application back does not require rolling this back.

-- +goose Up
-- Every stop carries its identity (SPA-103).
--
-- A ServiceStopPoint had no identity at all — only name/lat/lng/seq — so
-- nothing could name one stop. From here the write path mints
-- `{service}--{stop}` onto every stop and stores it, which is what lets a
-- compile result, and SPA-109's cross-service merge after it, refer to a
-- particular stop rather than to an index into a list.
--
-- As with 00007 there is no column to add: stops live in the `stops` jsonb
-- document, so the field arrives with the Go struct. What this migration
-- contributes is the invariant that it is always there.
--
-- ## The slug is identity, not the graph key
--
-- Worth stating here because the constraint below makes it look load-bearing
-- for routing, and it is not. Interchange between two services is nothing but
-- both emitting a graph edge under one key, so a per-service namespaced slug
-- used as the key would make interchange impossible rather than safe. SPA-109
-- decides the graph key at compile time by clustering co-located stops across a
-- scenario's members, and persists nothing. See transit.ServiceStopPoint.Slug.
--
-- ## Why presence and not uniqueness
--
-- Slugs must also be unique within a service — SPA-115's clustering walks stops
-- in ascending slug order, so a duplicate would make cluster keys depend on
-- input order. That is enforced in transit.StopSlugs, which disambiguates a
-- repeated name with a -2, -3, ... suffix, and is tested at that seam.
--
-- It is not re-enforced here because a CHECK cannot contain a subquery, so
-- expressing "the slugs in this document are distinct" would mean a stored
-- IMMUTABLE function — a second implementation of an invariant Go already
-- holds, and one that could drift from it. Presence is the part SQL can state
-- without duplicating anything: a missing key decodes to the empty string,
-- which is a plausible-looking slug rather than an obvious absence, so without
-- this constraint a stop with no identity would read as a stop whose identity
-- is "".
--
-- ## Backfill strategy: assert, do not migrate
--
-- The same choice 00007 made, for a stronger reason. Slugs are minted by
-- transit.Slugify — lowercase, every run of non-alphanumerics collapsed to a
-- single dash, truncated at 80 characters without a trailing dash, with a
-- "service" fallback — and then disambiguated against the stops before them.
-- Reproducing that in SQL would be a second implementation of the scheme, and
-- SPA-103 is explicit that a stored slug must match a derived one *exactly*:
-- they are compared, not merely both used, and a backfill that rounded a
-- Unicode name differently would leave one stop answering to two identities.
--
-- What is actually known about the row count, as opposed to assumed: the local
-- and CI databases are the throwaway container `make db-up` starts and the
-- schema is dropped on every run, so they are empty by construction rather than
-- by observation. The deployed database was not reachable from where this was
-- written — the same position 00007 was authored from. What carries the weight
-- is that 00007 refuses to run against *any* user_services row and has already
-- been deployed, so the only rows this can find are ones written between the two
-- deploys, by the write path 00007 shipped and this one amends. The assertion
-- below is what establishes that at deploy time instead of assuming it.
--
-- The remedy if it ever fires is cheaper than 00007's: a stop's identity is a
-- function of the row itself (service slug plus stop names), needing no route
-- geometry, so re-saving each service through PUT /api/services/{slug} mints
-- them, as would a one-off Go program calling transit.UserService.MintStopSlugs.
-- Do that and re-run rather than weakening the assertion.

-- +goose StatementBegin
DO $$
DECLARE
    unslugged bigint;
BEGIN
    SELECT count(*) INTO unslugged
      FROM user_services
     WHERE jsonb_path_exists(stops, '$[*] ? (!exists(@.slug) || @.slug == "")');
    IF unslugged > 0 THEN
        RAISE EXCEPTION
            'user_services holds % row(s) whose stops were written before stops had '
            'identities (SPA-103). Minting them means transit.Slugify and its '
            'disambiguation rule, which SQL cannot reach without becoming a second '
            'implementation of the scheme. Re-save each service through '
            'PUT /api/services/{slug}, or run a one-off Go program calling '
            'MintStopSlugs, and re-run this migration. See the comment at the head '
            'of 00008_user_service_stop_slugs.sql.', unslugged;
    END IF;
END $$;
-- +goose StatementEnd

ALTER TABLE user_services
    ADD CONSTRAINT user_services_stops_have_slugs
    CHECK (NOT jsonb_path_exists(
        stops,
        '$[*] ? (!exists(@.slug) || @.slug == "")'));

-- +goose Down
ALTER TABLE user_services DROP CONSTRAINT IF EXISTS user_services_stops_have_slugs;

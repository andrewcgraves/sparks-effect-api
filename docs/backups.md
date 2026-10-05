# Backups

Production Postgres is the only copy of the data this system cannot
regenerate. This page says what is backed up, how far back a restore can go,
how to do one, and what happened the last time we did.

## What matters

Irreplaceable: `users`, `user_services` and their children
(`user_service_frequency_windows`, `service_handovers`), `user_scenarios` and
their children (`user_scenario_services`), `service_publications`,
`account_tokens`, and the user-owned rows in `routes`, `scenarios`,
`stations` and `services`.

Can be discarded: `sessions`. Everyone signs in again.

Regenerable: `jobs`, `routing_jobs`, `isochrone_cache` and
`prerendered_isochrones`. Seeded scenarios reconcile from the embedded YAML
on boot (README → Persistence) and graphs recompile.

Author-level export to a file (SPA-241) is not a database backup.

## Where things stand

| | Production | Staging |
|---|---|---|
| Railway service | `Postgres`, project `sparks-effect` | same |
| Image | `ghcr.io/railwayapp-templates/postgres-ssl:18.6` | same |
| Volume | `postgres-volume`, 500 MB, `sfo` | |
| Volume backup schedule | **none** (checked 2026-10-05) | **none** |
| Volume backups held | **none** (checked 2026-10-05) | **none** |
| Point-in-time recovery | disabled; blocked by the `:18.6` pin | disabled |

Check it again with:

```sh
railway postgres -p 80a8d788-742e-4dc1-9722-dabcc1757bbd -e production -s Postgres pitr schedule list
railway postgres -p 80a8d788-742e-4dc1-9722-dabcc1757bbd -e production -s Postgres pitr backup list
railway postgres -p 80a8d788-742e-4dc1-9722-dabcc1757bbd -e production -s Postgres pitr status
```

Update the table whenever it changes.

## The target

Volume backups, **daily and weekly**, on both environments.

| Schedule | Taken | Kept |
|---|---|---|
| Daily | every 24 hours | 6 days |
| Weekly | every 7 days | 27 days |
| Monthly (not used) | every 30 days | 89 days |

Daily alone keeps 6 days, short of a week. Weekly is what makes a restore
point at least 7 days old exist. Between them the worst case is losing up to
24 hours of writes, and the oldest restore point is up to 27 days old.

Take an on-demand backup before anything risky, such as a migration that
drops or rewrites a column:

```sh
railway postgres -s Postgres -e production pitr backup create --name pre-<what>
```

Manual backups are limited to 50% of the volume's size. Grow the volume
before that bites.

### Enable it

Dashboard: project `sparks-effect` → `production` → `Postgres` → Backups tab →
tick Daily and Weekly. Do the same in `staging`.

CLI:

```sh
railway postgres -s Postgres -e production pitr schedule set --daily --weekly
railway postgres -s Postgres -e staging    pitr schedule set --daily --weekly
```

Neither redeploys anything.

### What volume backups do not cover

- They restore only into the **same project and environment**. A production
  backup cannot be restored into staging.
- Wiping the volume deletes every backup of it. Deleting the `Postgres`
  service or its volume takes the backups with it.
- They live on Railway. Losing the Railway account loses them.

Point-in-time recovery closes the first two gaps. It archives WAL to a
separate Railway bucket and restores to any second in about the last 4
weeks, into a **new sibling service**, leaving production untouched. It
needs the image on the major tag first (`postgres-ssl:18`, not `:18.6`),
which is a production database redeploy. That is a follow-up, not done
here. A nightly `pg_dump` to storage outside Railway would close the third
gap.

## Who can restore

Anyone with deploy access to the `sparks-effect` Railway project, through the
dashboard or a `railway login` session. The CLI's backup commands need a full
login. A narrower OAuth grant can read schedules and backups, but writes
fail with `OAUTH_INSUFFICIENT_GRANT`.

## Restore a volume backup

This replaces the database's data with the backup's. Every write since the
backup is gone. Stop and think about which backup you want.

1. Find the backup.

   ```sh
   railway postgres -s Postgres -e production pitr backup list
   ```

2. Restore it. In the dashboard, open the Backups tab, find the backup by
   date stamp and click **Restore**. Railway stages the change: a new volume
   named after the date stamp is mounted at `/var/lib/postgresql/data`, and
   `postgres-volume` is unmounted but kept. Review the staged change and
   click **Deploy**. Postgres redeploys on the restored data.

   Or from the CLI. It restores in place too. It has not been tried here
   yet, so prefer the dashboard, where the staged change can be reviewed:

   ```sh
   railway postgres -s Postgres -e production pitr backup restore <backup-id>
   ```

3. Restart `sparks-effect-api` so its pool reconnects. Boot runs migrations
   and the seed reconcile.

4. Check it. See [Row counts](#row-counts). Sign in as a known user and open
   one of their services.

5. Once it's confirmed, delete the old unmounted volume. Until then it is the
   way back.

### Schema

Migrations run on boot (goose, embedded). A backup taken on an **older**
schema is upgraded by the next API boot. A backup taken on a **newer**
schema than the running API is not downgraded. Deploy the API version that
was live when the backup was taken, or a newer one, before you restore.

## Restore a logical dump

Use this to copy production data somewhere other than production, such as a
scratch database for a drill or an investigation. It is read-only against
production.

You need Postgres 18 client tools. If they aren't installed, run them in the
`postgres:18` image as below. `PROD_URL` is the production `Postgres`
service's `DATABASE_PUBLIC_URL`, through the TCP proxy. Keep it out of shell
history and out of files you commit.

```sh
docker run --rm -d --name pg-scratch -e POSTGRES_PASSWORD=scratch -p 55432:5432 postgres:18
SCRATCH_URL=postgres://postgres:scratch@localhost:55432/postgres

time docker run --rm --network host -v "$PWD:/w" postgres:18 \
  pg_dump --format=custom --no-owner --no-acl --file=/w/prod.dump "$PROD_URL"

time docker run --rm --network host -v "$PWD:/w" postgres:18 \
  pg_restore --no-owner --no-acl --dbname="$SCRATCH_URL" /w/prod.dump
```

The dump holds every user's data. Delete it and stop the container when
you're done.

## Row counts

Run against the source and the restore, and compare:

```sql
SELECT 'users' AS t, count(*) FROM users
UNION ALL SELECT 'user_services', count(*) FROM user_services
UNION ALL SELECT 'user_scenarios', count(*) FROM user_scenarios
UNION ALL SELECT 'service_publications', count(*) FROM service_publications;
```

A volume restore matches the source as of the backup's timestamp, not as of
now. Take the source counts at backup time.

## Drill

A restore counts once it has actually been done. Repeat the drill after any
change to the backup setup, and at least once a quarter.

1. **Volume restore, staging.** Take a staging backup and record the row
   counts. Restore that backup in place on staging. Time from **Deploy** to
   `Postgres` healthy and the staging API answering. Recount.
2. **Production data, scratch.** Take a [logical dump](#restore-a-logical-dump)
   of production into a scratch database. Time the dump and the restore
   separately. Compare row counts against production taken at the same
   moment.

### Results

| Date | Drill | Backup age | Restore duration | `users` source / restored | `user_services` source / restored | By |
|---|---|---|---|---|---|---|
| _pending_ | volume restore, staging | | | | | |
| _pending_ | production dump → scratch | | | | | |

Not yet run. Backups were not enabled on 2026-10-05 (see
[Where things stand](#where-things-stand)), and the drill waits on that.

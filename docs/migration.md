# Migrate from Python linkding v1.47.0

This procedure moves an installed Python linkding v1.47.0 database and its data files into a separate Go installation. It supports SQLite and PostgreSQL. The source installation remains available for rollback until Go accepts writes.

## Prepare the source

1. Upgrade Python linkding to v1.47.0 through its normal upgrade path. The Go migration checks for its final `bookmarks` migration, `0054_bookmarkbundle_filter_shared_and_more`.
2. While Python linkding is still available, run its `python manage.py migrate_tasks` command if the legacy `background_task` table contains rows. Let its Huey worker finish queued and scheduled tasks. The Go CLI reports both legacy and Huey counts and refuses to migrate while either queue contains work. Go's own `migrate_tasks` command only checks for legacy rows; it does not execute Python tasks.
3. Stop the Python server and background worker. Back up the source database and the entire `data/` directory before copying. For PostgreSQL, back up the database separately. Keep these backups and the original installation unchanged during verification.

Build the Go binary as shown in the [README](../README.md), then inspect the stopped source. For SQLite:

```sh
./bin/linkding migrate-from-linkding --dry-run \
  --source-data /path/to/python/data
```

For PostgreSQL, supply a connection string for the source database:

```sh
./bin/linkding migrate-from-linkding --dry-run \
  --source-engine postgres \
  --source-dsn "$SOURCE_DSN" \
  --source-data /path/to/python/data
```

The JSON report includes user, bookmark, asset, legacy task, pending Huey task, and scheduled Huey task counts. `LegacyTasks`, `PendingTasks`, and `ScheduledTasks` must all be zero before migration. Dry-run reads the source; it does not create a target.

## Copy into Go

Choose a target `data/` directory that does not exist yet. The source and target must be different directories, and the target cannot be inside the source. SQLite:

```sh
./bin/linkding migrate-from-linkding \
  --source-data /path/to/python/data \
  --target-data /path/to/go/data
```

PostgreSQL requires a separate, existing, empty target database and its connection string:

```sh
./bin/linkding migrate-from-linkding \
  --source-engine postgres \
  --source-dsn "$SOURCE_DSN" \
  --target-dsn "$TARGET_DSN" \
  --source-data /path/to/python/data \
  --target-data /path/to/go/data
```

The command reports copied files and row counts. It checks references to stored files, compares SQLite table counts, checks copied PostgreSQL row counts, and verifies copied file hashes. A failed check leaves the source untouched; discard the incomplete target and resolve the reported problem before retrying with a new target.

## Verify and switch

Start Go with the migrated directory mounted at `/etc/linkding/data` when using Docker, or as `data/` below the binary's working directory. For PostgreSQL, also set the existing `LD_DB_ENGINE`, `LD_DB_HOST`, `LD_DB_PORT`, `LD_DB_DATABASE`, `LD_DB_USER`, and `LD_DB_PASSWORD` settings for the new database. Keep the previous external URL and `LD_CONTEXT_PATH`.

Before opening Go to writes, check `/health`, log in with an existing password, request `/api/user/profile/` with an existing API token, open an existing feed token URL, and download a migrated asset as its owner. Compare the source and target counts, relationships, password hashes, token values, and stored files. Existing Django session cookies need a new login.

If verification fails before Go accepts writes, stop Go and restart Python with its untouched source database and `data/` directory at the previous URL. Once Go accepts new writes, reverting to the old source would lose those writes; plan a reverse migration before any later rollback.

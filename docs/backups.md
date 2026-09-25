# Back up and restore data

Run these commands from the directory that contains the Go installation's `data/` folder. They support SQLite; PostgreSQL installations require a database backup and a separate copy of the data files.

## Full backup

```sh
./bin/linkding full_backup backup.zip
```

The ZIP contains an online SQLite snapshot as `db.sqlite3` and files from `data/assets/`, `data/favicons/`, and `data/previews/`. Missing file directories are skipped. Nested paths are retained, so a file stored as `data/assets/nested/page.html` is archived as `assets/nested/page.html`. This differs from Python linkding v1.47.0, which flattens nested file paths in its backup ZIP.

To restore, stop the server, extract the archive into an empty `data/` folder for the new installation, and start the server from that installation directory:

```sh
mkdir -p restored/data
unzip backup.zip -d restored/data
```

The SQLite snapshot is consistent while the server is running. Stop writes during the backup if the database and copied files must reflect the same moment; files are added to the ZIP after the database snapshot.

## Database-only backup

```sh
./bin/linkding backup backup.sqlite3
```

This deprecated command writes only a SQLite snapshot. To restore it, stop the server and place the file at `data/db.sqlite3` in a new installation. Copy `assets/`, `favicons/`, and `previews/` separately if needed.

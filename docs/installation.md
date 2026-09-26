# Install and configure linkding (Go)

This guide applies to the Go implementation of linkding v1.47.0. Replace `vX.Y.Z` in image and archive names with a published release tag. Until a release is published, build from source as described in the [README](../README.md).

## Run a release binary

Download the archive for your operating system and CPU from [GitHub Releases](https://github.com/juev/linkding/releases) and check it against `SHA256SUMS`. Releases contain Linux, macOS, and Windows archives for amd64 and arm64. Extract the archive and run the executable from its extracted directory: it loads `web/static/` relative to the current working directory and writes the SQLite database, secret key, and downloaded files to `data/`.

```sh
cd linkding_vX.Y.Z_linux_amd64
export LD_SUPERUSER_NAME=admin
export LD_SUPERUSER_PASSWORD='replace-this-password'
./linkding server
```

On Windows, set the same environment variables and run `./linkding.exe server` in PowerShell from the extracted directory. Open `http://localhost:9090/`. Set the initial superuser password before the first start; retain the `data/` directory across upgrades. The server also starts when the executable has no argument.

## Run the basic Docker image

The basic image contains the server, CA certificates, timezone and MIME data, static assets, and a built-in healthcheck. It supports SQLite and PostgreSQL, file uploads, previews, favicons, and backups. Automatic HTML snapshots require the plus image.

```sh
docker volume create linkding-data
docker run -d --name linkding -p 9090:9090 \
  --read-only --user 10001:10001 \
  --tmpfs /tmp:rw,nosuid,nodev,size=512m \
  -v linkding-data:/etc/linkding/data \
  -e LD_SUPERUSER_NAME=admin \
  -e LD_SUPERUSER_PASSWORD='replace-this-password' \
  ghcr.io/juev/linkding:vX.Y.Z
```

Open `http://localhost:9090/`. The named volume stores `db.sqlite3`, `secretkey.txt`, and downloaded files. The image runs without root; its data directory accepts an arbitrary numeric UID. When using a host bind mount instead of a named volume, make the host directory writable by the selected UID. Mount `/etc/linkding/data` even with PostgreSQL because it holds the secret key and files. If your platform forbids a read-only root filesystem, omit `--read-only`; the data volume is still required.

The plus image includes Chromium, SingleFile CLI, and uBlock Origin Lite. Substitute `ghcr.io/juev/linkding-plus:vX.Y.Z` in the command above. It enables automatic snapshots by default and stores Chromium's profile under the data volume. Keep the writable `/tmp` mount for Chromium. The image healthcheck runs `linkding healthcheck`; the HTTP endpoint is `/health` or `/<LD_CONTEXT_PATH>health`.

For Docker Compose, use the same mounts and options:

```yaml
services:
  linkding:
    image: ghcr.io/juev/linkding:vX.Y.Z
    ports:
      - "9090:9090"
    user: "10001:10001"
    read_only: true
    tmpfs:
      - /tmp:rw,nosuid,nodev,size=512m
    volumes:
      - linkding-data:/etc/linkding/data
    environment:
      LD_SUPERUSER_NAME: admin
      LD_SUPERUSER_PASSWORD: ${LD_SUPERUSER_PASSWORD}
    restart: unless-stopped

volumes:
  linkding-data:
```

Set `LD_SUPERUSER_PASSWORD` in the Compose environment before `docker compose up -d`. To enable snapshots, change `image` to the plus image.

## PostgreSQL

Create a PostgreSQL database and user, then set these variables on the binary or container:

```text
LD_DB_ENGINE=postgres
LD_DB_HOST=postgres
LD_DB_PORT=5432
LD_DB_DATABASE=linkding
LD_DB_USER=linkding
LD_DB_PASSWORD=replace-this-password
```

`LD_DB_HOST` must resolve from the server's environment; `postgres` is a typical Compose service name. The default engine is SQLite, stored at `data/db.sqlite3`. For encrypted PostgreSQL connections, set `LD_DB_OPTIONS` to JSON such as `{"sslmode":"verify-full","sslrootcert":"/path/to/ca.crt"}` and make the certificate readable inside the container.

## Server and integration settings

Environment variable names follow Python linkding v1.47.0. Boolean values accept `True`, `true`, or `1`; other values are false. Settings shown without a default are unset by default.

| Setting | Default | Use |
| --- | --- | --- |
| `LD_SERVER_HOST`, `LD_SERVER_PORT` | `[::]`, `9090` | Listen address. Map the chosen container port with `-p`. |
| `LD_CONTEXT_PATH` | empty | Relative URL prefix ending in `/`, such as `linkding/`. |
| `TZ` | `UTC` | Timezone for displayed dates. |
| `LD_SUPERUSER_NAME`, `LD_SUPERUSER_PASSWORD` | empty | Create the initial admin account. |
| `LD_DB_ENGINE` | `sqlite` | Set `postgres` for PostgreSQL. |
| `LD_DB_HOST`, `LD_DB_DATABASE`, `LD_DB_USER`, `LD_DB_PASSWORD`, `LD_DB_PORT` | `localhost`, `linkding`, `linkding`, empty, empty | PostgreSQL connection. |
| `LD_DB_OPTIONS` | `{}` | JSON driver options, including PostgreSQL TLS settings or a SQLite database path. |
| `LD_ENABLE_SNAPSHOTS` | false; true in plus image | Queue automatic HTML/PDF snapshots. |
| `LD_DISABLE_BACKGROUND_TASKS` | false | Prevent the background worker from running. |
| `LD_USE_X_FORWARDED_HOST` | false | Use the forwarded host for OIDC redirect construction. Set only behind a trusted proxy. |
| `LD_CSRF_TRUSTED_ORIGINS` | empty | Comma-separated external origins allowed for session writes. |
| `LD_CORS_ALLOWED_ORIGINS` | empty | Comma-separated origins allowed for cross-origin API requests. |
| `LD_ENABLE_OIDC`, `LD_ENABLE_AUTH_PROXY` | false | Enable OIDC or trusted authentication proxy integration. |

The configuration also accepts `LD_ALLOWED_INTERNAL_HOSTS`, `LD_DISABLE_URL_VALIDATION`, `LD_ENABLE_REFRESH_FAVICONS`, `LD_FAVICON_PROVIDER`, `LD_PREVIEW_MAX_SIZE`, `LD_SNAPSHOT_PDF_MAX_SIZE`, `LD_SINGLEFILE_PATH`, `LD_SINGLEFILE_OPTIONS`, `LD_SINGLEFILE_UBLOCK_OPTIONS`, `LD_SINGLEFILE_TIMEOUT_SEC`, `LD_DISABLE_ASSET_UPLOAD`, `LD_DISABLE_LOGIN_FORM`, `LD_SESSION_COOKIE_AGE`, `LD_REQUEST_TIMEOUT`, `LD_REQUEST_MAX_CONTENT_LENGTH`, and logging options. OIDC uses `OIDC_OP_*`, `OIDC_RP_*`, `OIDC_USE_PKCE`, `OIDC_VERIFY_SSL`, and `OIDC_USERNAME_CLAIM`. Keep secrets out of committed Compose files.

When serving under a proxy path, set `LD_CONTEXT_PATH=linkding/`, forward requests for `/linkding/`, and use `/linkding/health` for monitoring. Keep the external URL and context path when [migrating from Python linkding](migration.md). See [backups](backups.md) for SQLite backup and restore commands; PostgreSQL needs a database backup plus a copy of the data volume.

## Release workflow

Every push and pull request runs the Go/frontend checks and basic/plus image smoke tests. A `vX.Y.Z` tag runs the checks again, builds six cross-platform archives, creates `SHA256SUMS`, publishes the files to GitHub Releases, and pushes linux/amd64 and linux/arm64 basic and plus images to GHCR. Tag a commit only after the parity checks in the [specification](specs/linkding-parity.md) pass.

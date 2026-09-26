# Install and configure linkding (Go)

This guide applies to the Go port of upstream linkding v1.47.0. The examples use release tag `v1.47.1`; choose a newer tag from [GitHub Releases](https://github.com/juev/linkding/releases) when available. To build from source, follow the [README](../README.md).

## Run a release binary

Download the archive for your operating system and CPU from [GitHub Releases](https://github.com/juev/linkding/releases) and check it against `SHA256SUMS`. Releases contain Linux, macOS, and Windows archives for amd64 and arm64. Extract the archive and run the executable from its extracted directory: it loads `web/static/` relative to the current working directory and writes the SQLite database, secret key, and downloaded files to `data/`.

```sh
cd linkding_v1.47.1_linux_amd64
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
  ghcr.io/juev/linkding:v1.47.1
```

Open `http://localhost:9090/`. The named volume stores `db.sqlite3`, `secretkey.txt`, and downloaded files. The image runs without root; its data directory accepts an arbitrary numeric UID. When using a host bind mount instead of a named volume, make the host directory writable by the selected UID. Mount `/etc/linkding/data` even with PostgreSQL because it holds the secret key and files. If your platform forbids a read-only root filesystem, omit `--read-only`; the data volume is still required.

The plus image includes Chromium, SingleFile CLI, and uBlock Origin Lite. Substitute `ghcr.io/juev/linkding-plus:v1.47.1` in the command above. It enables automatic snapshots by default and stores Chromium's profile under the data volume. The extension is installed but disabled by default because it caused intermittent SingleFile load timeouts on both architectures. To enable it in an environment where capture succeeds, add `'--browser-arg="--load-extension=uBOLite.chromium.mv3"'` to `LD_SINGLEFILE_UBLOCK_OPTIONS` along with the default browser arguments. Keep the writable `/tmp` mount for Chromium. The image healthcheck runs `linkding healthcheck`; the HTTP endpoint is `/health` or `/<LD_CONTEXT_PATH>health`.

For Docker Compose, use the same mounts and options:

```yaml
services:
  linkding:
    image: ghcr.io/juev/linkding:v1.47.1
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

`LD_DB_HOST` must resolve from the server's environment; `postgres` is a typical Compose service name. The default engine is SQLite, stored at `data/db.sqlite3`. SQLite transactions use `IMMEDIATE` by default so concurrent writes wait for the busy timeout; set `LD_DB_OPTIONS={"transaction_mode":"deferred"}` only if that behavior is required. For encrypted PostgreSQL connections, set `LD_DB_OPTIONS` to JSON such as `{"sslmode":"verify-full","sslrootcert":"/path/to/ca.crt"}` and make the certificate readable inside the container.

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
| `LD_USE_X_FORWARDED_HOST` | false | Use `X-Forwarded-Host` and `X-Forwarded-Proto` for OIDC redirect construction. Set only behind a trusted proxy that replaces client-supplied forwarded headers. |
| `LD_CSRF_TRUSTED_ORIGINS` | empty | Comma-separated external origins allowed for session writes. |
| `LD_CORS_ALLOWED_ORIGINS` | empty | Comma-separated origins allowed for cross-origin API requests. |
| `LD_ENABLE_OIDC`, `LD_ENABLE_AUTH_PROXY` | false | Enable OIDC or trusted authentication proxy integration. |
| `LD_AUTH_PROXY_USERNAME_HEADER`, `LD_AUTH_PROXY_LOGOUT_URL` | `REMOTE_USER`, empty | Select the proxy username header and optional logout redirect. |

The configuration also accepts `LD_ALLOWED_INTERNAL_HOSTS`, `LD_DISABLE_URL_VALIDATION`, `LD_ENABLE_REFRESH_FAVICONS`, `LD_FAVICON_PROVIDER`, `LD_PREVIEW_MAX_SIZE`, `LD_SNAPSHOT_PDF_MAX_SIZE`, `LD_SINGLEFILE_PATH`, `LD_SINGLEFILE_OPTIONS`, `LD_SINGLEFILE_UBLOCK_OPTIONS`, `LD_SINGLEFILE_TIMEOUT_SEC`, `LD_DISABLE_ASSET_UPLOAD`, `LD_DISABLE_LOGIN_FORM`, `LD_SESSION_COOKIE_AGE`, `LD_REQUEST_TIMEOUT`, `LD_REQUEST_MAX_CONTENT_LENGTH`, and logging options. OIDC uses `OIDC_OP_*`, `OIDC_RP_*`, `OIDC_USE_PKCE`, `OIDC_VERIFY_SSL`, and `OIDC_USERNAME_CLAIM`. Keep secrets out of committed Compose files.

When serving under a proxy path, set `LD_CONTEXT_PATH=linkding/`, forward requests for `/linkding/`, and use `/linkding/health` for monitoring. Keep the external URL and context path when [migrating from Python linkding](migration.md). See [backups](backups.md) for SQLite backup and restore commands; PostgreSQL needs a database backup plus a copy of the data volume.

For proxy authentication, set `LD_ENABLE_AUTH_PROXY=true` and `LD_AUTH_PROXY_USERNAME_HEADER=HTTP_X_REMOTE_USER`, then have the proxy set the HTTP `X-Remote-User` header. The Go server converts Django's `HTTP_` setting to the corresponding HTTP header. The proxy must replace any client-supplied username header and block direct access to the server; otherwise clients could choose their own account.

## Release workflow

Every push and pull request runs the Go/frontend checks, `goreleaser check`, and basic/plus image smoke tests. After the parity checks in the [specification](specs/linkding-parity.md) pass, use [GoReleaser v2](https://goreleaser.com/) to inspect a local snapshot without publishing:

```sh
npm ci
npm run build
goreleaser check
goreleaser release --snapshot --clean
python3 scripts/verify_release.py dist
bash scripts/smoke_release_images.sh
```

The verifier checks all six archives, required runtime files, and `SHA256SUMS`. The smoke script starts both locally built image variants on amd64 and arm64 with a read-only root, arbitrary UID, and persistent volume. It checks SingleFile on both architectures with the image defaults. The Release workflow also checks capture on native arm64. The snapshot build requires Docker Buildx and a running Docker daemon. It does not publish a GitHub Release or push GHCR images. Run the Release workflow manually from GitHub Actions to rehearse CI, the GoReleaser snapshot, and the native arm64 image check. A manual run skips the publish job.

Push a version tag such as `v1.47.1` on the checked commit to start [the release workflow](../.github/workflows/release.yml). The workflow reruns CI, a GoReleaser snapshot, and a native arm64 Chromium capture before its publish job. GoReleaser builds the six binaries with `CGO_ENABLED=0`, packages the static files and notices, creates `SHA256SUMS`, publishes the archives to GitHub Releases, and pushes multiarch basic and plus images to GHCR. The workflow then checks the published files and image architectures. The release job uses the repository `GITHUB_TOKEN` with `contents: write` and `packages: write`; it needs no personal token. Review the GitHub Actions result and published release before announcing the tag.

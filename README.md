# linkding (Go implementation)

This repository is an independent Go implementation of [linkding](https://github.com/sissbruecker/linkding), targeting compatibility with upstream v1.47.0. It is under development: feature parity, migration, and release checks are not complete. The upstream project is maintained separately.

The implementation aims to preserve linkding's interface and public contracts, including its `LD_*` configuration names. The [parity specification](docs/specs/linkding-parity.md) records the target behavior and verification scenarios.

## Build and test

Go 1.27 and Node.js with npm are required to build the current source tree. From the repository root:

```sh
npm ci
npm run build
CGO_ENABLED=0 go build -o bin/linkding ./cmd/linkding
CGO_ENABLED=0 go test ./...
```

The frontend build writes generated JavaScript and CSS into `web/static/`; those files are not committed. The Go binary serves static files from that directory relative to its working directory.

## Docker

Build the basic image and start a fresh SQLite installation with a persistent Docker volume:

```sh
docker buildx build --target linkding -t linkding-go:basic --load .
docker volume create linkding-data
docker run -d --name linkding -p 9090:9090 \
  -v linkding-data:/etc/linkding/data \
  -e LD_SUPERUSER_NAME=admin \
  -e LD_SUPERUSER_PASSWORD=change-me \
  linkding-go:basic
```

Open `http://localhost:9090/` and replace `change-me` with your own password before starting the container. The image creates the SQLite database and secret key in `/etc/linkding/data` on first start. Its healthcheck calls `/health`, or the prefixed path when `LD_CONTEXT_PATH` is set.

Build the plus image for HTML snapshots:

```sh
docker buildx build --target linkding-plus -t linkding-go:plus --load .
```

Run it with the same port, volume, and `LD_*` settings. Plus enables snapshots by default and includes Chromium, SingleFile CLI, and uBlock Origin Lite. The basic image leaves automatic snapshots disabled. For PostgreSQL, set `LD_DB_ENGINE=postgres`, `LD_DB_HOST`, `LD_DB_PORT`, `LD_DB_DATABASE`, `LD_DB_USER`, and `LD_DB_PASSWORD`; keep `/etc/linkding/data` persistent for the secret key and stored files.

SQLite backup and restore commands are described in [Back up and restore data](docs/backups.md).
Migration from Python linkding v1.47.0 is described in [Migrate an existing installation](docs/migration.md).

## Attribution

The original linkding project is by Sascha Ißbrücker and is licensed under MIT. Its copyright and license notice are preserved in [LICENSE.txt](LICENSE.txt). Copied Django Admin assets and the common-password list are documented in [third-party notices](docs/third-party/README.md).

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

Tagged releases use GoReleaser; see the [release workflow](docs/installation.md#release-workflow) for local verification and publication.

## Docker

Build either image from this checkout:

```sh
docker buildx build --target linkding -t linkding-go:basic --load .
docker buildx build --target linkding-plus -t linkding-go:plus --load .
```

See [installation and configuration](docs/installation.md) for released binaries, GHCR images, Docker Compose, PostgreSQL, proxy settings, persistent storage, and health checks. The basic image contains the Go server; the plus image adds Chromium, SingleFile CLI, and uBlock Origin Lite for automatic snapshots.

SQLite backup and restore commands are described in [Back up and restore data](docs/backups.md).
Migration from Python linkding v1.47.0 is described in [Migrate an existing installation](docs/migration.md).

## Attribution

The original linkding project is by Sascha Ißbrücker and is licensed under MIT. Its copyright and license notice are preserved in [LICENSE.txt](LICENSE.txt). Copied Django Admin assets and the common-password list are documented in [third-party notices](docs/third-party/README.md).

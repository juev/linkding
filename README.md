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

## Attribution

The original linkding project is by Sascha Ißbrücker and is licensed under MIT. Its copyright and license notice are preserved in [LICENSE.txt](LICENSE.txt). Copied Django Admin assets and the common-password list are documented in [third-party notices](docs/third-party/README.md).

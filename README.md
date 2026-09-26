<div align="center">
  <a href="https://github.com/sissbruecker/linkding">
    <img src="https://raw.githubusercontent.com/sissbruecker/linkding/v1.47.0/assets/header.svg" height="50" alt="linkding">
  </a>
</div>

# linkding (Go port)

linkding is a bookmark manager you can host yourself. It keeps the interface focused on reading, organizing, and finding links. The name combines *link* with *Ding*, the German word for thing.

This repository is an independent Go port of [linkding v1.47.0](https://github.com/sissbruecker/linkding/tree/v1.47.0). It preserves the original interface, API, and `LD_*` settings while using a Go server. The [parity specification](docs/specs/linkding-parity.md) describes the compatibility target.

The [local load comparison](docs/performance/2026-09-26-linkding-comparison.md), its [optimization follow-up](docs/performance/2026-09-26-optimization-followup.md), the [post-release SQLite investigation](docs/performance/2026-09-26-sqlite-post-release.md), and the [query and algorithm analysis](docs/performance/2026-09-26-query-algorithm-analysis.md) measure the Python server and this Go port on the same bookmark fixture.

## Features

- Organize bookmarks with tags, search, and bundles.
- Edit bookmarks in bulk, write Markdown notes, and use read-it-later lists.
- Share bookmarks with other users or guests.
- Fetch titles, descriptions, icons, and previews automatically.
- Save local HTML snapshots with the plus image or archive pages with the Internet Archive.
- Import and export Netscape HTML bookmarks.
- Install the interface as a Progressive Web App (PWA), use the bookmarklet, or connect the [Firefox](https://addons.mozilla.org/firefox/addon/linkding-extension/) and [Chrome](https://chrome.google.com/webstore/detail/linkding-extension/beakmhbijpdhipnjhnclmhgjlddhidpe) extensions.
- Sign in locally or through OIDC or a trusted authentication proxy.
- Use the REST API and admin panel for integrations and account management.

The interface follows the pinned upstream release:

![Screenshot of linkding v1.47.0](https://raw.githubusercontent.com/sissbruecker/linkding/v1.47.0/docs/public/linkding-screenshot.png)

The original project also provides a [live demo](https://demo.linkding.link/), a [browser extension guide](https://linkding.link/browser-extension), and a list of [community projects](https://linkding.link/community). The demo runs upstream linkding and may be newer than this port.

## Install

Download a Linux, macOS, or Windows archive from the [v1.47.2 release](https://github.com/juev/linkding/releases/tag/v1.47.2), verify it with `SHA256SUMS`, and run the binary from the extracted directory. The archives include the static assets required by the server.

For Docker, create a persistent volume and start the basic image:

```sh
docker volume create linkding-data
docker run -d --name linkding -p 9090:9090 \
  --read-only --user 10001:10001 \
  --tmpfs /tmp:rw,nosuid,nodev,size=512m \
  -v linkding-data:/etc/linkding/data \
  -e LD_SUPERUSER_NAME=admin \
  -e LD_SUPERUSER_PASSWORD='replace-this-password' \
  ghcr.io/juev/linkding:v1.47.2
```

Open `http://localhost:9090/`. The basic image supports SQLite and PostgreSQL, uploads, previews, favicons, and backups. Use `ghcr.io/juev/linkding-plus:v1.47.2` for automatic HTML snapshots with Chromium and SingleFile. Both images support linux/amd64 and linux/arm64, a read-only root filesystem, and a non-root UID.

The [installation guide](docs/installation.md) covers binaries, Docker Compose, PostgreSQL, persistent files, proxy paths, health checks, and configuration. See [migration](docs/migration.md) before replacing a Python linkding installation and [backups](docs/backups.md) for backup and restore commands. The [original documentation](https://linkding.link/) covers common linkding features; use this repository's installation guide for the Go port.

## Develop

Build from source with Go 1.27 and Node.js 22 with npm:

```sh
npm ci
npm run build
mkdir -p bin
CGO_ENABLED=0 go build -o bin/linkding ./cmd/linkding
CGO_ENABLED=0 go test ./...
go vet ./...
```

The frontend build writes generated JavaScript and CSS to `web/static/`; those files are not committed. Run the binary from the repository root so it can serve those assets. To build the basic or plus image locally, use the `linkding` or `linkding-plus` target in [Dockerfile](Dockerfile). Tagged releases use [GoReleaser](docs/installation.md#release-workflow).

Contributions to this port can be proposed through [issues](https://github.com/juev/linkding/issues) and pull requests in this repository. The original linkding project is maintained separately by Sascha Ißbrücker under the MIT license. Its notice is preserved in [LICENSE.txt](LICENSE.txt); copied Django Admin assets and the common-password list are covered by [third-party notices](docs/third-party/README.md).

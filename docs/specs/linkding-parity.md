# linkding v1.47.0 migration contract

Status: target behavior; implementation and verification are incomplete.

Sources: the user's decision on 2026-09-25 and the [linkding v1.47.0 source](https://github.com/sissbruecker/linkding/tree/v1.47.0), commit `24b5ad6cc9bde497b5d1b1e86aed5a2fb7d25c2f`. If documentation and code disagree, responses from a running upstream instance at the pinned version are the reference. This document defines acceptance criteria for one complete release, not a sequence of partial releases.

## Requirements

- R1. A bookmark can be created, edited, deleted, archived, restored, marked as read, shared, and changed in bulk through the UI and API. On creation, an existing bookmark owned by the user is found by normalized URL and follows the update path. The UI form checks edited URLs by normalized URL with an exact fallback for records lacking a normalized URL; the REST serializer checks edited URLs by exact string. Tag changes, timestamps, and background jobs follow the corresponding upstream path. Validation and authorization errors leave state unchanged.
- R2. Search supports expression syntax and legacy mode, phrases, tags, logical operators, parentheses, filters, sorting, and pagination. Tags can be created, edited, deleted, and merged. Bundles store a search, filters, and order. Auto-tagging rules apply on creation. Given the same access context, UI, API, and RSS select the same bookmarks.
- R3. Local login, password changes, sessions, API and feed tokens, OIDC, auth proxy, roles, and the guest profile preserve access to private, shared, and public bookmarks and assets. Ordinary UI and API writes are limited to the owner; administrators manage data through admin. Existing Django session cookies require a new login after migration; passwords and tokens remain valid.
- R4. The REST API reproduces upstream routes, methods, JSON fields, statuses, errors, filters, pagination, and authentication. Existing clients and the browser extension continue to work at the same URL.
- R5. Metadata, favicons, previews, Wayback integration, HTML and PDF snapshots, uploaded assets, reader mode, and file downloads preserve formats, statuses, and access rules. Background jobs survive failures and retries; SingleFile runs serially. A completed asset record must not point to a missing file.
- R6. RSS, bookmarklets, the PWA manifest and share target, OpenSearch, Netscape HTML import and export, full backups, admin, and CLI match upstream. This includes UI notifications and acknowledgments, token management in settings, and queue inspection in admin.
- R7. All ordinary and admin pages use the appearance, CSS, images, themes, and interactions of linkding v1.47.0. Turbo Frame and Stream behavior, Lit components, forms, shortcuts, custom CSS, and responsive layout remain available. Preserve the original MIT license and third-party asset notices.
- R8. Migration accepts an installed Python linkding v1.47.0 instance; older installations first use the upstream upgrade path. It preserves users, groups, roles and permissions, password hashes, profiles, bookmarks, tags, bundles, assets, API and feed tokens, notifications, global settings, relationships, timestamps, and files from SQLite or PostgreSQL. The source database and backup remain untouched until verification. Cutover uses a maintenance window and retains the external URL.
- R9. The Go application supports SQLite and PostgreSQL, `LD_*` settings, context paths, proxies, health checks, and basic and plus images. Every Go binary builds with `CGO_ENABLED=0`; the plus image may contain external Chromium and SingleFile programs. CI builds and tests every change. A complete tagged release uses GoReleaser to publish checksummed bundles containing the executable, static assets, and notices for Linux, macOS, and Windows on amd64/arm64 through GitHub Releases, and basic and plus linux/amd64 and linux/arm64 images through GHCR. The basic image must be small and fully functional for its supported feature set; the plus image must run SingleFile HTML/PDF snapshots. Both images must support SQLite/PostgreSQL, persistent storage, custom ports/context paths, and non-root/read-only deployments with documented writable mounts. The installation guide covers binaries, Docker/Compose, settings, proxy, health, backups, migration, and the GoReleaser release process. Release all functionality together after the acceptance checks pass.

## HTTP surface

The routes come from [urls.py](https://github.com/sissbruecker/linkding/blob/v1.47.0/bookmarks/urls.py) and [api/routes.py](https://github.com/sissbruecker/linkding/blob/v1.47.0/bookmarks/api/routes.py). `LD_CONTEXT_PATH` prefixes every path, including auth, admin, and OIDC. Exact UI responses to unusual HTTP methods need to be measured against a running reference: `urls.py` does not itself constrain methods.

| Group | Paths |
| --- | --- |
| Login and navigation | `/`, `/login/`, `/logout/`, `/change-password/`, `/password-change-done/` |
| Bookmarks UI | `/bookmarks`, `/bookmarks/action`, `/bookmarks/archived`, `/bookmarks/archived/action`, `/bookmarks/shared`, `/bookmarks/shared/action`, `/bookmarks/new`, `/bookmarks/close`, `/bookmarks/{id}/edit` |
| Assets UI | `/assets/{id}`, `/assets/{id}/read` |
| Bundles UI | `/bundles`, `/bundles/action`, `/bundles/new`, `/bundles/{id}/edit`, `/bundles/preview` |
| Tags UI | `/tags`, `/tags/new`, `/tags/{id}/edit`, `/tags/merge` |
| Settings UI | `/settings`, `/settings/general`, `/settings/update`, `/settings/integrations`, `/settings/integrations/create-api-token`, `/settings/integrations/delete-api-token`, `/settings/import`, `/settings/export` |
| Other UI | `/toasts/acknowledge`, `/health`, `/manifest.json`, `/custom_css`, `/opensearch.xml`, `/live_reload` when DEBUG is enabled |
| Feeds | `/feeds/{feed_key}/all`, `/feeds/{feed_key}/unread`, `/feeds/{feed_key}/shared`, `/feeds/shared` |
| API root | `/api/` |
| Bookmarks API | `/api/bookmarks/` GET/POST; `/api/bookmarks/{id}/` GET/PUT/PATCH/DELETE; `/api/bookmarks/archived/` GET; `/api/bookmarks/shared/` GET; `/api/bookmarks/check/` GET; `/api/bookmarks/singlefile/` POST; `/api/bookmarks/{id}/archive/` and `/unarchive/` POST |
| Assets API | `/api/bookmarks/{id}/assets/` GET; `/api/bookmarks/{id}/assets/upload/` POST; `/api/bookmarks/{id}/assets/{asset_id}/` GET/DELETE; `/api/bookmarks/{id}/assets/{asset_id}/download/` GET |
| Tags, bundles, profile API | `/api/tags/` GET/POST and `/{id}/` GET/DELETE; `/api/bundles/` GET/POST and `/{id}/` GET/PUT/PATCH/DELETE; `/api/user/profile/` GET |
| Admin/OIDC | `/admin/` with standard Django model CRUD and `/admin/tasks/`; `/oidc/` and the routes supplied by `mozilla_django_oidc` when OIDC is enabled |

## Data and compatibility

The source models are defined in [models.py](https://github.com/sissbruecker/linkding/blob/v1.47.0/bookmarks/models.py). Migration covers `bookmarks_tag`, `bookmarks_bookmark`, `bookmarks_bookmark_tags`, `bookmarks_bookmarkasset`, `bookmarks_bookmarkbundle`, `bookmarks_userprofile`, `bookmarks_toast`, `bookmarks_feedtoken`, `bookmarks_apitoken`, and `bookmarks_globalsettings`, along with `auth_user`, group and permission tables, and the required Django admin relationships. `0054_bookmarkbundle_filter_shared_and_more` is the last bookmarks migration in this release. Validate the source through `django_migrations`, not just a version string. Copy `data/favicons`, `data/previews`, and `data/assets` while checking references and checksums. Run Python v1.47.0 `migrate_tasks` if the legacy `background_task` table still contains rows. Huey stores its queue separately in `data/tasks.sqlite3`; drain outstanding jobs before cutover. The Go migration and server reject remaining legacy tasks.

Upstream SQLite uses ICU for Unicode `lower()`, `LIKE`, and sorting. The pure Go implementation registers compatible functions and collation in `modernc.org/sqlite`, then compares results against upstream for Unicode, wildcard and escape behavior, `NULL`, and compound queries. Verify PostgreSQL independently. A known difference blocks release.

## Verification scenarios

- R1–R2: create a bookmark, repeat its URL in another record, apply tags and auto-tagging, archive it, and find it with a compound query through UI, API, and RSS. Check database records, order, pagination, and access errors. Repeat on both databases with a Unicode corpus.
- R3–R4: send owner, other-user, and guest requests with sessions and tokens. Compare HTTP statuses, redirects, cookies, JSON, and absence of side effects after rejection with upstream.
- R5–R6: process local HTTP fixtures for metadata, PDF, and HTML; interrupt and retry a job; check files, statuses, reader and download behavior, backup and restore, feeds, and admin actions.
- R7: open every UI route on desktop and mobile, submit forms, and use keyboard actions. Compare DOM, Turbo responses, and screenshots against the original server with fixed data.
- R8–R9: migrate populated SQLite and PostgreSQL installations while the Python server is stopped. Compare counts, IDs, relationships, hashes, files, login, and tokens. Start both container variants with `CGO_ENABLED=0`; verify the previous URL and `LD_CONTEXT_PATH`. Test linux/amd64 and linux/arm64 images with health, login, restart, persistent SQLite/PostgreSQL, arbitrary non-root UID, read-only root with writable mounts, and plus SingleFile jobs. Run a GoReleaser snapshot, inspect six OS/architecture archives with static files and SHA-256 checksums, and start its basic and plus images. Rehearse the tag workflow and verify GitHub Release assets and GHCR manifests/digests. Keep the source installation available for rollback before enabling Go writes.

No test described here yet establishes full parity. Extend the response matrix and fixtures with observations from the running upstream server as implementation proceeds.

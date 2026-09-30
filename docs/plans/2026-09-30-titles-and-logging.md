# Title normalization and console logging

Status: done. Mode: execution. The user approved the proposed fixes on 2026-09-30. The root `plan.md` records the earlier port and release; this task preserves that document.

Problem: HTTP requests are silent and long titles cause validation errors. Some metadata writes bypass the title limit.
Shipping: normalized titles across all save paths, console access logs, lifecycle messages, and source errors for bookmark saves and imports.
Not shipping: schema changes, historical data rewrites, release publication, or unrelated HTTP handler refactoring.
Verification: regression tests, SQLite/PostgreSQL integration tests, browser save, runtime logging smoke, Go tests, vet, build, and frontend build.

The [compatibility contract](../specs/linkding-parity.md#title-normalization-and-console-logs) defines the approved behavior. Title normalization uses one shared function at persistence boundaries and in the extractor. Request logging wraps the complete middleware chain. The existing standard logger writes to stderr.

## Steps

1. Add Unicode normalization and cover boundaries, control characters, and idempotence. Update API, forms, repository, import, metadata, admin, and background refresh; verify saved values and omitted PATCH titles.
2. Add request and lifecycle logs. Verify suppression, error responses, forwarded IPs, token redaction, informational statuses, and streaming capabilities.
3. Add source-error logging to the affected bookmark and import handlers while retaining public responses. Update installation documentation and the compatibility contract.
4. Run integration and project checks, inspect the final diff, and record results.

## State

Implementation, source-error logging, and documentation are complete. The original long-title POST regression returned 400; the fixed tests pass across SQLite and PostgreSQL for API, forms, admin, import, metadata, SingleFile, and background refresh. The full PostgreSQL-backed `CGO_ENABLED=0 go test -p 1 ./...`, `go vet ./...`, CGO-free build, frontend build, diff checks, and prose scan passed. Focused logging tests also passed after preserving sent statuses for aborted requests.

An isolated browser form accepted a title containing 614 Unicode code points and persisted a normalized title of 512 code points. The browser console had no errors after asset setup. Runtime stderr showed startup, the successful save request, and graceful shutdown. Test accounts and the generated secret key from the first smoke launch were removed, with zero users and bookmarks confirmed in the local database afterward. Subsequent checks used a temporary cwd. The test server and browser were stopped, and the disposable PostgreSQL container was removed. Release publication is outside this task's scope.

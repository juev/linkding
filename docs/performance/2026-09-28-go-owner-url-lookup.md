# SQLite URL lookup across bookmark owners

The Go server used `INDEXED BY` to force the single-column normalized-URL index when creating a bookmark. That made misses fast for one owner with 10,000 distinct URLs, but it scanned every matching index entry when many owners saved the same URL. This experiment compares that query with an owner-scoped composite index and separate normalized and legacy URL branches.

The [raw HTTP results](2026-09-28-go-owner-url-lookup-results.json) contain every run, response status, latency percentile, and database check.

## Method

The baseline is commit `a5b8d3015c8b2894fcd3dc5f8f514b44cc6858c7`; the candidate is this branch. Both binaries were built with `CGO_ENABLED=0` and Go 1.27.0, then run sequentially on the same Darwin arm64 host with `GOMAXPROCS=2` and background tasks disabled. These local processes had no container CPU or memory limits.

Each run started from a fresh copy of a SQLite database with the previous single-column index and no tags. The candidate applied its new migration at startup. The one-owner fixture contained 10,000 distinct bookmarks; 200 warmup and 1,000 measured POST requests created distinct URLs. The many-owner fixture contained one target owner and 10,000 other owners who had all saved `https://example.org/bench/shared/shared`. Its 200 warmup requests created that URL for the target owner, and 1,000 measured POST requests updated the same bookmark. The workloads must be compared within each fixture, not against each other.

The [load runner](../../scripts/perf/load.go) used closed-loop clients at 8 and 32 connections. Each variant ran three times per point, with alternating baseline/candidate order. The [fixture seeder](../../scripts/perf/seed_go_url_lookup.py) created the data and API token. The table shows medians; the JSON retains individual repeats.

| Fixture and clients | Baseline requests/s | Candidate requests/s | Baseline p95 ms | Candidate p95 ms |
| --- | ---: | ---: | ---: | ---: |
| One owner, 8 | 5,011 | 4,918 | 2.6 | 2.5 |
| One owner, 32 | 5,417 | 5,348 | 10.5 | 10.4 |
| Shared URL across owners, 8 | 533 | 6,884 | 15.2 | 3.6 |
| Shared URL across owners, 32 | 543 | 6,976 | 98.5 | 8.7 |

The shared-URL workload improved by about 12.9 times at both client counts. The one-owner throughput differences were smaller than the variation across repeats, so this test does not establish a throughput change there. All 24 measured runs returned 1,000 HTTP 201 responses with no load-runner errors. Every database passed `PRAGMA integrity_check`; one-owner runs ended with 11,200 bookmarks and shared-URL runs with 10,001. Candidate databases had only the new composite URL index, and baseline databases retained only the old index. None had `sqlite_stat1`.

## Migration and behavior checks

The SQLite migration replaces the URL index without copying the bookmark table. A migration test populated a database, applied the migration, rolled it back, and applied it again. The bookmark table kept its root page and row count, and the integrity check passed after each step. Tests also cover imported Python-schema databases, owner isolation, the oldest matching bookmark across normalized and legacy URL records, and the query plans for create, check, and normalized duplicate validation during edit. PostgreSQL retains its existing query and index.

These are short local runs. They do not measure sustained capacity, index-build time on a large live database, or write blocking during migration. The HTTP test includes the full create endpoint, while the earlier isolated SQL probe explained the cost of the old index on a shared URL that the target owner had not yet saved.

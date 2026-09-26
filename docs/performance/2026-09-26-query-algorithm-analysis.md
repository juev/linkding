# Query plans and application algorithms

The [post-release SQLite investigation](2026-09-26-sqlite-post-release.md) established that two better SQL plans made the Go server faster on its test fixture. It did not isolate a speed advantage from the Go language. This follow-up tested the same URL lookup in the pinned Python server and profiled work inside the Go search path. The Python server benefits from a better SQLite plan too. The Go search path also had an avoidable allocation cost that was independent of the URL lookup.

The [measurements](2026-09-26-query-algorithm-results.json) contain 54 HTTP result streams and summaries of the SQL probes and Go repository benchmarks. All HTTP points used fresh copies of the same 10,000-bookmark fixture and three repeats. The servers ran sequentially on Docker Desktop's Linux/arm64 VM, each limited to 2 CPUs and 2 GiB RAM. The [load runner](../../scripts/perf/load.go) sent 1,000 measured requests per stream after warmup, with background tasks disabled. Tables report medians of each metric across the three repeats.

## The original Python query benefits too

The pinned Python image's `Bookmark.query_existing` in `bookmarks/models.py` uses the same normalized-URL condition and fallback as the Go port. Its SQLite fixture had no `sqlite_stat1` rows. `EXPLAIN QUERY PLAN` selected the owner index and scanned that user's bookmarks on a URL miss. Forcing the existing normalized-URL index in a disposable SQL probe reduced 2,000 misses from 2.493 to 0.034 seconds. Running `ANALYZE` on a separate fixture copy made the unchanged Django query choose the normalized-URL index; 2,000 raw misses then took 0.031 seconds, and 2,000 `Bookmark.query_existing(...).first()` calls fell from 3.077 to 0.439 seconds.

We then ran the unmodified Python image against the plain and analyzed fixture copies. Each create point made 200 warmup writes and 1,000 measured writes to distinct URLs. The Go column uses the already published URL and tag-query fixes from commit `c964df7`. Every response was HTTP 201, every database ended with 11,200 bookmarks, and every SQLite integrity check returned `ok`.

| SQLite create | Python plain RPS | Python after `ANALYZE` RPS | Go RPS | p95 ms: plain / analyzed / Go | p99 ms: plain / analyzed / Go |
| --- | ---: | ---: | ---: | ---: | ---: |
| 8 clients | 347.6 | 412.0 | 1408.4 | 39.9 / 38.3 / 13.9 | 76.6 / 83.4 / 63.3 |
| 32 clients | 349.6 | 397.1 | 1383.1 | 113.3 / 104.6 / 46.1 | 148.2 / 143.0 / 80.7 |

The [measured Python throughput gain](2026-09-26-query-algorithm-results.json) is 18.5% at 8 clients and 13.6% at 32 clients. That is smaller than the isolated lookup gain because an HTTP create also validates, saves, handles tags, and serializes a response. In one service-level probe with an empty tag list, `create_bookmark` executed seven SQL statements, including two bookmark saves. The Go and Python applications use different request stacks and database access patterns; these results do not assign the remaining throughput difference to the programming language alone.

## Go search algorithm

Before this change, each SQLite text term called the Go scalar function `ld_ci_contains` separately for title, description, notes, and URL. The query still has to inspect candidate rows, but each function call also converts SQLite values across the pure-Go driver's function boundary. On a 10,000-bookmark repository search, an [allocation profile](2026-09-26-query-algorithm-results.json) attributed 97.8% of allocated objects to `modernc.org/sqlite.functionArgs` and `modernc.org/libc.GoString`. The CPU profile was dominated by SQLite file reads, so the allocation profile identifies a cost without claiming that substring matching itself dominated CPU time.

The new SQLite search expression calls one `ld_ci_contains_any` function for the four fields. It uses the same Unicode-aware substring comparison and preserves SQL's `NULL` result when no field matches but one is `NULL`; PostgreSQL keeps its existing expression. Direct SQLite function tests cover ASCII, Unicode, each end of the field list, and `NULL`. Existing strict and legacy search tests run on SQLite and PostgreSQL. A 300-iteration repository benchmark changed as follows:

| Search benchmark | Before | Combined function |
| --- | ---: | ---: |
| Time per request | 11.13 ms | 9.04 ms |
| Allocated bytes per request | 4.13 MB | 3.64 MB |
| Allocations per request | 215,550 | 147,538 |

The paired HTTP runs below used the same fixture and compared commit `c964df7` with a build that adds only the combined-function change. Every response was HTTP 200, with counts 2,000 for ordinary search and 100 for compound search; there were no load-runner errors.

| SQLite read | Go before RPS | Go after RPS | p95 ms before / after | p99 ms before / after |
| --- | ---: | ---: | ---: | ---: |
| Search, 8 clients | 103.6 | 134.4 | 110.6 / 83.8 | 128.9 / 96.1 |
| Search, 32 clients | 105.6 | 149.1 | 480.4 / 351.0 | 587.0 / 444.5 |
| Compound, 8 clients | 81.1 | 92.3 | 143.3 / 126.3 | 166.4 / 142.3 |
| Compound, 32 clients | 82.3 | 94.9 | 640.3 / 571.0 | 762.0 / 684.8 |

In an interleaved mixed run with 16 search and 16 create clients, median search throughput rose from 88.6 to 105.1 RPS and create throughput from 138.4 to 153.9 RPS. Search p95 fell from 292.7 to 268.0 ms; create p95 fell from 223.3 to 204.7 ms. Each run returned 1,000 HTTP 200 and 1,000 HTTP 201 responses, ended with 11,200 bookmarks, and passed the SQLite integrity check. Complete list, ordinary-search, and compound-search JSON responses matched the pinned Python server on the shared fixture.

## Other application work examined

The Go create path normalizes the URL twice, parses an empty auto-tagging script, clears tag relationships for a new bookmark without tags, and reads the created bookmark back after commit. These are concrete repeated operations in [bookmark creation](../../internal/bookmarks/repository.go) and [auto-tagging](../../internal/bookmarks/autotag.go). A 1,000-iteration repository create benchmark took 0.424 ms/op and 220 allocations/op; its short CPU profile was dominated by SQLite calls. We left those steps unchanged because this evidence does not establish a meaningful HTTP throughput gain from removing any one of them.

The Go list path already batches bookmark and tag hydration rather than querying once per bookmark. It still performs a count, an ordered ID query, and batch hydration before [JSON serialization](../../internal/httpserver/bookmarks_api.go). Larger structural changes, such as a search index or precomputed folded text, would require a schema and compatibility decision. The current search still scans candidate text, and the 32-client compound p99 of 684.8 ms remains above the 597.3 ms measured for Python in the [previous comparison](2026-09-26-sqlite-post-release.md#results). These short closed-loop runs do not establish sustained capacity or an SLA.

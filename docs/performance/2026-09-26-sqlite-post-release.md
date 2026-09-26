# SQLite investigation after v1.47.2

The [v1.47.2 comparison](2026-09-26-optimization-followup.md) found that Go handled SQLite reads faster than Python linkding v1.47.0 but handled creates 12–15% slower. This investigation found two SQL query plans that hid the Go server's advantage. With both changes, the Go candidate completed more creates and compound searches per second on the same fixture. This is a local comparison, not a production capacity estimate.

The [raw results](2026-09-26-sqlite-post-release-results.json) contain all 60 measured streams, including each run's throughput, latency percentiles, HTTP statuses, errors, and the create-run SQLite integrity checks. The [earlier report](2026-09-26-optimization-followup.md) covers the other read workloads, PostgreSQL, and background jobs. The changes described here are **after** the v1.47.2 release; the released binaries do not contain them.

## Driver and diagnosis

The local `nebula-mesh` checkout and this repository both require `modernc.org/sqlite v1.59.0`. Both use the pure Go driver. Nebula sets defensive mode, a 5-second busy timeout, foreign keys, and WAL through its DSN, with up to 25 open connections for a file database. Linkding uses a 5-second busy timeout, foreign keys, `IMMEDIATE` write transactions, WAL at startup, and up to four SQLite connections. Changing drivers cannot explain the measured difference between these two Go applications.

On the 10,000-bookmark fixture, SQLite chose the `owner_id` index for duplicate lookup and scanned the owner's bookmarks on each URL miss, even though the upstream schema has a `url_normalized` index. An SQLite-only `INDEXED BY` hint makes both arms of the existing normalized-URL/legacy-URL condition use the normalized-URL index. The `ORDER BY id LIMIT 1` rule remains in place so an older legacy match still wins. In a local 2,000-query probe, misses fell from 4.37 s to 29.5 ms and hits from 396.7 ms to 30 ms. These probe totals isolate the lookup; the HTTP numbers below measure the complete request.

Compound search also ran a correlated tag join for every candidate bookmark. For SQLite, the candidate query uses an uncorrelated subquery for tag IDs with the same Unicode-aware comparison, then checks membership in the bookmark-tag index. A 100-iteration count-query benchmark fell from 7.78 to 6.26 ms/op. PostgreSQL keeps its existing query. Regression tests cover strict and legacy tag search, Unicode case folding, bundles, and the old duplicate-selection rule on the database paths they affect.

## Method

We ran Python `sissbruecker/linkding:1.47.0`, the released Go v1.47.2 build, a Go build with only the URL lookup fix, and a Go build with both fixes, sequentially on Docker Desktop's Linux/arm64 VM. The host was an Apple Mac16,8 with 12 CPUs and 24 GiB RAM. Each application container had 2 CPUs and 2 GiB RAM. Every point started from a fresh copy of the [10,000-bookmark fixture](2026-09-26-optimization-followup.md#method): one user, 100 tags, 20,000 bookmark-tag links, and 10% archived bookmarks. Background tasks were disabled.

The [load runner](../../scripts/perf/load.go) used closed-loop clients and 1,000 measured requests per stream. Each point has three repeats. Creates used 200 warmup writes before 1,000 distinct-URL writes. Compound reads used 100 warmup requests and searched for `systems and #tag-008`, which returns 100 bookmarks. Mixed runs used 16 search clients and 16 create clients at the same time, with 1,000 requests per stream. Tables show medians; the JSON contains each repeat so ranges can be calculated.

## Results

The URL lookup fix changed the create path. The later tag-query fix does not run during these creates. Every create returned HTTP 201, each database ended with 11,200 bookmarks, and every `PRAGMA integrity_check` returned `ok`.

| SQLite create | Clients | Python RPS | Released Go RPS | Go with URL fix RPS | p95 ms Python / released / fixed | p99 ms Python / released / fixed |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Distinct URLs | 8 | 375.3 | 314.5 | 1462.0 | 38.0 / 36.2 / 12.1 | 64.9 / 218.5 / 46.5 |
| Distinct URLs | 32 | 370.7 | 320.2 | 1303.1 | 104.4 / 174.7 / 45.0 | 142.4 / 314.0 / 210.9 |

The compound-search points compare the URL-only build with the final candidate. All responses returned HTTP 200 with count 100 and no load-runner errors.

| SQLite compound search | Clients | Python RPS | Go with URL fix RPS | Go with both fixes RPS | p95 ms Python / URL fix / both | p99 ms Python / URL fix / both |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `systems and #tag-008` | 8 | 57.7 | 64.5 | 86.3 | 177.1 / 181.8 / 133.7 | 190.9 / 207.6 / 157.5 |
| `systems and #tag-008` | 32 | 58.5 | 65.3 | 87.2 | 585.7 / 810.8 / 605.2 | 597.3 / 1006.9 / 727.2 |

The earlier mixed run put Python at 52.6 search and 58.6 create RPS, and released Go at 68.3 search and 86.0 create RPS. We then alternated the two new Go builds on fresh copies to check that the tag-query change did not slow the ordinary search and create streams. These are separate measurement cohorts, so compare the new Go builds directly with each other.

| Paired mixed run, 16 clients per stream | Go with URL fix RPS | Go with both fixes RPS | p95 ms URL fix / both | p99 ms URL fix / both |
| --- | ---: | ---: | ---: | ---: |
| Ordinary search | 87.0 | 87.5 | 300.3 / 299.3 | 390.8 / 371.8 |
| Create | 136.0 | 139.1 | 218.1 / 221.9 | 306.0 / 274.4 |

Each mixed run returned 1,000 HTTP 200 and 1,000 HTTP 201 responses, ended with 11,200 bookmarks, and passed the SQLite integrity check. Complete JSON responses for list, ordinary search, and compound search matched the Python server on the same fixture: counts 9,000, 2,000, and 100.

## Alternatives and limits

Reducing the SQLite pool from four connections to two or one made a single 32-client screening run slower for list, ordinary search, and compound search, so the pool stayed at four. Increasing the cache to 8 MiB improved one local compound repository benchmark from 15.33 to 14.10 ms/op, much less than the tag-query change; the default was left alone. A sampled CPU profile of the original compound path was dominated by SQLite file reads. These probes identify likely costs but are not standalone capacity measurements.

At 32 clients, the candidate's compound p95/p99 (605.2/727.2 ms) still exceed Python's (585.7/597.3 ms), despite higher throughput. Create p99 at 32 clients remains above Python's (210.9 versus 142.4 ms). Three short closed-loop runs on one host do not establish an SLA or sustained capacity. Longer tests on the deployment hardware are needed before sizing a production installation.

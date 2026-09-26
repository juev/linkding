# Load comparison after the performance changes

On 2026-09-26, we repeated the [first local comparison](2026-09-26-linkding-comparison.md) after changing the Go server's bookmark loading, search, database pool, and background workers. The Go server now completes more read requests per second than Python linkding v1.47.0 in every measured case on this fixture. It also completes PostgreSQL creates faster. SQLite creates remain slower than Python, and some high-concurrency Go p95/p99 latencies remain higher. These results describe one local setup, not a production capacity limit.

The [machine-readable results](2026-09-26-optimization-results.json) contain every measured HTTP run, median, status count, response size, error count, and metadata-job run.

## Method

- The host was an Apple Mac16,8 with 12 CPUs and 24 GiB RAM, running Docker Desktop 29.4.0 in a Linux/arm64 VM. Each application container had 2 CPUs and 2 GiB RAM. PostgreSQL 16 had its own 2 CPU, 2 GiB container. Python used `sissbruecker/linkding:1.47.0`; Go used the local candidate build. The servers ran sequentially against separate copies of the same fixture.
- The fixture had one user, 10,000 bookmarks, 100 tags, and 20,000 bookmark-tag links; 10% of bookmarks were archived. The Go copies came from the project's Python-to-Go migration. API responses were compared as complete JSON values for list, search, and compound search on both databases; the UI showed the same 30 bookmark URLs in the same order. The API counts were 9,000, 2,000, and 100 respectively.
- The [load runner](../../scripts/perf/load.go) used closed-loop clients: each client sent its next request after consuming the previous response. Each read or create point had 1,000 requests; each UI point had 500. All points were repeated three times. The table reports the median RPS and its minimum–maximum range, plus median p95/p99 latency. RPS counts completed requests divided by wall time. The runner requested uncompressed responses and opened a fresh connection for each POST on both servers.
- The read endpoints and expected counts match the [first comparison](2026-09-26-linkding-comparison.md#test-setup). Background tasks were disabled for HTTP tests. For create, the runner disabled scraping and snapshots, made 200 warmup creates, then measured 1,000 distinct URLs on each fresh copy. SQLite ended with 11,200 bookmarks and `PRAGMA integrity_check=ok` on every create run; PostgreSQL ended with 11,200 bookmarks on every run. All read/UI requests returned HTTP 200, all create requests HTTP 201, and the load runner recorded zero errors.

## SQLite

| Case | Clients | Python RPS median [range] | Go RPS median [range] | Go/Python | p95 ms Python / Go | p99 ms Python / Go |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| List | 1 | 93.1 [92.7–99.6] | 214.4 [213.8–219.5] | 2.30× | 12.4 / 6.3 | 25.9 / 8.5 |
| List | 8 | 169.4 [168.4–175.2] | 340.8 [338.1–357.3] | 2.01× | 70.3 / 34.2 | 86.3 / 38.7 |
| List | 32 | 169.9 [168.3–171.2] | 349.3 [339.1–354.8] | 2.06× | 204.7 / 152.6 | 220.1 / 181.8 |
| Search | 1 | 34.5 [34.0–34.7] | 70.7 [70.3–71.4] | 2.05× | 31.6 / 17.7 | 44.5 / 19.4 |
| Search | 8 | 64.9 [64.8–65.2] | 104.4 [103.7–104.6] | 1.61× | 168.1 / 109.6 | 181.1 / 126.0 |
| Search | 32 | 64.9 [64.9–65.1] | 107.0 [105.2–107.8] | 1.65× | 514.6 / 494.2 | 558.7 / 608.9 |
| Compound | 1 | 31.3 [30.8–31.6] | 54.3 [54.3–54.6] | 1.74× | 35.1 / 22.5 | 48.2 / 24.9 |
| Compound | 8 | 58.5 [58.3–58.8] | 64.8 [63.9–65.0] | 1.11× | 175.9 / 180.4 | 190.5 / 202.9 |
| Compound | 32 | 58.6 [58.5–58.6] | 67.2 [64.0–67.6] | 1.15× | 590.5 / 771.4 | 600.9 / 929.9 |
| UI | 1 | 48.8 [48.6–49.0] | 135.3 [133.4–142.1] | 2.77× | 22.7 / 10.2 | 26.0 / 11.6 |
| UI | 8 | 89.7 [89.2–89.9] | 220.3 [213.2–224.8] | 2.46× | 105.6 / 49.4 | 120.4 / 54.6 |
| UI | 32 | 88.7 [88.6–88.9] | 231.2 [219.0–233.7] | 2.61× | 390.1 / 192.3 | 398.7 / 223.0 |
| Create | 8 | 346.6 [345.5–367.9] | 293.9 [277.6–310.8] | 0.85× | 41.9 / 38.5 | 71.2 / 208.4 |
| Create | 32 | 364.9 [361.5–366.0] | 322.7 [322.5–323.9] | 0.88× | 107.0 / 172.4 | 135.4 / 321.6 |

With 16 search clients and 16 create clients running together, the median search rates were 52.5 RPS for Python and 68.5 for Go; median create rates were 58.4 and 86.6 RPS. Go's p95 was higher in both streams: 441.0 versus 358.8 ms for search, and 347.4 versus 307.4 ms for create. Each of the three runs returned 1,000 HTTP 200 and 1,000 HTTP 201 responses, ended with 11,200 bookmarks, and passed the SQLite integrity check.

## PostgreSQL

| Case | Clients | Python RPS median [range] | Go RPS median [range] | Go/Python | p95 ms Python / Go | p99 ms Python / Go |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| List | 1 | 60.6 [59.9–62.7] | 338.4 [332.7–341.2] | 5.59× | 19.2 / 4.7 | 32.1 / 7.3 |
| List | 8 | 157.7 [155.0–160.2] | 814.9 [814.8–823.3] | 5.17× | 63.8 / 13.9 | 81.5 / 16.5 |
| List | 32 | 156.5 [156.4–158.6] | 843.5 [827.7–843.6] | 5.39× | 226.0 / 62.6 | 238.3 / 76.1 |
| Search | 1 | 36.4 [36.4–36.8] | 73.0 [72.4–74.2] | 2.00× | 30.8 / 15.8 | 42.3 / 18.0 |
| Search | 8 | 109.5 [109.2–109.9] | 152.1 [151.0–152.7] | 1.39× | 86.7 / 85.1 | 98.8 / 92.6 |
| Search | 32 | 109.7 [109.6–109.9] | 152.0 [151.9–152.7] | 1.39× | 315.3 / 365.4 | 327.9 / 427.2 |
| Compound | 1 | 62.5 [62.4–63.0] | 428.6 [427.9–437.3] | 6.85× | 18.9 / 4.2 | 32.6 / 6.7 |
| Compound | 8 | 157.6 [156.2–159.0] | 919.4 [872.1–920.7] | 5.83× | 67.2 / 13.0 | 79.9 / 15.2 |
| Compound | 32 | 156.3 [156.1–157.2] | 900.1 [899.3–905.6] | 5.76× | 226.3 / 59.0 | 239.0 / 70.5 |
| UI | 1 | 40.6 [40.1–40.8] | 263.2 [261.5–267.3] | 6.49× | 27.6 / 7.3 | 31.3 / 8.7 |
| UI | 8 | 114.0 [111.3–114.3] | 540.4 [529.2–548.3] | 4.74× | 81.0 / 21.2 | 90.8 / 24.0 |
| UI | 32 | 111.6 [110.6–115.1] | 528.8 [519.3–532.8] | 4.74× | 307.2 / 86.7 | 314.1 / 104.9 |
| Create | 1 | 99.6 [98.7–100.5] | 696.1 [682.4–710.6] | 6.99× | 12.2 / 1.7 | 13.8 / 2.5 |
| Create | 8 | 294.1 [290.0–296.9] | 2238.6 [2190.6–2344.5] | 7.61× | 31.9 / 5.3 | 36.0 / 7.0 |
| Create | 32 | 289.8 [287.5–292.6] | 2258.4 [2195.8–2287.9] | 7.79× | 124.9 / 23.5 | 129.6 / 210.4 |

## Metadata queue and diagnosis

For each database and server, we queued 100 metadata jobs against the same local mock site with a 50 ms response delay. We measured from the first mock request to the last response, excluding server startup and initial polling. Each of three runs updated all 100 titles and reached two concurrent mock requests; no Go job needed a second attempt.

| Database | Python median [range], s | Go median [range], s |
| --- | ---: | ---: |
| SQLite | 3.457 [3.388–3.471] | 3.224 [3.128–3.232] |
| PostgreSQL | 3.473 [3.453–3.686] | 3.500 [3.413–3.504] |

The Go server now uses two workers for ordinary jobs and one separate worker for `process_snapshot`; the existing database lock still protects snapshots across server instances. A queue test checks that normal jobs proceed while the snapshot lane is busy and that no snapshot is claimed by a normal worker.

The earlier Go list path loaded each page bookmark and its tags separately, requiring about 202 SQL calls for an API page of 100 items, including count and ID selection. The new path loads bookmark fields and tags in batches of at most 500 IDs while preserving the selected order and owner checks. A local 300-request UI benchmark fell from 16.5 to 6.8 ms/request after the matching-tag query was rewritten to start from tags and use `EXISTS`. This was an exploratory benchmark, not a paired container throughput point.

On the PostgreSQL fixture, one `EXPLAIN (ANALYZE, BUFFERS)` sample of the old `ILIKE` search count took 10.9 ms; the `UPPER(...) LIKE UPPER(...)` form used by Python took 7.6 ms for the corresponding count sample. Both scanned the 10,000-row bookmark table. The Go port now uses that expression and caps the PostgreSQL pool at `min(16, max(4, 2 × GOMAXPROCS))`; the 2-CPU container used four connections. The profile and query plans support these changes, but a single plan timing does not isolate their individual contribution to the final throughput.

Against the older Go build at 32 SQLite clients, the new build raised median list/search/compound/UI throughput from 244.1/56.8/41.8/68.5 to 349.3/107.0/67.2/231.2 RPS. All four p95 values also improved. This older build predates the default SQLite `IMMEDIATE` transaction fix, so this comparison applies to read workloads only.

## Limits

The 32-client SQLite compound p95/p99, PostgreSQL simple-search p95/p99, and several SQLite create/mixed tail latencies are still higher on Go than on Python. The SQLite create throughput is 12–15% lower at 8 and 32 clients. PostgreSQL metadata queue time is effectively tied within the observed run-to-run variation. These are visible follow-up targets, not evidence of a failed correctness check.

Three short closed-loop runs on one host do not establish sustained capacity, the number of workers needed for a particular installation, or an SLA. A production sizing decision needs its own traffic mix, target p95/p99 and error rate, deployment hardware, and longer open-loop or steady-state runs. Reproduce this comparison using the [fixture seed](../../scripts/perf/seed_linkding.py), [migration procedure](../migration.md), [load runner](../../scripts/perf/load.go), [login helper](../../scripts/perf/login.py), [job preparation scripts](../../scripts/perf/prepare_python_jobs.py) and [Go job preparation](../../scripts/perf/prepare_jobs/main.go), and [mock site](../../scripts/perf/mock_site.py). The first comparison documents the exact endpoints and command example.

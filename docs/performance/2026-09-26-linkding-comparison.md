# Local load comparison: Python linkding and the Go port

Measured on 2026-09-26 against Python linkding v1.47.0 and a local Go build. Both servers used the same 10,000-bookmark fixture and ran **sequentially**, with separate database copies. The numbers below are achieved throughput at fixed client counts, not a maximum sustainable capacity or an SLA.

The Go port was faster for PostgreSQL writes and most PostgreSQL list, compound-search, and UI cases. Python linkding handled simple search with 32 clients substantially faster. With SQLite, results were mixed; the Go port's default transaction mode returned errors under concurrent writes. Its `IMMEDIATE` SQLite transaction mode completed that write workload without errors. The single Go background worker processed the metadata queue more slowly than Python's two Huey threads.

## Test setup

- Host: Apple Mac16,8, 12 CPUs, 24 GiB RAM; Docker Desktop 29.4.0, Linux/arm64 VM with 12 CPUs and 12.6 GiB RAM.
- Each application container: 2 CPUs and 2 GiB RAM. The PostgreSQL 16 container also had 2 CPUs and 2 GiB RAM. PostgreSQL was shared by the two phases but each server used its own database.
- Python image: `sissbruecker/linkding:1.47.0`, image ID `sha256:cfea06932f7c9eabfd4fe1bda5911bd2bcaa62dd71a5773e63c577c94b31585e`. Its default uWSGI setup has two processes with two threads each; Huey has two worker threads.
- Go image: locally built `linkding-bench-go:9odnko`, image ID `sha256:9b4bcf8104cd58f0f35d9f247c2833f3122780c08b9de3cea05729ad9d54f917`, from worktree at `7451e898cbecc87dfee86c478a6f1e5bb8bb0937`. The worktree also had unrelated local OIDC changes. The Go server uses one HTTP process and one background worker.
- Fixture: one user, 10,000 bookmarks, 100 tags, 20,000 bookmark-tag associations; 10% archived. The seed is [seed_linkding.py](../../scripts/perf/seed_linkding.py). The Go database was created with the project's [migration command](../migration.md) from a stopped copy of the Python database. Counts and read responses were checked before comparing the servers.

The [load runner](../../scripts/perf/load.go) used 1, 8, and 32 concurrent clients. Each API point sent 1,000 requests; each UI point sent 500. A client sent its next request after receiving and consuming the previous response. Latency includes the complete response body. RPS is completed requests divided by elapsed wall time. `p95` is the 95th percentile of those request latencies. A response with the wrong status was counted as an error. The runner sent `Accept-Encoding: identity`; POST requests used a fresh connection because uWSGI closed some reused connections during an initial trial. Both servers used this same POST policy in the reported results.

Cases used these requests:

| Case | Request | Expected result on the initial fixture |
| --- | --- | --- |
| List | `GET /api/bookmarks/?limit=100` | 9,000 active bookmarks; first page has 100 |
| Search | `GET /api/bookmarks/?q=systems&limit=100` | 2,000 matches; first page has 100 |
| Compound | `GET /api/bookmarks/?q=systems%20and%20%23tag-008&limit=100` | 100 matches |
| UI | `GET /bookmarks` with a logged-in session | 30 distinct bookmark URLs on the page |
| Create | `POST /api/bookmarks/?disable_scraping=1&disable_html_snapshot=1` | Unique bookmark URL, HTTP 201 |

The API read response sizes matched between servers: 53,357 bytes for list, 53,398 for search, and 53,441 for compound search. UI HTML differed in size (Python 107,589 bytes; Go 56,554 bytes), so UI throughput compares each server's own rendering rather than byte-identical payloads. All listed read and UI requests returned HTTP 200; all listed create requests returned HTTP 201. Search counts were checked before measurement. Writes ran last on each database copy. The SQLite write phase started after the same 200 warmup creations on each server and ended at 13,200 bookmarks; PostgreSQL started after 20 smoke creations and ended at 13,020. Metadata scraping and snapshots were disabled for create requests.

## HTTP results

`Go/Python` is the throughput ratio. A value above 1 means the Go port completed more requests per second in this run. Numbers are rounded from the [raw results](2026-09-26-results.json). The Go SQLite create rows use `LD_DB_OPTIONS={"transaction_mode":"immediate"}`; all other SQLite rows use the default setting.

### SQLite

| Case | Clients | Python RPS | Go RPS | Go/Python | Python p95, ms | Go p95, ms |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| List | 1 | 98 | 110 | 1.13× | 12 | 12 |
| List | 8 | 177 | 235 | 1.33× | 65 | 42 |
| List | 32 | 167 | 241 | 1.44× | 219 | 154 |
| Search | 1 | 34 | 38 | 1.11× | 32 | 30 |
| Search | 8 | 66 | 55 | 0.84× | 164 | 189 |
| Search | 32 | 65 | 57 | 0.87× | 508 | 673 |
| Compound | 1 | 33 | 32 | 0.98× | 34 | 35 |
| Compound | 8 | 59 | 41 | 0.70× | 172 | 247 |
| Compound | 32 | 59 | 43 | 0.72× | 585 | 906 |
| UI | 1 | 49 | 58 | 1.17× | 22 | 20 |
| UI | 8 | 90 | 71 | 0.79× | 104 | 150 |
| UI | 32 | 90 | 71 | 0.79× | 382 | 547 |
| Create | 1 | 190 | 300 | 1.58× | 6 | 4 |
| Create | 8 | 347 | 307 | 0.89× | 40 | 42 |
| Create | 32 | 338 | 275 | 0.81× | 112 | 208 |

With the Go port's default SQLite transaction mode, a separate 1,000-request create run at eight clients produced **336 HTTP 201 and 664 HTTP 500**. A 100-request repeat produced 35 HTTP 201 and 65 HTTP 500. Failed requests did not create bookmarks. The `IMMEDIATE` setting completed 1,000 create requests at each of 1, 8, and 32 clients with no errors. This is a configuration-sensitive correctness issue for concurrent SQLite writes; the high RPS of the failed run is not useful throughput.

### PostgreSQL

| Case | Clients | Python RPS | Go RPS | Go/Python | Python p95, ms | Go p95, ms |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| List | 1 | 64 | 142 | 2.23× | 19 | 11 |
| List | 8 | 158 | 249 | 1.57× | 67 | 50 |
| List | 32 | 157 | 164 | 1.04× | 228 | 267 |
| Search | 1 | 38 | 41 | 1.07× | 30 | 31 |
| Search | 8 | 112 | 72 | 0.64× | 85 | 170 |
| Search | 32 | 113 | 41 | 0.36× | 306 | 1098 |
| Compound | 1 | 65 | 150 | 2.32× | 19 | 11 |
| Compound | 8 | 161 | 244 | 1.52× | 62 | 49 |
| Compound | 32 | 157 | 168 | 1.07× | 226 | 268 |
| UI | 1 | 41 | 107 | 2.62× | 28 | 13 |
| UI | 8 | 113 | 206 | 1.83× | 80 | 74 |
| UI | 32 | 114 | 164 | 1.44× | 297 | 286 |
| Create | 1 | 98 | 439 | 4.46× | 12 | 3 |
| Create | 8 | 301 | 1143 | 3.79× | 30 | 29 |
| Create | 32 | 291 | 580 | 1.99× | 122 | 103 |

The poor PostgreSQL simple-search result at 32 clients persisted after writes, with matched 13,020-row databases: Python reached 98 RPS, p95 350 ms; Go reached 31 RPS, p95 1,405 ms. Both returned 1,000 successful responses. A plausible contributor is the Go search path fetching the 100 returned bookmarks individually with their tags after selecting IDs, while Python prefetches tags for the page. This needs profiling before attributing the full gap to that query pattern.

## Background metadata jobs

On SQLite copies of the fixture, each server processed 100 queued metadata-refresh jobs for the same bookmark IDs. Each URL pointed to a local mock site with a 50 ms response delay. All 100 jobs completed and updated titles. Measured from the first mock request to the last mock response, Python took **3.38 s** with at most **2** concurrent mock requests; Go took **6.06 s** with at most **1**. These timings exclude application startup and therefore are not total queue-drain times. They show the concurrency of the default background-worker configurations in this workload.

## How to reproduce

Use disposable volumes and databases. Seed Python linkding v1.47.0 by running `python manage.py shell` with [seed_linkding.py](../../scripts/perf/seed_linkding.py) on stdin inside its container. The script writes a token to `data/benchmark-token`. Stop Python before copying its SQLite data directory; for PostgreSQL, seed a separate source database. Prepare the Go database with the [migration procedure](../migration.md). Start only one application server at a time with the container limits above. For UI tests, create its session cookie with [login.py](../../scripts/perf/login.py).

From this repository, with a copy of the disposable token at `TOKEN_FILE`:

```sh
go run ./scripts/perf/load.go \
  -base-url http://127.0.0.1:9090 \
  -token-file "$TOKEN_FILE" \
  -case search -concurrency 8 -requests 1000 -expected-count 2000
```

Run the same command against each server and vary `-case`, `-concurrency`, and `-requests` as described above. Use `-cookie-file` for `ui`; use a different `-run-id` for each `create` run and a fresh matching database copy for each server. The background-job setup uses [prepare_python_jobs.py](../../scripts/perf/prepare_python_jobs.py), [prepare_jobs](../../scripts/perf/prepare_jobs/main.go), and [mock_site.py](../../scripts/perf/mock_site.py). The [JSON results](2026-09-26-results.json) retain unrounded throughput, p50/p95/p99, response sizes, status counts, and error counts for every run.

## Interpretation and limits

This was one measured pass per HTTP point on a local Docker host, with no independent replication or multi-minute steady-state run. Each workload was closed-loop at a fixed number of clients; arrival rate, think time, request mix, and tail latency under a production traffic pattern were not measured. The table therefore cannot establish a safe maximum request rate, worker-equivalence factor, or production capacity threshold. It does show where the current Go port scales better or worse on the same fixture and where concurrent SQLite writes fail under the default setting. A follow-up capacity claim needs repeated runs, a defined latency and error target, realistic mixed traffic, and the intended deployment hardware.

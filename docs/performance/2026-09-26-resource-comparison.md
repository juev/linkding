# CPU and memory at equal request rates

Python linkding v1.47.0 and the optimized Go port both completed the same five workloads at 50 requests per second on a 10,000-bookmark SQLite fixture. At that rate, Go used 0.18–0.64 CPU cores for the four API workloads, versus 0.43–1.63 for Python. Median anonymous memory during those workloads was 13.5–24.3 MiB for Go and 119.6–157.6 MiB for Python. These are measurements of the complete server containers under this test, not minimum deployment requirements or a language-only comparison.

The [raw results](2026-09-26-resource-results.json) contain 30 load runs in 15 Python/Go pairs, three additional Python create runs after `ANALYZE`, six idle runs, per-sample cgroup counters, HTTP results, and database checks. The [optimization summary](2026-09-26-optimization-summary.md) covers the throughput changes that preceded this resource comparison.

## Method

- The host was an Apple Mac16,8 with 12 CPUs and 24 GiB RAM. OrbStack ran Docker 29.4.0 in a Linux/arm64 VM with 12 CPUs and 12 GiB RAM. The servers ran **sequentially**; each application container had a 2-CPU quota and a 2 GiB memory limit. Other containers on the host were left running.
- Python used `sissbruecker/linkding:1.47.0`. Go used a locally built basic image from commit `a964db5`, the application code planned for v1.47.3. Both used their image defaults, with `LD_DISABLE_BACKGROUND_TASKS=True`. The Python container had four uWSGI processes in an observed `docker top`; the Go container had one server process. No external database container was involved.
- Each run started from a fresh copy of the same logical fixture: one user, 10,000 bookmarks, 100 tags, 20,000 bookmark-tag links, and 10% archived bookmarks. The Go copy came from the [migration procedure](../migration.md). The main Python copy had no `sqlite_stat1` rows. The [fixture seed](../../scripts/perf/seed_linkding.py) and [first comparison](2026-09-26-linkding-comparison.md#test-setup) define the request data. API list, search, and compound responses had identical byte counts across servers; UI HTML was 107,589 bytes on Python and 56,554 on Go.
- The [load runner](../../scripts/perf/load.go) scheduled 50 request starts per second with at most 32 clients. Each point had 100 warmup requests, except create with 200, followed by 1,000 measured requests. List, search, compound, create, and authenticated UI each ran three times per server, alternating Python-first and Go-first pairs. Create used distinct URLs and disabled scraping and snapshots. The 95th-percentile scheduling delay stayed below 5 ms in every run, and completed throughput stayed within 49–51 RPS.
- The [container sampler](../../scripts/perf/measure_container.py) read the Docker Engine's cgroup counters every 250 ms. CPU is the change in `cpu_stats.cpu_usage.total_usage`; average cores divide CPU seconds by measurement time. Anonymous memory is `memory_stats.stats.anon`. Working set is `memory_stats.usage - memory_stats.stats.inactive_file`, so it also includes some file cache and kernel memory. Tables show the median of three run-level values. Sampled peaks can miss shorter spikes.

## CPU and latency

Every measured read and UI request returned HTTP 200; every create returned HTTP 201. Each run completed 1,000 requests without load-runner errors. The database ended with 10,000 bookmarks for reads and UI, or 11,200 after create warmup and measurement; every `PRAGMA integrity_check` returned `ok`.

| Workload at 50 RPS | Average CPU cores, Python / Go | CPU core-seconds per 1,000 requests, Python / Go | HTTP p95 ms, Python / Go |
| --- | ---: | ---: | ---: |
| List | 0.50 / 0.34 | 9.93 / 6.86 | 13.0 / 8.7 |
| Search | 1.51 / 0.59 | 30.22 / 11.78 | 34.5 / 16.9 |
| Compound search | 1.63 / 0.64 | 32.71 / 12.74 | 38.1 / 16.9 |
| Create | 0.43 / 0.18 | 8.53 / 3.50 | 12.2 / 7.1 |
| UI | 0.99 / 0.44 | 19.96 / 8.79 | 28.0 / 11.7 |

At this fixed rate, the Go container consumed about 31% less CPU for list and 56–61% less for the other workloads. The [raw runs](2026-09-26-resource-results.json) retain the spread: for search, Python used 29.89–30.32 core-seconds per 1,000 requests and Go used 10.62–12.88. The UI comparison includes the different HTML response sizes reported above.

## Memory

| Workload | Anonymous MiB, Python / Go | Working-set MiB, Python / Go | Sampled working-set peak MiB, Python / Go |
| --- | ---: | ---: | ---: |
| List | 147.2 / 18.2 | 171.7 / 19.8 | 172.9 / 32.9 |
| Search | 151.5 / 22.7 | 164.8 / 37.0 | 167.7 / 38.8 |
| Compound search | 157.6 / 24.3 | 169.6 / 31.7 | 172.5 / 40.4 |
| Create | 119.6 / 13.5 | 138.8 / 37.6 | 139.7 / 39.8 |
| UI | 150.3 / 18.2 | 176.9 / 44.7 | 177.5 / 45.7 |

In separate 10-second idle intervals after startup, median anonymous memory was 68.2 MiB for Python and 9.3 MiB for Go. Both consumed less than 0.001 CPU core on average in those short intervals. Working-set memory varied more than anonymous memory between fresh containers because the cgroup attribution of file-cache pages varied: for list, the three working-set medians ranged from 147.7 to 187.1 MiB on Python and from 18.7 to 34.1 MiB on Go. The anonymous-memory figures were steadier.

## Python `ANALYZE` check and limits

An additional three Python create runs used the same bookmark data after SQLite `ANALYZE`. They consumed a median 8.67 CPU core-seconds per 1,000 requests at 50 RPS, compared with 8.53 on the plain fixture. This fixed-rate test did not establish a CPU saving from `ANALYZE`. The [earlier saturated create comparison](2026-09-26-query-algorithm-analysis.md#the-original-python-query-benefits-too) did find a throughput gain for Python after `ANALYZE`; the two experiments answer different questions.

These 20-second load windows measure one fixture, SQLite, the basic Go image, and one arrival rate. They exclude background jobs, snapshot processing, PostgreSQL, and the resource use of the external load generator. Other host containers could affect latency. The working-set values are observed samples, not a safe memory limit; sizing a deployment needs its own traffic mix, duration, and latency target.

To repeat one point after preparing separate [fixture copies](2026-09-26-linkding-comparison.md#how-to-reproduce) and starting one container with `--cpus 2 --memory 2g`:

```sh
go build -o /tmp/linkding-perf-load ./scripts/perf/load.go
python3 scripts/perf/measure_container.py --container linkding-test -- \
  /tmp/linkding-perf-load -base-url http://127.0.0.1:9090 \
  -token-file /path/to/fixture/benchmark-token \
  -case search -expected-count 2000 -rate 50 -concurrency 32 -requests 1000
```

Run the 100-request warmup before the measured command, use a new fixture copy for each repeat, and vary `-case` and `-expected-count` as above. The UI case also requires a cookie from [login.py](../../scripts/perf/login.py); create requires a unique `-run-id` and 200 warmup requests. The sampler returns both HTTP and cgroup metrics as JSON.

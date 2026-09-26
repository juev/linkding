# Performance optimization results

This page collects the measured changes to the Go port through the code planned for v1.47.3. Each before/after pair comes from one experiment on the 10,000-bookmark SQLite fixture. The linked reports retain the individual runs, latency distributions, HTTP statuses, and test conditions. The numbers below are achieved throughput under closed-loop load, not a capacity guarantee.

| Change | Comparable before → after | Result |
| --- | --- | --- |
| Batch bookmark and tag loading, search expressions, and worker changes delivered in v1.47.2 | Go SQLite list at 32 clients: 244.1 → 349.3 RPS; search: 56.8 → 107.0; compound search: 41.8 → 67.2; UI: 68.5 → 231.2 | More reads complete per second. The old build predates the default SQLite `IMMEDIATE` transaction fix, so these pairs cover reads only. [Measurements](2026-09-26-optimization-followup.md). |
| Use the normalized-URL index for SQLite duplicate lookup after v1.47.2 | Go create at 8 clients: 314.5 → 1462.0 RPS; at 32 clients: 320.2 → 1303.1 | Removes a scan of the owner's bookmarks on a URL miss. All creates returned HTTP 201 and the database checks passed. [Paired measurements](2026-09-26-sqlite-post-release.md). |
| Replace the correlated SQLite tag search with a tag-ID set | Go compound search at 8 clients: 64.5 → 86.3 RPS; at 32 clients: 65.3 → 87.2 | Avoids repeating the tag join for each candidate bookmark. [Paired measurements](2026-09-26-sqlite-post-release.md). |
| Combine four SQLite Unicode substring calls per search term | Go ordinary search at 8 clients: 103.6 → 134.4 RPS; at 32 clients: 105.6 → 149.1. Compound search: 81.1 → 92.3 and 82.3 → 94.9 | Repository-search allocations fell from 215,550 to 147,538 per request; the HTTP improvements survived three paired repeats. [Algorithm analysis](2026-09-26-query-algorithm-analysis.md). |

The pairs belong to different experiments and must not be multiplied into a single speedup. The v1.47.2 read changes also include PostgreSQL search and connection-pool work, plus two concurrent ordinary-job workers and a separate serial snapshot worker; their [report](2026-09-26-optimization-followup.md) covers PostgreSQL and job timings. SQLite concurrent-create HTTP 500s were eliminated by the default `IMMEDIATE` transaction mode before v1.47.2; the [first comparison](2026-09-26-linkding-comparison.md) records the failure and its reproduction.

The Python server benefits from SQLite query planning as well. Running `ANALYZE` on its fixture raised create throughput from 347.6 to 412.0 RPS at 8 clients and from 349.6 to 397.1 at 32 clients. The remaining difference between complete servers cannot be assigned to the implementation language alone. At 32 clients, Go compound-search tail latency remained above Python in the last closed-loop comparison. See the [query and algorithm analysis](2026-09-26-query-algorithm-analysis.md) for these limits.

The [resource comparison](2026-09-26-resource-comparison.md) measures CPU time and memory for Python and the final Go build at equal request arrival rates. Its fixed-rate results answer a different question from the closed-loop throughput experiments above.

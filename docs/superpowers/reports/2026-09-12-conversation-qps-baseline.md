# Conversation QPS local baseline — 2026-09-12

All 12 fixed-load cases passed on the local development host. This is a
regression-floor validation, not maximum capacity, a released-build result,
or a GitHub-hosted runner certification. No publication Workflow was invoked.

## Measurement identity

- Host: Apple M4, 10 logical CPUs, 32 GiB RAM, macOS arm64.
- Server processes: real single-node and three-node clusters on the same host;
  256 hash slots, 12 physical slots, one/three Channel replicas respectively,
  and `GOMAXPROCS=2` per process.
- Source base: `13f9687192c316d3c6fa967c0f8ae593d60005e5`; `source_dirty=true` includes the pending
  persisted-read implementation and gate. This receipt cannot authorize release.
- Server binary SHA-256: `0b9c088b95d297931de32bad050d71e4dbec3a7dc802d6707fb97984c09eb6f8`.
- Profile SHA-256: `82bfcfa49af20186c0d1875716f14ac1c7ba2740de3e5f77197eebb15589649a`.
- UTC start: `2026-09-12T06:42:54.075243Z`.
- Raw evidence: [2026-09-12-conversation-qps-baseline.json](2026-09-12-conversation-qps-baseline.json).

## Workload and results

Each topology prepares 600 groups in three cohorts, 24 reader UIDs, and three
256-byte messages per Channel through public APIs. Each UID owns 200 groups.
Preparation has a five-minute deadline and is excluded from measurement.
The existing authenticated benchmark eviction API unloads only the generated
fixture channels on every node after writes finish, retaining all busy-runtime
safety guards. Setup may wait up to one minute for busy work to settle; HTTP
failures fail immediately. No processes restart and no eviction occurs during
reads. Every measured phase requires zero active runtimes across all roles
before and after, zero additional loads, and zero membership writes. Storage
and OS caches are explicitly warmed by 24 untimed requests per case. These
are cold-runtime reads, not physical cold-disk reads. No sends run during measurement.

Each case schedules fixed arrivals for 15 seconds, using eight workers and an
eight-entry queue. Latency includes queue/scheduling delay and full response
validation. Every Channel and latest/recent message identity is checked.
Measured requests never retry. Completions after the fixed window do not add
to achieved QPS. Reject any failed/dropped request, incomplete response, Channel
runtime load or membership write; require QPS at least 95% of offered load and
P99 at most 500 ms. These are initial source-controlled regression floors;
there was no supplied production QPS/SLO target and no automatic threshold tuning.

| Nodes | Endpoint | Page size | Offered QPS | Achieved QPS | P50 ms | P95 ms | P99 ms | Errors / drops | Loads / writes |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | `/conversation/list` | 25 | 100 | 100.00 | 3.82 | 4.66 | 5.86 | 0 / 0 | 0 / 0 |
| 1 | `/conversation/list` | 100 | 50 | 50.00 | 6.95 | 8.17 | 12.75 | 0 / 0 | 0 / 0 |
| 1 | `/conversation/list` | 200 | 25 | 25.00 | 11.21 | 14.38 | 19.55 | 0 / 0 | 0 / 0 |
| 1 | `/conversation/sync` | 25 | 40 | 40.00 | 8.59 | 11.91 | 18.39 | 0 / 0 | 0 / 0 |
| 1 | `/conversation/sync` | 100 | 30 | 30.00 | 10.77 | 14.86 | 20.93 | 0 / 0 | 0 / 0 |
| 1 | `/conversation/sync` | 200 | 20 | 20.00 | 15.43 | 23.46 | 27.39 | 0 / 0 | 0 / 0 |
| 3 | `/conversation/list` | 25 | 100 | 100.00 | 4.77 | 5.58 | 14.60 | 0 / 0 | 0 / 0 |
| 3 | `/conversation/list` | 100 | 50 | 50.00 | 8.05 | 9.25 | 18.74 | 0 / 0 | 0 / 0 |
| 3 | `/conversation/list` | 200 | 25 | 25.00 | 11.57 | 19.85 | 25.80 | 0 / 0 | 0 / 0 |
| 3 | `/conversation/sync` | 25 | 40 | 40.00 | 12.43 | 21.51 | 27.36 | 0 / 0 | 0 / 0 |
| 3 | `/conversation/sync` | 100 | 30 | 30.00 | 17.97 | 26.16 | 32.00 | 0 / 0 | 0 / 0 |
| 3 | `/conversation/sync` | 200 | 20 | 20.00 | 26.41 | 36.72 | 43.91 | 0 / 0 | 0 / 0 |

CPU seconds, heap bytes and allocation deltas are in the raw receipt. macOS
cannot provide the Linux process collector's CPU counter, so local CPU is
`null`, not zero. Linux release runs require that counter on every node.
All runtime-load and membership-write counters must be present on every OS.

## Publication gate

Both Docker and binary publishers depend on the read-only reusable
`conversation-qps-gate.yml` for the requested tag. The gate builds the tagged
server on `ubuntu-24.04` and requires a clean source tree, correct source,
profile and binary identities, and all 12 unique passing cases. Publishers
compare their own checkout with the gate's source SHA before release work.
A missing/skipped test or incomplete receipt fails closed. Success/failure
reports and logs remain available as Actions artifacts for 90 days. There is
no threshold override or bypass input. The existing signed APT/RPM publication
and exact-version public client verification requirements remain in place.

The Workflow definitions are implemented and statically validated locally;
GitHub-hosted execution still needs verification after the change is merged.

## Validation and limitations

- Relevant metric and acceptance-policy unit tests passed.
- Workflow contracts passed for dependency ordering, reduced permissions,
  source binding and complete evidence; the actual jq filter rejected missing
  or duplicate cases, wrong source, dirty source, missing CPU, latency failures
  and request errors even when a forged receipt claimed success.
- Actionlint, `go-format`, `flow-doc-contracts`, and `git diff --check` passed.
- The full scripts suite has an existing unrelated failure:
  `TestOfficialServerUsesManagedGoroutines` reports the unmanaged `.Go` call in
  `internal/infra/cluster/plugin_cluster.go:126`; reproduced in the unchanged
  main working copy as well. Focused release/gate contracts passed.
- Exploratory setup runs found that three-node restart/recovery can leave
  hundreds of resident runtimes before any conversation reads. This observation
  is not attributed to either endpoint. The final fixture uses the existing
  safe benchmark eviction API to construct explicit cold state and excludes
  process recovery from the QPS workload, keeping the stricter all-role zero
  residency and zero-new-load checks without a recovery exception.
- The generator's worker-start scheduling drop was fixed with a bounded queue;
  missing zero-valued membership metrics and the unavailable macOS CPU counter
  are handled explicitly. The original fixed workload and QPS/P99 floors were
  not relaxed to make a measured failure pass.
- The preceding sync implementation's broad docs tests retain the pre-existing
  Manager `/manager/login` catalog mismatch; focused sync documentation tests
  and OpenAPI generation checks passed.
- This bounded group-read matrix does not establish 100,000-member fanout,
  long-duration leak behavior, production networking or maximum endpoint QPS.

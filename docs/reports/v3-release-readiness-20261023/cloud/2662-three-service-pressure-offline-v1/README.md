# Existing service-pressure snapshots (read-only)

The snapshots directly establish nonzero **sampled accepted-work queue wait** for services 13/14/15 between first-measured and terminal. Service 13 on nodes 2/3 has substantially more accumulated sampled wait than service 14. They do **not** prove that owner READ queueing caused the trace's waits, the quota waits, or a historical failure.

| Node / service | wait Δcount = Δ+Inf | sampled wait Δsum s | task Δcount = Δ+Inf | sampled task Δsum s | admission ok Δ |
|---|---:|---:|---:|---:|---:|
| 1 / 13 | 273 | 0.500743040 | 274 | 46.650135967 | 8720 |
| 1 / 14 | 112 | 0.148816708 | 112 | 0.020125792 | 3589 |
| 1 / 15 | 106 | 0.361975252 | 106 | 0.002720341 | 3371 |
| 2 / 13 | 296 | 100.255955296 | 296 | 72.501053085 | 9465 |
| 2 / 14 | 107 | 0.174570833 | 107 | 0.010367292 | 3436 |
| 2 / 15 | 102 | 0.169902460 | 102 | 0.002080585 | 3268 |
| 3 / 13 | 276 | 36.606013284 | 276 | 55.554210216 | 8824 |
| 3 / 14 | 111 | 0.189664040 | 111 | 0.005971839 | 3540 |
| 3 / 15 | 106 | 0.305491837 | 106 | 0.007521416 | 3393 |

All 63 selected counter/histogram series were present in both snapshots, unchanged in full label identity and monotone; every histogram count equals its +Inf bucket at each endpoint and in the delta. No selected-series reset/new/missing case occurred. The 36 gauge rows are endpoint observations only. Workers=128 and queue capacity=4096 for all nine service/node combinations. Queue depth=0 at both endpoints. Service13 inflight first→terminal is node1 5→0, node2 17→0, node3 7→0; service14/15 is 0→0. These endpoint maxima are not interval peaks. Observer-dropped counter is 0→0 on each node. Only admission result=ok series were present; absent error outcome series are **unknown**, not an independently observed zero.

Source semantics: `pkg/cluster/net/ids.go:11–40,200–205` assigns 13=presence authority, 14=presence owner action, 15=delivery push. Service14 pools all owner operations; it cannot isolate READ. `pkg/transport/server.go:55` installs ObserverDrain. `pkg/transport/internal/core/observer_aggregate.go:6,25–27,93–100` samples durations one in 32; count batches for admission remain exact. `internal/app/observability.go:1100–1118` observes sampled durations and exact admission counts. `pkg/transport/internal/rpc/service.go:353` measures enqueue-to-handler-start wait after admission/executor scheduling, including queue/scheduling time; `:546–580` measures task wall time, not CPU time. `pkg/metrics/runtime_pressure.go:94–142,301–312` defines metrics/labels and units. Samples are not multiplied by 32 here.

The first and terminal snapshots are sequential per-node HTTP collections, not simultaneous samples. The parent task described them as approximately +5s/+35s; this audit does not independently infer exact per-scrape timestamps. The supervisor captures first after the measured barrier and terminal after coordinator exit, including user-disconnect cleanup effects. They are not the trace's approximately +4..14s window. No RPC/request join, exact percentile, causal attribution, total-unsampled latency, or peak-pressure claim follows.

Each of the six fixed files was read once (under 2 MiB), matched its previously sealed capture SHA/size, parsed using a closed metric/label allowlist, hashed again in memory, and checked for unchanged source stat before/after. Raw text and node_name were not copied. Additional CPU/RSS/physical-memory/PSI gauges were requested after those buffers had been released; they were **not extracted**, to preserve the one-read budget. No headroom/OOM conclusion is made.

No tests, builds, runtime, DB, cloud or network calls were performed. Source-only Git blob reads verified all cited source/rule digests. Read-only discovery encountered missing optional FLOW/one guessed path; this did not affect the six captures or produce an analysis retry.

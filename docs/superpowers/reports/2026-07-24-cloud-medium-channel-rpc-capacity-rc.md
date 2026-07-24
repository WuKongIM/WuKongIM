# Cloud Medium Channel RPC Capacity Release Candidate

Date: 2026-07-24

Base revision: `dca1a1d7e135103d77fb584a7c04a42659d4b8af`

Failed cloud run: `gh-30047907477-1`

## Decision

**LOCAL GO for one cohesive PR.**

**NO-GO for paid Alibaba Cloud Provision until the PR is merged, exact-main
CI and Nightly are green, and a fresh inventory check proves that no live
resources exist.**

The candidate directly changes the bounded capacity that saturated in the
failed cloud run. It does not add unbounded goroutines. Local evidence proves
the configured capacity, deterministic blocking-RPC gain, three-process
correctness at 5,000 messages/s, and healthy queue conservation. Absolute
single-host tail latency remains variable, so it is recorded as supporting
evidence rather than presented as a cloud-equivalent latency measurement.

## Released Cloud Evidence

Cleanup workflow `30052648453` succeeded. Independent Analysis workflow
`30052790447` proved `provider.state=released` and `resources.length=0`.

The measured cloud window completed 6,923,659 successful sends and all 10,500
connects, but failed the Medium acceptance boundary:

- actual ingress: 3,846.48/s versus 4,500/s;
- SENDACK P99: 4.369s versus 1s;
- RECV P99: 78.383ms;
- SENDACK errors: zero;
- all seven targets and all three WuKongIM processes remained continuous;
- simulator CPU and memory peaked near 57% and 22.8%;
- no restart or OOM occurred.

Recipient queues, Channel append handoff, Presence routing, ACK binding, Slot
apply, and PreferredLeader reconciliation were healthy. The Channel
replication RPC boundary was not:

- `channelv2-rpc` queue pressure reached 100% on nodes 1 and 3 and 99.61% on
  node 2;
- the effective Medium contract fixed the pool at 50 workers;
- the actual logical Slot leader distribution was 3/4/3;
- Gateway backlog peaked at 1,148 and later drained.

This is a product-capacity defect, not simulator or infrastructure saturation.

## Bounded Capacity Derivation

The candidate keeps generic Small and Large defaults at 50 workers and changes
only the sealed Cloud Medium profile.

Using the completed cloud measurement:

`ceil(50 workers * 4500 / 3846.48 * 1.50 headroom) = 88 workers`

The fixed Medium value is 96 workers. This is:

- above the measured capacity requirement with 50% headroom;
- bounded and explicit in the immutable runtime contract;
- below the existing 128-worker promoted transport service pool;
- validated by rendered-config, normalized-config, bundle, Bootstrap Gate, and
  scale-isolation tests.

The physical/logical topology remains 256 physical hash slots, 10 logical Slot
Raft groups, and three replicas.

## Deterministic Blocking-RPC Benchmark

Apple M4, three samples, 4,000 Pull tasks, batch size eight, 5ms blocking
transport time:

| Signal | 50 workers | 96 workers | Result |
| --- | ---: | ---: | ---: |
| Throughput | 65,464-75,726 items/s | 128,137-130,611 items/s | 69-99% higher |
| Queue-wait P99 | 44.455-50.600ms | 23.544-24.262ms | 46-53% lower |
| RPC calls/op | 500.3-500.7 | 500.0-502.7 | unchanged batching |
| Bytes/op | 12.01-12.11MB | 11.81-11.82MB | about 1.6-2.4% lower |
| Allocs/op | 34,180-35,396 | 31,752-31,792 | about 7-10% lower |

The benchmark isolates the exact bounded worker effect. It does not claim that
localhost transport has the same service time as Alibaba Cloud networking.

## Three-Process Evidence And Observer Effect

The high-fidelity gate runs three real `cmd/wukongim` processes, WKProto TCP
sockets, Raft, Pebble, 256/10/3 topology, and actual 3/4/3 Slot leadership.
It was extended to report the exact `channelv2-rpc` queue and worker ratios and
to separate offered load from the minimum accepted ingress. Acceptance fails
unless one sampling cycle contains the exact queue-capacity and worker gauges
from all three nodes, and unless the historical minimum and maximum worker
gauges both equal 96; missing or drifted metrics cannot masquerade as 0%
pressure.

At 5,000/s with bounded one-second metric sampling:

- actual ingress: 5,000.19/s;
- SENDACK P99: 1,084.58ms;
- RECV P99: 2,108.40ms;
- maximum Channel RPC queue ratio: 4.49%;
- maximum Channel RPC worker ratio: 9.38%;
- maximum Gateway queue ratio: 0.44%;
- processes continuous and queues drained;
- metric sample errors: zero.

This run failed the absolute latency thresholds, but it proved that 96 Channel
RPC workers were not the local bottleneck.

The original 250ms full-registry sampling inflated the same single-host path:
with sampling, SENDACK/RECV P99 were 920.65/2,064.69ms; without sampling they
were 816.42/1,604.52ms. The gate now samples once per second to reduce this
observer effect while retaining pressure evidence.

Three additional no-sampler repetitions at 5,000/s produced:

| Run | Actual ingress | SENDACK P99 | RECV P99 |
| --- | ---: | ---: | ---: |
| 1 | 4,999.97/s | 906.92ms | 2,190.68ms |
| 2 | 4,999.97/s | 847.57ms | 1,947.40ms |
| 3 | 5,000.17/s | 1,220.54ms | 2,311.79ms |

The median SENDACK result passes at 11.1% more offered load than the cloud
target, but the worst local run does not. The variation and the low measured
RPC occupancy prevent using these absolute tails as proof of the cloud result.
The cloud-specific GO argument is instead the exact saturated-boundary
diagnosis plus bounded 50%-headroom derivation and deterministic RPC gain.

The final 500/s CI-scale three-process gate passed with 3/3 Channel RPC metric
nodes, min/max workers 96/96, 500.02/s ingress, 505.50ms SENDACK P99, 399.16ms
RECV P99, 0.20% maximum RPC queue pressure, 1.04% maximum RPC worker pressure,
zero metric errors, continuous processes, and fully drained queues.

## Verification

- Focused worker batching and target-skew tests: passed three repetitions.
- Focused runtime-contract and Bootstrap Gate tests: passed three repetitions.
- Focused race tests for worker, deploy, and E2E acceptance paths: passed.
- Deterministic benchmark: passed three samples per worker count.
- Missing Channel RPC metrics and worker-count drift negative tests: passed.
- CI-scale three-process sealed-metric acceptance: passed.
- User `.gitignore` change remains outside this candidate.

The explicit-root repository Go gate, independent review, PR delivery,
exact-main CI/Nightly, and final no-live-resource proof remain mandatory
before changing the paid-cloud decision to GO.

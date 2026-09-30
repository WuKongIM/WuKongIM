# MQTT unsubscribe sealing contention

Candidate: `fd9ba0012da0c9575c5f87f079544601d3482216`.
Baseline: `c3f4ac4aa04d252cf5a8e81fb350549602d49f8d`.

The uninstrumented candidate passes two sequential 2,000-member runs and one
original-size 100,000-member acceptance. All retain 500 persistent clients,
twenty initial messages, 200 churners and three rounds. Each confirms the fresh
post-churn message, zero duplicate/missing/reordered/wrong-identity/unexpected
receipts and all 600 retirements: 1,800 cycles/retirements across the three runs.

| Ordinary run | Members | Fanout | Churn | Retired | Idle barriers/client/s | Test time |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Repeat 1 | 2,000 | 172.219s | 19.421s | 600 | 3.632 | 282.96s |
| Repeat 2 | 2,000 | 142.589s | 19.242s | 600 | 3.887 | 221.33s |
| Original size | 100,000 | 165.206s | 20.126s | 600 | 4.086 | 374.77s |

Both smaller runs additionally observe all 34 public subscription closure series
at zero near/after churn completion. The full run's control replies and final
fresh receipt per persistent client pass; no separate public closure snapshot
was captured. The original full deadlines, attempts, assertions and worker
counts are unchanged. All three runs have profiling disabled and no runtime
overlay or concurrent task-owned tests/builds. The shared host is not controlled,
and this is neither a percentage speedup nor a broader crash/restore qualification.
[Acceptance provenance](acceptance-provenance.json), [full artifact](candidate-full.json).

A bounded diagnostic process run reproduces one unsubscribe closure after all
500 clients receive twenty publications. It fails zero-based churn round 1 with
public `unsubscribe/conflict=1`. Its fixed error-boundary chain starts at source
sealing CAS rejection and reaches foreground subscription completion. The
winning source writer is not identified; a later winner-shape probe fails before
churn at the original receipt deadline. Both failures are retained separately.

The deterministic test exercises real subscription/group projection/drain and
storage with a competing SourceProgress or closed-drain commit. The baseline
fails four valid contention cases, fresh-read error propagation and cancellation.
The eighteen-case matrix passes with race after repair. Unknown effects and
changed/regressed evidence retain failures; no inline source write retry is added.

Related race checks, real single-node cluster Paho entry/delivery and three-node
source preparation/drain/pending-removal composition, vet and the named
`flow-doc-contracts` check pass. The final exact-candidate three-node check is
recorded separately in `final-three-node.txt`.

## Evidence

- `candidate-{scale-1,scale-2,full}.json` and `*-phases.txt`: each ordinary run.
- `candidate-scale-{1,2}-closures.json`: bounded public closure snapshots.
- `candidate-build.json`, `acceptance-provenance.json`: exact committed source,
  binary/source/fixture hashes, commands, observed sizes and measurement limits.

- `baseline-seal-conflicts.txt`: fixed, body/identity-free error-boundary records.
- `diagnostic-provenance.json` and `frozen-context.json`: source, bounded recipe,
  hashes, hypotheses' distinguishing result and measurement limitations.
- `baseline-inflight.json`, `baseline-scale-2000-pass.json`: preceding passing
  diagnostic attempts. A pass does not erase the intermittent failure.
- `baseline-scale-10round-{failure,receipts}.json`: churn failure and independent
  post-failure receipt/closed-client snapshot; the snapshot is not exact deadline time.
- `winner-probe-fanout-{failure,receipts}.json`: separate initial receipt failure.
- `contention-matrix-red.txt`, `contention-matrix-green.txt`, `final-race.txt`,
  `related-race.txt`, `app-integration.txt`, `flow-contracts.txt`: validation.

Diagnostic recipe: save `diagnostic-generator.txt` as `generate.go` and
`diagnostic-probe.txt` as `debug_unsubscribe_probe.go` in
`/tmp/mqtt-unsubscribe-diagnostic`, run the generator from the frozen source,
then build `./cmd/wukongim` with its generated `-overlay .../overlay.json`.
The provenance supplies the exact process test arguments. Diagnostic logs are
capped at 512 fixed records; the foreground marker is explicitly copied through
nested Owner-derived contexts. No debug code belongs to the production package.

The full acceptance keeps 100,000 members, 500 clients, twenty initial messages,
200 churners, three rounds, the original receipt/request deadlines and all
identity/order/retirement assertions. Extended churn only increases diagnostic
reproduction opportunities and grants no larger capacity claim.

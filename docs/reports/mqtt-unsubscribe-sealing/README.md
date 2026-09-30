# MQTT unsubscribe sealing contention

Baseline: `c3f4ac4aa04d252cf5a8e81fb350549602d49f8d`.

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
`flow-doc-contracts` check pass. Ordinary candidate acceptance follows separately.

## Evidence

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

# MQTT compound original-read validation

At `838222fcfc4f4a94ed957056c6802dcf9b0ef749`, the routed Service chain uses
**two fresh metadata reads instead of five** for one recovered plan and anchored
original page. The real Service regression fails on five reads before the
implementation. Outer placement, final Owner/receive checks, native committed
propagation and the existing four-reader admission remain enforced. RPC 104
binds the fixed consumer start and complete accounted frontier; matched peers
are required. No authority cache, worker increase or generic retry is added.

The ordinary original-size E2E **passes once**: 100,000 members, 500 persistent
clients, twenty initial publications and one post-churn publication, all 600
retirements, and zero missing/duplicate/reordered/foreign/unexpected publications.
Initial fanout takes 151.982s. Quiet barriers are 4.149/client/s, below the
unchanged ten/client/s ceiling. Total test time is 370.84s.

| Ordinary run | Members / clients / initial publications | Initial fanout | Final outcome |
| --- | --- | --- | --- |
| Baseline `437f7297c` | 2,000 / 500 / 20 | Three-minute receipt wait fails; deferred snapshot has 9,862 receipts and all clients open | FAIL before churn |
| Compound candidate | 2,000 / 500 / 20 | 151.380s; all clients have twenty receipts | FAIL at zero-based round 2 (third round) unsubscribe; one conflict closure |
| Compound candidate | 100,000 / 500 / 20 | 151.982s; full final identity/order assertions pass | PASS, including post-churn delivery and 600 retirements |

The smaller candidate failure remains an **unlocalized intermittent unsubscribe
conflict**. Its post-churn, final identity/order and retirement assertions are
not reached. The full pass does not prove this race repaired or qualify broader
MQTT failure/recovery behavior. Stage counters cannot distinguish subscription
CAS exhaustion, a changed child, or a drain/projection conflict; the next probe
must identify the terminal stage before changing retry or admission behavior.

This is one local sequential comparison, with no controlled host-noise estimate.
The baseline never completes fanout, so no baseline completion latency or
percentage speedup is available. Earlier instrumented measurements are separate
from these ordinary runs. Failure receipt histograms are deferred snapshots,
not precisely timestamped deadline samples.

All workloads keep 256 hash Slots, twelve initial Slot groups, 500 clients,
twenty initial publications, 200 churners and three rounds. Only the first pair's
member preparation is narrowed. Profiling and runtime diagnostic overlays are
off; receipt deadlines, assertions and workers are unchanged. The baseline build
uses an external overlay only to restore/exclude candidate Go files and exactly
recreate its product source. Other task processes were preserved.

## Evidence and reproduction

- [Original-size passing artifact](candidate-full.json)
- [Smaller candidate failure](candidate-2000-failure.json), [receipt snapshot](candidate-2000-receipts.json)
- [Baseline failure](baseline-2000-failure.json), [receipt snapshot](baseline-2000-receipts.json)
- [Exact commands, binary/source hashes, frozen instructions and limits](provenance.json)
- [Read-budget RED](read-budget-red.txt), [storage-error RED](storage-error-red.txt),
  [related race](related-race.txt), [final race](final-race.txt),
  [real cluster integration](app-integration.txt), [FLOW contracts](flow-contracts.txt)

For an ordinary reproduction, use a clean worktree at the candidate revision,
build `cmd/wukongim`, then run the unchanged process-level gate:

```sh
GOWORK=off go build -o /tmp/wukongim-mqtt-compound ./cmd/wukongim
WK_E2E_BINARY=/tmp/wukongim-mqtt-compound \
WK_E2E_MQTT_SCALE_MEMBERS=100000 WK_E2E_MQTT_SCALE_CONNECTIONS=500 \
WK_E2E_MQTT_SCALE_MESSAGES=20 WK_E2E_MQTT_SCALE_CHURN=200 \
WK_E2E_MQTT_SCALE_ROUNDS=3 WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-compound-reproduction \
GOWORK=off go test -p 1 -tags=e2e ./test/e2e/mqtt/scale \
-run '^TestGroupScaleDeliveryAndChurnRetirement$' -count=1 -timeout=15m -v
```

Leave `WK_E2E_MQTT_SCALE_PROFILE` unset. Run the baseline in a separate clean
worktree at `437f7297c`, using 2,000 members for the sequential pair. Do not run
both workloads concurrently. Every attempt must retain its own verdict/artifact;
a passing attempt cannot replace a failed one.

Toolchain/dependency preparation interrupted several launches before a captured
workload. Three fixed dependencies were eventually copied as original proxy
archives into a task-private cache and verified by `go mod download` against
`go.sum`; repository dependency/configuration files were not changed. One early
unsuccessful launch lacks preserved terminal stderr and is excluded. Explicit
worktree commands and file hashes provide source provenance: both binaries'
embedded VCS metadata reports the primary checkout revision, so that metadata
is not used as source proof. The candidate contains the new compound methods;
the baseline does not.

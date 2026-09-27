# MQTT interrupted unsubscribe process acceptance

Validated on 2026-09-27: all eight process scenarios pass (224.552s). See the
[frozen context, RED/green checks and result artifacts](../reports/mqtt-unsubscribe-fault-acceptance.json).
This is one part of the still-active full MQTT implementation goal.

## Failure inventory before implementation

- Removing intent commits but the foreground call fails before projection/UNSUBACK;
  the transport closes and the client must not have to repeat UNSUBSCRIBE.
- Projection finishes, including UID qualification removal, but final subscription
  completion fails. Background scheduling must retain the independent pending
  subscription obligation after the binding recovery entry disappears.
- Background completion is invisible to operators or cannot be distinguished from
  attempted work. Add a fixed aggregate confirmation event, materialized at zero,
  without identity labels or a unique-subscription claim.
- Recovery drops or replaces an already sent unacknowledged publication, its
  PacketID, original body/identity or DUP flag. Resume must preserve the exchange.
- Unadmitted pre-unsubscribe backlog or messages sent while unsubscribed leak into
  a new subscription generation. Fresh resubscription must establish a new start.
- Single-node-only behavior hides routing defects. Both inbox and group targets,
  both failure points and both single-node and three-node clusters use 256 Slots.
- Failpoint-enabled tests pass without executing the selected point, or ordinary
  product builds acquire injection controls/runtime dependencies. Instrument only
  a temporary source copy using existing gofail tooling and verify hit counts.

## Additional failure found by the process scenario

Before either injected fault, the three-node group case repeatedly disconnects
the first SUBSCRIBE after 0.8–1.0 seconds. Bounded phase probes show protected
source preparation succeeds, then initial shared replay copy returns the typed
`channel: not ready` error before anchor admission. The existing request loop
waits only for explicit replay pending, so the client loses the connection.
Regression coverage must preserve the Preparing generation and original start,
never grant SUBACK from a partial copy, and keep cancellation, unknown errors,
conflicts and failed anchor writes outside automatic retry. A copy admission or
quorum-readiness yield must remain distinct from those terminal/uncertain results.
Follower copy admission must first read its own durable HW: the source-reader
contract accepts an already-persisted boundary, whereas a remote copy request
can legitimately be ahead. A lagging receiver must neither claim corruption nor
advance HW from the request. Real corruption, failed local reads and cancellation
still reject copying; a native checkpoint advance can make a subsequent turn ready.

The final probe observed both followers at `LEO=1, HW=0` for requested boundary 1;
their local reads were healthy. The repair checks local HW before calling the
strict source reader, and marks only typed copy-stage readiness/backpressure as
replay pending. Existing request attempts/deadlines and exact intent validation
remain in force; it neither advances a follower checkpoint nor weakens SUBACK proof.

Full-suite rerun then exposed the analogous all-replica confirmation window:
recovery planning can be asked for an anchor before that replica has committed
it. Recovery must check receiver-owned HW before planning/import/retirement, yield
while native replication catches up, and propagate errors once that boundary is
already committed. Confirmation must never accept partial recovery, infer HW from
the target anchor, or convert unknown/corrupt evidence into successful SUBACK.
The second bounded probe confirmed target 2 at `LEO=2, HW=1`, with a healthy
local checkpoint read. Recovery now yields before planning or effects until that
target is locally committed; typed readiness/pressure remains pending at the
all-replica confirmation boundary. Corrupt committed anchors still fail.

The next full run reached seven passing scenarios, then exposed a pre-intent
UNSUBSCRIBE CAS race (`expected=7`, `current=8`). Failure inventory for its repair:
only an explicit conflict receipt permits a retry; a fresh same-Owner read must
show the identical child and a strictly advanced parent revision. Cap removal
intent writes at three attempts within the original deadline. Unknown/lost replies,
port errors named conflict, cancellation, changed child/Owner and unchanged parent
evidence must not rebase. Preserve subscription generation/options/operation and
perform projection once, only after the Removing write succeeds.

## Scope

Use the approved process MQTT/WK protocol and public observability seams.
Faults return before projection or before final child CAS after real projection;
no storage internals are imported by the harness. This proves recoverable request
failure plus connection closure, not abrupt process-crash/partition isolation.
Those and the rest of the complete MQTT design remain required.

## Reproduction

```sh
scripts/build-gofail-binary.sh --package internal/usecase/mqttsession --out /tmp/wukongim-mqtt-gofail
WK_E2E_BINARY=/tmp/wukongim-mqtt-gofail WK_E2E_GOFAIL_MQTT=1 WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-unsubscribe-fault-reports GOWORK=off go test -p 1 -tags=e2e ./test/e2e/mqtt/unsubscribe_recovery -count=1 -timeout=10m -v
```

The first point returns a controlled failure immediately after real Removing
intent, before foreground projection; entry closes without UNSUBACK success.
The second point blocks the background final subscription CAS after successful
projection and exact current-child checks. All nodes enable the second point so
Slot routing cannot bypass it. The test requires an actual hit and no completion
event, removes the gate and waits for background confirmation before reconnect.
An inbox test therefore exercises pending subscription recovery after completed
UID qualification has left its binding recovery index. No tables are inspected.

`wukongim_mqtt_consumer_events_total{event="subscription_removal_confirmed"}`
materializes zero and counts timely successful confirmation turns, including
idempotent retries. It is not an exact unique-subscription counter. Labels remain
fixed and body/identity-free. No config, schema, RPC or production gofail runtime
dependency is introduced. The generated build and its module changes remain in
the build script's temporary source copy.

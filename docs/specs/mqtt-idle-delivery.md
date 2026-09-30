# MQTT idle delivery and source wakes

One-second connection polling currently repeats discovery, recovery, accounting
and window reads even after a complete pass found no work. Measured at 500
subscribers, the idle load was 14,893 Slot barriers/s. This change reduces that
load without caching any send authority or changing consensus.

## Contract

After an entirely successful, idle subscription/source pass, ConnectionDelivery
may skip authoritative reads for at most ten seconds. A skip first checks the
exact local Owner execution gate and caller cancellation. Progress, busy work,
errors, partial source scans, private QoS-0 completion and terminal cleanup cannot
establish an idle hint. Initial exchange recovery always runs. Each wake advances
an atomic invalidation sequence; a wake during a pass prevents that pass from
installing a quiet hint. ACK, subscription and close wakes use this same path.

The existing Deliveries scheduler indexes observed Channel source IDs against
its retained entries. One complete pass replaces a task's interest set, bounded
by MaxSubscriptions. An inbox exceeding that bound falls back to ordinary polling
and drops its index; it cannot silently omit work. Removed tasks and Stop remove
their interests. Notifications visit only the interested entries, coalesce with
queued/running turns, and retain existing failure backoff. No new worker, message
queue, distributed proof, storage record or per-connection timer is introduced.
Retained index memory is O(connections × bounded interests); a first wake costs
O(interested connections × log scheduled connections). Repeated wakes do not add
heap records or publications, and queued/running tasks keep only one wake bit.

App adapts ordinary durable post-commit envelopes into Channel source wakes and
wakes again when replay maintenance confirms an anchor. The latter matters:
native commit is not accepted replay coverage. Notifications are node-local,
best effort, and can race interest registration or occur on another node. The
ten-second authoritative refresh recovers those misses and detects permission,
quota, ownership, source and placement changes. A wake never authorizes a send;
every actual effect still uses the current Session/source/receive checks.

## Failure inventory (before code)

1. Repeated quiet polls keep issuing Slot reads; cache never expires or a lost
   remote/registration wake leaves a message undelivered.
2. ACK, SUBSCRIBE, UNSUBSCRIBE or close cannot invalidate the hint; a wake during
   a turn is overwritten by its idle result.
3. Busy credit, unfinished source scans, errors or pending completion are treated
   as quiet; recovery order or retained exact-owner ending is lost.
4. Fencing/cancellation bypasses the local gate while idle; cached permission
   allows sending after revocation or takeover.
5. A source wake visits unrelated clients, loses running-task wakes, bypasses
   failure backoff or leaks entries after churn/Stop. Oversized source sets grow
   without a bound or suppress unindexed inbox sources.
6. Post-commit is mistaken for replay coverage; an early wake sleeps through the
   later anchor. Duplicate hints create extra sends or reset PacketID/order.

Validation uses deterministic existing usecase fixtures before implementation,
the real scheduler integration seam, and process-level scale/interop/recovery
scenarios. Scale now uses twelve physical Raft groups with 256 hash Slots,
matching the product initial-Slot default. Its artifact records idle barrier rate
alongside delivery identity/order, churn and latency. This remains partial MQTT
acceptance and makes no unmeasured capacity claim.

## Validation evidence (2026-09-30)

- Source revision `a1454d5f144b884c679c20833d94a051514cfd84`; applicable
  instruction/navigation digests frozen in
  [source context](../reports/mqtt-idle-source-context.json).
- Tests preceded implementation: missing quiet-hint APIs failed in
  `/tmp/mqtt-idle-red.log`; missing bounded source indexing/wake adapters failed
  in `/tmp/mqtt-source-wake-red.log`. Deterministic usecase coverage verifies
  zero Slot reads during quiet polls, missed-wake refresh, wake during a pass,
  cancellation/fencing, fresh revocation, clock regression, pressure and errors.
- Whole unit suites for usecase/runtime/app passed (132.499s/0.617s/3.741s).
  Focused Sender/coordinator/quiet-hint race coverage passed (6.931s).
  Scheduler and app/three-node delivery race coverage passed (1.752s/40.030s).
- The existing subscription-entry integration first failed its three-second
  receive assertion because the manually composed fixture supplied no anchor
  wake. Composing the real anchor-to-scheduler adapter restored the unchanged
  assertion; empty-group and subscription-entry race cases passed (12.726s).
- Final-product process interop passed in both topologies (41.527s); persistent
  reconnect/future inbox/takeover, graceful restart/shutdown passed (130.642s),
  and SIGKILL/restart with original PacketID/DUP passed (22.477s).
- The final candidate passed 2,000 members/500 subscribers/2 publications plus
  post-churn publication/10 churn retirements (106.98s). No duplicate, missing,
  reordered, unexpected or wrong-identity delivery occurred. Idle barriers were
  2,178.71/s (4.3574/subscriber/s); last-receipt p50 was 27.783s, max 28.772s.
  Artifact: [mqtt-idle-scale-500.json](../reports/mqtt-idle-scale-500.json).
  Command: `WK_E2E_BINARY=/tmp/wukongim-mqtt-idle-final WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-idle-final-500 WK_E2E_MQTT_SCALE_MEMBERS=2000 WK_E2E_MQTT_SCALE_CONNECTIONS=500 WK_E2E_MQTT_SCALE_MESSAGES=2 WK_E2E_MQTT_SCALE_CHURN=10 WK_E2E_MQTT_SCALE_ROUNDS=1 GOWORK=off go test -p 1 -tags=e2e ./test/e2e/mqtt/scale -count=1 -timeout=10m -v`.
- Three old-binary attempts (500/100/32 subscribers) stopped in cold SUBSCRIBE
  with `unconfirmed`, before measuring idle cost. They are not paired latency
  baselines. The previously recorded 14,893 idle barriers/s used a different
  initial-Slot configuration. Measurements ran on a shared developer machine.

- The final default 100,000-member run stopped after 726.31s at the public HTTP
  member-preparation deadline. The first 95,000 members' batches were confirmed;
  the batch from 95,000 timed out and may have partial effects. MQTT CONNECT,
  SUBSCRIBE, fanout and churn were not reached. Bounded failure artifact:
  [mqtt-idle-scale-full.json](../reports/mqtt-idle-scale-full.json).

Full 100,000-member acceptance remains incomplete. Delivery latency remains high;
node-local hints are not distributed notifications. The known intermittent cold
SUBSCRIBE failure, unavailable-node isolation and complete MQTT acceptance remain
open. No production capacity or complete MQTT qualification is claimed.

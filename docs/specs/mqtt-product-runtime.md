# MQTT product composition

The existing module integrations do not prove that a `cmd/wukongim` process can
serve MQTT. Product composition must join authentication, exact-owner RPC,
connection renewal, group and inbox subscriptions, future person admission,
replay maintenance, delivery, ACKs and bounded lifecycle work in `internal/app`.
The approved complete acceptance scope remains unchanged; enabling this path
in a development checkout is not a completed MQTT release.

## Failure inventory before implementation

1. The product loader/harness rejects MQTT keys, ignores TOML/env overrides, or
   enables a listener by default. Bounds/defaults and displayed effective values
   must agree; invalid configuration fails before opening resources.
2. Authentication bypasses durable device tokens or invokes WK device-conflict
   rules, causing same-UID MQTT ClientIDs to kick each other or the native client.
3. Only group projection is wired; a successful inbox SUBACK then loses the
   first native person message because shared future-source admission is absent.
4. Connected clients never receive because coordinator, replay, delivery or ACK
   wakes are omitted. Public message identity/payload must cross protocols intact.
5. Cross-node takeover cannot reach the exact owner, or unavailable ownership is
   silently interpreted as isolated. Uncertain sends must retain their barrier.
6. Startup opens admission before cluster readiness/workers. Bind failure,
   constructor failure, Stop timeout or restore leaves workers using closed
   dependencies. Stop must join and preserve dependencies on incomplete cleanup.
7. Configured capacity is unbounded, creates per-session goroutines, or bypasses
   existing per-owner/worker/queue/backlog limits.
8. An interop pass is reported as full acceptance. Will uncertain redispatch,
   unavailable-owner recovery, offline cleanup/accounting, restore and scale gates
   still require their own complete implementation and process-level evidence.

## Initial evidence

Before changes, the existing single-node process E2E failed at configuration
rendering: `unsupported config key WK_MQTT_ENABLE`. It did not start an MQTT node.
Use the existing Paho/WKProto interop test as the first product-path gate, then
extend acceptance through the full approved failure scenarios. No mock storage,
hand-installed subscriptions or manual cursor preparation can substitute for it.

## Implemented composition and limits

`mqtt.enable` defaults to false. Enabling it adds a raw TCP listener to the owned
Gateway, always uses durable device-token authentication, and enables future
person-source preparation in the shared appender. Namespace defaults to `main`;
all nodes must enable this development path and use the same stable namespace,
so native ingress on any node prepares inbox sources. Mixed enabled/disabled
ingress is not a supported configuration. Defaults are 100,000 retained
owners, 128 subscriptions, 16 workers in each connection/delivery cohort, 1 MiB
inbound packets, a 24-hour Session expiry ceiling, candidate quotas of 10,000
messages/64 MiB and a 64-exchange window (also bounded by peer Receive Maximum).
These values are limits, not measured capacity guarantees.

App owns exact-owner RPC registration, group/inbox projection selection and
worker lifecycle. Startup precedes Gateway admission; Stop closes admission and
joins MQTT producers/owners before stopping Gateway's physical-close callbacks.
Any incomplete cleanup retains transport/cluster/message dependencies for retry.
Successful join records a [graceful boot retirement](mqtt-owner-retirement.md)
for exact older-owner RPC recovery after process restart.
Restore stops the same terminal runtime and currently keeps admission closed;
fresh restore-generation reactivation is still required. No old registry is
reactivated and no unavailable owner is assumed isolated.

## Product evidence

- Before wiring, process E2E failed at the unknown MQTT enable configuration key.
- Existing `test/e2e/mqtt/interop` passes both 1-node and 3-node cluster cases,
  each with 256 hash Slots: rejected bad token, advertised QoS/capability bounds,
  inbox SUBACK, first native person message reaching MQTT with its original
  identity, MQTT reaching WK, and multiple ClientIDs coexisting with WK.
- Preserved reports: `/tmp/mqtt-product-reports/mqtt-interop-1.json` and
  `/tmp/mqtt-product-reports/mqtt-interop-3.json`. Reproduce with
  `WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-product-reports GOWORK=off go test -p 2 -tags=e2e ./test/e2e/mqtt/interop -count=1 -timeout=4m -v`.
- Full `internal/config` and `internal/app` default-tier tests pass with `-race`.
  A discovered expiry-bound gap first failed a focused regression test, then
  normalization was tightened to reject a configured ceiling above 24 hours.

This evidence does not cover automatic detached Will publication, safe uncertain
redispatch, orphan-owner sweeps, unavailable-owner takeover, offline accounting
and cleanup, restore reactivation, or scale qualification. The approved complete
objective remains open. No tables, columns, storage encoding or RPC IDs change
in this composition step. Frozen governing context is recorded in the adjacent
[implementation evidence JSON](../reports/mqtt-product-runtime.json).

## Subsequent Will scheduling

[Will scheduling](mqtt-will-scheduling.md) now composes a separate bounded scanner
and four-turn cohort, with product-process evidence for delayed abnormal-close
publication and normal-close cancellation. The historical interop evidence above
retains its original scope. Remaining crash/restore/offline/scale gates stay open.

## Subsequent consumer maintenance

[Consumer maintenance](mqtt-consumer-maintenance.md) now joins original-content
accounting, exact quota/revocation cleanup, contiguous ACK projection and
Channel-binding removal through a separately bounded cohort. Offline clients
and full receive windows no longer depend on online delivery turns for
accounting. UID/cursorless cleanup, storage reclamation, crash/restore and scale
qualification remain distinct requirements. Existing recovery indexes are reused;
this composition adds no table, column, command or RPC encoding.

[Ended UID qualification retirement](mqtt-qualification-retirement.md) now runs
in that same cohort, separately observing confirmed qualification tombstones.
It preserves successor lifetimes and independent source/drain obligations.
Subsequent pending-unsubscribe recovery is described below; complete record reclamation remains required.

## Subsequent owner sweeping

[Owner sweeping](mqtt-owner-sweeping.md) now drives the existing local heap with
one managed loop, independent of connection registration. App starts it before
Gateway admission and joins it after fencing owners, before final registry close
and retirement publication. Pending expiry, failed-close retry and sampled owner
metrics are wired; uncertain effects remain retained. This completes the earlier
orphan-scheduling gap, not unavailable-owner or unknown-effect recovery.

[Closed-source maintenance](mqtt-closed-source-maintenance.md) now reuses that
consumer cohort to resume seals and one accounting range for closed subscriptions,
including Offline Sessions. It has no local Owner dependency or network authority.
[Pending removal recovery](mqtt-pending-removal-recovery.md) now shares the same
cohort with subscription-index discovery. It finishes closed inbox/group intent,
including interrupted preparation before the first binding and failed final
completion writes. Three-node product-worker composition is verified with a
controlled interruption; full product-listener fault acceptance remains required.

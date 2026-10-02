# Ordinary membership proposal scheduling

## Observed failure and hypotheses

At source revision c4afe729d, the MQTT scale scenario did not finish public HTTP
preparation of 100,000 members within its twelve-minute context: 95,000 members'
batches confirmed and the last 5,000-member request timed out before MQTT began.
A smaller 10,000-member run spent 52.322 seconds preparing members. It completed
all delivery identity checks but failed the subscriber-normalized idle assertion
because fixed node-wide maintenance dominates a one-subscriber run; it is only
provisioning evidence, not an MQTT scale pass.

Ranked falsifiable hypotheses before changes:

1. Two supervised proposal workers serialize too many small Hash-Slot commands.
   The profile will show commit waits, and bounded wider submission will reduce
   preparation time without reducing projected rows or disabling durability.
2. Disk synchronization is saturated. Wider submission will not materially
   improve wall time and proposal apply latency will increase.
3. Metadata work grows with the stored member set. Equal-sized later public
   HTTP batches will slow progressively and CPU samples will show table work.

The original process acceptance is the full MQTT scale command. A smaller
opt-in process probe prepares 10,000 members within 30 seconds through the same
HTTP/usecase/Slot path, records bounded per-batch metrics, verifies projected
row accounting and sampled public conversation results, and always writes an
artifact when a report directory is supplied. The budget preserves headroom
for the subsequent MQTT phases and measures the reported preparation symptom.
Timing evidence is host-specific; profiles run only in a separate diagnostic
attempt, not an acceptance window.

## Failure inventory before implementation

- An increased worker count is unbounded, leaks after cancellation, or returns
  before admitted proposals finish.
- One failed proposal is reported as success, starts more queued work after
  cancellation, or hides already committed rows from accounting.
- Several Hash Slots sharing one physical Slot monopolize all workers while
  another physical Slot is ready; changing routing creates authority shortcuts.
- Wider scheduling changes membership source-version, join visibility, durable
  apply, or reset/removal semantics; ordinary SEND starts writing memberships.
- Metadata batching silently drops, duplicates, or merges membership rows or
  crosses a logical Hash Slot without an explicit command contract.

All existing command formats, ownership and durable completion requirements
must remain intact. No new multi-Hash-Slot command is justified merely by this
performance hypothesis.

## Evidence (2026-09-30)

- Source/instruction/navigation digests: [frozen context](../reports/mqtt-membership-source-context.json).
- Before implementation, bounded scheduling/cancellation/failure tests failed
  waiting for a third admitted proposal with the former two-worker cohort
  (`/tmp/mqtt-membership-scheduling-red.log`). The same focused race suite passed
  with eight workers (2.109s); the entire cluster unit suite passed (8.449s).
- The process probe reproduced the setup deadline through the real public API:
  `WK_E2E_BINARY=/tmp/wukongim-mqtt-idle-final WK_E2E_MQTT_MEMBERSHIP_PROBE=1 WK_E2E_MQTT_MEMBERSHIP_PROFILE=1 WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-membership-red-profile GOWORK=off go test -p 1 -tags=e2e ./test/e2e/mqtt/scale -run TestGroupMembershipProvisioningBudget -count=1 -timeout=2m -v`.
  First 5,000 members took 25.321s; the second request hit the 30-second total
  budget. [Before artifact](../reports/mqtt-membership-before.json). This was a
  separate diagnostic run, including one goroutine dump and ten-second CPU profile.
  The CPU profile collected 0.69s of samples in 10.02s. Both projection submitters
  were waiting in Slot Future.Wait, with 256 logical commands for the active
  1,000-member chunk. The earlier unprofiled 10,000-member run took 52.322s.
- Changing only the ordinary proposal-worker limit from two to eight passed
  the unprofiled probe: first/second batches 6.084s/5.915s; 10,000 members
  confirmed in 12.000s with exactly 10,000 projected rows and 2,572 Slot
  proposals before verification. Three sampled public conversations hydrated
  the committed publication; SEND did not write more memberships.
  [After artifact](../reports/mqtt-membership-after.json).
  Command: `WK_E2E_BINARY=/tmp/wukongim-mqtt-membership-8 WK_E2E_MQTT_MEMBERSHIP_PROBE=1 WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-membership-8 GOWORK=off go test -p 1 -tags=e2e ./test/e2e/mqtt/scale -run TestGroupMembershipProvisioningBudget -count=1 -timeout=2m -v`.

The evidence supports synchronization-wait serialization as the limiting factor
in this preparation workload on this host. It does not prove physical-Slot
fairness, unavailable-owner isolation, or a host-independent capacity target.
The eight workers retain fail-fast cancellation and joined admitted commands;
no distributed ownership, command format, membership visibility or durability
semantics changed. The full 100,000-member run confirmed all member-preparation requests, then
failed at cold SUBSCRIBE before fanout/churn (214.45s total). Public metrics
recorded 485 active owners and 2 deadline/13 canceled closures. Active owners do
not prove successful SUBACKs. [Full-run failure artifact](../reports/mqtt-scale-membership-8-failure.json).
Complete MQTT scale acceptance remains incomplete; subscription diagnosis is
a separate next step.

- Additional process checks passed: recipient authority (3.77s), monotonic badge
  and hide/remove/rejoin (6.19s/4.90s), three-node hydration, unavailable-Leader
  retry and four-node non-replica ingress routing (19.93s/25.55s/20.63s).
  The pagination case initially received HTTP 408 on its first SEND; its
  unchanged focused rerun passed (5.21s). That one-off timeout is recorded,
  not treated as a repaired failure. Logs: `/tmp/mqtt-membership-directory-e2e.log`
  and `/tmp/mqtt-membership-directory-retry.log`.
- `go vet ./pkg/cluster`, formatting and diff checks passed. The named
  `flow-doc-contracts` check passed with 87 compliant files, no invalid files
  and eleven existing length warnings.

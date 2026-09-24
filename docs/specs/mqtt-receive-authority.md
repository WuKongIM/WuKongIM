# MQTT receive authority

## Contract and ordering

Production receive policy requires a fresh coherent channel/membership view,
not SEND privilege or an asynchronous UID directory projection. RPC 91 read kind
19 (`MQTTReadMembership`) uses the existing foreground-gated Node and current
Slot route, fresh local ReadIndex/apply barrier and one pinned metadata snapshot.
The query identifies one `(channel_id, channel_type, uid)`; it routes by channel
ID. Exactly three point reads cover the channel, member and subscriber allocation
sequence. The channel cache must not override the pinned snapshot.

The reply includes its exact key, optional channel/member and the allocation
high water. A present member must carry a positive incarnation at or below that
water. Missing rows are authoritative absence only in a complete, well-formed
reply. New query/result fields are omitted on older kinds; old peers reject kind
19, and no fallback to separate reads, caches or convenient replicas is allowed.
No table or durable format is added by this read.

ReceiveAuthorization implements SubscriptionAuthorizer. Group targets use type
2, require an existing non-disbanded channel and actual UID membership, and return
the member incarnation. Ban/SendBan, AllowStranger and system privileges cannot
substitute for membership or revoke receiving merely because sending is muted.
Self-inbox permission is the immutable admitted UID binding (version 0); another
UID is denied. This does not establish inbox source protection or projection.
Entries remain responsible for canonical topic mapping. Calls validate bounded
input, obey cancellation and a bounded timeout, redact callback panics and never
turn unavailability or malformed evidence into a definitive membership denial.

A successful fresh read is the authorization decision's ordering point. A
removal committed before that read must deny. A remove/rejoin changes the
incarnation, so subscription/recovery/accounting/sender comparisons reject old
intent even if membership is present again. Sender's final check remains the
last authority dependency before enqueue; previously authorized or queued data
cannot be recalled. Definite denial or changed incarnation ends the exact Session
through the existing sender path. Transport errors retain state for retry.

## Failure inventory before implementation

- Group absence, removal and disband deny; empty/open groups or send privileges
  never grant membership. Sending mutes do not deny an existing receiver.
- Repeated adds and unrelated membership changes preserve authorization;
  removal/rejoin and channel recreation invalidate old authorization.
- Channel and member values must belong to one snapshot despite live cache
  refreshes; malformed or regressed allocator state cannot mint a grant.
- Local and remote reads require a fresh apply barrier; stale origin replicas,
  changed routing, wrong Slot/key, incomplete replies and unavailable authority
  fail closed without declaring definite revocation.
- Read kind 19 is exclusive and bounded. Older read JSON remains byte-compatible;
  malformed/extra fields and foreign channel/member identities fail validation.
- Self inbox is limited to the admitted UID; invalid inputs and cancellation
  cause no authority call. Panic and timeout cannot escape or become denial.
- Actual Slot membership mutations must drive existing subscription and sender
  checks: a rejoined member cannot resend old exchanges or restore old backlog;
  new Session/subscription intent may acquire the new incarnation.
- Single-node and three-node integration must use production authorization,
  preserve native subscription/removal behavior and retain exact ending debt.

## Scope

This completes the production receive policy and its authority port, not the
product listener, fair delivery scheduler, unattended revocation discovery,
inbox projection, Will execution or process-level feature acceptance.

## Discovered idle-anchor propagation regression

The three-node receive test reproduced a permanent preparation failure on the
second consumer: the leader held anchor 4 at HW 4 while an ISR replica held LEO 4,
checkpoint/HW 3. Planning returned a valid maintenance-only tail, so neither
business append nor another copy propagated the committed frontier. Recovery
correctly refused the replica's uncommitted journal; retry alone did not help.

Before repair, extend the failure inventory: a leader's valid anchored planning
read must request bounded native committed-frontier propagation, including idle
and write-fenced plans; the request contains exact installed authority, never a
caller-supplied HW. Missing capability, cancellation, refresh failure and changed
placement must withhold the plan. Malformed/no-anchor plans must not schedule;
forwarding schedules only at the leader. A hint proves no replica recovery.
This also recovers a commit whose observer stopped before issuing a hint. No wire
or durable format change is required.


## Validation and remaining scope

Verified locally on 2026-09-24:

- New metadata, Slot and usecase tests were written before implementation; initial
  compilation failures are captured in `/tmp/mqtt-receive-red.log` and
  `/tmp/mqtt-receive-app-red.log`. The idle-anchor regression reproduced in the
  three-node test and failed its focused regression before repair.
- `GOWORK=off go test -race -p 2 ./pkg/db/meta ./pkg/slot/proxy ./internal/usecase/mqttsession -count=1`
  passed the complete suites (28.370 s, 14.716 s, 52.306 s).
- `GOWORK=off go test -race -p 2 ./pkg/cluster/channels -count=1`
  passed (7.813 s), including idle/fenced refresh, rejected malformed evidence,
  cancellation, unavailable refresh and placement changes.
- `GOWORK=off go test -race -p 2 ./pkg/cluster ./internal/app ./internal/usecase/mqttsession -run 'Test(ReceiveAuthorization|MQTT.*NodeForegroundGates|MQTTProtocol)' -count=1`
  passed (1.715 s, 2.119 s, 2.807 s).
- `GOWORK=off go test -tags=integration -p 2 ./internal/app -run 'TestMQTT(SubscriptionIntentSingleNodeCluster|GroupSourcePreparationThreeNodeRecovery)$' -count=1 -timeout=4m -v`
  passed (23.328 s). The repeatable `mqtt_receive_authority_evidence` artifact
  verifies native same-version rejoin, cross-node owner resume, old exchange and
  backlog denial, exact Session ending, retained cursor/counters and fresh
  lifetime/subscription permission. Real Node, Slot, channel protection, replay
  workers, accounting and sender are composed; the wire sink is controlled.
  The enclosing test retains independent three-replica disk-reopen evidence.
- The same two app integration cases also passed with `-race` (25.689 s),
  covering native permission changes, cross-node resume and restarted replay
  workers with joined cleanup. No race was reported.
- `flow-doc-contracts` render/check: 86 compliant, zero invalid, nine existing
  length warnings. Existing Darwin linker warnings did not fail race tests.

No new table, durable format, process goroutine or full-group scan is introduced
by receive authorization. RPC 91 kind 19 requires matched nodes. This is not a
product listener/process acceptance result; fair delivery scheduling, inbox
future-source admission, automatic end/cleanup discovery, Will execution,
restore/unavailable-owner fencing and full MQTT offline transfer remain open.

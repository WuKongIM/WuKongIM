# MQTT pending removal recovery

Status: recovery primitives and bounded product composition implemented;
complete process fault and scale acceptance remains required.
Frozen source revision and governing context are in the
[evidence report](../reports/mqtt-pending-removal-recovery.json).

## Scope and approved seams

Reuse the approved subscription projection, routed metadata, SourceDrain and
bounded consumer-maintenance seams. A cleanup turn captures authoritative
Session/child identity and never admits a live Owner or grants network execution.
Existing subscription recovery (kind 5) remains the scheduling obligation after
UID qualification leaves source-binding recovery (kind 12). No new table is needed.

## Failure inventory before implementation

- A disconnected Owner must not prevent draining a durable Removing intent;
  Active/Preparing intent, foreign UID, ended/new lifetime or changed Owner must
  not grant cleanup of the captured current lifetime.
- Cursor pages and drain receipts must be complete, coherent, bounded and ordered;
  mixed envelopes, Runtime extras, malformed continuations and late callbacks
  must authorize no checkpoint advancement.
- One cursor page per turn bounds work; pending ranges retain the current cursor.
  Independent discovery progress and all begun inflight exchanges survive.
- Lost replies after qualification closure, window release, source checkpoint or
  qualification completion must resume from durable state without double release.
- Cancellation, panic, clock regression and revision conflicts return no receipt;
  permission revocation must not prevent safe cleanup.
- A preparation interrupted before its first UID/group binding needs discovery
  from pending subscription intent, without manufacturing delivery authority.
- A failed final subscription write must retain its recovery index even after UID
  qualification is Removed. A committed reply loss must be harmless on retry.
- Completion must reread exact captured Owner and complete child after projection;
  it cannot follow a successor, activate pending intent or erase inflight work.
- Subscription scanning must share a fixed bounded worker cohort, validate full
  pages before dispatch, preserve pressured candidates, rotate led Slots/streams
  and join all work before dependencies stop. Hints never grant authority.

## Acceptance limits

Use failure-first seam tests with actual metadata transitions, then routed cluster
and real-process coverage. Existing process regressions alone do not demonstrate
injected interrupted-UNSUBSCRIBE recovery. Record exact achieved coverage and
outstanding composition/fault acceptance in the report.

## Implemented behavior and bounds

`InboxRemoval.RemoveClosed` shares foreground checkpoint stages but obtains UID
and parent authority from fresh routed metadata. It scans at most one cursor page
(default 8, maximum 64); each nested `SealClosed` inherits the enclosing Owner
instead of recapturing a successor. Source windows release at most one qualified
range per turn, leaving all inflight exchanges unchanged. Discovery checkpoints
remain independent. An empty initial projection can create then close its UID
qualification without claiming any Channel-source release.

`SubscriptionRemoval` point-reads the candidate, captures the full child and
parent Owner, invokes inbox removal or protected group discovery, then rereads
both before the final revision CAS. Group discovery handles missing first bindings
without inventing delivery authority. A failed completion write leaves subscription
recovery index 2 present; a committed lost reply observes Removed on retry. Fresh
ended/replaced lifetimes are left for separate reclamation. Preparing/Active rows
are never activated by this cleanup. Foreground Unsubscribe accepts a concurrently
completed child only when all original identity/options still match.

`ConsumerWorker` rotates read kinds 12 and 5 over currently led hash Slots with
one existing bounded cohort. Defaults remain 16 admitted keys, 32 pages per scan,
16 rows per page, 200 ms cadence, two-second scan budget, 250 ms reads and five-second
execution turns. There are at most two cursors per Slot (512 at the default 256).
Complete pages are validated before admission; terminal request cursors and
pressure preserve row progress, future boundaries and lost Slots discard hints.
Subscription work identity excludes RecoveryAtMS. Preparing remains indexed but
is skipped. Stop joins both kinds of work; no new task, table, encoding, RPC,
configuration field or per-Session goroutine is introduced. Existing aggregate
turn metrics include this work and do not claim unique subscription counts.
The subsequent fixed `subscription_removal_confirmed` event exposes observed
completion (including retries); failures and late results cannot increment it.

## Additional reproduced races

The first nested cleanup recaptured a newly connected Owner between the outer
check and source read, reducing pending messages from 2 to 1 before rejecting the
outer checkpoint. A failure-first regression led to passing the captured Owner
through `SealClosed`; the successor's window is now unchanged.

Once background completion existed, foreground projection success/conflict/pending
could incorrectly fail an already-completed Unsubscribe. Failure-first regressions
now require a fresh exact-Owner/full-child completion check. Different generations,
operations and options cannot satisfy it. No failed receive authorization is needed
for removal and no new live execution is granted.

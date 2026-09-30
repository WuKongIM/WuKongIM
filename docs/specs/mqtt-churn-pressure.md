# MQTT churn with in-flight delivery

This opt-in process probe tightens the remaining subscription conflict loop.
It uses a real single-node cluster with 256 logical Hash Slots, twelve physical
Raft groups, public HTTP provisioning, persistent Paho clients and WKProto SEND.
The full scale acceptance is unchanged. This probe intentionally churns before
all initial publications arrive; it verifies control replies, exact fresh
post-churn delivery and retirement, without requiring unadmitted old messages
to be delivered through a replacement subscription.

## Failure inventory before implementation

- SUBSCRIBE/UNSUBSCRIBE closes a healthy connection during old delivery progress;
  capture the failed phase and exact count of confirmed control replies.
- The probe accidentally runs after all delivery has completed; assert and
  record pending initial receipts at the first churn boundary.
- Setup fails before contention; record that phase separately, never claim the
  intended churn failure was reached.
- A post-churn publication is missed, duplicated, reordered or has a different
  identity; match all observed publications to actual SENDACK identities and
  require the fresh one exactly once for every subscriber. Old begun exchanges
  may complete, but unadmitted old messages need not cross subscription removal.
- Churn tombstones remain indefinitely; require every successful removal's
  retirement through the public fixed aggregate counter within a bounded wait.
- The sender's own idle expiry spoils the control signal; send real PING/PONG
  between bounded rounds and before the final SEND, without reconnecting.
- Dependency pressure or unknown outcomes are silently retried; clients issue
  each control request once, with original five-second packet deadlines.
- Temporary diagnostics change authority, deadlines or capacity; use only fixed
  source-stage error wrappers in a separate Go overlay build, record its source,
  and do not call its results complete scale acceptance.

Ranked hypotheses: new-intent/quota/cursor CAS races with parent progress;
background removal/projection changes exact child/binding; source-protection or
replay admission backpressure escapes bounded pending handling. Each has a
separate source-position prediction. Reproduce before choosing a repair.

## Recovery yield regression inventory

The 64-connection diagnostic reproduces a churn failure in
`Confirm -> Step -> recover -> StepChannelMQTTReplayRecovery`. Unlike Confirm's
direct recovery branch, Step returns receiver backpressure without pending
classification. Test the following outcomes before changing production code:

- Fresh nested planning observes an anchor committed after Confirm's first
  read. A bounded recovery rejection must retain Preparing, not close the client.
- Step reaches recovery through repair rotation, an idle accepted tail or a
  write fence. Not-ready/backpressure yields must preserve detached hints and
  target fairness; they grant no anchor, recovery or subscription receipt.
- Cancellation wins over a returned readiness error. Conflict, stale authority,
  write fencing, invalid input, unknown errors and dependency deadlines stay
  failures and never acquire the pending signal.
- A pending result triggers no inline retry, copy or anchor commit. A later
  Confirm rereads placement/accepted progress and requires every replica's exact
  coverage before success. Anchor errors retain their existing non-retry policy.

## Preparing revision contention inventory

The 500-connection loop additionally captures quota-scan parent changes,
definite group cursor Init rejection and a changed accounting snapshot while
draining. Before the next repair, cover a single unrelated renewal, perpetual
contention, replacement child/Owner, unchanged/regressed parent, revoked
permission, cancellation, plain conflict errors and lost replies. Preparation
may retry at most three times only after a definite rejection or a read-only
newer revision, with the original absent/Removed child and fresh quota/permission
checks. A rejected cursor Init preserves the first protected boundary, pins the
complete Preparing child and may reuse only an independently verified exact
cursor. Unknown effects stop immediately. Drain must distinguish valid newer
snapshots from corrupt, foreign or regressed evidence before yielding; no stale
accounting mutation may be proposed.

## Evidence and repair

Two 64-connection overlay probes captured raw receiver backpressure from nested
recovery. The corrected forty-case route matrix fails sixteen cases on the
frozen production source and passes with cancellation precedence and typed
pending classification. Anchor errors retain their previous non-retry behavior.

The subsequent 500-connection diagnostic captures quota-scan revision change,
cursor Init CASConflict and drain snapshot mismatch. Twenty-seven preparation
cases fail seven baseline cases; nine drain snapshot cases fail real renewal
and ACK interleavings. The repairs bound quota/intent/Init attempts to three,
pin complete original intent/Owner and preserve the first protected boundary.
SourceDrain validates newer coherent accounting before yielding; no stale window
write occurs and corrupt/regressed evidence remains a failure.

The ordinary candidate passes the 500-connection/twenty-message in-flight probe:
1,100 SUBACKs, 600 UNSUBACKs, 500 exact fresh receipts, 600 retirements and zero
subscription closure observations. Initial receipts are still 0/10,000 at
churn, so the overlap trigger is verified. The full usecase race suite passes
in 176.921s; focused checks pass in 8.173s, vet and FLOW contracts pass.
[Artifacts and provenance](../reports/mqtt-recovery-yield-evidence.json).
The `foreground_retries` artifact field counts client control retries (zero);
it does not count the server's bounded pending/CAS attempts.

This narrower probe does not qualify the unchanged 100,000-member scale test.
Parallel supplementary checks failed at cold three-node SUBSCRIBE and old-intent
reclamation. Isolated reruns pass all 64 cold admissions (93.186s) and the selected
single-node/three-node app composition gates (38.161s). Retain both outcomes;
these runs do not prove performance under concurrent test-suite load.

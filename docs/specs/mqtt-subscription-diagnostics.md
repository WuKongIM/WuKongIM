# MQTT subscription closure diagnostics

Status: entry observations and planning-read readiness verified; underlying failure diagnosis in progress. This change supplies
missing observations; it does not claim to fix the historical initial SUBSCRIBE
failure or post-resume EOF.

## Failure inventory (written before implementation)

- Every failed SUB/UNSUB packet requesting entry closure reports one fixed reason;
  a later filter failure still reports once and emits no partial ACK.
- Distinguish Owner capacity/fencing, cancellation/deadline, clock, conflict,
  invalid evidence, exhausted pending, unconfirmed intent, unknown dependency,
  malformed reply/write, unsupported entry, malformed packet and callback panic.
- Errors joined with unconfirmed intent retain their underlying diagnostic class;
  a diagnostic conflict is not proof of a definite non-write. Metrics never change
  retry policy or authorize a negative ACK.
- Definite per-filter denial/quota and successful requests produce no closure
  observation. Scope errors before and after the usecase are observed without
  changing scope lifetime, deadline, checks or cleanup ordering.
- Optional observer failure cannot replace a packet result or interrupt closure.
- The public counter pre-materializes only subscribe/unsubscribe and seventeen
  fixed reasons (34 series/node). Unknown labels are ignored; nil is safe. No
  ClientID, UID, topic, error text, payload, address or per-request label is kept.
- Real-process interrupted UNSUBSCRIBE must change the ingress node's deadline
  closure counter from zero to one, retain the existing recovery assertions, and
  record the observation in its repeatable JSON artifact.

## Signal

`wukongim_mqtt_subscription_closures_total{operation,reason}` counts entry close
requests from SUB/UNSUB processing, not unique connections or physical isolation.
An admitted operation canceled by fencing can report `canceled`; this metric
does not infer the cause of context cancellation. It does not cover closes originating in delivery, renewal or the gateway. Reasons
are `disabled`, `malformed`, `owner_limit`, `fenced`, `deadline`, `canceled`,
`clock`, `conflict`, `evidence`, `pending`, `unconfirmed`, `denied`, `quota`,
`callback`, `reply_evidence`, `reply_write`, and `unknown`.

## Hypotheses

1. Concurrent Session revision rejects subscription intent/activation: expect a
   conflict observation, then a targeted CAS probe to distinguish definite reject.
2. Projection/replay preparation escapes pending handling: expect pending,
   evidence or unknown; a bounded dependency probe must locate it before repair.
3. Owner scope closes or saturates: expect fenced/owner_limit/deadline/canceled;
   correlate with aggregate Owner work. A successful rerun is not a diagnosis.

## Current evidence

- Related access, metrics and app packages pass `-race` (2.105s / 3.679s /
  4.769s); the FLOW named check has no invalid files.
- The previous binary fails the process assertion because the metric is absent.
- The new binary reproduced initial three-node group SUBSCRIBE closure on the
  first bounded repetition, after 918.628ms and before any configured fault or
  publication. The ingress node reported exactly one `subscribe/unconfirmed`
  observation. The new metric identifies the entry path but the underlying
  dependency and cause still require a targeted probe.
- A separate error-only group/replay stage probe passed all ten repetitions
  (231.274s), including the exact injected closure counter and recovery identity.
  It captured no natural failure. Probe sources were removed immediately after
  build; passing repetitions do not prove a repair. The next diagnostic loop
  should isolate cold first-subscription admission to improve capture frequency.
- Exact binary, rule and log hashes are recorded in
  [the evidence report](../reports/mqtt-subscription-diagnostics.json).

## Focused admission loop

`TestColdGroupSubscriptionAdmission` now performs 64 fresh group/Session
subscriptions in one real three-node 256-Slot cluster, using the original first
fixture then deterministic new identities. It contains no publication, ACK,
UNSUBSCRIBE, enabled failpoint or application retry. Its bounded JSON records
both successful and failed attempts, fixed closure categories and admission
latency. It is admission coverage, not yet a reproducer of the intermittent bug.

Both the stage-probe binary (98.723s) and ordinary candidate (91.516s) passed all
64 attempts. Candidate median/P95/max SUBSCRIBE time was 976.598/1094.784/1464.490
ms. These sequential local observations do not qualify scale or long-sequence
performance. They do not exclude process-startup timing: repeated cold groups in
one warmed cluster omit repeated process startup. Exact commands and artifact
hashes are in [the admission report](../reports/mqtt-cold-subscribe-admission.json).
The two historical connection failures remain unresolved; no retry or timeout
policy was changed by this work.

## Planning-read readiness inventory (before implementation)

A replay planning query is read-only with respect to consumer intent and anchor
admission. Typed `channel.ErrNotReady`/`ErrBackpressured` from that query should
keep existing Preparing intent pending under the existing request attempt/time
bound. The initial Confirm query and its nested Step query must both preserve
that classification. This is a separate code-path defect hypothesis; the
historical natural closure has not yet been observed at this boundary.

- No copy, anchor mutation, replica completion or successful SUBACK may follow a
  failed plan read. A later attempt must start from fresh authority/plan reads.
- Only typed readiness/capacity yields gain `ErrReplayPending`; unknown errors,
  deadlines, stale authority, corruption and invalid configuration do not.
- Synchronous cancellation after the read wins over its readiness classification.
- Recovery must still confirm every replica and exact captured anchor before
  completion. Anchor write outcomes and metadata authority failures retain their
  existing policy; this change cannot grant generic retries.
- The initial and nested paths need deterministic RED coverage before changes.

The sixteen initial/nested planning cases ran RED before implementation: typed
readiness/capacity cases lacked pending and cancellation lost to the plan error.
They now pass (race, 1.890s). Production changes only classify those read-only
admission yields and give synchronous cancellation precedence. Existing copy,
anchor and all-replica proof rules remain in force. The complete usecase/access/app
race runs passed (112.323s / 1.937s / 5.132s), as did the FLOW named check.

The startup-only loop separately passed 20 fresh-process repetitions without the
gofail control endpoint (248.170s), then 20 with the original control endpoint and
WaitListed readiness but no enabled fault (251.183s). The wider Subscribe/group/
replay probes captured no failing stage in either profile. These passing runs
preceded the planning fix and therefore cannot verify or explain that fix.

The updated candidate passed all eight interrupted-SUBSCRIBE and eight
interrupted-UNSUBSCRIBE product-process cases (266.172s / 224.640s): both
256-Slot topologies, inbox/group, and both intent/final-completion interruption
boundaries. The evidence verifies offline completion before reconnect, original
message/PacketID recovery and fresh delivery; UNSUBSCRIBE also verifies the exact
fixed closure observation. These controlled request failures are not abrupt
crash/partition proofs. See [the planning report](../reports/mqtt-plan-readiness.json).
The initial natural SUBSCRIBE failure and historical post-resume EOF remain open.

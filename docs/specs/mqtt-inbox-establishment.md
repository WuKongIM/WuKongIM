# MQTT inbox establishment

Status: establishment implemented; safe removal and complete product composition
remain required. The product listener remains disabled.
This continues the approved authoritative metadata, source and subscription seams.

## Failure inventory before implementation

- Commit a UID Preparing qualification before any directory scan or source effect.
  Future person appends see Preparing qualifications through the existing handshake.
- One call scans at most one bounded directory page. Other channel types and
  tombstones consume the same budget; progress includes the full native ID/type.
  Canonical person candidates must include the admitted UID. Malformed, duplicate,
  unordered, mixed-kind or inconsistent page evidence fails closed.
- Each source needs replicated protection, its fixed cursor and all-replica replay
  confirmation before the UID cursor can advance. Closed intent or malformed
  preparation is not success; pending replay retains the same boundary on retry.
- Each completed candidate commits progress independently. Lost qualification,
  progress or completion replies cannot reset boundaries, skip pending work or
  duplicate durable identities. Owner takeover within the lifetime can continue;
  old owners, replaced children and changed options cannot publish stale receipts.
- A receipt requires current exact intent, fresh self-inbox authorization and the
  completed qualification. Callback failures/panics, cancellation, lost ownership,
  bad CAS receipts, revision overflow and clock regression cannot activate intent.
  Every exit releases its owner scope and returns no receipt on error.
- Qualification completion is immutable. Active same-generation option replacement
  retains its established qualification without rescanning or recapturing starts;
  a resumed Preparing intent can advance the qualification intent revision.
- App/cluster integration must establish qualification through the real usecase,
  prepare existing directories before activation and cover a future first message
  while offline via the automatic shared Appender. No hand-written qualification
  or projection receipt may stand in for this establishment proof.

## Contract

InboxEstablishment implements the Establish half of SubscriptionProjection. It
reuses durable UID binding discovery fields, InboxSources and shared replay
confirmation. It owns no worker or queue. Unfinished pages return ErrReplayPending
for the existing bounded SubscriptionRequests controller; failures preserve debt.
Complete safe inbox removal, offline maintenance, rollout/restore fencing and
product lifecycle composition remain necessary for the full feature.

## Verification

The failure tests first failed without InboxEstablishment; app integration first
failed without its composition factory. The existing/future-source cluster test
uses real qualification and source preparation, default bounded subscription
requests and the automatic shared Appender. Its evidence records
`qualification_fixture=false`, `initial_existing_source=true`,
`automatic_append_hook=true`, `offline_accounted=1` and `hash_slots=256`.
It is app/cluster integration, not process-level product acceptance.

```sh
GOWORK=off go test -race -p 2 ./internal/usecase/mqttsession -count=1
GOWORK=off go test -race -p 2 -tags=integration ./internal/usecase/mqttsession ./internal/app -run '^(TestMQTTInboxEstablishment|TestMQTTInboxAppenderOffline|TestMQTTInboxAdmissionOffline)' -count=1 -v
```

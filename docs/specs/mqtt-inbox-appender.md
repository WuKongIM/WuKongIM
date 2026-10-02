# Automatic person append preparation

The approved inbox contract covers every ordinary persistent person message,
including native/API sends to a new conversation while the MQTT subscriber is
offline. A usecase-owned Appender decorator admits native directory work when
needed, drives bounded InboxAdmission turns, rereads its exact completed
checkpoint and binds that snapshot's runtime authority to the actual append.
App composes this before the existing cluster appender. Reusable channelappend
keeps its existing ordering, worker bounds, idempotency and shutdown ownership.

Failure inventory at the approved usecase, append and app-composition seams:

- The first business append cannot run before both UID registrations, source
  protection, cursor initialization and the final checkpoint. Offline Sessions
  must be covered without opening a connection execution scope.
- Missing/pending native directory must admit resumable projection work once per
  request; ready checkpoints must not recreate directory tasks or rescan sources.
- Each call has finite turn and time budgets; unfinished progress survives the
  returned error. Waiting is cancellable, and failures do not busy-loop or append.
- A final fresh read must still identify the exact completed checkpoint, current
  directory and caller epochs. A caller-supplied route cannot be silently replaced.
  Changed, missing, malformed, mixed-kind and stale evidence must reject append.
- Group and all-SyncOnce append batches retain their existing path; mixed person
  batches cannot use a command item to skip ordinary admission. NoPersist never
  reaches this durable seam. The decorator must not mutate borrowed message data.
- Preparation callback panic/cancellation cannot append. Once append starts,
  its committed receipt/error retains ordinary semantics and possible-write
  recovery; cancellation after a committed receipt must not erase success.
- Actual native SEND must invoke preparation automatically with no explicit
  admission call in the test. Complete cluster/projector ports are required;
  default native composition remains unchanged while MQTT product rollout is off.

This reuses the approved append and authoritative metadata boundaries; it is not
a new protocol entry. The private composition gate is temporary until complete
MQTT configuration/lifecycle is installed. Full initial inbox projection, safe
runtime deletion/restore activation, offline scheduling and process E2E acceptance
remain required; the product listener is not enabled by this component.

Validation: the usecase tests first failed to compile without InboxAppender.
The native integration test then failed on the missing admission checkpoint while
the private gate had no wiring. After composition, the same native SEND completes
without any explicit directory/admission call, followed by real shared replay and
one offline pending message. Its qualification is still an explicit fixture.

Repeatable checks:

```sh
GOWORK=off go test -race -p 2 ./internal/usecase/mqttsession -count=1
GOWORK=off go test -race -p 2 -tags=integration ./internal/usecase/mqttsession ./internal/app -run '^(TestMQTTInboxAppender|TestMQTTInboxAdmissionOfflineDirectory|TestMessageSendDoesNotSynchronouslyAdmitPersonDirectory)' -count=1 -v
```

The test evidence line records `automatic_append_hook=true`, `hash_slots=256`,
`offline_accounted=1` and `product_listener=false`. Pending-directory integration
checks turn exhaustion, total deadline and cancellation without a business append.
This is app/cluster integration coverage, not process-level product acceptance.

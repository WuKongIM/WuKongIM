# Typed MQTT retirement admission

The native retirement committer accepts a whole-anchor selection bounded by an
anchor captured before a fresh consumer-floor read. Product orchestration owns
that ordering and fresh Slot authority; this native port does not infer consumer
permission from local storage, age, absence or request fields. It must remain an
internal trusted port. [Ordered production](mqtt-retirement-production.md) now
supplies permission through routed admission; automatic scheduling remains pending.

A request carries current full placement, the captured and selected anchor proofs,
consumer Through, and a server control identity. Under the installed Channel
sequencer, the adapter checkpoints only its own HW and independently reloads both
immutable committed anchors and the latest retirement. Unsupported stores, future
or changed proofs, different installed membership, write fences and unready owners
fail closed. Each read is bounded; no body is read, copied or deleted.

The canonical command hashes source plus selected Through. Already retired equal
or older responsibility returns its existing committed proof without appending;
equal prefixes must retain the exact reference. An unresolved same-command retry
uses the original retained payload and server identity. Other pending commands
backpressure. New decisions use the ordinary format-6 quorum path. Completion
reloads the committed journal; caller cancellation never invents a committed reply.

## Failure inventory before implementation

1. A consumer floor is rounded upward, a captured/selected proof is forged or
   foreign, a future anchor passes, or malformed placement claims write authority.
2. Admission bypasses installed authority, quorum, write fences, capacity, source
   activation or independent committed anchor/journal proof.
3. Concurrent, delayed and post-restart retries create duplicate controls, change
   the original ID/timestamp, regress a retired prefix or overwrite pending work.
4. A timeout/partial quorum is reported committed; restored voters cannot complete
   the exact pending control; learners improperly vote or lose the journal.
5. Source cleanup or reopen loses retry evidence, store leases leak, or admission
   itself mutates shared replay baselines/bodies or consumer metadata.
6. A candidate below a newer retirement fails harmless retry, while an inconsistent
   equal reference is accepted. A success grants more consumer authority than the
   independently committed existing decision.

Tests precede code at typed contracts and the real-disk native quorum runtime,
using actual exchange codecs, three voters and a learner. Consumer-floor admission
is controlled in this fixture. [Reactor queue admission](mqtt-retirement-reactor.md)
and [fresh RPC routing](mqtt-retirement-routing.md) now use this port; usecase/product entry wiring and process-level
acceptance remain required. This is not complete product admission.

## Frozen context

Source `58b8647fb868699c3c9ad5aab3524b8e4158a8ea`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/channel/FLOW.md`: `5e8027cf3f5e60d31c3351750d350b54a1ea7e525f22601b78b79fae5ea0607c`

# Bounded replay coordination turns

The MQTT replay usecase connects fresh Channel metadata, accepted-prefix planning,
quorum copying, anchor admission and exact-target recovery through narrow existing
ports. One turn performs either one bounded copy plus anchor acceptance, or one
recovery step on one current replica. It does not create protection, release source
history, promote learners, deliver messages or authorize subscription completion.

A body-free process cursor binds the source incarnation and complete placement
hash, with at most 256 replica continuation entries. Copy and recovery alternate
while new content exists. An idle source performs recovery without generating
anchor-only traffic. Each replica pins its own target anchor until completion;
newer anchors do not reset an unfinished interval. Every attempted replica yields
to the next, including failures, so an unavailable node cannot starve its peers.
Scan/donor continuations survive rotation; import requires a later coverage read.
Changed source/placement restarts only hints, deriving content progress from storage.
The caller owns a bounded number of these cursors; they are never persisted truth.

The result retains a detached continuation even on an operation error, so a failed
copy yields to recovery on the next continued turn. A caller continuing the same
source visit must keep that continuation. The bounded background scanner may end
the visit on failure and seed its next cold visit from a rotating discovery pass;
all content progress still comes from durable planning. See [worker scheduling](mqtt-replay-worker.md).
Current metadata/plan validation precedes work; explicit row/byte/time limits and
cancellation checks bound the turn. Copy receipts must match the planned starting
prefix and stay within its captured frontier. Only validated quorum receipts reach
anchor admission. Server control identities use the app allocator. Every network
port independently rechecks fresh authority; an uncertain result is retried from
new authoritative planning, not interpreted as durable absence.

App composition reuses the existing SlotMetaSource fresh reader and foreground
Node APIs. No protocol packets, concrete infrastructure construction or per-source
goroutines belong in the usecase. Background lifecycle/fair source scheduling,
source release and the complete product listener remain subsequent requirements.

## Failure inventory before code

1. A foreign source, malformed/weak placement or changed membership reuses old
   continuation; oversized cursors or aliased returned slices escape work bounds.
2. Copying starts at local copy-ahead or past a captured frontier; malformed or
   foreign receipts reach anchor admission; a lost commit reply creates duplicate
   controls instead of rereading accepted progress.
3. New messages or anchors continually reset replica recovery; one unavailable
   target monopolizes the source; donor/scan continuations are discarded on rotation.
4. Import is mislabeled complete, idle tails grow controls, or an error erases
   the next-action hint and permanently favors failing copy over repair.
5. Cancellation, invalid timestamps/IDs or dependency failures permit a later
   effect; outcomes falsely claim source release, readiness or SUBACK authority.
6. Composition passes fake local authority instead of actual Node/Slot/RPC work.

Tests precede implementation at usecase contracts and the existing real three-node
app composition seam. Full product E2E and background worker ownership remain open.

## Frozen context

Source `31903e7c6`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `internal/usecase/mqttsession/FLOW.md`: `d2093be88d8bb09602137215a87330aba9ce1cc17ad5c34438b9fb584e6cc0f2`
- `internal/app/FLOW.md`: `0d8a01b8041db4a3109bdea5e61c324006e2d6f65bd5638790d6e39acef19e92`
- `pkg/channel/FLOW.md`: `2ba2d86f1b4ac413c66f873546b60f5dc955c60814185b2f866184fda618bcfe`
- `pkg/cluster/FLOW.md`: `756ea5d026cae3194d7ccdda2c778c8aa979b755a2a7bb8531b30cb4064ce877`

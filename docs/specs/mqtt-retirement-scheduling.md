# Bounded automatic MQTT retirement scheduling

`ReplayMaintenance` composes the existing copy/recovery coordinator and ordered
retirement producer. A cold source pass rotates copy, recovery, retirement.
Copy/recovery passes map onto their original two-phase target/donor sequence,
preserving fair replica rotation. Retirement pages use the separate producer
cursor; neither phase may consume the other phase's hints. App requires both
capabilities when constructing the existing replay worker.

The same managed worker and joined lifecycle remain in use. Each hash Slot
retains at most one advancing finite journal continuation, whether forward
recovery or reverse retirement. Retirement continuation requires an unchanged
source, pass, authority, capture and floor, with a strictly decreasing positive
exclusive position. Mixed outcomes or mutable targets fail closed. Durable
commits, exhausted scans, permission changes and errors yield to later sources.
Every resumed retirement turn still rereads ordered consumer permission.

Aggregate observations count successful retirement commit operations, including
idempotent retries, separately from replica completion. They do not prove cleanup.
No per-source map, goroutine, unbounded queue, new table or wire format is added.

## Failure inventory before implementation

1. Retirement is never reached, or its insertion repeats/skips recovery targets
   or donor rotation. A failing copy/source starves other phases or sources.
2. A reverse scan moves forward, changes its capture/floor/source/pass/authority,
   mixes retirement and recovery hints, or claims a simultaneous durable result.
3. Continued scans grow memory with source cardinality, lost Slots/sources keep
   old hints, or stop/restart overlaps work or retains process scheduling state.
4. App silently wires only copy/recovery; no background decision is produced
   without a test manually invoking the producer.
5. Background retirement ignores real ACK gaps or unknown registration, or
   committed decisions fail to reach/apply on all source replicas while idle.
6. Observation counters are mistaken for durable storage evidence. Real storage
   must independently show the replicated decision and applied retired baseline.

Tests precede implementation at the approved usecase/runtime and app/Node/storage
seams. Controlled subscription/window admission is retained; product listener,
complete start/stop/restore composition and process/load acceptance remain required.

## Frozen context

Source `ac70e33dbfe59c4776db2826d137e861be1686a2`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `internal/runtime/mqttsession/FLOW.md`: `2e641f7f6a79ada5b4d78334c598e33065d07c154bf368bf4a734f2d0ebe54c7`
- `internal/usecase/mqttsession/FLOW.md`: `0dd3ff75ea157671be24a2dc7b9b2d8dede9871c689c13fef2a24773f162763e`
- `internal/contracts/mqttsession/FLOW.md`: `1ced45463dabe7fa9fb4168e9bd4b3eabb09d1ee2041d31d0b6ff8f46ed4caf7`
- `internal/app/FLOW.md`: `85ff635dfe8d3b5dc270c6a32a2b634e6f60d1b4c21b0997e1987344da675678`
- `pkg/channel/FLOW.md`: `f409f21633ab545ec268bd3914b71c0882023447a98c56e2ab89a161c6fd281e`
- `pkg/db/FLOW.md`: `3560287fef837ef40dcf04754ac1037a28ec85d26dae7748f465ab9524c624fe`
- `pkg/db/message/FLOW.md`: `de30b69972ef9e9446bc781fdb12d150cc92c90c526cb3d4b1af816c6ba863f1`

## Native repair prerequisite discovered by the app scenario

After source release and physical original-history trim, clearing the fixture's
migration fence installs another native authority. Native learner repair starts
at position one; that body has already been removed even though the learner has
the exact prefix. A later tail merges into the stuck earlier repair. The failing
app evidence reopens voters at LEO/HW 8 and the learner at LEO/HW 5, without a
retirement proof (`/tmp/mqtt-retirement-scheduling-app-diag.log`).

Before changing native repair, reproduce with real stores: catch up a learner,
trim an original prefix at the leader, then install another fenced authority.
A failed bounded fetch may perform one read-only follower probe and one bounded
local identity read. Only an exact match under the unchanged local frontier can
advance the repair hint past the already-durable follower tail. An unknown,
malformed, changed, ahead or mismatched tail cannot skip missing history; a probe
never installs authority or gives a learner a vote. If the tail is already at the
repair target, commitment must also be proved or the final proposal replayed.
The existing work-generation checks still fence any retained progress.

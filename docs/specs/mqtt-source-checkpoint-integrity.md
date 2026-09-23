# MQTT source checkpoint integrity

This prerequisite closes storage escape paths found while designing replicated
source activation. It adds no tables, commands, source authority or product
listener. Source System 12 remains a replica-local materialized decision.

An installed source requires its explicit checkpoint. Missing checkpoints,
invalid checkpoint shape or a checkpoint below copied-through are corruption,
including on reads and otherwise idempotent checkpoint writes. An ordinary
writer cannot repair this evidence by recreating a checkpoint. The raw legacy
checkpoint setter must not regress a protected channel's committed high
watermark. Channels without source protection retain the legacy setter contract.

Source/checkpoint validation reads the source first: initial activation writes
its source and explicit checkpoint atomically, and reading a missing checkpoint
before a newly installed source would manufacture a false corruption report.
Validation uses bounded point reads, with one additional source-state read per
checkpoint load. It does not scan history, create per-channel workers or retain
unbounded cache state. Checkpoint mutation owns the canonical checkpoint mutex;
suffix truncation owns append then checkpoint until physical commit, so a
concurrent checkpoint advance cannot invalidate a previously checked cut.

## Failure inventory before code

1. The raw typed or compatibility checkpoint setter lowers HW and allows a
   subsequent suffix cut to erase a formerly committed protected message.
2. A missing explicit checkpoint is treated as genesis by raw/monotonic/HW-only
   setters, batched checkpoint updates, fetched append, snapshot install or
   exact-proposal retry. A failed operation writes rows, history or a snapshot.
3. A malformed source, malformed checkpoint or copied frontier above HW is
   silently repaired; an idempotent HW update hides the same corruption.
4. Protected reads or source advancement turn corrupt evidence into successful
   progress. Native channels lose their original non-monotonic setter behavior.
5. A suffix cut checks old HW, a concurrent writer commits a higher HW, then the
   cut deletes the newly committed protected suffix. Both truncation facades
   need the same checkpoint fence; recovery replacement already holds it.
6. Rejected operations leak append/checkpoint locks or alter durable state;
   valid checkpoint advancement, exact retry and shared replay stop working.

Focused storage regressions are required because these paths deliberately
inject missing/corrupt physical keys and orchestrate canonical mutex ownership;
a public MQTT process test cannot deterministically create this evidence.
They do not replace the still-required product process acceptance suite.

## Frozen context

Source `6d462a582`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/db/FLOW.md`: `8b723e6f1402c2361a872e3a6e4021ac4146782ebc9b416cb7b9cc9787bb9d75`
- `pkg/db/message/FLOW.md`: `105c4fa3d871b261c3920f2658476e6479989ae181466e5e2e13f17d115a011b`

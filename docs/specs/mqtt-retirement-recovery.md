# MQTT retirement application during recovery

Explicit RPC-99 version 3 requests apply the latest locally committed retirement
before repair planning or contacting a donor. Versions 1/2 retain their existing
bytes and behavior. The target reads one pinned HW/source/journal proof, rechecks
fresh placement, then independently applies that exact committed decision under
storage locks with a fixed 64-primary-row budget. No request carries a consumer
floor or a caller-supplied deletion position. Generation and historical authority
must agree; missing capabilities, corrupt evidence and changed authority fail.
A newer retirement committed concurrently is discovered on a later turn.

The plan still describes logical target coverage. A separate RetirementPending
flag reports remaining bounded physical cleanup; direct coordinator visits retain
the target anchor and rotate fairly. The managed worker yields to other sources
after each durable cleanup step and resumes from storage on its next cold pass;
it must not label cleanup as a journal-scan continuation. Target completion waits
for cleanup even when no body needs repair. Source release remains an independent explicit request. Cleanup
uses existing durable cursors, with no new schema or new native control writes.

## Failure inventory before implementation

1. A pending or foreign decision is returned as latest; missing source, anchor,
   journal, paired proposal or native identity is accepted, including after trim.
2. Latest lookup scans an unbounded history, changes storage, leaks its snapshot
   or returns partial proof after cancellation/close. Restart loses discovery.
3. Recovery fetches already retired bodies before applying its own decision,
   accepts caller authority as proof, or mutates after placement/fence change.
4. Ordinary v1/v2 requests unexpectedly retire content; v3 silently downgrades,
   accepts unknown/trailing flags or acknowledges cleanup without intent.
5. Cleanup exceeds its fixed budget, unsupported stores silently fall through,
   or a storage failure/panic leaks the admission slot or store handle.
6. Logical coverage hides pending cleanup; coordinator drops target progress,
   starves other replicas, or native HW/source release is fabricated by cleanup.
7. A learner missing all retired bodies cannot recover the retained suffix from
   pruned donors, or restart and repeated exact steps resurrect retired content.

Validation first uses failure-injected boundary tests and real-disk storage plus
cluster-service integration; the existing three-node TCP recovery test exercises
version-3 routing. Consumer retirement admission and complete product MQTT E2E
remain required; controlled committed decisions in these tests are not producer
or product acceptance evidence.

## Frozen context

Source `22c055699d08d64211cbd74fd14dbe3c6538f46b`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/db/FLOW.md`: `3560287fef837ef40dcf04754ac1037a28ec85d26dae7748f465ab9524c624fe`
- `pkg/db/message/FLOW.md`: `b0477caabee93c966fcc0de2e9f76d1d36f3f3d4474f5cd6f2aa6f2e5be50461`
- `pkg/channel/FLOW.md`: `ffcd2a1ebc577979ae7755f3610072db1e43a0ec05c59ce02f69afa4389d4d45`
- `pkg/cluster/FLOW.md`: `8933302e2da476a5f0eb371f14f21779d65fd8011033bdea5dab55c81dcff2d5`
- `internal/usecase/mqttsession/FLOW.md`: `d571a1d557bf2f638897ef6f0bbd129e01d9f01fbd00f2ce3c2981770abe4f35`
- `internal/contracts/mqttsession/FLOW.md`: `c4b054833519113b67d9eac40d424e272b43414902139eb8d89bdd15ff2106c2`
- `internal/runtime/mqttsession/FLOW.md`: `2e641f7f6a79ada5b4d78334c598e33065d07c154bf368bf4a734f2d0ebe54c7`

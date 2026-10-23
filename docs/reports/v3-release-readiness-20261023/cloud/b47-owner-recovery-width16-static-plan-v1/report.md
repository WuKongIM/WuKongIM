# Bounded owner-recovery width: static proposal, not an executed experiment

Source b47c03d461a6d8918e537e027bf56539ce78c585 / tree 7ce68e726fea61bd6d176fbc7053dede920d5b00, clean before/after. No Go, tests, build, workload, profiles, network, cloud or repository mutations were performed. Parent-supplied trace observations motivate this hypothesis; overlapping duration sums do not establish wall-clock saturation or exact RPC causality.

## Feasibility and limits

A diagnostic C16/K8192/owner16 candidate is mechanically compatible with the existing ownership protocol, provided **global owned bytes are explicitly fixed to 128<<20**. Current recovery_flights.go:14–19 derives bytes from cohorts×32MiB; changing only C would silently permit 512MiB. Keep page bytes32MiB, 512 UID/256 target packing (recovery.go:84–100; owner_recovery.go:84–96), and all proofs/fences unchanged.

Admission is atomic for the entire new subset; it waits without partial reservation, clones each distinct admitted UID once, and returns all accounting on completion (recovery_flights.go:124–194,255–270). Thus C16/K8192/128MiB bounds retained flights and their cloned string bytes. Metadata cardinality can grow fourfold; maps, headers, Directory/input/RPC data, waiting callers, allocator rounding and GC are outside that byte ledger. This is **not a whole-heap/RSS or Mac memory safety guarantee**. Local headroom remains unverified.

The matching adapter gate is currently4 (presence_recovery.go:33,70–75). Each admitted round queries owners serially (104–107), so16 rounds do not imply16×member-count simultaneous synchronous reads per authority node. Preserve owner250ms, plan5s (delivery/runtime.go:17,505), retry50/backoff100ms and the distinct endpoint-leader fanout4 (infra/cluster/presence.go:52–54). Keep shared mixed-purpose services13/14 outside CancelRunning=true (transport.go:334–357); canceled server work may continue. More width can move waiting into transport, scheduler or owner execution and increase interference; it does not increase CPU or storage capacity. Fairness remains wake-and-recheck, without FIFO admission guarantees.

## Minimal tests FIRST, freeze identical RED/GREEN files

1. Exported recovery path, real Directory, controlled external owners: hold16 short-UID cohorts on distinct targets, prove all16 enter;17th waits/cancels, no leaked reservation. Current4 must fail the positive fifth-entry expectation. Adapt, do not remove, the existing quota/proof-recheck test (uid_flight_integration_test.go:310–382), retaining proof-install-before-wake and follower cancellation.
2. Independently prove adapter owner gate16/17 with controlled member reads and cancellation/release; otherwise widening only one gate merely relocates its queue. Timed concurrency fixtures belong in integration tier, join every goroutine.
3. Exercise now-independent byte quota with free cohort/key capacity: four legal512×65535-byte pages consume128MiB−2048 bytes; one new2048-byte UID fills128MiB; another byte must wait, then admission resumes after release. Keep existing32MiB/page rejection and full legal-page coverage. This fixture itself needs over128MiB total heap; check its resource admission before running, without claiming the ledger bounds its RSS.
4. Cover16×512 distinct short keys=8192, overflow waiting/no partial ownership, dedup charging and complete cleanup. Retain existing target fences, owner boot/sequence, tombstones, partial proofs, FIFO eviction, leader/follower cancellation, and completion-before-old-flight-join regression cases.
5. Freeze scope diff: only these two gates, corresponding key allowance, explicit unchanged byte cap, necessary tests/comments/FLOW/knowledge. No new option framework; all timeout/retry/page/transport mutation semantics and parser/report/verdict gates stay unchanged. Run directly related normal/race/integration validation and applicable named FLOW check before any candidate process run.

## Finite paired process experiment: root decision pending

After #1032 merges, freeze the exact b47 baseline binary (or verify merged-source equivalence explicitly) with existing native toolchain/build provenance, original config SHA308ecc916ed701b1d7b2f47e15c32f2bd8aa1b1ec43b42ec98dfe58a95a52694, full10000 online/2000 groups/500 SEND/s/batch1, product GOMAXPROCS2 and all existing coordinator settings. No profiles or new instrumentation. Run baseline **once**, fresh DB,600s outer bound/120s measured target, unchanged cleanup and all original acceptance gates.

Require a complete valid final RED for the old message_loss and exact accepted-message/sequence expiry joined to presence failure. Other failures, missing final evidence or a passing baseline stop this comparison as inconclusive; do not treat a different gate as the old symptom.

Only after that control and tests/review, freeze width16 candidate and run **once**, sequentially on the same host with fresh DB and identical workload/environment/bounds/cleanup. Keep first-failure evidence, complete final report, accepted QPS, loss/expiry joins, existing fixed public queue/task/client result cuts and their absent/error states; do not infer per-request wait from aggregate histograms. Verify owned PIDs/listeners are gone after each arm. No automatic reruns, tracing, CPU captures, cloud starts or budget changes.

A valid candidate pass following a valid old-symptom control supports a narrow local width hypothesis, not sole root cause, Linux acceptance, long stability or GA readiness. If16 still fails, stop this hypothesis; no further width or byte-budget escalation.

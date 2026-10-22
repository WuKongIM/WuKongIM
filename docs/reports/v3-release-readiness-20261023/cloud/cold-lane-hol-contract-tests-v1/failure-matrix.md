# Failure matrix recorded before test implementation

Source2ab3e1a8bd95670603bc38314036c797a5124682. Approved seam: exported NewAuthorityRecovery(...).EndpointsByTargets; real runtime Directory with RequireRecovery=true; controllable owner-read port. Test package is external presence_test, integration build tag. No manual lane mutation, production hooks, implementation edit, server process, sockets or real long sleep.

The diagnostic property is that unrelated cold UID B can complete while another cold page A is awaiting owner I/O. Existing FLOW explicitly documents lane admission; it does not promise independent UID latency. An observed RED proves coarse admission HOL, not by itself a correctness bug or cloud root cause.

| Case | Controlled owner boundary | Expected diagnostic/control result | False explanations excluded |
| --- | --- | --- | --- |
| Proposed isolation (intentional RED on current code) | A page carries32 target hash slots and owner read pauses after entry; B uses another UID at same complete target as one A group; fake permits B immediately | B completes before A is released; current code expected to fail with B context deadline | Gate event proves A reached owner read; B never shares A UID; owner fake has no saturated global gate |
| Non-conflicting lane control | A covers only hash slot0; B at hash slot1, independently cold | B completes before release A, both queries return real Directory-validated routes | General serialization, broken owner fake, missing directory authority |
| Same UID coalescing control | A owner read paused; concurrent B has the same target+UID | Neither gets a fabricated ready result while A is held; after release both receive same real Directory route and owner boundary sees one read | Duplicate owner work or invented proof; note a bounded scheduling guard is not visibility into private waiter state |
| Canceled same-UID waiter control | A paused; B joins same public lookup then its context is canceled | B promptly returns canceled; A owner context stays live, then A completes after release | Cancellation leaking from one waiter to owner work |

Common failure checks: setup/start watchdog failure is not the expected RED; owner errors/results, complete route fences and returned UID alignment are checked; every test cancellation releases A and joins owned goroutines, including assertion failure. Owners return complete positive results only after gate release; no fail-then-succeed mirror fake.

Existing TestAuthorityRecovery tests in the same focused run cover failed owner proof, negative proof caching, old-term completion, aligned sibling errors and512/512/1 page preservation. New quota/tombstone implementation scenarios are outside this tests-only task; they remain required before any production redesign. No threshold/deadline/retry changes are proposed here.

One focused go test command will run new cases and the existing AuthorityRecovery contracts. Process wrapper hard timeout120s, Go test timeout90s. Integration assertions use channel gates and bounded timers, no time.Sleep. No implementation will be changed to turn the deliberate RED green.

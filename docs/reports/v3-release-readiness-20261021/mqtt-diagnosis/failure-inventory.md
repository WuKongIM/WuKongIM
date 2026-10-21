# Failure inventory before changes

Exact observed failure: TestSourceDrainProgressBeforeSealDoesNotClose/canceled reports Unconfirmed + SourceDrainPending after root cancellation, instead of cancellation without pending. CI log lines 1612–1621.

Ranked falsifiable hypotheses before testing:
1. Nested owner/subscription context only checks its immediate derived parent, whose AfterFunc cancellation is not synchronous. A root with intentionally held callbacks should reproducibly expose pending; checking original ancestor synchronously should stop at first effect boundary.
2. Test fixture cancels another context or source CAS interception bypasses real flow. Recording original Err and using the existing delayedCancellation context through public Unsubscribe should disprove this if genuine nested operations still see cancellation too late.
3. Inner cancellation is overwritten by complete/finish pending handling. Force cancellation/error at projection and inspect returned chain; if inner already returns canceled but pending survives the replacement is outer handling, otherwise fault precedes outer handling.

Required invariants: root cancellation must dominate pending classification; no source sealing/read/write after canceled effect boundary; committed Removing intent retained; no inline source CAS retry; operations released; noncanceled monotonic contention remains pending; unknown/rejected/changed-owner cases remain conservative errors. Do not weaken existing assertions or infer a fixture flake from retry success.

## Evidence after hypotheses

- Original canceled case: 30/30 passed locally; CI failure remains preserved. This is not proof of a fixture-only bug.
- Same real public path with the existing delayedCancellation parent: 3/3 failed with the exact CI Unconfirmed+SourceDrainPending chain.
- One bounded pre-fix probe: original parent Err=context.Canceled, source CAS calls=1, three additional source qualification reads after cancellation. Probe removed from final sources.
- H1 confirmed: subscriptionOperation stores only direct parent, while Owners.Begin creates operation context from owner context and forwards cancellation through AfterFunc. Three nested scopes lose synchronous original-root visibility.
- H2 contradicted: original parent already canceled at the real source rejection, and held-callback test still goes through real progress/CAS/storage.
- H3 not the primary cause: inner qualification performs three reads and creates pending; there is no observed inner canceled error replaced by pending. Outer complete's failed completion read retains that pending, exposing H1. Fix fences nested effects, rather than relabeling final errors.

## Minimal fix boundary

A private context key carries only the existing ancestor subscription scope. Each nested begin checks its ancestor before admission and each operation check verifies all ancestor parent cancellation and owner fences, then its own. Existing deadline min(parent, timeout), cancel/done lifecycle and runtime owner admission remain unchanged. No business context-value copying, new goroutine, retry or background work. Concrete failing call chain has three active scopes: Unsubscribe, GroupProjection.project, SourceDrain.SealGroup. Scope ancestry is acyclic (constructor references only an already-created parent); admitted same-owner scopes remain limited by Owners.MaxOperations.

The final regression retains Removing intent, source CAS count exactly one, no fresh qualification reads after cancellation, and the exact progressed binding with EndKnown=false. All original positive/negative modes remain intact.

FLOW already required synchronous parent cancellation; one sentence now explicitly includes the original caller across nested projection/source scopes. Root will run the policy-defined flow-doc-contracts named check and add Unreleased: Fix MQTT unsubscribe cancellation being mistaken for pending source cleanup when cancellation propagation is delayed.

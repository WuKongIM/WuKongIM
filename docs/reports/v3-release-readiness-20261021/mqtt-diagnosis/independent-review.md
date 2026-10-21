# MQTT nested cancellation — independent read-only review

2026-10-21. Reviewed the current uncommitted change in `v3-release-recovery`, based on `5f3ecdf93e762a1b1551b44977a44f2e63c97802`. This review did not edit repository files, execute tests, run product processes or dispatch workflows.

**No actionable issue found in the reviewed change.** The private ancestry fixes synchronous observation of original caller cancellation across existing nested owner scopes without relaxing owner admission, deadline limits or durable source evidence.

## Evidence

- Read root `AGENTS.md`, package-scoped `internal/usecase/mqttsession/FLOW.md` and `internal/runtime/mqttsession/FLOW.md`. The changed FLOW sentence accurately describes the stronger cancellation boundary and adds no authorization or completion promise.
- `subscriptions.go:341–350`: each scope first checks its immediate parent, recursively checks the earlier scope, then revalidates its own runtime operation. Every ancestor therefore still performs its own owner/deadline fence through `runtime.Operation.Check`; a parent failure only prevents further work.
- `subscriptions.go:353–373`: ancestor checking occurs before a new scope is admitted, but every child still calls `Owners.Begin(parent, requestedOwner)`. The private context value is not substituted for owner identity, UID, permissions or a durable receipt. `owner.go:269–307` continues exact owner lookup, active/lease checks and per-owner operation capacity admission.
- `subscriptions.go:367–373`: child deadlines remain the minimum of its configured timeout and the supplied parent's deadline. The returned context derives from `op.Context()` and carries only the unexported ancestry key; arbitrary business context values are not copied into the owner context. UID and durable session checks remain separate (`subscriptions.go:399–433`, `group_projection.go:87–96`).
- `subscriptions.go:376–381` and `owner.go:313–327`: the existing cancel/Done lifecycle remains on all scoped exits, including panic handling. No goroutine, retry queue or background work is added. Each constructor links only an already-created scope, so the normal nesting graph is acyclic; the concrete Unsubscribe → GroupProjection → SourceDrain chain is finite, and each live same-owner scope still consumes the bounded `MaxOperations` capacity. Done ancestors remain fenced rather than reusable authority.
- `source_drain.go:352–395` and `source_drain_contention.go:13–68`: the original pre/post dependency checks now see caller cancellation before the fresh contention-qualification reads. The source CAS evidence, same-owner/child/binding checks and pending classification remain unchanged. Background closed-intent scopes still use their separate durable qualification path and gain no live owner capability.
- `source_drain_contention_test.go:16–36,82–87,128–145`: `canceled-delayed` uses the existing held-AfterFunc parent through the real public Unsubscribe/projection/drain path. Both canceled modes require cancellation identity, zero post-cancel qualification reads, one source CAS, zero retained operations, Removing subscription intent and the exact progressed binding with `EndKnown=false`. Original noncanceled and unknown/conflict/changed-owner controls remain present. This is an effect-boundary regression, not a final-error relabeling test.

## Limits

This is a read-only implementation review. I read the retained failure inventory and red/green contention-log outcomes, but did not independently rerun the author's tests or certify pending CI. The new regression directly covers delayed cancellation on the three-scope source-contention path; this review does not claim new exhaustive test coverage for every nested deadline/owner-fence combination.

Cancellation checks do not revoke a previously committed or concurrently admitted durable effect, and the change makes no such claim. The test deliberately preserves prior Removing/progress state and continues to return Unconfirmed where appropriate. MQTT's broader release qualification remains outside this review.

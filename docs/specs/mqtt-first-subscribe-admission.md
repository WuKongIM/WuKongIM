# First MQTT group subscription admission

## Authorized seams and failure inventory

The operator approved repeated real-process E2E through Product HTTP provisioning
and send, independent Paho MQTT 5, and fixed public metrics. Reuse the MQTT
worktree at source revision `49c74b1c0`; freeze governing files in the report.
No isolated test or product change precedes the first black-box reproduction.

Failures to distinguish before implementation:

1. A valid first SUBSCRIBE times out or closes before successful SUBACK, for a
   group with empty or existing native history and local or remote ingress.
2. Permission or projection waits exhaust the shared five-second request bound.
3. Background recovery races the same intent and produces an unconfirmed reply,
   false conflict, changed cursor, or active intent with a lost SUBACK.
4. Owner-operation pressure, cancellation or reply admission closes a connection
   after otherwise successful establishment.
5. A repair acknowledges before exact replicated source/intent/permission proof,
   retries an unknown write, changes the original start, or enlarges deadlines.
6. SUBACK succeeds but a future QoS 1 native publication is missing or has wrong
   message identity. Repair must preserve the existing cluster authority paths.

## Repeatable feedback loop

Each topology uses 256 hash slots. Twenty rounds each create two new groups,
one empty and one with native history, and new ClientIDs. Every client issues
one initial SUBSCRIBE; three-node ingress rotates. Existing server and Paho
five-second deadlines remain unchanged. Each successful SUBACK is followed by
one independently verified native QoS 1 delivery. Emit bounded per-round phase,
latency, outcome and fixed closure metric counts, without identities/content.

`WK_E2E_BINARY=/tmp/wukongim-mqtt-subscribe WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-subscribe-admission GOWORK=off go test -p 1 -tags=e2e ./test/e2e/mqtt/subscribe_admission -count=1 -timeout=25m -v`

Initial root cause is unconfirmed. Preserve red outcomes and use external bounded
diagnostic builds when existing metrics cannot distinguish the awaited stage.

## Evidence-backed repair candidate and parallel failure inventory

The first empty-group packet is minimized to one first SUBSCRIBE on node 3.
Two independent runs fail at the existing five-second Paho/server bound. An
external overlay measures first source preparation at 1.68 s; replay confirmation
returns pending twice, then succeeds just as final checks are canceled. Permission
reads take tens of milliseconds and no Owner capacity/conflict is observed.

A repair candidate explicitly opts the product's concurrent-safe cluster adapter
into at most four joined replica-confirmation calls. Other providers default to
serial execution. Before implementation, account for incomplete/unavailable or
wrong-anchor replicas, stale final placement, cancellation, dependency panic,
unknown errors mixed with temporary pending, and a slow admitted call still live
when another finishes. Every call joins before return; no partial quorum proof,
unknown retry or deadline enlargement can authorize SUBACK. Bounded per-call
cohorts and at most 256 result slots replace the serial sum of independent waits.

The original 40-case run also contains future-delivery failures with zero-expiry
Clean Start Sessions and seven setup failures after its five-minute context.
Those are preserved. The minimized historical reproducer uses the restore
fixture's persistent Session parameters and its existing 30-second receive bound;
only the initial five-second subscription verdict diagnoses the present symptom.

## One admitted anchor followed by fresh confirmation

The parallel-only candidate still fails the first packet of the full three-node
matrix. Its single-node 40-case result passes. A successful copy/anchor currently
forces a new projection turn, which repeats source/intent/permission work before
all-replica proof. Evaluate continuing one known committed anchor within the same
bounded Confirm call, with a fresh authoritative plan and unchanged placement.

Before implementation distinguish: copy/anchor error or unknown reply; no anchor
admitted; read cancellation/pressure/unknown failure; changed authority/fence;
changed source boundary or regressed committed progress; one bounded copy still
below the required start; incomplete/wrong-anchor replica; and final placement
change. Never submit a second copy/anchor in that call. Only a successful bounded
step, fresh valid captured plan covering the unchanged start, every eligible
replica and the existing final placement fence can produce confirmation. Existing
isolated confirmation contracts are adjusted test-first for the new phase; they
must retain incomplete-proof and unknown-outcome failure semantics.

## Captured plan superseded by a successful bounded step

A retained three-node trace shows the initial plan has no anchor, while Step's
fresh plan sees a background-committed anchor and successfully recovers one
replica. Step returns no error but `Anchored=false`; Confirm unnecessarily yields
and repeats about 0.5 s of source preparation. A second trace confirms this
interleaving. Expand the fresh coverage phase after any successful bounded Step,
including recovery or an idle step; do not infer confirmation from Step's flags.
Before implementation require fresh all-replica proof for a background anchor,
retain fixed source/placement, yield for no/insufficient anchor, and preserve all
Step unknown/error/cancellation stops. No second Step or copy may run in the call.

## Request-owned preparation continuation

The latest ten independent cold runs retain a tenth five-second failure. Preserve
that red outcome. Prepared source/cursor establishment is positively committed
before replay pending, but full projection repeats it on every existing request
turn. Retain one body-free prepared source/start in the caller's existing wait
loop, bound to the complete immutable Preparing child, exact Owner and UID.
This is a private per-request continuation, not a durable/global grant or queue.
Keep the existing 64-turn/five-second bound; no new loop or retry is introduced.

Before implementation distinguish: cache miss/new request; changed child/Owner/UID;
permission version/denial after pending; renewed same Owner/parent revision;
source boundary stability while tail advances; canceled/fenced scope; unknown
preparation/replay result; and partial replica proof. Only positive completed
preparation may be retained. Each turn still rereads Owner/intent, reauthorizes,
confirms fresh placement/anchor/all replicas and performs the unchanged final
activation checks. A new packet must prepare from durable evidence again.

## Reuse the current captured plan for the bounded copy

A retained cache probe proves exact Owner/UID/full-child reuse. Preparation takes
2.24 s; confirmation still repeats fresh metadata/plan inside Step before copy.
The next confirmation consumes another 1.17 s. Extract the existing copy/anchor
validation into one private helper shared with managed Step. Confirm uses its
already fresh validated plan for one copy; infrastructure continues to fence and
independently validate each effect. Confirm then captures new coverage and all
replicas. It does not invoke a maintenance recovery or release originals.

Before implementation retain stale-authority/changed-prefix/invalid-copy proof
failure, one-page/one-anchor bounds, no unknown effect retry, typed copy yields,
positive commit followed by unavailable/unknown/canceled planning, insufficient
new anchor, fixed source/start, every-replica proof and final fresh placement.
Background commitment may make the captured copy temporarily unready; a later
existing request turn rereads the anchor without another preparation.

## Supervise the admitted confirmation cohort

Final architecture inspection found that joined confirmation calls also need the
existing process supervisor. Before registration, add a public metric assertion
to the mixed-result process case: a fixed MQTT burst identity must account for
the admitted cohort. Keep the per-call four-worker bound, synchronous join,
dependency-panic rejection and cancellation ownership. Use the existing catalog
and supervisor rather than another pool. Task labels contain no runtime identity;
completion must not create a required permanent task or a persistent worker.

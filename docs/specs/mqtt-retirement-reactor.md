# MQTT retirement reactor admission

The existing Channel append reservation, mailbox and bounded queue own typed
retirement admission. A separate typed task in the store-append pool invokes the
native sequencer. Reactor goroutines perform no storage I/O. Queue/flush checks
require exact recovered authority, full placement, current write admission,
retirement-capable storage, a committed captured anchor and canonical control
bytes. Owned placement slices remain valid after caller cancellation.

Completion verifies the exact operation/generation and retirement proof. Current
durable success advances monotonic HW even if its observer canceled or its guard
subsequently rejects a reply. Historical/newer already-committed proofs never
insert the caller's proposed record into the recent cache. Queued cancellation
can discard work; started durability belongs to its worker until completion.
Consumer-floor read ordering remains a trusted product responsibility. Fresh
[Slot/RPC routing](mqtt-retirement-routing.md) and ordered
[consumer production](mqtt-retirement-production.md) are connected; automatic
scheduling and full product lifecycle remain required work.

## Failure inventory before implementation

1. Retirement bypasses ordinary append ordering, queue byte bounds, recovered
   authority, current status/placement/fence or incapable-storage rejection.
2. Mixed activation/anchor/retirement intent, altered canonical bytes or a captured
   anchor above reactor HW is accepted as a business proposal or valid control.
3. Request membership aliases survive cancellation, pending cleanup is orphaned,
   observer cancellation rolls back durable progress, or a queued canceled request
   starts storage work.
4. Foreign operation/generation, wrong source/reference, regressed prefix, future
   manifest authority or missing/malformed results change state or publish success.
5. Historical/concurrent retries append again, cache a phantom caller record or
   reuse positions for subsequent business; reopen loses the decision.
6. Worker routing uses checkpoint pools, batches controls as ordinary appends,
   or a panic/unsupported dependency fabricates a proof.

Validation uses the already approved Channel service/worker/reactor seams, with
real-disk single-node-cluster service integration for ordering, retries, restart
and cancellation. It is not fresh Slot or product MQTT process acceptance.

## Frozen context

Source `80d9e39fc87ee5bd75e3226ddf2b2b4cd907de34`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/channel/FLOW.md`: `ef171c0a318e3d34afb25784db53d7eb828424f4cee231dc374f07bb034f6090`
- `pkg/channel/reactor/FLOW.md`: `bfc812ad196b80b3174fcdc187a9cdac1f9e10d4936c72ab38608033fd557ecb`
- `pkg/channel/worker/FLOW.md`: `43bdb671f02d8115121e6a0926a4caa2f6404ceecd854060600f87bd2406bc30`

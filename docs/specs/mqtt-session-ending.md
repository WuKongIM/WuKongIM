# Exact-owner Session ending

App.End accepts a trusted current-owner identity and an explicit application end
reason (revocation, source loss, quota or administrative ending). It is not a
client packet operation or proof that permission was denied. The sender/maintenance
caller must distinguish definitive denial from unavailable authority, release all
of its admitted owner scopes, and supply a bounded context before invoking End.

End reads current authority, proves exact execution/transport quiescence, then
rereads the same owner. A changed owner fails without following or terminating the
successor. Ended/offline state, lease expiry and missing/foreign registries do not
replace isolation. A previously ended owner still needs quiescence, which lets
quota-ending cleanup stop an owner after the accounting transaction already ended
its Session. The first persisted end reason is retained.

One existing lifecycle End command atomically ends the Session and makes any live
Will ready; detached/cancelled work is preserved. Delivery counters, allocators,
children and source bindings remain until their separately verified cleanup.
Connecting again gives Session Present=false and a new lifetime, while retaining
the ClientID-to-UID binding. End does not publish a Will or authorize source GC.

Capture the observation before isolation. A concurrent admitted write may advance
the timestamp; drain completion cannot restart that observation. If the active
lease had already expired at observation, record its original abnormal disconnect
first, then reread and end. This bounded two-command case preserves the original
Will disconnect/due clock. An ambiguous intermediate result stops the turn;
authoritative state supports a later retry. No retry loop, table, schema or RPC
change is introduced.

## Failure inventory before implementation

- End commits before admitted effects drain or without physical close proof;
  an already ended/offline row is mistaken for isolation.
- Stale identity, takeover during isolation or concurrent proposal ends a new
  owner; retry overwrites a prior reason or resolves a Will twice.
- End silently ACKs exchanges, drops charged backlog, changes subscription/source
  responsibility, permits Session Present=true or rebinds the ClientID to a new UID.
- Armed/delayed Will is lost; normal-disconnect cancellation or detached ready work
  is resurrected; delayed close/retry restarts its original clock.
- Expired active ownership supplies a new disconnect clock; a lost intermediate
  reply triggers another write without fresh evidence.
- Invalid reason/owner/context, malformed/foreign/partial authority, exhausted
  revision, regressed/non-monotonic time, canceled dependencies, unavailable
  isolation, callback panic or malformed/lost/conflicting commit is reported as
  success or permits another effect after cancellation.

Use the already approved public lifecycle/Owners/metadata seams and the real
three-node app/Slot/RPC integration. Transport close callbacks are controlled in
the latter; product process acceptance and actual send-time revocation remain
separate required work.

## Frozen context

Source `5e6e1819c5cd9cc434069b43ae2f1d0aa1a66678`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `internal/usecase/mqttsession/FLOW.md`: `cbd612610e0f9fc16bc7a4c9cb87aff6e15cea5b5e7f3963e73cec208bce9164`
- `internal/app/FLOW.md`: `a586dfaff85f2e258e894c81e195909e27e9cac5428581545ae4ea77176ffe87`
- `internal/contracts/mqttsession/FLOW.md`: `ae9101a196195e04bf9e45c9c5242b0de9c17bb334dd80d9d1fb46764b534424`
- `internal/runtime/mqttsession/FLOW.md`: `c8e693a925f071992ed91cd352db4a7a5a6b9a3b1a5c8667ac6604031770b6de`
- `pkg/db/FLOW.md`: `3560287fef837ef40dcf04754ac1037a28ec85d26dae7748f465ab9524c624fe`
- `pkg/db/meta/FLOW.md`: `39d9e3024bb42cd14587b12557c0d58b9250444c21ceac6598581517096e0079`

# Ordered MQTT retirement production

One entry-neutral usecase turn connects the existing ordered retention planner,
bounded historical selector and routed retirement committer. It owns no worker,
payloads, local deletion or mutable per-source map. App injects the real planner,
Node ports and server allocator. Scheduling will call this alongside copy and
replica recovery; a committed result does not mean every replica has cleaned up.

Every turn captures the current accepted anchor before reading the strict
consumer minimum. Unknown obligations, no anchor and a stable write fence yield
without selecting or committing. A candidate is rounded down to a complete
anchor by the existing independently verified journal selector. Only a verified
candidate receives a server ID/time and reaches the typed commit port.

A body-free continuation retains the source, full authority, original captured
proof, conservative floor and decreasing exclusive journal cursor. Each resumed
turn reruns the ordered planner. Increasing consumer progress may preserve the
older conservative floor; a lower floor, changed source or changed authority
discards the visit. This refines the routing spec's restart rule: only a decrease
invalidates the retained floor, avoiding starvation under ongoing consumer
progress. Newer anchors never move an unfinished scan's finite target. Same-
authority malformed/future/inconsistent hints and selector replies fail closed.
The store independently revalidates every historical proof and scan cursor.

## Failure inventory before implementation

1. Reversed anchor/consumer read ordering skips a concurrent unknown binding,
   or ACK gaps/removing obligations permit a larger retirement floor.
2. No anchor, unknown responsibility, write fence or exhausted history triggers
   a commit; a candidate above the capped floor or from another source is used.
3. Resuming trusts the previous floor without fresh reads, follows a growing
   latest anchor forever, reuses changed authority, or ignores a floor decrease.
4. Invalid/mixed selector outcomes, changed capture, nondecreasing continuation
   or malformed commit proof is reported as successful progress.
5. Cancellation, dependency error, invalid ID/time or timeout permits a later
   effect; repeated uncertain commits fabricate a new durable decision.
6. App wiring bypasses the foreground Node or reports GC from a scheduling hint.

Tests precede code at the existing approved usecase and app/Node seams. The
three-node app scenario uses real Slot ACK/progress projection and journals,
with controlled initial subscription/window admission; it does not constitute
product-listener or process-level acceptance. Automatic scheduling and all
remaining approved MQTT product scope remain required.

## Frozen context

Source `6feac9205dd40d8172d699bdc14671e0e065c146`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `internal/usecase/mqttsession/FLOW.md`: `c26ce0c8c3bf5d574acf30c29f61aca5f84762ae9595c6a5169c02965400e452`
- `internal/contracts/mqttsession/FLOW.md`: `c4b054833519113b67d9eac40d424e272b43414902139eb8d89bdd15ff2106c2`
- `internal/app/FLOW.md`: `0136b2bb52f840127e53884e5e7e2115302ede68ef8cc4e1a19819d043a8da64`

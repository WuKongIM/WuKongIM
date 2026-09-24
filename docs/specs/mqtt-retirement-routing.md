# Routed MQTT retirement and historical selection

The Node foreground gates expose two trusted internal capabilities. Selection
reads a bounded reverse anchor page bound to the caller's captured proof and
consumer floor. Retirement admits that exact whole-anchor decision through the
existing reactor queue. Neither operation derives consumer permission: the
product planner must capture an accepted anchor before reading fresh obligations.

Both sides of a forward perform fresh Slot metadata reads before and after the
operation. Selection permits an unchanged migration write fence and reads only
committed journals; it shares bounded donor-read admission, owns one store lease
and publishes no checkpoint. The producer must retain its captured proof and floor
across continuation, restarting when a fresh floor changes; each reply must match
the exact request and the store verifies its cursor. Retirement requires unfenced metadata and binds full ordered
placement/status/quorum by the existing authority hash; fresh server metadata,
not caller leases or retention overlays, reaches the reactor. A post-commit
failure withholds its reply, without undoing admitted durability.

RPC 100 commits retirement; RPC 101 selects historical anchors. Both use closed
version-1 envelopes, full request echoes, bounded immutable proofs and typed
errors. A fixed serving leader prevents recursive rerouting after gateway swaps.
Unknown peers fail explicitly; no fallback silently degrades either operation.
Commit survives observer cancellation according to the existing mutation policy;
selection is bounded to five seconds and a maximum of 64 journal candidates.

## Failure inventory before implementation

1. Missing fresh Slot capability, isolation, stale route/leader/membership/status/
   quorum or changed write fence admits work or returns a proof after authority loss.
2. Caller metadata overrides fresh lease/retention or changes full authority;
   malformed, foreign, future or altered proofs pass association checks.
3. Selection rounds upward, changes captured proof/floor, accepts mixed outcomes,
   leaks a store lease after error/panic, or exceeds the no-queue read budget.
4. RPC accepts truncation, trailing bytes, oversized input, unknown version/status,
   changed echo/serving node, malformed proof or an error with success payload.
5. Gateway replacement recursively forwards or capability absence silently falls
   back. Node maintenance/not-started gates permit either operation.
6. Real three-node routing loses historical selection, uncertain/concurrent retry
   identity, authority/restart evidence, or permits an isolated Slot owner to reply.

Tests precede code at the approved Node/service/storage seams. Three-node TCP
integration supplies controlled consumer permission; product listener, automatic
consumer producer and full process-level acceptance remain separate work.

## Frozen context

Source `24884ff7a60ff3d94f54c0efcdb835c42e309996`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/channel/FLOW.md`: `44194fb8fed90b10c8a8b8c265348bfc7273592c978c74b74dae4f7b5192b72a`
- `pkg/cluster/FLOW.md`: `cd37662c9883982aad6d69241840ac439b646d6c8cd0b6f42cc8c729bc0c6f7e`

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

## Idle retirement propagation

After verifying a local committed retirement, the serving leader requests bounded
native tail propagation to current voters through `CommittedReplicaRefresher`.
The native sequencer supplies its own committed boundary; the request and its
proof never supply HW. Idempotent retries also request propagation. Scheduling
is not a receipt that followers have completed repair. Learners retain the
existing native commit repair path. No new record, table or RPC is introduced.

Failure inventory before this fix:

1. A final retirement remains uncommitted on idle voters until another append;
   the real three-node test must observe the exact newest proof on every voter
   without another business write or a test-generated checkpoint.
2. Missing refresh capability permits local admission, or a scheduling failure
   falsely reports success. A retry must reuse committed identity and retry
   propagation; uncertain durability is never undone.
3. Invalid proof or stale post-commit authority schedules refresh, or authority
   changes/cancellation during refresh still permit a success reply. Refresh
   must use exact fresh placement and final authority must be rechecked.
4. An origin node refreshes its own log for a remotely committed retirement.

The existing approved Node/service/storage seams cover these cases. The initial
three-node test fails at `idle voter 1 must learn the committed retirement` in
`/tmp/mqtt-retirement-propagation-red.log` before the production change.

Frozen source `f1feb6929d04bf354b995a9e5ab7821cd9a1e0f1`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/channel/FLOW.md`: `f409f21633ab545ec268bd3914b71c0882023447a98c56e2ab89a161c6fd281e`
- `pkg/cluster/FLOW.md`: `f451f2fb8141689d54b553a53d16ea830ba8fd053d7e795a5738261b659e0bb5`

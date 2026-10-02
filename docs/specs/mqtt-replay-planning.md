# Coherent MQTT replay planning

`PlanMQTTReplay` reads the protected source and latest accepted anchor at one
reactor-captured committed frontier. Callers provide exact Channel/leader/route
fences and source generation, never HW or an assumed accepted prefix. The existing
checkpoint pool persists that captured frontier, then the store performs one
pinned source/latest-journal read. An omitted exact command skips only the
optional retry lookup; sequencer admission still supplies its exact command.

The result contains source, optional anchor and explicit presence. `NextRange`
derives a bounded copy request from the accepted prefix (or activation start).
Local copy progress cannot move this start. If the only remaining position is
the latest anchor itself, there is no work; ordinary content after that position
includes the control in the next range. Planning neither copies bodies nor
accepts an anchor, releases a source, or supplies learner/migration readiness.

## Failure inventory before code

1. Caller-supplied HW, local copying ahead or pending anchors become accepted
   progress; source and anchor come from different snapshots.
2. A zero optional command performs an unrelated lookup, breaks exact retry, or
   admits a missing source, future HW, foreign generation or malformed journal.
3. A worker leaks a lease on read/checkpoint failure or panic, uses an unbounded
   pool, or returns a frontier different from the reactor's captured boundary.
4. Cancellation, route/epoch change, write fence, unrecovered leader, foreign
   result kind/op/generation or eviction loses waiter ownership or returns a plan.
5. Empty/no-anchor state, an idle anchor tail, short pages, restarted readers or
   later appends choose the wrong next position; range arithmetic overflows.
6. The public method bypasses recovered Channel admission or treats a local
   pinned snapshot as fresh distributed routing authority.

Tests precede changes at the approved storage, Channel facade/worker/reactor and
cluster seams. The Node facade and body-free RPC 97 recheck fresh Slot authority
before and after the read, including full ordered placement and strict majority.
`WMPQ/WMPR` version 1 has a 4 KiB cap, exact request echo, closed error statuses and
an optional fixed anchor proof. Forwarding binds one serving leader; the stable
gateway follows runtime replacement. The existing mutation admission budget covers
the checkpoint side effect and calls have a five-second deadline. A local snapshot
alone cannot replace these fresh checks. No durable schema or content encoding
change is needed; RPC 96's existing proof bytes are unchanged and reused.

## Frozen context

Source `7ac762878`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/channel/FLOW.md`: `6facf597fe7da7906a1bc88b5c5073cc1e5f30d099a47aa111456508d4821ff6`
- `pkg/channel/reactor/FLOW.md`: `60749c8e92bf86cd9c7d4ccb2f4f8639274d8ed59303402b6e56a0709c736491`
- `pkg/channel/worker/FLOW.md`: `69ec5cb5634423965c6168bb4bd4f79a4ceb8eb42eecdea67f8b5e31b27f8335`
- `pkg/cluster/FLOW.md`: `0c85c2c3cf96a92bcf92cd802a28ade73dd2db7f750c82e4808e35adc9fb530b`
- `pkg/db/FLOW.md`: `8b723e6f1402c2361a872e3a6e4021ac4146782ebc9b416cb7b9cc9787bb9d75`
- `pkg/db/message/FLOW.md`: `3d9d57d9911f19c1853f5334dfd728b97342604c3d39f1a4746fa22617c13d04`

## Maintenance-only tails

[Bounded maintenance-tail verification](mqtt-maintenance-tail-planning.md) extends
the lone-anchor idle rule to committed anchor/retirement suffixes. It changes no
accepted prefix: subsequent business includes those same control positions in
its contiguous copy. RPC request v1 is unchanged; replies with this assertion
use v2 and cannot be silently read by older peers.

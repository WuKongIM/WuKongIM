# MQTT replay recovery during migration write fencing

The next migration gates require shared replay coverage, which must remain
recoverable while native business admission is fenced. A stable write fence permits
read-only accepted-prefix planning and immutable replica recovery. It must not
permit source activation, new replay copying/anchor admission or business appends.
Planning captures the exact fence as well as HW and rejects completion after any
fence change (including renewal, removal or addition). Fresh Slot placement and
the existing data-plane admission guard remain mandatory.
Native quorum read readiness is distinct from `CommitReady`: a recovered fenced
leader has read readiness while write admission intentionally remains false.

The bounded replay coordinator selects recovery whenever the current placement
has a write fence. Without a committed anchor there is nothing to recover; the
turn yields without copying or committing. Existing per-Slot scheduling and
replica/donor rotation remain unchanged. RPC 99 already permits stable-fence
recovery; RPC 97 now uses the same authority checks. No persistent formats change.

This enables migration admission work; it does not yet authorize promotion,
fence clearing, source release or consumer GC. Automatic failover must first
establish a recovered native leader under the fence, then recover shared content
before clearing it; requiring content before selecting that leader can deadlock.

## Failure inventory before code

1. A migration waits for shared coverage while planning/recovery rejects its fence.
2. Allowing read planning also admits business appends, copying or anchor controls.
3. A plan completes after fence renewal/removal, role/route/epoch changes, canceled
   admission, unavailable data-plane authority or incomplete native recovery.
4. Fenced turns without an anchor manufacture one, or repeatedly choose one target
   instead of rotating bounded recovery work; errors erase durable responsibility.
5. In-memory tests pass but the real three-node app worker cannot repair the learner
   through the fenced Node/RPC/reactor path.

Tests precede implementation at reactor, routed service and coordinator seams,
plus the existing three-node app composition. This is integration evidence; the
full MQTT product listener and process acceptance remain separate required work.

## Frozen context

Source `d1028ad67`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/channel/FLOW.md`: `71b6afc3d30923eca639d2b9976d9251b8866c293dcb71d0c2c27c1f32fc2ce0`
- `pkg/channel/reactor/FLOW.md`: `da31013309d92d448a2712cf5d0a67fe9f279b9157d0dd09994da099423ef0fc`
- `pkg/cluster/FLOW.md`: `97edfaeb38542cc1494dde36eecfe8a89255061999b209a8bf9d1a4e7a2f7698`
- `pkg/slot/FLOW.md`: `144acbb23ecc6006a3dd4eabaddc93db17be970b811a5a04d23687406f63ce90`
- `internal/usecase/mqttsession/FLOW.md`: `fdccd925849c6594b2076bb435c37bbca0a120c30d07fd2706093b7dfdc3d2c6`
- `internal/app/FLOW.md`: `c6897089cc14dfca8f443ab6868cdcc098efc6923cb4e603699a7aee5b8d202f`

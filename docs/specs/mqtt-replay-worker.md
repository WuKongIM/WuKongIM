# Bounded background replay scheduling

One optional node-owned loop scans distinct durable sources in locally led hash
slots (256 by default). Each turn bounds Slot pages, source visits and call time;
no source/session gets a goroutine. A Slot retains one discovery position, a pass
ordinal, and at most one pinned journal-scan continuation. Page validation precedes
any effects. Cursors advance only past attempted sources, including failures;
unstarted entries remain discoverable. Lost Slot ownership drops all its hints.
Stop cancels and joins the exact run; a timeout prevents an overlapping restart.

Discovery now uses [read kind 17](mqtt-tombstone-source-discovery.md), preserving
source hints in retained primary tombstones after the last live consumer leaves.
This does not add consumer responsibility or bypass the ordered retention plan.
The worker never downgrades to active-source-only kind 16. Source deactivation
and eventual safe tombstone pruning are separate lifecycle requirements.

A source visit ends after one copy/anchor attempt, one imported recovery interval,
one coverage result or one failed donor round. Only successful journal scanning
continues the same source/target on its next Slot turn. Its exact pinned target is
finite and scan progress must increase; new messages cannot extend that visit.
An error or placement change ends the visit. Completed imports are durable progress,
not whole-target completion. Source bindings are reread on each continuation page;
removal cannot strand a process-only task.

The per-Slot pass ordinal seeds cold coordinator selection: all replicas receive
a copy phase and a recovery phase, then the next cycle rotates initial donor hints.
Fresh planning still supplies authority and accepted content progress. The seed
never supplies a content position or proof. This avoids an unbounded per-source
cache, prevents repeated cold visits from always selecting replica one, and lets
later donors participate even when every earlier bounded donor round fails.
Normal within-visit scan continuations take precedence over the seed. Donor and
replica progress is rederived from durable state on the next discovery pass.

Shared body-free cursor/result DTOs live in internal/contracts/mqttsession so the
runtime and usecase do not depend on each other. App composes real Node/Slot ports
and the coordinator into the optional worker. This is not yet full product MQTT
start/stop/restore wiring: the listener stays unavailable until the remaining
subscription, delivery, owner recovery and Will capabilities are complete.

## Failure inventory before code

1. A million source identities allocate a million retained cursors, or a failed
   source/cache entry prevents later sources from being attempted.
2. Every cold visit copies or repairs replica one forever, or donor reset never
   reaches healthy donors beyond the first bounded donor round.
3. Partial page/time budgets skip unstarted sources; duplicate, regressing,
   foreign, over-limit or late pages partially dispatch side effects.
4. A scan loses its pinned target/progress, repeats after newer anchors, or reports
   an import as completion. Placement changes must not perpetually extend a visit.
5. Lost Slot ownership or source removal retains stale work; an empty final page
   fails to wrap the pass; provider-owned slices are sorted/mutated in place.
6. Cancellation detaches work, Stop timeout permits another run, or a startup
   context unexpectedly owns the background lifetime. Restore/restart must use
   fresh scan hints and no per-identity task labels.
7. Composition hides a local-authority substitute or manually pumps turns instead
   of demonstrating the managed loop across real Node/RPC/storage boundaries.

Tests precede implementation. The worker's fixed task identity, sweep bounds,
joined lifecycle and cold-source fairness are checked independently, followed by
three-node TCP/disk app composition. Full product process/load acceptance remains.

## Frozen context

Source `45183397e`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `internal/contracts/mqttsession/FLOW.md`: `5bd4d98da6fa4d59615b62f31f98056add1127274a29816c8d5a181015329841`
- `internal/runtime/mqttsession/FLOW.md`: `cd6b01821860621d502db203c9dbd29604e9bab459d78c584022ce43802c6320`
- `internal/usecase/mqttsession/FLOW.md`: `e1c29f1ddb8208dfb595d5fb475ecd3cf4d8c5aaed9b043f276253776f74ab2f`
- `internal/app/FLOW.md`: `4bf2ebb8a3618fb90d89154991997710cfb73c3c5e7024a0bf2b59508b861878`
- `pkg/goroutine/FLOW.md`: `8d17b24c8059fa09759a72a88d1a7cf92fabfd9b1bcde92ae3dad316a2ce6776`

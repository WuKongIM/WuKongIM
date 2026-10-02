# MQTT retired replay storage

A replica applies only an independently verified committed format-6 retirement.
Its retained anchor supplies the exact cumulative byte counters and prefix digest
after the retired bodies are gone. Message table 2 System 2 stores the retirement
control position and a separate deleted-through cursor. The existing local replay
frontier advances to at least that baseline; a later verified frontier is retained.
This is completion of retired responsibility, not a claim to still have its bodies.

Application holds append then checkpoint ownership and synchronously commits the
baseline reference, replay frontier and one bounded cleanup step. Each step visits
at most the requested 1..256 primary keys (plus one lookahead), then deletes the
corresponding primary and meter ranges with two range tombstones. Missing local
copies need no download before retirement; gaps are skipped by seeking keys.
The cursor records logical removal from the engine; actual disk reclamation is
performed by the existing bounded compaction machinery. Exact/older retries
verify their own committed proof and cannot regress a newer materialized baseline.

Reads, metering and exports reject ranges below the logical baseline even while
physical cleanup is incomplete. The baseline supplies its own endpoint for suffix
hashing; repair, readiness and source release treat independently committed older
anchors as retired responsibility. Historical readiness may not borrow retirement
authority newer than its captured HW. Import cannot resurrect a retired prefix.

Pruned portable backups use version 3, adding an optional baseline-reference field
per channel and serializing only the retained suffix. Native-only version 1 and
unpruned version 2 remain byte compatible. Export verifies that the retirement is
covered by the backup cut and normalizes deletion progress to its complete prefix:
the archive contains none of those bodies. Preflight independently verifies all
source/anchor/retirement references and suffix hashes before writing. Restore
publishes baseline and frontier atomically after bounded row batches; conflicting
existing baselines or a rollback to an unpruned archive fail. Fully retired content
is a valid zero-row version-3 archive. Matched binaries/tools are required.

This storage seam consumes a replicated decision, not a caller consumer floor.
Explicit [recovery application](mqtt-retirement-recovery.md) now discovers and
applies the latest committed decision before requesting donor bodies. Product
consumer admission still must select/commit decisions through current authority
before enabling MQTT access.

## Failure inventory before implementation

1. Pending, foreign, missing or changed retirement/source/anchor proofs authorize
   deletion; current-frontier incompatibility is hidden by advancing a baseline.
2. Cleanup exceeds its key budget, leaks orphan meters, changes native HW/source
   release or loses its durable cursor; old retries regress or resurrect data.
3. All-content or absent-content retirement requires an old body/meter; suffix
   copy/import, metering, repair and readiness fail or silently restart counters.
4. A historical readiness/backup cut uses a later retirement; concurrent readers
   observe a baseline without its matching frontier or expose a retired prefix.
5. Backup exports retired rows, drops references, rejects an empty retained suffix,
   corrupts cumulative hashes/counts, accepts stale target state, or restores a
   baseline before its suffix is durable. Existing version-1/2 backups change.
6. Restart, cleanup retries, suffix replication and backup/restore lose the exact
   baseline or allow incomplete proof to appear as successful recovery.

Validation uses the existing durable MessageDB and Channel store seams, with real
disk integration, explicit corruption, reopen, portable backup and suffix transfer.
The optional storage port is not yet composed into a product entrypoint, so native
quorum/adapter integration substitutes for an app wiring test at this stage;
product entry and process acceptance remain required before completion.

## Frozen context

Source `a2cea920f585c548aae5cfae98f1df54fbefde58`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/db/FLOW.md`: `8b723e6f1402c2361a872e3a6e4021ac4146782ebc9b416cb7b9cc9787bb9d75`
- `pkg/db/message/FLOW.md`: `89ba2cc1684451ec2da8e25d235fd48c44801d6c267229c71da2113d6f7ec1c2`
- `pkg/channel/FLOW.md`: `1e6343bc22ab83a3e9be426c5513588defd74cb31a549aa463effee649b4fb6f`

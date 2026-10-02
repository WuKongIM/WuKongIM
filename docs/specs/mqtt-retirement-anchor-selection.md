# MQTT retirement anchor selection

A coherent consumer floor may fall between accepted replay anchors. Retirement
must select a whole committed anchor at or below that floor, retaining its exact
digest and cumulative counters as the future pruned baseline. Selecting only the
latest anchor could prevent reclamation indefinitely behind a slower consumer.

The existing MessageDB/Channel store seam performs a pinned, bounded reverse
scan of System 14. An exact captured anchor limits the scan to the prefix observed
before the consumer read. Each candidate is verified against its own committed
journal, source, paired proposal and entry; caller bytes and local shared-copy
progress supply no proof. Original message bodies are unnecessary.

One call verifies at most 64 scanned anchors plus the captured anchor and an
optional continuation. A continuation names the last skipped anchor, which must
still be committed, within the capture and above the requested consumer floor.
It excludes that anchor on the next reverse page. Exhaustion, a whole eligible
anchor and a continuation are distinct results. Changed floors that make the
cursor eligible require restarting selection. No scan reads consumer rows,
writes metadata, advances HW, commits a retirement or deletes replay content.

Tests remain at the approved durable storage and Channel adapter seams, using
real disk stores, reopen/backup and explicit corruption injection. Selection is
not independently a GC authorization: the producer must retain the ordered
anchor-before-consumer protocol, current authority checks and replicated control.

## Failure inventory before code

1. A floor between anchors is rounded upward, a later anchor enters a captured
   plan, or a missing local shared copy is treated as absent accepted proof.
2. An unbounded historical scan blocks a worker; cursors repeat, cross the captured
   boundary, name business records, skip an eligible endpoint or fail after reopen.
3. Missing/changed journals, entry identities, paired manifests, activation or
   source state become usable evidence; a pending anchor is exposed before HW.
4. Foreign generations, changed equal-prefix counters, invalid budgets, closed
   leases or cancellation return a candidate. A malformed result crosses the adapter.
5. Selection mutates source/copy/checkpoint state, depends on trimmed original
   content, or is incorrectly reported as consumer permission or physical GC.

## Frozen context

Source `b825be9df8bfad5509fd0bed205c5ca657682139`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/db/FLOW.md`: `8b723e6f1402c2361a872e3a6e4021ac4146782ebc9b416cb7b9cc9787bb9d75`
- `pkg/db/message/FLOW.md`: `0f49947ceb0e7b1f98e835a7657d398e2c78181632580f9928ccc0e085508c05`
- `pkg/channel/FLOW.md`: `1dc9db785d5127c91e8a50ffc45ff594291c6e4cb9f89cca7d43206ace870a27`

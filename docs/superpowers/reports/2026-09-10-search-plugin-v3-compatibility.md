# Original search plugin compatibility rehearsal

This records successive synthetic compatibility checks, including a stronger failing rehearsal and its runtime repair. It is not production cutover acceptance.

## Identity

- Plugin: `wk.plugin.search`, version `0.0.1`, priority `1`, methods `PersistAfter` and `Route`, empty configuration.
- Source: `WuKongIM/plugins` commit `10b1795449c4dc7da1e8871863f7458c1e94198e`, Linux/amd64 binary, 63,324,496 bytes.
- SHA-256: `68079973350ec42480d84ee83770e5f7910fc06daeba41b746362e28492d939f`.
- Server: v3 source `ad3504338` plus the format-19 reader and legacy plugin HTTP route fixes, subsequently committed as `d5f1f3aac` and `04d3e3f7d`. The runtime candidate was built before the route commit and is a development artifact, not the beta.13 release.
- Candidate compressed server SHA-256: `943a5ea9b5632567b81fac94f8d47debc2a45e10b1f2a2e8a9b2f909acb9756b`.
- Linux native synthetic clusters used 12 logical Slots, 256 physical hash slots. The three-node cluster used three Slot and Channel replicas.

## Reproduced failure and repair

The beta.13 server started the original plugin and durably saved a synthetic group message, but `POST /plugins/wk.plugin.search/search` returned 404. The existing internal plugin Route usecase lacked a Product HTTP entry. With the bounded legacy entry and app wiring restored, the same indexed message was returned after restarting the server. `/usersearch` returned the same message through conversation lookup and node forwarding.

## Native three-node acceptance

A fresh cluster first stored a synthetic historical message without a plugin program. After clean shutdown, each target received the original executable and a disposable copy of the single-node checkpoint DB, without a Bleve index. The original plugin rebuilt the index through native committed-history Host RPCs.

| Check | Result |
| --- | --- |
| Historical-index rebuild, `/usersearch` through nodes 101, 102, 103 | All returned sequence 1 |
| New messages submitted through different node APIs | Sequences 2 and 3 committed |
| User search through all three node APIs | Each returned sequences 1, 2, 3 |
| First full cluster restart | Each returned sequences 1, 2, 3 |
| Second full cluster restart | Each returned sequences 1, 2, 3 |

All synthetic three-node services were stopped after acceptance. The production v2 cluster was not used for synthetic writes.

## Migration projection and limits

The migration profile permits only this exact executable and source registration. Import seeds every retained message channel at sequence zero on every target, without copying old indexes. A fresh original-plugin startup rebuilds all retained history. Independent offline verification checks the complete seed set, rejects missing/extra/advanced checkpoints and already-started index state, and holds a read-only lock that excludes the plugin writer.

The profile is not proof for arbitrary search versions, multiple competing plugins, nonempty configuration, different methods, other architectures, production history, performance, client behavior, or eventual cutover. Startup readiness alone does not certify index readiness. Rebuilding full history on every target consumes additional storage and CPU; original cold backups remain intact. Actual imported history, business HTTP integration, and iOS acceptance are still required.


## Stronger archive-based rehearsal and corrected conclusion

The complete `TestOriginalSearchArchiveWorkflow` integration test uses the exact
original executable, derives JSON text payloads from the original three-node
fixture, and performs prepare, export, archive-only import, exact resume, and an
independent CLI verification after the source paths are unavailable. Its targets
use 12 logical Slots, 256 hash slots, and three replicas. The offline test passed.

On Linux, the imported targets rebuilt all three historical messages and indexed
a fourth message. A full restart then returned only the original three search
results, although all three native history APIs still returned the fourth
message. The earlier rotating-write rehearsal therefore did not establish
search freshness after a leader change. The original executable's index-ready
marker skips future startup catch-up, and best-effort PersistAfter notifications
do not make every potential leader's private index current.

The source profile remains an offline mapping and seed contract. Production
cutover additionally requires an upgraded and validated runtime plugin; the
original executable alone does not satisfy the business gate.

## Search runtime repair

Plugin source `431cc64` in `WuKongIM/plugins` changes channel-scoped search to
catch up committed history through bounded existing queues before returning a
result. Query failures and deadlines propagate through usersearch, and bucket
serialization protects checkpoints from concurrent startup/queued indexing.
Unscoped global search retains its previous index-only behavior.

The pre-commit Linux/amd64 candidate subsequently committed as `431cc64` is
53,330,072 bytes, SHA-256
`bc721cfc7e6440f04b6d8c559e37ebefd06c025381c1c3815ba0d110048556b1`.
A clean rebuild from commit `431cc64bdd6e21a6db365e88a4fcc997b1269ca4` produced identical executable bytes. Its full plugin tests and focused race tests passed. Native acceptance found:

- Previously missed committed messages were included after plugin upgrade.
- Six messages remained searchable through all three node APIs after two full
  cluster restarts, with exact business sequences `[1, 2, 3, 4, 7, 8]`.
- A new write through Leader 103 committed at sequence 9. After stopping that
  leader, replacement Leader 101 and node 102 both returned all seven messages.
- The former leader rejoined and returned the same seven messages.

Channel recovery authority barriers can consume log positions; assertions use
actual message IDs/sequences and monotonic new writes, not consecutive integers.
The forced-leader harness completed its business assertions but encountered a
cleanup error when systemd had already unloaded the stopped transient unit.
The remaining rejoin service was explicitly stopped and a subsequent inventory
confirmed that no synthetic search service remained running.

This evidence covers synthetic data and the exact tested source. The production
source still requires consistency decisions, native import, business integration,
search rebuild capacity checks, and real iOS acceptance.

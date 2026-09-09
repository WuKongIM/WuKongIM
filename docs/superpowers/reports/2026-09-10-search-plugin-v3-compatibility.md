# Original search plugin compatibility rehearsal

This is compatibility evidence for one exact executable, not production cutover acceptance.

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

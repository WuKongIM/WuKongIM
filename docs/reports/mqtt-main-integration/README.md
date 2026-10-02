# MQTT PR #998 main integration

Status: main integration resolved and validated on Darwin/arm64.

Validated source: `0c1a24e0415ca716fa7332e7fd29b27c0043e562` (both exact parents
below). Subsequent delivery changes contain this report only.

Sources: MQTT candidate `9314a16310b87af64250fd91a6d1c9a2d7cfd3e7` and main
`6e60587d637025ea7e2179e84112351305540919`. Frozen instruction/navigation digests
and the failure inventory were captured before resolution.

## Resolution

- Main RPC 91/92 (send permissions/stream delivery) and Slot commands 67/68/69
  (send ban/Channel info/batched memberships) remain unchanged. Unpublished MQTT metadata/owner RPCs
  move to 106/107; Session/subscription/cursor commands move to 80/81/82. Other MQTT IDs
  remain unchanged. Existing codec/catalog fixtures pin the resulting identities.
- Main asynchronous quorum submission and worker completion ownership remain.
  Both synchronous controls and callback business commits share one locked
  sequencer, exact MQTT manifest matching and all-replica preparation before
  original dispatch. Definitely unwritten outcomes release sequencing; unknown
  storage effects retain their charged preparation proof.
- Composition keeps the ordered Gateway submission owner and MQTT lifecycle.
  Stop fences new sessions, joins Gateway submissions, then joins MQTT while
  transport and business dependencies remain alive. Timeouts retain dependencies.
- Membership upserts keep main physical-Slot coalescing/byte bounds and the
  MQTT branch's eight-worker upper bound. Existing controlled-concurrency tests
  use independent physical owners so coalescing cannot falsify their contract.
- Gateway shares its existing shard worker between joined MQTT packet callbacks
  and deferred WK SEND. Packet bytes and admitted drain ownership survive until
  callback completion; packets never enter WK preparation. Existing packet tests
  cover deferred on/off, ordered dispatch, executing-byte bounds and budget reuse.
- Controller service reads use main's synchronized publication helper; cleanup
  uses one mutex acquisition after stopping the captured service. Both
  explicit-token WKProto fixture APIs delegate to one authenticated handshake,
  retaining injected socket observation and exact device flags.
- Changelog, current knowledge and FLOW retain both parents' facts; the FLOW
  index is regenerated. Historical acceptance reports retain their original
  source and binary identities rather than being relabeled as merge validation.

## Compatibility and limits

Pre-integration MQTT development Raft logs are incompatible with relocated
command IDs. They are not a supported upgrade source. Use pre-MQTT data or a
separately verified migration; matched nodes/tools and cold rollout remain.
Resolving PR conflicts does not establish Linux, complete MQTT/fault/load or
release qualification. The PR remains a draft. Product binaries are not race
builds; race runs cover the tests, harness and in-process runtime cases.


## Validation

Go 1.25.11 was downloaded from the official Go distribution and checked against
its published SHA-256. `build-manifest.json` pins product source files and the
ordinary/instrumented binary hashes; the ordinary binary has no gofail dependency.
The fault binary was generated only in a temporary source copy.

| Check | Result |
| --- | --- |
| App, node access, cluster adapters and cluster focused unit packages | PASS |
| Slot FSM codec/catalog/state contracts and Gateway unit packages | PASS |
| Controller, Channel replication and cluster transport race tests | PASS |
| App deferred wiring/stop and Gateway deferred integration race tests | PASS |
| Gateway packet/MQTT/deferred integration race tests, including deferred on/off, byte retention, ordering and drain | PASS, 3.348 s |
| Authenticated MQTT/WKProto interop, single-node and three-node clusters | PASS, 38.521 s |
| Will Delay, abnormal publication and normal cancellation, both topologies | PASS, 55.725 s |
| WKProto/HTTP/MQTT ingress, two consumers, full-budget Will recovery and retirement, both topologies | PASS, 158.164 s |
| Live accepted-storage three-node TCP partition and replay/reopening | PASS, 115.550 s |
| Charged post-commit unknown preparation under actual TCP isolation | PASS, 91.043 s |
| E2E suite and native single-node SEND race tests | PASS |
| Named `flow-doc-contracts` | PASS: 88 compliant, 0 invalid, 19 advisory length warnings |
| Module checksums and Git whitespace/conflict checks | PASS |

Both partition receipts prove bidirectional relay isolation and surviving public
Slot quorum, preserve responsibility across the outage, and finish with all three
reserved gauges at zero. No product process remains. The eight final receipts,
exact commands/results, compressed logs and artifact hashes are retained here.
FLOW warnings are advisory: current expanded modules retain both parents' ownership,
protocol and recovery constraints while staying within the enforced 150-line cap.

The first transient merge was RED: compilation exposed main membership command
69 colliding with the MQTT cursor, and a Controller contract hung on the automatic
merge's nested mutex acquisition (the owned test was stopped with SIGQUIT after
175 s to retain its stack). Initial product interop/Will/partition SUBSCRIBE failed
when MQTT packets entered WK deferred preparation. Before the Gateway repair, two
existing packet integration tests gained deferred on/off variants: joined variants
passed, deferred variants failed on ordered packet execution. The repaired variants
and original product scenarios pass above. RED logs are retained separately and
are not claimed as acceptance of the final source.

Verify retained receipts, both frozen instruction parents and current source:

```sh
python3 docs/reports/mqtt-main-integration/verify.py --source
```

To repeat process acceptance, build the pinned source with Go 1.25.11, set
`WK_E2E_BINARY` to that ordinary binary and run the exact arguments in
`validation-results.json`; use the temporary-copy build command there for the
unknown-preparation case. Artifact directories are harness-only. Full Linux and
MQTT failure/load qualification remains outstanding.

## Standards

[Final independent Standards review](standards-review.md): **0 actionable findings**.
The reviewed integration preserves repository boundaries, bounded ownership and
existing workers. It does not audit every historical branch commit.

## Spec

[Final independent Spec review](spec-review.md): **0 actionable findings**.
Exact manifest/unknown responsibility, permanent main identities, MQTT command
relocation and joined/deferred dispatch are consistent with the integration scope.

Review findings remaining: Standards **0** (no worst issue); Spec **0** (no worst issue).

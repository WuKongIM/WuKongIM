# MQTT PR #998 main integration

Status: candidate resolved; validation in progress.

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
release qualification. Final commands, receipts and review results follow below.

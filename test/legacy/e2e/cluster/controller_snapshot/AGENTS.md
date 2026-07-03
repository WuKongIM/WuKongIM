# controller_snapshot AGENTS

This file is for agents working on `test/legacy/e2e/cluster/controller_snapshot`.

## Purpose

Prove a Controller Raft follower that is stopped before leader-side compaction
can rejoin by receiving and restoring a Controller metadata snapshot through the
real process, transport, API, and manager paths.

## Cluster Shape

One real three-node static cluster. All three nodes are Controller voters and
data nodes. The test stops one non-leader Controller voter, keeps quorum with the
other two voters, generates Controller metadata, compacts the leader, and
restarts the stopped voter.

## External Steps

### Large Controller Snapshot Restore

This opt-in scenario only runs when `WK_E2E_CONTROLLER_SNAPSHOT=1`.

1. Start a real three-node cluster with `WK_TEST_MODE=true` and fast Controller
   log compaction overrides.
2. Poll `/manager/nodes/:node_id/controller-raft` until one node reports
   `role=leader`.
3. Stop a non-leader Controller voter and record its local applied/restore
   watermarks.
4. Generate deterministic payload-heavy Controller metadata in bounded batches
   through `POST /testdata/e2e/cluster/controller-snapshot-jobs`.
5. Trigger `POST /manager/nodes/:node_id/controller-raft/compact` on the
   Controller leader and require the snapshot index to pass the stopped
   follower's applied index.
6. Restart the stopped follower with its original data directory.
7. Poll the restarted node's `/manager/nodes/:node_id/controller-raft` status
   until `restore.last_snapshot_index` and `applied_index` cover the leader's
   compacted snapshot.

## Observable Outcome

The restarted Controller voter reports `role=follower`, `health=healthy`, and a
restore snapshot index at or beyond the leader compaction snapshot index. This
proves snapshot transfer and restore work through the black-box cluster path.

## Failure Diagnostics

- generated configs, stdout, stderr, `logs/app.log`, and `logs/error.log`
- last `/readyz` observations stored by the suite
- last `/manager/nodes/:node_id/controller-raft` body observed by status polling
- last `/manager/nodes/:node_id/controller-raft/compact` response body observed
  by Controller compaction
- last `/testdata/e2e/**` response body observed by test-data generation

## Run

`go test -tags=e2e,legacy_e2e ./test/legacy/e2e/cluster/controller_snapshot -count=1`

Opt-in large snapshot run:

`WK_E2E_CONTROLLER_SNAPSHOT=1 WK_E2E_CONTROLLER_SNAPSHOT_ROWS=700 WK_E2E_CONTROLLER_SNAPSHOT_PAYLOAD_BYTES=131072 WK_E2E_CONTROLLER_SNAPSHOT_BATCH_ROWS=50 go test -timeout=25m -tags=e2e,legacy_e2e ./test/legacy/e2e/cluster/controller_snapshot -run TestControllerFollowerRestoresLargeSnapshot -count=1 -v`

## Maintenance Rules

- If Controller Raft status fields, compaction routes, test-data generation
  routes, run commands, or diagnostics change, update this file in the same
  change.
- Keep scenario-specific helpers in this package until another e2e scenario
  needs the same behavior.

# ControllerV2 Usage

`pkg/controllerv2` is a reusable Controller engine. It owns durable
`cluster-state.json`, Controller Raft command apply, bootstrap planning, and
full-file state sync. External callers should use the root package facade:
`Runtime`, strongly typed `ClusterState` / `StateEvent`, Raft message stepping,
and state sync request/response contracts.

Production startup should stay in the caller, for example `pkg/clusterv2/control`;
this package should not import `pkg/clusterv2`.

The snippets below use `cv2*` import aliases for
`github.com/WuKongIM/WuKongIM/pkg/controllerv2/...` packages.

## Internal Packages

| Package | Use |
| --- | --- |
| `state` | Durable `cluster-state.json` model, validation, checksum, and hash-slot helpers. |
| `statefile` | Atomic load/save for the local state file. |
| `command` | Versioned Controller Raft command payloads. |
| `fsm` | Applies committed commands and saves the final state once per batch. |
| `raft` | Controller Raft service, WAL, snapshots, scheduled apply, and proposal API. |
| `planner` | Pure planning; the default planner creates initial slot assignments and tasks. |
| `sync` | Full-file state sync from Controller voters to non-controller nodes. |
| `server` | Thin facade that connects local state, planner ticks, proposals, and sync. |

## Controller Voter

A Controller voter runs Controller Raft and applies committed commands through
the FSM. A single-node deployment is still a single-node cluster and uses the
same path.

```go
store := cv2statefile.New(filepath.Join(stateDir, "cluster-state.json"))
sm, err := cv2fsm.New(store)
if err != nil {
	return err
}

svc, err := cv2raft.NewService(cv2raft.Config{
	NodeID:         nodeID,
	Peers:          []cv2raft.Peer{{NodeID: nodeID, Addr: addr}},
	AllowBootstrap: allowBootstrap,
	RaftDir:        filepath.Join(stateDir, "raft"),
	StateMachine:   sm,
	Transport:      raftTransport,
})
if err != nil {
	return err
}
if err := svc.Start(ctx); err != nil {
	return err
}
defer svc.Stop()

srv, err := cv2server.New(cv2server.Config{
	StateSource: sm,
	Proposer:   svc,
	Now:        time.Now,
})
if err != nil {
	return err
}
```

After the service elects a leader, propose `command.KindInitClusterState` once to
create the first state file. Then call `srv.TickPlanner(ctx)` from the control
loop to let the bootstrap planner create missing slot assignments and reconcile
tasks.

## Non-Controller Mirror

A non-controller node does not join Controller Raft. It mirrors the leader state
file through `sync.Client` and can use `server.Server` only as a local facade.

```go
store := cv2statefile.New(filepath.Join(stateDir, "cluster-state.json"))
client := cv2sync.NewClient(cv2sync.ClientConfig{
	ClusterID: clusterID,
	Store:     store,
	Peers:     peerPicker,
})

srv, err := cv2server.New(cv2server.Config{
	SyncClient: syncAdapter{client: client},
})
if err != nil {
	return err
}
if err := srv.SyncOnce(ctx); err != nil {
	return err
}
local := srv.LocalState()
```

`peerPicker` resolves Controller voter IDs to `sync.Endpoint` implementations.
The production wrapper can adapt this to RPC; tests can provide in-memory
endpoints. `syncAdapter` is a small caller-owned adapter that calls
`client.SyncOnce(ctx)`, then returns `client.LocalState()`.

## State Sync Endpoint

Controller voters expose sync by wrapping their local FSM snapshot:

```go
endpoint := cv2sync.NewServer(cv2sync.ServerConfig{
	NodeID:    nodeID,
	ClusterID: clusterID,
	LeaderID:  svc.LeaderID,
	Ready: func() bool {
		return sm.Snapshot(ctx).Revision != 0
	},
	Snapshot: func(ctx context.Context) (cv2state.ClusterState, error) {
		return sm.Snapshot(ctx), nil
	},
})
```

`GetState` returns leader redirects, not-ready responses, not-modified responses,
or a full encoded state payload.

## Operational Notes

- Keep `cluster-state.json` and the Controller Raft directory under the same
  node-local state root, but as separate paths.
- Only the Controller Raft leader can accept mutating `Propose` calls; callers
  should retry on `cv2raft.ErrNotLeader` after discovering the new leader.
- `ProbePropose` appends a non-mutating readiness entry. It does not change
  `Revision` or rewrite business state.
- `Revision` is the logical state version. `AppliedRaftIndex` is the Raft entry
  already materialized into `cluster-state.json`.
- Planner commands use `ExpectedRevision` as a compare-and-set guard. Refresh
  from the FSM after successful proposals.
- `cluster-state.json` is a materialized snapshot. The Controller Raft WAL and
  applied metadata remain the source for committed log recovery.

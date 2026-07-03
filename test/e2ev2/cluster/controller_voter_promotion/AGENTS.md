# controller_voter_promotion AGENTS

This scenario proves Controller voter promotion through a real multi-node
`cmd/wukongim` cluster.

## Scenario Contract

- Start a static three-node cluster with manager HTTP enabled.
- Start node 4 in seed-join mode and activate it as an active data node.
- Prove node 4 is not a Controller voter before promotion and the manager
  action hint allows promotion.
- Promote node 4 through `POST /manager/nodes/:node_id/controller-voter/promote`.
- Prove durable manager node inventory and node-local Controller Raft status
  converge on node 4 as a Controller voter.
- Prove the promoted node still accepts real WKProto SEND traffic.

## Rules

- Keep tests black-box: use real `cmd/wukongim` processes, public manager
  HTTP endpoints, public readiness probes, and WKProto.
- Do not import `internal/app`, `internal/usecase`, storage internals, or
  control-plane internals.
- Prefer polling manager and readiness endpoints over fixed sleeps.
- Run this package serially with `-p=1`.

## Running

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/controller_voter_promotion -count=1 -timeout 6m -p=1
```

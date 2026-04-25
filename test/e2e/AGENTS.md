# test/e2e AGENTS

This file is for agents working inside the `test/e2e` layer. It defines the
structure, execution model, and documentation maintenance rules for this
directory tree.

## Core Rules

- Keep this layer process-level and black-box: validate behavior through real
  processes, real protocols, and real public entrypoints, and do not import
  `internal/app`.
- Prefer externally observable outcomes. Do not depend on internal state,
  internal hooks, or test-only shortcuts.
- Do not use fixed sleeps. Prefer health checks, handshake probes, polling,
  and eventual-consistency assertions.
- Put reusable test support in `test/e2e/suite` so business assertions do not
  become scattered across scenario files.
- Keep failure diagnostics bounded: prefer the minimum useful config, stdout,
  stderr, log tails, and manager-plane responses.
- When an e2e test needs extra observability, prefer existing `/manager/`
  APIs. If the current APIs are insufficient and a new `/manager/` endpoint is
  required, ask the developer for confirmation before implementing it.

## Structure Contract

- The `test/e2e` root should contain only shared documentation, `suite/`, and
  business-domain directories.
- Add new scenarios under `test/e2e/<domain>/<scenario>/`.
- Every business-domain directory must have its own `AGENTS.md`.
- Every scenario directory must contain its own `AGENTS.md` and one primary
  `*_test.go` file.
- `suite/` owns reusable black-box harness code only, not scenario-specific
  business assertions.

## Documentation Consistency

- `AGENTS.md` files under `test/e2e` are primarily for agents, not end-user
  README documents.
- When the e2e structure changes, the directory layout and the corresponding
  `AGENTS.md` files must stay in sync.
- When a domain or scenario is added, removed, or renamed, update the relevant
  `AGENTS.md` files in the same change.
- When a scenario's run command, core steps, or failure-diagnosis entrypoints
  change, update the corresponding `AGENTS.md` at the same time.

## Running

- Full suite: `go test -tags=e2e ./test/e2e/... -count=1`
- For focused work, run the target scenario package directly.
- When a scenario needs custom artifact roots or node config overrides for
  debugging, pass `test/e2e/suite` options directly to
  `StartSingleNodeCluster(...)` or `StartThreeNodeCluster(...)`.

## Catalog

| Domain | Scenario path | Purpose | Run |
| --- | --- | --- | --- |
| `message` | `test/e2e/message/single_node_send_message` | Prove one fresh single-node cluster can complete a real WKProto `Send -> SendAck -> Recv -> RecvAck` closure. | `go test -tags=e2e ./test/e2e/message/single_node_send_message -count=1` |
| `message` | `test/e2e/message/cross_node_closure` | Prove a three-node cluster can deliver one person-channel message when sender and recipient are connected to different follower nodes. | `go test -tags=e2e ./test/e2e/message/cross_node_closure -count=1` |
| `message` | `test/e2e/message/slot_leader_failover` | Prove cross-node delivery survives a slot leader failover after both clients are already connected on follower nodes. | `go test -tags=e2e ./test/e2e/message/slot_leader_failover -count=1` |
| `message` | `test/e2e/message/expired_leader_lease` | Reproduce the current cross-node delivery failure after channel runtime metadata is observed with an expired leader lease. | `go test -tags=e2e ./test/e2e/message/expired_leader_lease -count=1` |
| `message` | `test/e2e/message/idle_cross_node_delivery_timeout` | Reproduce the current cross-node delivery timeout after one bootstrap message sits idle until the channel runtime leader lease expires. | `go test -tags=e2e ./test/e2e/message/idle_cross_node_delivery_timeout -count=1` |

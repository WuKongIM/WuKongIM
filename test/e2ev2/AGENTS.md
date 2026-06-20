# test/e2ev2 AGENTS

This file is for agents working inside the `test/e2ev2` layer. This tree owns
black-box e2e coverage for `cmd/wukongimv2` and internalv2 behavior only.

## Core Rules

- Keep this layer process-level and black-box: validate behavior through real
  `cmd/wukongimv2` processes, real protocols, and public HTTP entrypoints.
- Do not import `internalv2/app`, `internalv2/usecase`, or storage internals.
- Describe deployment shape as a single-node cluster or multi-node cluster,
  never as a cluster-bypassing standalone mode.
- Put reusable v2 test support in `test/e2ev2/suite`; scenario files should
  focus on business assertions.
- The v2 suite builds `./cmd/wukongimv2` by default. Use `WK_E2EV2_BINARY` to
  point at a prebuilt v2 binary.
- Keep failure diagnostics bounded: prefer config paths, stdout/stderr tails,
  app log tails, public HTTP responses, and public metrics.

## Structure Contract

- The `test/e2ev2` root contains only shared documentation, `suite/`, and
  business-domain directories.
- Add new scenarios under `test/e2ev2/<domain>/<scenario>/`.
- Every business-domain directory must have its own `AGENTS.md`.
- Every scenario directory must contain its own `AGENTS.md` and one primary
  `*_test.go` file.
- `suite/` owns reusable v2 black-box harness code only, not
  scenario-specific business assertions.

## Running

- Full v2 suite: `GOWORK=off go test -tags=e2e ./test/e2ev2/... -count=1`
- Focused suite helpers: `GOWORK=off go test -tags=e2e ./test/e2ev2/suite -count=1`
- For focused work, run the target scenario package directly.

## Catalog

| Domain | Scenario path | Purpose | Run |
| --- | --- | --- | --- |
| `message` | `test/e2ev2/message/single_node_send` | Prove `cmd/wukongimv2` can complete one single-node cluster WKProto `SEND -> SENDACK` closure and expose sender/receiver rows through `/conversation/list`. | `GOWORK=off go test -tags=e2e ./test/e2ev2/message/single_node_send -count=1` |
| `message` | `test/e2ev2/message/recipient_authority` | Prove `cmd/wukongimv2` routes committed group messages through recipient UID authority, updates subscriber-owned `/conversation/list` rows, and exposes low-cardinality authority metrics, with an opt-in 100k subscriber stress path. | `GOWORK=off go test -tags=e2e ./test/e2ev2/message/recipient_authority -count=1` |
| `message` | `test/e2ev2/message/cross_node_delivery` | Prove a static three-node `cmd/wukongimv2` cluster can deliver person-channel messages between users connected to different nodes in both directions. | `GOWORK=off go test -tags=e2e ./test/e2ev2/message/cross_node_delivery -count=1 -timeout 2m` |
| `control` | `test/e2ev2/control/bootstrap_task` | Prove a static three-node `cmd/wukongimv2` cluster completes ControllerV2 bootstrap Slot tasks and clears active task state from manager Slot inventory while node-local Slot Raft status is observable. | `GOWORK=off go test -tags=e2e ./test/e2ev2/control/bootstrap_task -count=1 -timeout 2m` |
| `control` | `test/e2ev2/control/slot_leader_transfer` | Prove a static three-node `cmd/wukongimv2` cluster accepts single and batch Slot leader-transfer manager requests, clears ControllerV2 tasks, and observes legal non-source Slot Raft leaders. | `GOWORK=off go test -tags=e2e ./test/e2ev2/control/slot_leader_transfer -count=1 -timeout 2m` |

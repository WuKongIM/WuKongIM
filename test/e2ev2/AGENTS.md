# test/e2ev2 AGENTS

This file is for agents working inside the `test/e2ev2` layer. This tree owns
black-box e2e coverage for `cmd/wukongim` and internal behavior only.

## Core Rules

- Keep this layer process-level and black-box: validate behavior through real
  `cmd/wukongim` processes, real protocols, and public HTTP entrypoints.
- Do not import `internal/app`, `internal/usecase`, or storage internals.
- Describe deployment shape as a single-node cluster or multi-node cluster,
  never as a cluster-bypassing standalone mode.
- Put reusable v2 test support in `test/e2ev2/suite`; scenario files should
  focus on business assertions.
- The v2 suite builds `./cmd/wukongim` by default. Use `WK_E2E_BINARY` to
  point at a prebuilt v2 binary.
- `WK_E2EV2_BINARY` is accepted only as a temporary fallback during the
  transition; new scripts and docs should use `WK_E2E_BINARY`.
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
| `message` | `test/e2ev2/message/single_node_send` | Prove `cmd/wukongim` can complete one single-node cluster WKProto `SEND -> SENDACK` closure and expose sender/receiver rows through `/conversation/list`. | `GOWORK=off go test -tags=e2e ./test/e2ev2/message/single_node_send -count=1` |
| `message` | `test/e2ev2/message/webhook` | Prove a single-node cluster posts `user.onlinestatus`, `msg.notify`, and `msg.offline` webhook callbacks to an external HTTP endpoint after a real WKProto SEND. | `GOWORK=off go test -tags=e2e ./test/e2ev2/message/webhook -count=1 -timeout 2m -p=1` |
| `message` | `test/e2ev2/message/recipient_authority` | Prove `cmd/wukongim` routes committed group messages through recipient UID authority, updates subscriber-owned `/conversation/list` rows, and exposes low-cardinality authority metrics, with an opt-in 100k subscriber stress path. | `GOWORK=off go test -tags=e2e ./test/e2ev2/message/recipient_authority -count=1` |
| `message` | `test/e2ev2/message/cross_node_delivery` | Prove a static three-node `cmd/wukongim` cluster can deliver person-channel messages between users connected to different nodes in both directions. | `GOWORK=off go test -tags=e2e ./test/e2ev2/message/cross_node_delivery -count=1 -timeout 2m` |
| `cluster` | `test/e2ev2/cluster/dynamic_node_join` | Prove dynamic data-node seed join, activation, delivery, onboarding, scale-in drain, unsafe-operation conflicts, negative join/activation paths, and concurrent task guards through public manager and WKProto entrypoints. | `GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -count=1 -timeout 10m -p=1` |
| `cluster` | `test/e2ev2/cluster/dynamic_node_readiness` | Prove Stage 9 dynamic-node production readiness: health freshness, manager/metrics evidence, and join/onboard/scale-in/remove while real WKProto traffic continues. | `GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_readiness -count=1 -timeout 12m -p=1` |
| `cluster` | `test/e2ev2/cluster/dynamic_node_faults` | Prove opt-in gofail-backed dynamic-node join, onboarding, scale-in, and remove fault recovery through public manager and WKProto entrypoints. | `scripts/build-gofail-binary.sh --cmd ./cmd/wukongim --package internal/usecase/management --package pkg/controller --package pkg/cluster/tasks --package pkg/cluster/net --out /tmp/wukongim-gofail`<br>`WK_E2E_BINARY=/tmp/wukongim-gofail WK_E2EV2_GOFAIL_DYNAMIC_NODE=1 GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -count=1 -timeout 15m -p=1` |
| `cluster` | `test/e2ev2/cluster/controller_voter_promotion` | Prove an activated dynamic data node can be promoted through manager HTTP into Controller Raft voting membership and still serve real WKProto traffic. | `GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/controller_voter_promotion -count=1 -timeout 6m -p=1` |
| `control` | `test/e2ev2/control/bootstrap_task` | Prove a static three-node `cmd/wukongim` cluster completes ControllerV2 bootstrap Slot tasks and clears active task state from manager Slot inventory while node-local Slot Raft status is observable. | `GOWORK=off go test -tags=e2e ./test/e2ev2/control/bootstrap_task -count=1 -timeout 2m` |
| `control` | `test/e2ev2/control/slot_leader_transfer` | Prove a static three-node `cmd/wukongim` cluster accepts single and batch Slot leader-transfer manager requests, clears ControllerV2 tasks, and observes legal non-source Slot Raft leaders. | `GOWORK=off go test -tags=e2e ./test/e2ev2/control/slot_leader_transfer -count=1 -timeout 2m` |

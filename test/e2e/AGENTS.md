# test/e2e AGENTS

This file is for agents working inside the `test/e2e` layer. This tree owns
black-box e2e coverage for `cmd/wukongim` and internal behavior only.

## Core Rules

- Keep this layer process-level and black-box: validate behavior through real
  `cmd/wukongim` processes, real protocols, and public HTTP entrypoints.
- Do not import `internal/app`, `internal/usecase`, or storage internals.
- Describe deployment shape as a single-node cluster or multi-node cluster,
  never as a cluster-bypassing standalone mode.
- Put reusable test support in `test/e2e/suite`; scenario files should
  focus on business assertions.
- The e2e suite builds `./cmd/wukongim` by default. Use `WK_E2E_BINARY` to
  point at a prebuilt binary.
- The entire `WK_E2E_*` namespace is harness-only. `NodeProcess` removes those
  variables from every spawned node; use `NodeSpec.Env` only for real node
  runtime variables such as `WK_NODE_ID` or non-config controls such as
  `GOFAIL_HTTP`.
- Keep Unix socket placement independent from artifact storage. `Workspace`
  owns a short per-workspace plugin socket root; an explicit node-level
  `WK_PLUGIN_SOCKET_PATH` override remains authoritative.
- Keep failure diagnostics bounded: prefer config paths, stdout/stderr tails,
  app log tails, public HTTP responses, and public metrics.
- Channel recovery polling through public `/message/send` must retry only the
  exact `503 {"error":"retry required"}` status and reuse one stable
  idempotency key and request body. Broad error retries and regenerated
  `client_msg_no` values are forbidden.

## Structure Contract

- The `test/e2e` root contains only shared documentation, `suite/`, and
  business-domain directories.
- Add new scenarios under `test/e2e/<domain>/<scenario>/`.
- Every business-domain directory must have its own `AGENTS.md`.
- Every scenario directory must contain its own `AGENTS.md` and one primary
  `*_test.go` file.
- `suite/` owns reusable black-box harness code only, not
  scenario-specific business assertions.

## Running

- Full suite: `GOWORK=off go test -tags=e2e ./test/e2e/... -count=1`
- Focused suite helpers: `GOWORK=off go test -tags=e2e ./test/e2e/suite -count=1`
- For focused work, run the target scenario package directly.

## Catalog

| Domain | Scenario path | Purpose | Run |
| --- | --- | --- | --- |
| `backup` | `test/e2e/backup/three_node_restore` | Prove a real three-node source publishes a baseline plus incremental point, a fresh three-node successor restores and activates it, and normal-mode history plus sequence-continuous writes work after restart. | `GOWORK=off go test -tags=e2e ./test/e2e/backup/three_node_restore -count=1 -timeout 8m -p=1` |
| `backup` | `test/e2e/backup/controller_leader_failover` | Prove a new Controller Leader resumes the same persisted incremental backup job after process failover and the stopped combined Controller/data node rejoins. | `GOWORK=off go test -tags=e2e ./test/e2e/backup/controller_leader_failover -count=1 -timeout 8m -p=1` |
| `backup` | `test/e2e/backup/controller_leader_outage` | Prove a three-Slot-replica/two-Channel-replica cluster recovers readiness and a new Controller Leader completes the same incremental backup job while the stopped former Controller Leader/data node remains offline. | `GOWORK=off go test -tags=e2e ./test/e2e/backup/controller_leader_outage -count=1 -timeout 8m -p=1` |
| `backup` | `test/e2e/backup/data_node_outage` | Prove a three-Slot-replica/two-Channel-replica cluster recovers readiness and one incremental backup job publishes its exact restore point while a non-Controller-Leader data node remains offline. | `GOWORK=off go test -tags=e2e ./test/e2e/backup/data_node_outage -count=1 -timeout 8m -p=1` |
| `message` | `test/e2e/message/single_node_send` | Prove `cmd/wukongim` can complete one single-node cluster WKProto `SEND -> SENDACK` closure and expose sender/receiver rows through `/conversation/list`. | `GOWORK=off go test -tags=e2e ./test/e2e/message/single_node_send -count=1` |
| `message` | `test/e2e/message/webhook` | Prove a single-node cluster posts `user.onlinestatus`, `msg.notify`, and `msg.offline` webhook callbacks to an external HTTP endpoint after a real WKProto SEND. | `GOWORK=off go test -tags=e2e ./test/e2e/message/webhook -count=1 -timeout 2m -p=1` |
| `message` | `test/e2e/message/message_event_stream` | Prove `/message/event` buffers stream deltas in the Slot-leader cache, forwards from non-leader nodes, fails closed after Slot-leader cache loss, proposes one finish batch, exposes public metrics, and survives restart through `/channel/messagesync` event summaries. | `GOWORK=off go test -tags=e2e ./test/e2e/message/message_event_stream -count=1 -timeout 2m` |
| `message` | `test/e2e/message/recipient_authority` | Prove `cmd/wukongim` routes committed group messages through recipient UID authority, updates subscriber-owned `/conversation/list` rows, and exposes low-cardinality authority metrics, with an opt-in 100k subscriber stress path. | `GOWORK=off go test -tags=e2e ./test/e2e/message/recipient_authority -count=1` |
| `message` | `test/e2e/message/cross_node_delivery` | Prove a static three-node `cmd/wukongim` cluster can deliver person-channel messages between users connected to different nodes in both directions. | `GOWORK=off go test -tags=e2e ./test/e2e/message/cross_node_delivery -count=1 -timeout 2m` |
| `cluster` | `test/e2e/cluster/dynamic_node_join` | Prove dynamic data-node seed join, activation, delivery, onboarding, scale-in drain, unsafe-operation conflicts, negative join/activation paths, and concurrent task guards through public manager and WKProto entrypoints. | `GOWORK=off go test -tags=e2e ./test/e2e/cluster/dynamic_node_join -count=1 -timeout 10m -p=1` |
| `cluster` | `test/e2e/cluster/dynamic_node_readiness` | Prove Stage 9 dynamic-node production readiness: health freshness, manager/metrics evidence, and join/onboard/scale-in/remove while real WKProto traffic continues. | `GOWORK=off go test -tags=e2e ./test/e2e/cluster/dynamic_node_readiness -count=1 -timeout 12m -p=1` |
| `cluster` | `test/e2e/cluster/dynamic_node_faults` | Prove opt-in gofail-backed dynamic-node join, onboarding, scale-in, and remove fault recovery through public manager and WKProto entrypoints. | `scripts/build-gofail-binary.sh --cmd ./cmd/wukongim --package internal/usecase/management --package pkg/controller --package pkg/cluster/tasks --package pkg/cluster/net --out /tmp/wukongim-gofail`<br>`WK_E2E_BINARY=/tmp/wukongim-gofail WK_E2E_GOFAIL_DYNAMIC_NODE=1 GOWORK=off go test -tags=e2e ./test/e2e/cluster/dynamic_node_faults -count=1 -timeout 15m -p=1` |
| `cluster` | `test/e2e/cluster/controller_voter_promotion` | Prove an activated dynamic data node can be promoted through manager HTTP into Controller Raft voting membership and still serve real WKProto traffic. | `GOWORK=off go test -tags=e2e ./test/e2e/cluster/controller_voter_promotion -count=1 -timeout 6m -p=1` |
| `control` | `test/e2e/control/bootstrap_task` | Prove a static three-node `cmd/wukongim` cluster completes Controller bootstrap Slot tasks and clears active task state from manager Slot inventory while node-local Slot Raft status is observable. | `GOWORK=off go test -tags=e2e ./test/e2e/control/bootstrap_task -count=1 -timeout 2m` |
| `control` | `test/e2e/control/slot_leader_transfer` | Prove a static three-node `cmd/wukongim` cluster accepts single and batch Slot leader-transfer manager requests, clears Controller tasks, and observes legal non-source Slot Raft leaders. | `GOWORK=off go test -tags=e2e ./test/e2e/control/slot_leader_transfer -count=1 -timeout 2m` |

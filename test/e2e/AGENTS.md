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
- When an e2e test needs large or specialized seed data, prefer adding a
  dedicated test-data generation API over direct store/database access. The API
  must be disabled by default, must only allow guest access when an explicit
  test mode is enabled, and should be reusable for later e2e scenarios with
  similar data-generation needs.

## Test Data API Router Rules

- Put e2e-only data generation endpoints under the dedicated prefix
  `/testdata/e2e`; do not add them under production business routes such as
  `/user/**`, `/channel/**`, or `/message/**`.
- Use `POST /testdata/e2e/<domain>/<dataset>` for data generation. The
  `<domain>` segment should match the owning e2e domain when possible
  (`cluster`, `message`, etc.), and `<dataset>` should describe the reusable
  dataset shape, not one narrow test function.
- Return `404` or do not register the route when explicit test mode is
  disabled. Guest access is only allowed after that test-mode guard has passed.
- Keep route inputs in JSON request bodies, include deterministic fields such
  as `prefix`, `count`, `seed`, or `payload_bytes`, and make generation
  idempotent where practical so failed e2e runs can be retried safely.
- Add reusable client helpers for these routes in `test/e2e/suite`; scenario
  tests should call those helpers instead of constructing raw test-data URLs.

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
| `bench` | `test/e2e/bench/wkbench_smoke` | Prove wkbench can prepare tiny benchmark data through the target bench API, drive person and group WKProto traffic through one worker, write a successful report with non-zero sendack success, and fail preflight when server bench mode is disabled. | `GOWORK=off go test -tags=e2e ./test/e2e/bench/wkbench_smoke -count=1` |
| `cluster` | `test/e2e/cluster/dynamic_node_join` | Prove a fourth data node can join a running three-node cluster through seed config, exchange WKProto person messages with an existing node, receive Slot resources through manager onboarding, and optionally catch up from a large Slot snapshot. | `go test -tags=e2e ./test/e2e/cluster/dynamic_node_join -count=1` |
| `cluster` | `test/e2e/cluster/controller_snapshot` | Prove a stopped Controller follower can rejoin after leader compaction by restoring a large Controller Raft snapshot generated through the test-data API. | `go test -tags=e2e ./test/e2e/cluster/controller_snapshot -count=1` |
| `cluster` | `test/e2e/cluster/node_scalein_channel_drain` | Prove manager-driven data-node scale-in drains Slot ownership, channel replicas, active channel migrations, and runtime sessions before reporting the node safe to remove while WKProto delivery continues. | `go test -tags=e2e ./test/e2e/cluster/node_scalein_channel_drain -count=1 -timeout 3m` |
| `cluster` | `test/e2e/cluster/gofail_transport` | Opt-in smoke that starts a gofail-enabled single-node cluster and verifies transport failpoints are exposed through `GOFAIL_HTTP`. | `WK_E2E_BINARY=/tmp/wukongim-gofail WK_E2E_GOFAIL_TRANSPORT_SMOKE=1 go test -tags=e2e ./test/e2e/cluster/gofail_transport -count=1` |
| `message` | `test/e2e/message/single_node_send_message` | Prove one fresh single-node cluster can complete a real WKProto `Send -> SendAck -> Recv -> RecvAck` closure. | `go test -tags=e2e ./test/e2e/message/single_node_send_message -count=1` |
| `message` | `test/e2e/message/cross_node_closure` | Prove a three-node cluster can deliver one person-channel message when sender and recipient are connected to different follower nodes. | `go test -tags=e2e ./test/e2e/message/cross_node_closure -count=1` |
| `message` | `test/e2e/message/delivery_tag_group_delivery` | Prove a three-node cluster can deliver group messages through delivery tag subscriber partitions, refresh after public subscriber mutations, and run opt-in 100k subscriber stress. | `go test -tags=e2e ./test/e2e/message/delivery_tag_group_delivery -count=1` |
| `message` | `test/e2e/message/request_scoped_subscriber_delivery` | Prove request-scoped `/message/send` subscribers deliver only to requested online subscribers across nodes. | `go test -tags=e2e ./test/e2e/message/request_scoped_subscriber_delivery -count=1` |
| `message` | `test/e2e/message/channel_activation_profile` | Opt-in profile scenario that activates configurable waves of 100 concurrent group channels through real API/WKProto entrypoints and writes pprof CPU/heap top artifacts. | `WK_E2E_CHANNEL_ACTIVATION_PROFILE=1 go test -tags=e2e ./test/e2e/message/channel_activation_profile -count=1` |
| `message` | `test/e2e/message/channel_leader_transfer` | Prove an active person-channel leader can be transferred to a non-leader ISR replica through manager APIs while WKProto delivery continues. | `go test -tags=e2e ./test/e2e/message/channel_leader_transfer -count=1 -timeout 2m` |
| `message` | `test/e2e/message/slot_leader_failover` | Prove cross-node delivery survives a slot leader failover after both clients are already connected on follower nodes. | `go test -tags=e2e ./test/e2e/message/slot_leader_failover -count=1` |
| `message` | `test/e2e/message/expired_leader_lease` | Reproduce the current cross-node delivery failure after channel runtime metadata is observed with an expired leader lease. | `go test -tags=e2e ./test/e2e/message/expired_leader_lease -count=1` |
| `message` | `test/e2e/message/idle_cross_node_delivery_timeout` | Reproduce the current cross-node delivery timeout after one bootstrap message sits idle until the channel runtime leader lease expires. | `go test -tags=e2e ./test/e2e/message/idle_cross_node_delivery_timeout -count=1` |

# message AGENTS

This file is for agents working inside `test/e2e/message`.

## Domain Purpose

This domain covers black-box message and conversation behavior for
`cmd/wukongim`.

## Scenarios

| Scenario | Purpose | Run |
| --- | --- | --- |
| `no_persist` | Prove ordinary transient HTTP sends to WKProto subscribers, receive flags, permission checks, and unchanged history in 256-Hash-Slot single-node and three-node clusters. | `GOWORK=off go test -tags=e2e ./test/e2e/message/no_persist -count=1 -timeout 3m -p=1` |
| `single_node_send` | Prove WKProto `SEND -> SENDACK`, person membership establishment, and zero repeat membership writes after `directory_ready` in a single-node cluster. | `GOWORK=off go test -tags=e2e ./test/e2e/message/single_node_send -count=1` |
| `javascript_web_quickstart` | Prove the published localhost-BFF JavaScript quickstart completes bidirectional durable messaging, SENDACK/receive, disconnect, reconnect, and offline sync in Chromium against a real 256-Hash-Slot single-node cluster with Token auth enabled; HTTP readiness precedes browser CONNECT with BFF-issued credentials. History must contain the offline message after bounded BFF projection recovery and the UI must display it once regardless of live/sync arrival order. Failure summaries include only capture bounds and fixed-spec line numbers. Failure evidence is bounded to three PNGs of at most 2 MiB each. | `WK_E2E_DOCS_JAVASCRIPT_WEB=1 GOWORK=off go test -tags=e2e ./test/e2e/message/javascript_web_quickstart -count=1 -timeout 10m -p=1 -v` |
| `easy_sdk_jsonrpc` | Prove source-aligned EasySDK iOS v1.1.0 binary/camelCase and Android v1.0.4 text/snake_case JSON-RPC profiles complete bidirectional messaging, acknowledgments, ping correlation, disconnect cleanup, and reconnect through public endpoints on a 256-slot single-node cluster without claiming SDK artifact execution. | `GOWORK=off go test -tags=e2e ./test/e2e/message/easy_sdk_jsonrpc -count=1 -timeout 2m -p=1` |
| `easy_sdk_docs_release` | Compile the literal bilingual Web EasySDK tutorial against an integrity-pinned npm package and prove Chromium messaging, cleanup, reconnect and connection deadlines on a 256-hash-slot single-node cluster. | `WK_E2E_EASYSDK_ARTIFACTS=/absolute/artifact-directory GOWORK=off go test -tags=e2e ./test/e2e/message/easy_sdk_docs_release -count=1 -timeout=5m -p=1 -v` |
| `conversation_directory` | Prove candidate-bounded pagination, activation priority, message-time ordering stability, monotonic badge state, and hide/remove/rejoin transitions through public APIs. | `GOWORK=off go test -tags=e2e ./test/e2e/message/conversation_directory -count=1 -timeout 2m` |
| `conversation_directory_multi_node` | Prove Channel-Leader-grouped hydration, persisted-head reads without runtime activation, whole-page failure and original-cursor recovery, and UID membership reads through a four-node ingress outside the UID Slot replica set. | `GOWORK=off go test -tags=e2e ./test/e2e/message/conversation_directory_multi_node -count=1 -timeout 3m -p=1` |
| `legacy_conversation_sync` | Prove the deprecated v2.2 `/conversation/sync` projects first and later WKProto messages into exact sender and offline-recipient person/group views in single-node and remote-Leader multi-node clusters. | `GOWORK=off go test -tags=e2e ./test/e2e/message/legacy_conversation_sync -count=1 -timeout 3m -p=1` |
| `webhook` | Prove single-node cluster post-commit callbacks and three-node `msg.before_send` admission, custom codes, mutation, timeout/error policies, and committed history through WKProto and HTTP; also verify authenticated fault handling, bounded per-node overload/recovery, callback counts, and public metrics; build the runnable Go callback and validate its decisions/history against a single-node cluster. | `GOWORK=off go test -tags=e2e ./test/e2e/message/webhook -count=1 -timeout 2m -p=1` |
| `send_permission` | Prove `cmd/wukongim` enforces migrated legacy send-permission decisions through public channel-management and `/message/send` HTTP APIs. | `GOWORK=off go test -tags=e2e ./test/e2e/message/send_permission -count=1` |
| `terminal_disband` | Prove message-usecase permission admission rejects a disbanded source for ordinary, system-UID, and system-device sends, exposes terminal list/pull behavior, and performs no membership fanout. | `GOWORK=off go test -tags=e2e ./test/e2e/message/terminal_disband -count=1 -timeout 2m` |
| `message_event_stream` | Prove `/message/event` buffers stream deltas in the Slot-leader cache, forwards from non-leader nodes, fails closed after Slot-leader cache loss, proposes one finish batch, exposes public metrics, and survives restart through `/channel/messagesync` event summaries. | `GOWORK=off go test -tags=e2e ./test/e2e/message/message_event_stream -count=1 -timeout 2m` |
| `recipient_authority` | Prove committed group SEND has zero recipient membership writes, membership-backed `/conversation/list` still hydrates the user view, and low-cardinality directory/hydration metrics are exposed, with an opt-in 100k subscriber stress path. | `GOWORK=off go test -tags=e2e ./test/e2e/message/recipient_authority -count=1` |
| `medium_recipient_hotpath` | Opt-in higher-fidelity local Cloud Medium gate plus a separate 30-minute, 5,000-channel permission-pressure soak. Both use a real three-node process cluster, WKProto sockets, Raft, Pebble, exact Presence convergence, SENDACK/RECV latency, zero measured membership writes, and bounded public pressure evidence. | Short gate: `WK_E2E_MEDIUM_RECIPIENT_HOTPATH=1 WK_E2E_MEDIUM_RECIPIENT_ENFORCE_ACCEPTANCE=1 GOWORK=off go test -tags=e2e ./test/e2e/message/medium_recipient_hotpath -run TestCloudMediumScaledRecipientHotPath -count=1 -timeout 5m -p=1 -v`<br>Soak: `WK_E2E_MEDIUM_RECIPIENT_PERMISSION_SOAK=1 WK_E2E_MEDIUM_RECIPIENT_SOAK_DURATION=30m WK_E2E_MEDIUM_RECIPIENT_GROUP_CHANNELS=5000 WK_E2E_MEDIUM_RECIPIENT_QPS=4500 GOWORK=off go test -tags=e2e ./test/e2e/message/medium_recipient_hotpath -run TestCloudMediumPermissionSoak -count=1 -timeout 40m -p=1 -v` |
| `cross_node_delivery` | Prove a static three-node `cmd/wukongim` cluster delivers person-channel messages across nodes in both directions, including explicit Slot/Channel two-replica topology and an opt-in same-host 2/2-versus-3/3 comparison at 2,000 SEND/s. | `GOWORK=off go test -tags=e2e ./test/e2e/message/cross_node_delivery -count=1 -timeout 2m` |
| `cmd_sync` | Prove ordinary membership and CMD membership/log isolation, explicit CMD binding, `/message/sync` delivery, and `/message/syncack` draining in a single-node cluster. | `GOWORK=off go test -tags=e2e ./test/e2e/message/cmd_sync -count=1 -timeout 2m` |
| `message_retention` | Prove a static three-node `cmd/wukongim` cluster forwards manager retention requests to the channel leader, physically cleans retained local message rows when enabled, and consistently hides retained message sequences after leader restart. | `GOWORK=off go test -tags=e2e ./test/e2e/message/message_retention -count=1 -timeout 2m -p=1` |
| `channel_failover` | Prove a static three-node `cmd/wukongim` cluster preserves Channel quorum-acknowledged messages after one data node stops or pauses with TCP connections retained, rotates bounded scans across ten Slots, fails over affected channel leaders, checks continued writes and exact cross-ingress retries before the paused leader resumes, verifies sending through that node after resume and complete More-driven history pagination, and fails closed for new placement while a required replica is unavailable. | `GOWORK=off go test -tags=e2e ./test/e2e/message/channel_failover -count=1 -timeout 6m -p=1` |
| `bench_churn` | Prove wkbench identity-swap churn updates real group membership before the replacement UID sends in the next traffic window. | `GOWORK=off go test -tags=e2e ./test/e2e/message/bench_churn -count=1 -timeout 2m` |
| `channel_failover` | Prove a static three-node `cmd/wukongim` cluster preserves Channel quorum-acknowledged messages after one data node stops, fails over affected channel leaders, and fails closed for new placement while a required replica is unavailable. | `GOWORK=off go test -tags=e2e ./test/e2e/message/channel_failover -count=1 -timeout 3m -p=1` |
| `bench_churn` | Prove immediate cross-node Bench Token creation/rotation and exact rejection, including a non-replica, then identity-swap group churn with Gateway Token auth enabled. | `GOWORK=off go test -tags=e2e ./test/e2e/message/bench_churn -count=1 -timeout 2m` |
| `chat_lifecycle` | Prove a real three-node person Channel becomes naturally absent after five idle minutes, reheats through real traffic, preserves sequence/metadata continuity, runs a full version-zero sync after every login, and preserves person/group recipient sequence under cross-ingress bursts. | `GOWORK=off go test -tags=e2e ./test/e2e/message/chat_lifecycle -run 'Test(PersonChannel(NaturalReheat|CrossIngressBurstPreservesReceiveSequence)|GroupChannelCrossIngressBurstPreservesReceiveSequence)$' -count=1 -timeout 9m -p=1` |

## Maintenance Rules

- When adding a new message scenario, create
  `test/e2e/message/<scenario>/`.
- Give each scenario its own `AGENTS.md` and one primary
  `<scenario>_test.go`.
- If a scenario is added, removed, renamed, or its run command, steps, or
  diagnostics change, update this file and the scenario's `AGENTS.md` in the
  same change.
- Keep one-off helpers inside the scenario directory first. Only promote them
  to `test/e2e/suite` after real multi-scenario reuse appears.

- The Channel failover fixture uses 256 Hash Slots and explicitly disables Token authentication for its existing tokenless readiness client. SDK acceptance keeps Token authentication enabled.

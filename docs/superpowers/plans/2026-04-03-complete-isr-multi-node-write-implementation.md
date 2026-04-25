# Complete ISR Multi-Node Write Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make message send use the real `pkg/storage/channellog` data plane with multi-node ISR replication, commit-before-ack semantics, durable recovery, and no dependency on cross-node online delivery.

**Architecture:** Keep the current `raftcluster/metastore` stack as the control plane and add a separate `isrnode/channellog` data plane. Control-plane metadata becomes the authoritative source of leader, ISR, epoch, feature, and lease state; `internal/app` projects that metadata into both `isrnode` and `channellog`, and `internal/usecase/message` switches from the local-memory fallback to the real durable cluster path with request-scoped contexts.

**Tech Stack:** Go 1.23, `pkg/cluster/raftcluster`, `pkg/storage/metadb`, `pkg/storage/metafsm`, `pkg/storage/metastore`, `pkg/replication/isr`, `pkg/replication/isrnode`, `pkg/transport/nodetransport`, `pkg/storage/channellog`, `github.com/bwmarrin/snowflake`, existing gateway/API integration tests.

---

## File Map

### Control-plane metadata

- Create: `pkg/storage/metadb/channel_runtime_meta.go`
  Responsibility: authoritative `ChannelRuntimeMeta` model and Pebble-backed CRUD for replication metadata.
- Modify: `pkg/storage/metadb/catalog.go`
  Responsibility: register new family/columns/indexes for runtime metadata.
- Modify: `pkg/storage/metadb/codec.go`
  Responsibility: encode/decode runtime metadata family values.
- Modify: `pkg/storage/metadb/batch.go`
  Responsibility: write batch support for runtime metadata upsert/delete.
- Create: `pkg/storage/metadb/channel_runtime_meta_test.go`
  Responsibility: persistence and validation coverage.
- Modify: `pkg/storage/metafsm/command.go`
  Responsibility: add FSM commands for upsert/delete runtime metadata.
- Modify: `pkg/storage/metafsm/statemachine.go`
  Responsibility: apply runtime metadata commands through metadb write batch.
- Modify: `pkg/storage/metafsm/state_machine_test.go`
  Responsibility: FSM coverage for runtime metadata.
- Modify: `pkg/storage/metastore/store.go`
  Responsibility: expose `GetChannelRuntimeMeta`, `UpsertChannelRuntimeMeta`, and optional list APIs for preload.
- Modify: `pkg/storage/metastore/integration_test.go`
  Responsibility: distributed metadata read/write coverage.

### Durable channel state

- Create: `pkg/storage/channellog/state_store.go`
  Responsibility: real `ChannelStateStore` backed by Pebble keys in `channellog`.
- Create: `pkg/storage/channellog/state_store_test.go`
  Responsibility: idempotency read/write, checkpoint commit, snapshot restore coverage.
- Modify: `pkg/storage/channellog/db.go`
  Responsibility: expose real state-store factory helpers.
- Modify: `pkg/storage/channellog/store_keys.go`
  Responsibility: add key prefixes for idempotency and state snapshot payloads.
- Modify: `pkg/storage/channellog/store_codec.go`
  Responsibility: encode/decode idempotency and state snapshot payloads.
- Modify: `pkg/storage/channellog/apply.go`
  Responsibility: use the real committing state store for committed apply.
- Modify: `pkg/storage/channellog/storage_integration_test.go`
  Responsibility: real-store send/fetch/idempotency coverage.

### Message ID generation

- Create: `internal/runtime/messageid/snowflake.go`
  Responsibility: snowflake-backed `channellog.MessageIDGenerator`.
- Create: `internal/runtime/messageid/snowflake_test.go`
  Responsibility: uniqueness and node-id validation coverage.
- Modify: `internal/app/config.go`
  Responsibility: validate `Node.ID` against the snowflake node-id range and add channel-log storage path defaults.
- Modify: `internal/app/config_test.go`
  Responsibility: config validation/default coverage for snowflake and channel-log storage.
- Modify: `go.mod`
  Responsibility: add `github.com/bwmarrin/snowflake`.

### ISR core fetch serving

- Create: `pkg/replication/isrnode/fetch_service.go`
  Responsibility: narrow inbound fetch-serving API for transport adapters.
- Modify: `pkg/replication/isrnode/types.go`
  Responsibility: expose the narrow fetch-serving contract without leaking wire details.
- Modify: `pkg/replication/isrnode/runtime.go`
  Responsibility: implement generation/group validation for inbound fetch serving.
- Modify: `pkg/replication/isrnode/transport.go`
  Responsibility: keep fetch-response handling aligned with the new serving path.
- Modify: `pkg/replication/isrnode/session_test.go`
  Responsibility: request/response serving coverage.
- Modify: `pkg/replication/isrnode/runtime_test.go`
  Responsibility: stale-generation and wrong-group rejection coverage.

### Shared node transport

- Create: `pkg/transport/nodetransport/rpcmux.go`
  Responsibility: explicit RPC service multiplexer for sharing one `HandleRPC` slot.
- Create: `pkg/transport/nodetransport/rpcmux_test.go`
  Responsibility: service dispatch and unknown-service behavior.
- Modify: `pkg/transport/nodetransport/server.go`
  Responsibility: support registering a mux-backed RPC handler without breaking existing users.
- Modify: `pkg/transport/nodetransport/client.go`
  Responsibility: helper for service-prefixed RPC payloads if needed.
- Modify: `pkg/cluster/raftcluster/cluster.go`
  Responsibility: register raftcluster forwarding under the RPC mux instead of owning the raw slot directly.
- Modify: `pkg/cluster/raftcluster/forward_test.go`
  Responsibility: forwarding behavior under RPC mux.

### ISR production transport adapter

- Create: `pkg/replication/isrnodetransport/doc.go`
  Responsibility: package contract and control/data-plane separation notes.
- Create: `pkg/replication/isrnodetransport/codec.go`
  Responsibility: on-wire request/response codec for ISR fetch serving.
- Create: `pkg/replication/isrnodetransport/codec_test.go`
  Responsibility: codec round-trip coverage.
- Create: `pkg/replication/isrnodetransport/adapter.go`
  Responsibility: `isrnode.Transport` implementation using `nodetransport`.
- Create: `pkg/replication/isrnodetransport/session.go`
  Responsibility: `isrnode.PeerSessionManager` and peer session reuse.
- Create: `pkg/replication/isrnodetransport/adapter_test.go`
  Responsibility: transport/session behavior and backpressure coverage.
- Create: `pkg/replication/isrnodetransport/integration_test.go`
  Responsibility: multi-node fetch request/response integration coverage.

### App wiring and metadata sync

- Modify: `internal/app/app.go`
  Responsibility: hold data-plane runtime fields and accessors.
- Modify: `internal/app/build.go`
  Responsibility: construct channel-log DB, isr runtime, message ID generator, metadata refresher/applier, and real message app dependencies.
- Modify: `internal/app/lifecycle.go`
  Responsibility: start/stop data-plane components in a deterministic order.
- Modify: `internal/app/lifecycle_test.go`
  Responsibility: composition and lifecycle ordering coverage.
- Modify: `internal/app/integration_test.go`
  Responsibility: end-to-end app bootstrap + data-plane availability coverage.
- Create: `internal/app/channelmeta.go`
  Responsibility: local metadata refresher, projector, preload, and reconcile loop.
- Create: `internal/app/channelmeta_test.go`
  Responsibility: metadata preload/reconcile behavior.

### Request context plumbing and durable send switch

- Modify: `internal/usecase/message/app.go`
  Responsibility: accept durable cluster and refresh collaborators with context-aware send flow.
- Modify: `internal/usecase/message/send.go`
  Responsibility: stop using `context.Background()` and use request-scoped context.
- Modify: `internal/usecase/message/command.go`
  Responsibility: add send context carrier if needed by the usecase boundary.
- Modify: `internal/usecase/message/send_test.go`
  Responsibility: timeout and retry-on-refresh coverage.
- Modify: `internal/access/gateway/handler.go`
  Responsibility: pass ingress request context into message usecase.
- Modify: `internal/access/gateway/frame_router.go`
  Responsibility: route send with context-aware usecase entrypoint.
- Modify: `internal/access/gateway/integration_test.go`
  Responsibility: send timeout and durable-ack behavior.
- Modify: `internal/access/api/server.go`
  Responsibility: pass HTTP request context into message usecase.
- Modify: `internal/access/api/message_send.go`
  Responsibility: use request context in the send path.
- Modify: `internal/access/api/integration_test.go`
  Responsibility: HTTP send timeout and durable-ack behavior.

### Multi-node end-to-end coverage

- Create: `pkg/storage/channellog/multinode_integration_test.go`
  Responsibility: three-node durable-send/fetch/restart semantics without app wiring.
- Create: `internal/app/multinode_integration_test.go`
  Responsibility: three real app instances with leader send and durable read-back.

## Task 1: Add Authoritative Control-Plane `ChannelRuntimeMeta`

**Files:**
- Create: `pkg/storage/metadb/channel_runtime_meta.go`
- Create: `pkg/storage/metadb/channel_runtime_meta_test.go`
- Modify: `pkg/storage/metadb/catalog.go`
- Modify: `pkg/storage/metadb/codec.go`
- Modify: `pkg/storage/metadb/batch.go`
- Modify: `pkg/storage/metafsm/command.go`
- Modify: `pkg/storage/metafsm/statemachine.go`
- Modify: `pkg/storage/metafsm/state_machine_test.go`
- Modify: `pkg/storage/metastore/store.go`
- Modify: `pkg/storage/metastore/integration_test.go`

- [ ] **Step 1: Write failing metadb persistence tests**

```go
func TestShardStoreUpsertAndGetChannelRuntimeMeta(t *testing.T) {
	db := openTestDB(t)
	shard := db.ForSlot(7)
	meta := ChannelRuntimeMeta{
		ChannelID:    "u1",
		ChannelType:  1,
		ChannelEpoch: 3,
		LeaderEpoch:  2,
		Replicas:     []uint64{1, 2, 3},
		ISR:          []uint64{1, 2},
		Leader:       1,
		MinISR:       2,
		Status:       2,
		LeaseUntilMS: 1700000000000,
	}

	require.NoError(t, shard.UpsertChannelRuntimeMeta(context.Background(), meta))
	got, err := shard.GetChannelRuntimeMeta(context.Background(), "u1", 1)
	require.NoError(t, err)
	require.Equal(t, meta, got)
}
```

- [ ] **Step 2: Run the targeted metadb tests**

Run: `go test ./pkg/storage/metadb -run 'TestShardStoreUpsertAndGetChannelRuntimeMeta' -v`
Expected: FAIL because runtime metadata storage does not exist yet.

- [ ] **Step 3: Add metadb runtime metadata model and persistence**

```go
type ChannelRuntimeMeta struct {
	ChannelID    string
	ChannelType  int64
	ChannelEpoch uint64
	LeaderEpoch  uint64
	Replicas     []uint64
	ISR          []uint64
	Leader       uint64
	MinISR       int64
	Status       uint8
	Features     uint64
	LeaseUntilMS int64
}
```

Implementation notes:
- keep this separate from `metadb.Channel`
- store one primary record per `(channelID, channelType)`
- encode replica and ISR sets deterministically

- [ ] **Step 4: Add FSM commands and metastore facade**

```go
func EncodeUpsertChannelRuntimeMetaCommand(meta metadb.ChannelRuntimeMeta) []byte
func (s *Store) GetChannelRuntimeMeta(ctx context.Context, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error)
func (s *Store) UpsertChannelRuntimeMeta(ctx context.Context, meta metadb.ChannelRuntimeMeta) error
```

- [ ] **Step 5: Run package tests**

Run: `go test ./pkg/storage/metadb ./pkg/storage/metafsm ./pkg/storage/metastore -run 'Test.*ChannelRuntimeMeta|TestStateMachine' -v`
Expected: PASS

- [x] **Step 6: Commit**

```bash
git add pkg/storage/metadb pkg/storage/metafsm pkg/storage/metastore
git commit -m "feat: add control-plane channel runtime metadata"
```

## Task 2: Add Real `channellog` State Storage

**Files:**
- Create: `pkg/storage/channellog/state_store.go`
- Create: `pkg/storage/channellog/state_store_test.go`
- Modify: `pkg/storage/channellog/db.go`
- Modify: `pkg/storage/channellog/store_keys.go`
- Modify: `pkg/storage/channellog/store_codec.go`
- Modify: `pkg/storage/channellog/apply.go`
- Modify: `pkg/storage/channellog/storage_integration_test.go`

- [ ] **Step 1: Write failing real-state-store tests**

```go
func TestStoreStateFactoryPersistsIdempotencyAndRestoresSnapshot(t *testing.T) {
	db := openTestDB(t)
	key := ChannelKey{ChannelID: "u1", ChannelType: 1}
	store, err := db.StateStoreFactory().ForChannel(key)
	require.NoError(t, err)

	entry := IdempotencyEntry{MessageID: 9, MessageSeq: 3, Offset: 2}
	require.NoError(t, store.PutIdempotency(IdempotencyKey{
		ChannelID: "u1", ChannelType: 1, SenderUID: "s1", ClientMsgNo: "m1",
	}, entry))

	got, ok, err := store.GetIdempotency(IdempotencyKey{
		ChannelID: "u1", ChannelType: 1, SenderUID: "s1", ClientMsgNo: "m1",
	})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, entry, got)
}
```

- [ ] **Step 2: Run the targeted tests**

Run: `go test ./pkg/storage/channellog -run 'TestStoreStateFactoryPersistsIdempotencyAndRestoresSnapshot' -v`
Expected: FAIL because no real state store factory exists.

- [ ] **Step 3: Implement real state storage**

```go
type stateStore struct {
	store *Store
}

func (s *stateStore) PutIdempotency(key IdempotencyKey, entry IdempotencyEntry) error
func (s *stateStore) GetIdempotency(key IdempotencyKey) (IdempotencyEntry, bool, error)
func (s *stateStore) Snapshot(offset uint64) ([]byte, error)
func (s *stateStore) Restore(snapshot []byte) error
```

Rules:
- idempotency state lives in the same Pebble DB as the channel log
- committed apply remains driven by checkpoint advancement
- snapshot payload must round-trip the full idempotency state

- [ ] **Step 4: Expose a real factory from `channellog.DB`**

```go
func (db *DB) StateStoreFactory() StateStoreFactory
```

- [ ] **Step 5: Run package tests**

Run: `go test ./pkg/storage/channellog -run 'TestStoreStateFactoryPersistsIdempotencyAndRestoresSnapshot|TestClusterWithRealStoreSendFetchAndSeqReads' -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add pkg/storage/channellog
git commit -m "feat: add real channellog state storage"
```

## Task 3: Add Snowflake Message ID Generation

**Files:**
- Create: `internal/runtime/messageid/snowflake.go`
- Create: `internal/runtime/messageid/snowflake_test.go`
- Modify: `internal/app/config.go`
- Modify: `internal/app/config_test.go`
- Modify: `go.mod`

- [ ] **Step 1: Write failing generator and config tests**

```go
func TestNewSnowflakeGeneratorRejectsNodeIDOutsideRange(t *testing.T) {
	_, err := messageid.NewSnowflakeGenerator(1024)
	require.Error(t, err)
}

func TestConfigRejectsNodeIDSnowflakeOverflow(t *testing.T) {
	cfg := validConfig()
	cfg.Node.ID = 1024
	require.Error(t, cfg.ApplyDefaultsAndValidate())
}
```

- [ ] **Step 2: Run the targeted tests**

Run: `go test ./internal/runtime/messageid ./internal/app -run 'TestNewSnowflakeGeneratorRejectsNodeIDOutsideRange|TestConfigRejectsNodeIDSnowflakeOverflow' -v`
Expected: FAIL because the generator package and validation do not exist yet.

- [ ] **Step 3: Add the generator and dependency**

```go
type SnowflakeGenerator struct {
	node *snowflake.Node
}

func NewSnowflakeGenerator(nodeID uint64) (*SnowflakeGenerator, error)
func (g *SnowflakeGenerator) Next() uint64
```

- [ ] **Step 4: Wire config validation**

Rules:
- validate `Node.ID <= 1023`
- add `Storage.ChannelLogPath` default under `Node.DataDir`

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/runtime/messageid ./internal/app -run 'TestNewSnowflakeGenerator|TestConfig' -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/runtime/messageid internal/app/config.go internal/app/config_test.go go.mod go.sum
git commit -m "feat: add snowflake message id generator"
```

## Task 4: Extend `isrnode` For Inbound Fetch Serving

**Files:**
- Create: `pkg/replication/isrnode/fetch_service.go`
- Modify: `pkg/replication/isrnode/types.go`
- Modify: `pkg/replication/isrnode/runtime.go`
- Modify: `pkg/replication/isrnode/transport.go`
- Modify: `pkg/replication/isrnode/runtime_test.go`
- Modify: `pkg/replication/isrnode/session_test.go`

- [ ] **Step 1: Write failing serving tests**

```go
func TestServeFetchRejectsUnknownGeneration(t *testing.T) {
	env := newTestEnv(t)
	mustEnsure(t, env.runtime, testMeta(42, 1, 1, []isr.NodeID{1, 2}))
	_, err := env.runtime.(interface {
		ServeFetch(context.Context, isrnode.FetchRequestEnvelope) (isrnode.FetchResponseEnvelope, error)
	}).ServeFetch(context.Background(), isrnode.FetchRequestEnvelope{
		GroupKey: testGroupKey(42),
		Epoch: 1, Generation: 99, ReplicaID: 2, MaxBytes: 1 << 20,
	})
	require.Error(t, err)
}
```

- [ ] **Step 2: Run the targeted tests**

Run: `go test ./pkg/replication/isrnode -run 'TestServeFetchRejectsUnknownGeneration' -v`
Expected: FAIL because no inbound fetch-serving API exists.

- [ ] **Step 3: Add the narrow serving surface**

```go
type FetchRequestEnvelope struct {
	GroupKey   isr.GroupKey
	Epoch      uint64
	Generation uint64
	ReplicaID  isr.NodeID
	FetchOffset uint64
	OffsetEpoch uint64
	MaxBytes   int
}

type FetchResponseEnvelope struct {
	GroupKey   isr.GroupKey
	Epoch      uint64
	Generation uint64
	TruncateTo *uint64
	LeaderHW   uint64
	Records    []isr.Record
}
```

- [ ] **Step 4: Implement runtime serving**

Rules:
- validate group exists and generation matches
- reject stale epoch
- execute underlying `replica.Fetch(...)`
- keep wire codec out of this layer

- [ ] **Step 5: Run package tests**

Run: `go test ./pkg/replication/isrnode -run 'TestServeFetch|TestHandleEnvelope' -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add pkg/replication/isrnode
git commit -m "feat: add isrnode inbound fetch serving"
```

## Task 5: Add Shared RPC Multiplexer And ISR Transport Adapter

**Files:**
- Create: `pkg/transport/nodetransport/rpcmux.go`
- Create: `pkg/transport/nodetransport/rpcmux_test.go`
- Modify: `pkg/transport/nodetransport/server.go`
- Modify: `pkg/transport/nodetransport/client.go`
- Modify: `pkg/cluster/raftcluster/cluster.go`
- Modify: `pkg/cluster/raftcluster/forward_test.go`
- Create: `pkg/replication/isrnodetransport/doc.go`
- Create: `pkg/replication/isrnodetransport/codec.go`
- Create: `pkg/replication/isrnodetransport/codec_test.go`
- Create: `pkg/replication/isrnodetransport/adapter.go`
- Create: `pkg/replication/isrnodetransport/session.go`
- Create: `pkg/replication/isrnodetransport/adapter_test.go`
- Create: `pkg/replication/isrnodetransport/integration_test.go`

- [ ] **Step 1: Write failing mux tests**

```go
func TestRPCMuxRoutesByServiceID(t *testing.T) {
	mux := nodetransport.NewRPCMux()
	mux.Handle(1, func(ctx context.Context, body []byte) ([]byte, error) { return []byte("raft"), nil })
	mux.Handle(2, func(ctx context.Context, body []byte) ([]byte, error) { return []byte("isr"), nil })
	resp, err := mux.HandleRPC(context.Background(), append([]byte{2}, []byte("payload")...))
	require.NoError(t, err)
	require.Equal(t, []byte("isr"), resp)
}
```

- [ ] **Step 2: Run transport tests**

Run: `go test ./pkg/transport/nodetransport ./pkg/cluster/raftcluster -run 'TestRPCMuxRoutesByServiceID|TestRaftTransport|TestForward' -v`
Expected: FAIL because the mux does not exist and raftcluster still owns the raw RPC slot.

- [ ] **Step 3: Implement RPC mux and move raftcluster onto it**

Rules:
- keep wire-level RPC framing unchanged
- add one-byte service selection inside RPC payload
- register raftcluster forwarding under a fixed service ID

- [ ] **Step 4: Write failing ISR transport adapter tests**

```go
func TestAdapterRoundTripsFetchRequestAndResponse(t *testing.T) {
	// start two nodetransport-backed adapter endpoints
	// trigger one fetch request
	// assert response payload returns committed records
}
```

- [ ] **Step 5: Implement `isrnodetransport`**

Rules:
- use the shared RPC mux service ID for ISR fetch serving
- keep session reuse per peer
- preserve backpressure reporting to `isrnode`

- [ ] **Step 6: Run package tests**

Run: `go test ./pkg/transport/nodetransport ./pkg/cluster/raftcluster ./pkg/replication/isrnodetransport -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add pkg/transport/nodetransport pkg/cluster/raftcluster pkg/replication/isrnodetransport
git commit -m "feat: add shared rpc mux and isr transport adapter"
```

## Task 6: Wire Metadata Sync And Data Plane Into `internal/app`

**Files:**
- Create: `internal/app/channelmeta.go`
- Create: `internal/app/channelmeta_test.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/lifecycle.go`
- Modify: `internal/app/lifecycle_test.go`
- Modify: `internal/app/integration_test.go`

- [ ] **Step 1: Write failing app composition tests**

```go
func TestNewBuildsChannelLogDataPlane(t *testing.T) {
	cfg := testConfig(t)
	app, err := New(cfg)
	require.NoError(t, err)
	require.NotNil(t, app.ChannelLog())
	require.NotNil(t, app.ISRRuntime())
}
```

- [ ] **Step 2: Run targeted app tests**

Run: `go test ./internal/app -run 'TestNewBuildsChannelLogDataPlane' -v`
Expected: FAIL because the app does not construct the data plane yet.

- [ ] **Step 3: Build metadata projector and sync loop**

```go
type channelMetaSync struct {
	store    *metastore.Store
	runtime  isrnode.Runtime
	cluster  channellog.Cluster
	localNode uint64
}
```

Rules:
- preload all groups containing the local node
- reconcile updates by bounded periodic refresh in V1
- apply to `isrnode` first, then `channellog`

- [ ] **Step 4: Wire app build and lifecycle**

Rules:
- open `channellog.DB`
- create snowflake generator
- create `isrnode.Runtime`
- create `channellog.Cluster`
- start cluster first, then metadata sync, then gateway/API
- stop gateway/API before closing data-plane resources

- [ ] **Step 5: Run app tests**

Run: `go test ./internal/app -run 'Test(NewBuilds|Start|Stop|AppStart)' -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/app
git commit -m "feat: wire channel log data plane into app"
```

## Task 7: Plumb Request Contexts And Switch Durable Send

**Files:**
- Modify: `internal/usecase/message/app.go`
- Modify: `internal/usecase/message/command.go`
- Modify: `internal/usecase/message/send.go`
- Modify: `internal/usecase/message/send_test.go`
- Modify: `internal/access/gateway/handler.go`
- Modify: `internal/access/gateway/frame_router.go`
- Modify: `internal/access/gateway/integration_test.go`
- Modify: `internal/access/api/server.go`
- Modify: `internal/access/api/message_send.go`
- Modify: `internal/access/api/integration_test.go`

- [ ] **Step 1: Write failing timeout/cancel tests**

```go
func TestSendDurablePersonUsesRequestContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	app := newTestApp(t, withSendContext(ctx), withBlockingCluster())
	_, err := app.Send(testSendCommand())
	require.ErrorIs(t, err, context.Canceled)
}
```

- [ ] **Step 2: Run targeted usecase/access tests**

Run: `go test ./internal/usecase/message ./internal/access/gateway ./internal/access/api -run 'TestSendDurablePersonUsesRequestContext' -v`
Expected: FAIL because durable send still uses `context.Background()`.

- [ ] **Step 3: Add context-aware send surface**

Recommended shape:

```go
type MessageUsecase interface {
	Send(ctx context.Context, cmd message.SendCommand) (message.SendResult, error)
}
```

Rules:
- plumb ingress request context down to durable send
- keep one-shot metadata refresh semantics
- do not reintroduce a global service object

- [ ] **Step 4: Switch app wiring to the real `channellog.Cluster`**

Rules:
- remove durable-path dependence on `*raftcluster.Cluster`
- keep local fanout best-effort and post-commit

- [ ] **Step 5: Run tests**

Run: `go test ./internal/usecase/message ./internal/access/gateway ./internal/access/api -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/usecase/message internal/access/gateway internal/access/api
git commit -m "feat: use request-scoped durable send contexts"
```

## Task 8: Add Three-Node Durable Write Integration Coverage

**Files:**
- Create: `pkg/storage/channellog/multinode_integration_test.go`
- Create: `internal/app/multinode_integration_test.go`

- [x] **Step 1: Write the failing three-node `channellog` integration test**

```go
func TestThreeNodeClusterSendCommitsBeforeAckAndSurvivesFollowerRestart(t *testing.T) {
	// boot 3 isr nodes with shared metadata and real stores
	// send through leader
	// assert ack only after commit
	// restart follower
	// assert committed message is still readable
}
```

- [x] **Step 2: Run the package-level integration test**

Run: `go test ./pkg/storage/channellog -run 'TestThreeNodeClusterSendCommitsBeforeAckAndSurvivesFollowerRestart' -v`
Expected: FAIL until the full data plane is wired.

- [x] **Step 3: Write the failing three-app integration test**

```go
func TestThreeNodeAppGatewaySendUsesDurableCommit(t *testing.T) {
	// boot 3 apps
	// send to leader via gateway
	// read back from channellog store
}
```

- [x] **Step 4: Run the app integration test**

Run: `go test ./internal/app -run 'TestThreeNodeAppGatewaySendUsesDurableCommit' -v`
Expected: FAIL until app-level wiring is complete.

- [x] **Step 5: Make both pass and run the final verification batch**

Run: `go test ./pkg/storage/channellog ./pkg/replication/isrnode ./pkg/replication/isrnodetransport ./pkg/transport/nodetransport ./pkg/storage/metadb ./pkg/storage/metafsm ./pkg/storage/metastore ./internal/usecase/message ./internal/access/gateway ./internal/access/api ./internal/app -count=1`
Expected: PASS

Execution notes:
- reviewer found and fix applied: `channelMetaSync` now tracks locally applied groups before `cluster.ApplyMeta`, so `Stop()` can clean up runtime state even when cluster meta apply fails mid-flight
- stability fix applied: `isrnode` now schedules delayed follower replication retry after send failures, instead of stopping after a single automatic retry
- harness stabilization applied: the three-app integration test refreshes channel metadata on the elected leader first, then followers, closing the leader-not-ready startup window in the test harness
- verification run on 2026-04-04:
  - `go test ./pkg/storage/channellog ./pkg/replication/isrnode ./pkg/replication/isrnodetransport ./pkg/transport/nodetransport ./pkg/storage/metadb ./pkg/storage/metafsm ./pkg/storage/metastore ./internal/usecase/message ./internal/access/gateway ./internal/access/api ./internal/app -count=1`
  - `go test ./internal/app -run 'TestThreeNodeAppGatewaySendUsesDurableCommit' -count=3`
  - both passed

- [ ] **Step 6: Commit**

```bash
git add pkg/storage/channellog internal/app
git commit -m "test: cover complete isr multi-node durable writes"
```

## Local Plan Review

Because this harness does not cleanly allow the plan-document-reviewer delegation path for this turn, perform a local review before execution:

- confirm every task has exact file paths
- confirm every new exported type has at least one package test
- confirm transport work does not assume a second raw `HandleRPC` slot
- confirm app lifecycle order starts control plane before data plane before ingress
- confirm final verification command covers every touched package

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-04-03-complete-isr-multi-node-write-implementation.md`. Ready to execute?

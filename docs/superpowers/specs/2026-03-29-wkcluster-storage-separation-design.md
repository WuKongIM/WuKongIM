# wkcluster 存储分离设计

## 目标

将 `pkg/wkcluster/` 重构为纯粹的集群协调库,分布式业务存储拆分为独立的 `pkg/wkstore/` 包。开发者修改存储相关内容只需关注 `wkstore`,而 `wkcluster` 不再包含任何业务存储逻辑。

## 现状问题

`wkcluster` 目前混合了两类职责:

1. **集群协调**: leader 选举、路由、转发、transport 管理、节点发现
2. **业务存储**: `CreateChannel`/`UpdateChannel` 等 API、直接创建 `wkdb.DB`/`raftstore.DB`/`wkfsm.StateMachine`

这导致 `wkcluster` 的 import 图包含 `wkdb`、`raftstore`、`wkfsm`,开发者改业务存储逻辑需要进入协调层代码。

## 设计方案: 彻底解耦 + wkfsm 合并

### 拆分后依赖关系

```
上层应用 (组装层)
├── wkstore (业务分布式存储)
│   ├── wkcluster (纯集群协调)
│   │   ├── multiraft (raft 引擎)
│   │   └── wktransport (网络传输)
│   ├── wkdb (应用数据 KV)
│   └── raftstore (raft 日志 KV)
├── wkdb
└── raftstore
```

**关键改进**: `wkcluster` 的 import 中不再出现 `wkdb`、`raftstore`、`wkfsm`。

注: `wkcluster` 通过 Config 中的工厂闭包在运行时接收 storage 和 state machine 实例,这些闭包在上层组装时捕获 `wkdb.DB`/`raftstore.DB` 引用。Go 编译器只检查 import 路径,闭包不会引入编译期依赖,因此不存在 import cycle。

### 包职责

#### `pkg/wkcluster/` — 纯集群协调库

职责:
- 管理 `multiraft.Runtime` 生命周期
- 通用路由: `SlotForKey(key string) GroupID`(不绑定 channel 语义)
- Proposal 提交与 leader 转发: `Propose(ctx, groupID, cmd []byte) error`
- Transport 管理 (server, pool, client)
- 节点发现 (discovery)
- 保留现有的 `Server()` 和 `Discovery()` 访问器方法

不再依赖: `wkdb`, `raftstore`, `wkfsm`

#### `pkg/wkstore/` — 业务分布式存储 (新建)

职责:
- 命令编码/解码 (从 `wkfsm` 移入)
- `StateMachine` 实现 (从 `wkfsm` 移入)
- 业务 API: Channel CRUD + User CRUD
- 持有 `wkdb.DB` 引用

#### `pkg/wkfsm/` — 删除

整个包合并到 `wkstore`。

#### 不变的包

- `pkg/multiraft/` — 纯 raft 引擎
- `pkg/wktransport/` — 纯传输层
- `pkg/wkdb/` — 纯应用数据存储
- `pkg/raftstore/` — 纯 raft 日志存储

---

## 详细设计

### 1. wkcluster 新的 Config

```go
type Config struct {
    // 节点身份
    NodeID     multiraft.NodeID
    ListenAddr string

    // 集群拓扑
    GroupCount uint32
    Nodes      []NodeConfig
    Groups     []GroupConfig

    // 存储工厂 (外部注入)
    // 调用方通过闭包捕获 raftstore.DB/wkdb.DB,wkcluster 无需 import 这些包。
    // 返回 error 以便工厂内部做校验(如 nil db、无效 groupID)。
    NewStorage      func(groupID multiraft.GroupID) (multiraft.Storage, error)
    NewStateMachine func(groupID multiraft.GroupID) (multiraft.StateMachine, error)

    // 网络/Raft 调优
    ForwardTimeout time.Duration
    PoolSize       int
    TickInterval   time.Duration
    RaftWorkers    int
    ElectionTick   int
    HeartbeatTick  int
    DialTimeout    time.Duration
}
```

移除: `DataDir`, `RaftDataDir`
新增: `NewStorage`, `NewStateMachine` 工厂函数(均返回 error)

注: 工厂返回类型为 `multiraft.StateMachine` 而非 `multiraft.BatchStateMachine`,与 `multiraft.GroupOptions.StateMachine` 字段类型一致。具体实现可以同时满足 `BatchStateMachine` 接口,由 multiraft runtime 在运行时检测。

### 2. wkcluster 新的公开 API

```go
// Propose 向指定 group 提交命令,自动处理 leader 转发。
// 内部包含完整的 leader 检测、远程转发、重试/退避逻辑
// (即当前 proposeOrForward 的全部行为)。
// 调用方无需关心 leader 位置或网络转发细节。
func (c *Cluster) Propose(ctx context.Context, groupID multiraft.GroupID, cmd []byte) error

// SlotForKey 通用路由: key -> groupID (CRC32 hash)
func (c *Cluster) SlotForKey(key string) multiraft.GroupID

// LeaderOf 查询 group 的当前 leader
func (c *Cluster) LeaderOf(groupID multiraft.GroupID) (multiraft.NodeID, error)

// IsLocal 判断节点是否是本节点
func (c *Cluster) IsLocal(nodeID multiraft.NodeID) bool
```

实现说明: 将现有 `proposeOrForward` 重命名为 `Propose` 并导出。`forwardToLeader` 和 `handleForwardRPC` 保持为 wkcluster 内部方法不变。

移除: `CreateChannel`, `UpdateChannel`, `DeleteChannel`, `GetChannel`

### 3. wkcluster Cluster struct 变更

```go
type Cluster struct {
    cfg        Config
    server     *wktransport.Server
    raftPool   *wktransport.Pool
    raftClient *wktransport.Client
    fwdClient  *wktransport.Client
    runtime    *multiraft.Runtime
    router     *Router
    discovery  *StaticDiscovery
    stopped    atomic.Bool
}
```

移除: `db *wkdb.DB`, `raftDB *raftstore.DB`

### 4. wkcluster Start() 变更

```go
func (c *Cluster) Start() error {
    // 1. 创建 discovery
    // 2. 创建 transport server, pool, clients
    // 3. 创建 multiraft.Runtime (使用注入的 NewStorage/NewStateMachine)
    // 4. 创建 router
    // 5. Open/Bootstrap groups (调用 cfg.NewStorage/cfg.NewStateMachine)
    // 不再: 打开 wkdb.DB, 打开 raftstore.DB
}
```

openOrBootstrapGroup 变更:

```go
func (c *Cluster) openOrBootstrapGroup(g GroupConfig) error {
    // 之前:
    //   storage := c.raftDB.ForGroup(uint64(g.GroupID))
    //   sm, err := wkfsm.NewStateMachine(c.db, uint64(g.GroupID))
    // 之后:
    storage, err := c.cfg.NewStorage(g.GroupID)
    if err != nil {
        return fmt.Errorf("create storage for group %d: %w", g.GroupID, err)
    }
    sm, err := c.cfg.NewStateMachine(g.GroupID)
    if err != nil {
        return fmt.Errorf("create state machine for group %d: %w", g.GroupID, err)
    }
    // ... 其余不变
}
```

### 5. wkcluster Router 变更

```go
// SlotForKey 替代原来的 SlotForChannel,不绑定 channel 语义
func (r *Router) SlotForKey(key string) multiraft.GroupID {
    h := crc32.ChecksumIEEE([]byte(key))
    return multiraft.GroupID(h%r.groupCount) + 1
}
```

### 6. wkstore 包结构

```
pkg/wkstore/
├── store.go          // Store 主结构体 + 业务 API (channel + user)
├── command.go        // 命令编码/解码 (从 wkfsm 移入)
├── statemachine.go   // StateMachine 实现 (从 wkfsm 移入)
└── store_test.go
```

### 7. wkstore Store 结构体

```go
type Store struct {
    cluster *wkcluster.Cluster
    db      *wkdb.DB
}

func New(cluster *wkcluster.Cluster, db *wkdb.DB) *Store {
    return &Store{cluster: cluster, db: db}
}
```

### 8. wkstore 业务 API

注: 保持现有类型签名不变 — `channelType` 为 `int64`,`Channel` 为值类型返回。

Channel 操作:

```go
func (s *Store) CreateChannel(ctx context.Context, channelID string, channelType int64) error {
    groupID := s.cluster.SlotForKey(channelID)
    cmd := encodeUpsertChannelCommand(channelID, channelType, false)
    return s.cluster.Propose(ctx, groupID, cmd)
}

func (s *Store) UpdateChannel(ctx context.Context, channelID string, channelType int64, ban bool) error {
    groupID := s.cluster.SlotForKey(channelID)
    cmd := encodeUpsertChannelCommand(channelID, channelType, ban)
    return s.cluster.Propose(ctx, groupID, cmd)
}

func (s *Store) DeleteChannel(ctx context.Context, channelID string, channelType int64) error {
    groupID := s.cluster.SlotForKey(channelID)
    cmd := encodeDeleteChannelCommand(channelID, channelType)
    return s.cluster.Propose(ctx, groupID, cmd)
}

func (s *Store) GetChannel(ctx context.Context, channelID string, channelType int64) (wkdb.Channel, error) {
    slot := s.cluster.SlotForKey(channelID)
    return s.db.ForSlot(uint64(slot)).GetChannel(ctx, channelID, channelType)
}
```

User 操作:

```go
func (s *Store) UpsertUser(ctx context.Context, uid string, token string, deviceFlag uint64, deviceLevel uint8) error {
    groupID := s.cluster.SlotForKey(uid)
    cmd := encodeUpsertUserCommand(uid, token, deviceFlag, deviceLevel)
    return s.cluster.Propose(ctx, groupID, cmd)
}

func (s *Store) GetUser(ctx context.Context, uid string) (wkdb.User, error) {
    slot := s.cluster.SlotForKey(uid)
    return s.db.ForSlot(uint64(slot)).GetUser(ctx, uid)
}
```

### 9. wkstore StateMachine 工厂

```go
func NewStateMachineFactory(db *wkdb.DB) func(groupID multiraft.GroupID) (multiraft.StateMachine, error) {
    return func(groupID multiraft.GroupID) (multiraft.StateMachine, error) {
        if db == nil {
            return nil, fmt.Errorf("db is nil")
        }
        return &stateMachine{db: db, groupID: uint64(groupID)}, nil
    }
}
```

### 10. 上层组装示例

```go
// 打开存储
db := wkdb.Open(dataDir)
raftDB := raftstore.Open(raftDataDir)

// 创建集群 (纯协调,不知道业务)
cluster := wkcluster.New(wkcluster.Config{
    NodeID:     nodeID,
    ListenAddr: ":5000",
    GroupCount: 16,
    Nodes:      nodes,
    Groups:     groups,
    NewStorage: func(groupID multiraft.GroupID) (multiraft.Storage, error) {
        return raftDB.ForGroup(uint64(groupID)), nil
    },
    NewStateMachine: wkstore.NewStateMachineFactory(db),
})

// 创建业务存储
store := wkstore.New(cluster, db)

// 启动
cluster.Start()

// 使用
store.CreateChannel(ctx, "channel1", 1)
ch, _ := store.GetChannel(ctx, "channel1", 1)
```

### 11. 关停顺序

资源关闭必须按以下顺序执行,确保 raft runtime 停止 apply 后才关闭数据库:

```go
// 1. 停止集群 (停止 raft runtime,排空进行中的 proposal)
cluster.Stop()

// 2. 关闭 raft 日志存储
raftDB.Close()

// 3. 关闭应用数据存储
db.Close()
```

调用方负责管理 `wkdb.DB` 和 `raftstore.DB` 的生命周期。`wkcluster` 不再拥有这些资源。

---

## 文件变更清单

| 操作 | 文件 | 说明 |
|------|------|------|
| 修改 | `pkg/wkcluster/cluster.go` | 移除 `db`/`raftDB` 字段,Start()/Stop() 改为使用注入的工厂函数,不再打开/关闭 DB |
| 修改 | `pkg/wkcluster/config.go` | 移除 DataDir/RaftDataDir,新增 NewStorage/NewStateMachine |
| 修改 | `pkg/wkcluster/router.go` | `SlotForChannel` 改名为 `SlotForKey` |
| 修改 | `pkg/wkcluster/forward.go` | `proposeOrForward` 重命名为 `Propose` 并导出 |
| 删除 | `pkg/wkcluster/api.go` | 业务 API 移到 wkstore |
| 修改 | `pkg/wkcluster/errors.go` | 移除业务相关错误(如果有) |
| 修改 | `pkg/wkcluster/cluster_test.go` | 改为通过 `wkstore.Store` 调用业务 API |
| 修改 | `pkg/wkcluster/stress_test.go` | 改为通过 `wkstore.Store` 调用业务 API |
| 修改 | `pkg/wkcluster/router_test.go` | `SlotForChannel` → `SlotForKey` |
| 新建 | `pkg/wkstore/store.go` | Store 主结构体 + Channel/User 业务 API |
| 新建 | `pkg/wkstore/command.go` | 命令编码/解码 (从 wkfsm/command.go 移入) |
| 新建 | `pkg/wkstore/statemachine.go` | StateMachine 实现 (从 wkfsm/state_machine.go 移入) |
| 新建 | `pkg/wkstore/store_test.go` | 业务 API 集成测试 |
| 删除 | `pkg/wkfsm/` | 整个包删除 (代码已迁移到 wkstore) |

### 测试迁移策略

1. **wkfsm 测试迁移**: `wkfsm/` 下的集成测试、压力测试、benchmark 迁移到 `pkg/wkstore/`,更新 import 和构造函数调用
2. **wkcluster 测试改写**: `cluster_test.go` 和 `stress_test.go` 中所有 `c.CreateChannel` 等调用改为创建 `wkstore.Store` 后通过 store 调用
3. **wkcluster 测试需要提供工厂**: 测试中通过闭包注入 `raftstore.NewMemory()` 和 `wkstore.NewStateMachineFactory(testDB)` 到 Config

## 验收标准

1. `pkg/wkcluster/` 的 import 中不出现 `wkdb`、`raftstore`、`wkfsm`
2. `wkcluster` 无 `CreateChannel` 等业务 API
3. `wkstore` 通过 `wkcluster.Propose` 提交命令,业务逻辑自包含
4. `wkstore` 包含 Channel CRUD 和 User CRUD
5. 所有现有测试迁移后通过
6. `pkg/wkfsm/` 目录已删除
7. 关停顺序正确: cluster.Stop() → raftDB.Close() → db.Close()

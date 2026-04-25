# Multi-Raft 通用库对外 API 设计

## 1. 背景

目标是将参考 Cockroach 的 multi-raft 运行时拆成一个通用库，但不直接复刻
Cockroach 的 `Store/Replica` 结构，也不把 `etcd/raft.RawNode` 的内部驱动
模型直接暴露给使用者。

该库的定位是：

- 提供节点内多 raft group 的统一运行时
- 对外暴露清晰、易理解、可实现的 API
- 将持久化、状态机、网络发送留给业务通过接口注入

该库不负责：

- 默认存储实现
- 默认网络实现
- 默认快照传输协议
- 数据库语义相关能力

本设计选择的边界是 `runtime-only`，选择的 API 风格是“半抽象”：

- 保留 `Group`、`Message`、`Proposal`、`ConfigChange` 等必要 raft 术语
- 隐藏 `RawNode`、`Ready`、`HasReady`、`AckApplied`、统一 tick 调度等内部细节

## 2. 设计目标

### 2.1 目标

1. 使用者只需要理解少量概念即可接入：
   - `Runtime`
   - `Group`
   - `Transport`
   - `Storage`
   - `StateMachine`
2. 对外主路径清晰：
   - 创建运行时
   - 打开或初始化 group
   - 接收网络消息并投递
   - 提交 proposal
   - 等待本地 apply 完成
3. 库内部托管多 group 调度：
   - tick
   - request 路由
   - ready 处理
   - 重新入队
4. 允许业务替换：
   - raft 元数据持久化
   - 日志存储
   - 状态机
   - 网络发送

### 2.2 非目标

1. 不追求把 raft 完全“业务化命名”，避免语义失真
2. 不追求把所有高级能力一次性做进 V1
3. 不提供直接暴露 `RawNode` 的逃生口
4. 不在 V1 中解决跨节点成员发现与地址解析

## 3. 总体分层

### 3.1 对外分层

- `Runtime`
  - 节点内 multi-raft 运行时
  - 负责 group 生命周期、调度和消息驱动
- `Group`
  - 对外不是一个长期可直接操作的对象句柄
  - group 通过 `GroupID` 标识，由 `Runtime` 统一管理
- `Storage`
  - 负责 raft 持久化视图
- `StateMachine`
  - 负责业务状态机 apply / snapshot / restore
- `Transport`
  - 负责消息发送

### 3.2 内部分层

内部实现允许保留如下概念，但不对外暴露：

- 单 group raft 内核
- per-group mailbox
- 调度器
- ready 驱动循环
- 本地存储确认与重新入队机制

这部分是通用库的核心价值，也是与直接使用 `RawNode` 的主要区别。

## 4. 对外 API 草案

```go
package multiraft

type GroupID uint64
type NodeID uint64

type Options struct {
    NodeID       NodeID
    TickInterval time.Duration
    Workers      int
    Transport    Transport
    Raft         RaftOptions
}

type RaftOptions struct {
    ElectionTick  int
    HeartbeatTick int
    PreVote       bool
    CheckQuorum   bool
    MaxSizePerMsg uint64
    MaxInflight   int
}

type Runtime struct{}

func New(opts Options) (*Runtime, error)
func (r *Runtime) Close() error

type GroupOptions struct {
    ID           GroupID
    Storage      Storage
    StateMachine StateMachine
}

type BootstrapGroupRequest struct {
    Group    GroupOptions
    Voters   []NodeID
    Learners []NodeID
}

func (r *Runtime) OpenGroup(ctx context.Context, opts GroupOptions) error
func (r *Runtime) BootstrapGroup(ctx context.Context, req BootstrapGroupRequest) error
func (r *Runtime) CloseGroup(ctx context.Context, groupID GroupID) error

func (r *Runtime) Step(ctx context.Context, msg Envelope) error
func (r *Runtime) Propose(ctx context.Context, groupID GroupID, data []byte) (Future, error)
func (r *Runtime) ChangeConfig(ctx context.Context, groupID GroupID, change ConfigChange) (Future, error)
func (r *Runtime) TransferLeadership(ctx context.Context, groupID GroupID, target NodeID) error
func (r *Runtime) Status(groupID GroupID) (Status, error)
func (r *Runtime) Groups() []GroupID

type Envelope struct {
    GroupID GroupID
    Message raftpb.Message
}

type Future interface {
    Wait(ctx context.Context) (Result, error)
}

type Result struct {
    Index uint64
    Term  uint64
    Data  []byte
}

type Status struct {
    GroupID      GroupID
    NodeID       NodeID
    LeaderID     NodeID
    Term         uint64
    CommitIndex  uint64
    AppliedIndex uint64
    Role         Role
}

type Transport interface {
    Send(ctx context.Context, batch []Envelope) error
}

type Storage interface {
    InitialState(ctx context.Context) (BootstrapState, error)
    Entries(ctx context.Context, lo, hi, maxSize uint64) ([]raftpb.Entry, error)
    Term(ctx context.Context, index uint64) (uint64, error)
    FirstIndex(ctx context.Context) (uint64, error)
    LastIndex(ctx context.Context) (uint64, error)
    Snapshot(ctx context.Context) (raftpb.Snapshot, error)

    Save(ctx context.Context, st PersistentState) error
    MarkApplied(ctx context.Context, index uint64) error
}

type BootstrapState struct {
    HardState    raftpb.HardState
    ConfState    raftpb.ConfState
    AppliedIndex uint64
}

type PersistentState struct {
    HardState *raftpb.HardState
    Entries   []raftpb.Entry
    Snapshot  *raftpb.Snapshot
}

type StateMachine interface {
    Apply(ctx context.Context, cmd Command) ([]byte, error)
    Restore(ctx context.Context, snap Snapshot) error
    Snapshot(ctx context.Context) (Snapshot, error)
}

type Command struct {
    GroupID GroupID
    Index   uint64
    Term    uint64
    Data    []byte
}

type Snapshot struct {
    Index uint64
    Term  uint64
    Data  []byte
}

type ConfigChange struct {
    Type    ChangeType
    NodeID  NodeID
    Context []byte
}

type ChangeType uint8

const (
    AddVoter ChangeType = iota + 1
    RemoveVoter
    AddLearner
    PromoteLearner
)

type Role uint8

const (
    RoleFollower Role = iota + 1
    RoleCandidate
    RoleLeader
    RoleLearner
)
```

## 5. API 语义

### 5.1 Runtime

`Runtime` 表示单个节点内的 multi-raft 运行时实例。

约束：

- 一个进程通常只创建一个 `Runtime`
- `Runtime` 内部负责所有 group 的 tick、路由和 ready 处理
- 调用方不需要显式驱动单个 group 的 ready 循环

### 5.2 OpenGroup

`OpenGroup` 用于加载一个已经存在的 group。

适用场景：

- 节点启动恢复已有 group
- 动态装载本地已有分片

约束：

- `Storage.InitialState()` 必须返回可恢复的 raft 状态
- 如果 group 不存在，返回显式错误
- V1 不做“收到消息后自动懒创建”

### 5.3 BootstrapGroup

`BootstrapGroup` 用于创建新 group。

语义：

- 仅允许对不存在的 group 调用
- 调用后 group 进入正常运行状态
- `Voters/Learners` 用于初始化成员集合

### 5.4 Step

`Step` 是网络入口。

业务侧 transport 收到远端消息后，应将消息包装为 `Envelope` 后调用：

```go
err := rt.Step(ctx, env)
```

语义：

- `Runtime` 按 `GroupID` 找到目标 group
- 将消息投递到该 group 的输入队列
- 触发相应调度

外部无需关心：

- 是否马上 `Step` 到内核
- 是否需要先处理本地消息
- 是否要再触发 ready

### 5.5 Propose

`Propose` 用于提交普通复制命令。

```go
fut, err := rt.Propose(ctx, groupID, data)
res, err := fut.Wait(ctx)
```

建议语义：

- `Propose` 成功仅表示已被运行时接收
- `Future.Wait` 成功表示该 proposal 已在本地 apply 完成
- `Result.Data` 是 `StateMachine.Apply()` 的返回值

这样对使用者最直观，不需要额外理解“写入 raft log 成功”和“状态机 apply
成功”的区别。

### 5.6 ChangeConfig

`ChangeConfig` 用于成员变更。

V1 建议只暴露单步语义：

- 添加 voter
- 移除 voter
- 添加 learner
- 提升 learner

是否支持 joint consensus 的底层细节由库内部决定，不向业务 API 传播。

### 5.7 TransferLeadership

用于显式迁移 leader。

保留该 API 的原因：

- 它是常见且稳定的 raft 控制能力
- 如果不暴露，使用方通常会很快要求补上

### 5.8 Status

`Status` 应返回只读状态快照，至少包含：

- `GroupID`
- `NodeID`
- `LeaderID`
- `Term`
- `CommitIndex`
- `AppliedIndex`
- `Role`

V1 只提供查询，不提供可变句柄。

## 6. 核心扩展接口

### 6.1 Transport

```go
type Transport interface {
    Send(ctx context.Context, batch []Envelope) error
}
```

设计原则：

- transport 只负责发，不负责收
- 收到消息如何监听、如何解码，由业务自己决定
- 收到消息后统一调用 `Runtime.Step`

建议：

- `Send` 应尽量快速返回
- 如果内部有队列，阻塞策略由 transport 自己决定
- V1 允许 runtime 直接按批调用 `Send`

### 6.2 Storage

`Storage` 是 raft 持久化接口，不是业务数据存储接口。

职责：

- 向 raft 提供日志、term、snapshot、hard state
- 持久化新日志、hard state、snapshot
- 记录 apply 进度

约束：

- `Save` 返回前必须 durable
- `MarkApplied` 必须只在状态机 apply 成功后推进
- `AppliedIndex` 恢复后应能与状态机状态保持一致

### 6.3 StateMachine

`StateMachine` 只负责业务命令执行。

```go
type StateMachine interface {
    Apply(ctx context.Context, cmd Command) ([]byte, error)
    Restore(ctx context.Context, snap Snapshot) error
    Snapshot(ctx context.Context) (Snapshot, error)
}
```

约束：

- `Apply` 必须幂等地遵守日志顺序
- 不应在不同节点产生非确定性行为
- 业务错误应尽量编码进返回结果，而不是直接返回 `error`
- `error` 更适合表示节点本地致命错误

## 7. 推荐调用路径

### 7.1 启动恢复

```go
rt, err := multiraft.New(opts)
if err != nil {
    return err
}

for _, g := range localGroups {
    if err := rt.OpenGroup(ctx, g); err != nil {
        return err
    }
}
```

### 7.2 初始化 group

```go
err := rt.BootstrapGroup(ctx, multiraft.BootstrapGroupRequest{
    Group: multiraft.GroupOptions{
        ID:           100,
        Storage:      store,
        StateMachine: fsm,
    },
    Voters: []multiraft.NodeID{1, 2, 3},
})
```

### 7.3 提交命令

```go
fut, err := rt.Propose(ctx, 100, cmd)
if err != nil {
    return err
}

res, err := fut.Wait(ctx)
if err != nil {
    return err
}

_ = res.Data
```

### 7.4 接收消息

```go
func onReceive(groupID multiraft.GroupID, msg raftpb.Message) error {
    return rt.Step(ctx, multiraft.Envelope{
        GroupID: groupID,
        Message: msg,
    })
}
```

## 8. 为什么不暴露 Ready

这是本设计中最重要的决策之一。

如果暴露 `Ready`，使用者就必须理解：

- 什么时候取 `Ready`
- 什么时候持久化
- 什么时候发消息
- 什么时候 apply
- 什么时候 `Advance/AckApplied`
- 什么时候重新调度

这样 API 虽然“底层可控”，但对大多数用户并不清晰，也不符合该库的定位。

因此 V1 的基本策略是：

- 对外隐藏 `Ready`
- 对内托管完整处理循环
- 对外只保留必要的输入输出点

## 9. V1 明确不做

1. 不暴露 `RawNode`
2. 不提供自动 group 懒创建
3. 不提供默认 snapshot 传输协议
4. 不设计 `ReadIndex` 和 lease read API
5. 不暴露底层调度器类型
6. 不做 transport 地址发现
7. 不将 metrics、tracing、quiesce 作为首版必需能力

## 10. 后续收敛建议

在真正开始实现前，还需要再收敛三件事：

1. `Status` 的最终字段集合
2. 错误模型
   - group 不存在
   - group 已关闭
   - proposal 超时
   - 非 leader
3. `ConfigChange` 的最终形状
   - 是否支持批量变更
   - 是否暴露 learner promotion

## 11. 当前结论

V1 对外 API 应采用“高层托管 + 半抽象”方案：

- 对外暴露少量稳定概念
- 对内吸收 `etcd/raft` 的驱动复杂度
- 让使用者以“运行时 + 三个接口”的心智模型接入

这版设计既保留 raft 语义的准确性，也避免将实现细节泄漏到公共 API。

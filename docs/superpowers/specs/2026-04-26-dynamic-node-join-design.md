# Dynamic Node Join 设计

- 日期：2026-04-26
- 范围：让新节点加入已有集群的流程从“修改所有节点配置并重启”简化为“配置 seed + join token 后启动新节点”
- 关联目录：
  - `cmd/wukongim`
  - `internal/app`
  - `pkg/cluster`
  - `pkg/controller/meta`
  - `pkg/controller/plane`
  - `pkg/controller/raft`
  - `internal/access/manager`

## 1. 背景

当前集群节点发现依赖启动时的 `WK_CLUSTER_NODES` 静态列表。新增节点要被已有节点识别，通常需要把新节点写入所有节点配置并滚动重启。这个流程对生产运维不友好，也容易把普通 data 节点加入与 Controller Raft voter 变更混在一起。

当前代码已经具备一些可复用基础：

- Controller 持久化 `ClusterNode`，并通过 heartbeat 维护 `Alive / Suspect / Dead / Draining`。
- Planner 可根据健康节点选择 bootstrap、repair、rebalance 目标。
- Slot 副本迁移已有安全执行链路：`AddLearner -> CatchUp -> PromoteLearner -> TransferLeader -> RemoveOld`。
- Manager API 已经有节点列表、draining、resume 等运维入口。

缺口在于：成员发现仍是静态的；节点加入缺少明确的 Join RPC、认证、持久化 membership 语义，以及旧节点不重启即可发现新节点的机制。

## 2. 目标与非目标

## 2.1 目标

1. 新节点只需配置 seed 和 join token 即可加入已有集群。
2. 已有节点不需要修改配置或重启，就能发现并连接新节点。
3. Controller 持久化成员表成为运行时 discovery 的权威来源。
4. 普通节点加入默认只加入 data / slot worker 池，不自动改变 Controller Raft voter 集合。
5. 加入流程具备明确认证、幂等、冲突检测和可观测状态。
6. 保持现有单节点集群语义：单节点部署仍视为单节点集群。

## 2.2 非目标

当前阶段不做以下内容：

- 不自动把新节点加入 Controller Raft voter 集合。
- 不实现 Controller voter 的完整 add/remove/rebalance 流程。
- 不实现节点删除、节点替换、审批流、证书轮换等完整成员生命周期。
- 不实现跨版本滚动升级协议矩阵，只保留基础版本兼容校验入口。
- 不改变现有 Slot 安全迁移顺序。

## 3. 核心设计

## 3.1 从完整节点列表改为 seed + 动态成员表

新增配置：

```conf
WK_CLUSTER_SEEDS=["wk-node1:7000","wk-node2:7000"]
WK_CLUSTER_ADVERTISE_ADDR=wk-node4:7000
WK_CLUSTER_JOIN_TOKEN=xxx
```

语义：

- `WK_CLUSTER_SEEDS` 是启动时用于发现 Controller leader 的少量已有节点地址。
- `WK_CLUSTER_ADVERTISE_ADDR` 是当前节点对其他节点发布的集群 RPC 地址。
- `WK_CLUSTER_JOIN_TOKEN` 用于 Join RPC 认证。
- `WK_CLUSTER_NODES` 保留为兼容首次 bootstrap 的配置，不再作为运行时 discovery 的唯一来源。

新节点最小配置示例：

```conf
WK_NODE_ID=4
WK_NODE_NAME=wk-node4
WK_NODE_DATA_DIR=/var/lib/wukongim
WK_CLUSTER_LISTEN_ADDR=0.0.0.0:7000
WK_CLUSTER_ADVERTISE_ADDR=wk-node4:7000
WK_CLUSTER_SEEDS=["wk-node1:7000"]
WK_CLUSTER_JOIN_TOKEN=xxx
```

## 3.2 Controller membership 成为权威

在 `pkg/controller/meta` 中把 `ClusterNode` 从单纯观测状态扩展为正式 membership 记录。建议字段：

```go
type ClusterNode struct {
    NodeID          uint64
    Name            string
    Addr            string
    Role            NodeRole
    JoinState       NodeJoinState
    Status          NodeStatus
    LastHeartbeatAt time.Time
    JoinedAt        time.Time
    CapacityWeight  int
}
```

建议枚举：

- `NodeRoleData`：普通 data / slot worker，默认角色。
- `NodeRoleControllerVoter`：Controller Raft voter，必须通过单独运维命令变更。
- `NodeJoinStateJoining`：Join 已提交但尚未完成首轮心跳 / full sync。
- `NodeJoinStateActive`：可被 Planner 调度。
- `NodeJoinStateRejected`：冲突或认证失败后留下的拒绝记录，可选。

`Membership` 和 `Health` 必须分离：

- Membership 表示节点是否属于集群。
- Health 表示节点当前是否可用。

这样可以避免“陌生 NodeID 发来心跳”直接被当成合法入群。

## 4. Join RPC 流程

## 4.1 RPC 定义

在 cluster internal RPC 中新增 `JoinCluster`：

```go
type JoinClusterRequest struct {
    NodeID         uint64
    NodeName       string
    AdvertiseAddr  string
    CapacityWeight int
    Token          string
    Version        string
}

type JoinClusterResponse struct {
    ControllerLeaderID uint64
    Nodes              []controllermeta.ClusterNode
    HashSlotTable      []byte
    Joined             bool
}
```

RPC 处理规则：

1. seed 节点收到 join 请求。
2. 如果本节点不是 Controller leader，则返回 leader hint 或转发到 leader。
3. Controller leader 校验 token、NodeID、地址、版本和现有 membership。
4. 校验通过后提交 `CommandKindNodeJoin` 到 Controller Raft。
5. 提交成功后返回当前 membership、HashSlotTable、Controller leader 信息。

## 4.2 幂等与冲突

Join 必须支持幂等：

- 同一 `NodeID + AdvertiseAddr` 重复 join，视为成功，返回当前 membership。
- 同一 `NodeID` 但地址不同，默认拒绝，避免误把旧节点身份绑定到新地址。
- 不同 `NodeID` 但地址相同，拒绝。
- 已复制到 Controller Raft 的冲突 join 命令按 stale no-op 处理，避免并发 precheck 竞态导致 apply loop 退出。
- 已处于 `Draining` 的节点重复 join，不自动恢复，需要显式 `resume`。
- token 缺失或错误，拒绝且不写入 membership。

## 4.3 新节点启动状态机

新节点启动流程：

1. 启动本地 transport，仅包含 seed discovery。
2. 进入 `joining` 状态，向 seed 发起 `JoinCluster`。
3. 收到成功响应后写入本地 join cache，可用于重启恢复。
4. 使用响应中的 membership 初始化 DynamicDiscovery。
5. 启动 Controller client、runtime observation、heartbeat。
6. 完成首次 heartbeat 和 runtime full sync 后进入 `active`。

如果 Join RPC 暂时失败：

- 按指数退避重试。
- 不启动 managed Slot 写入能力。
- 健康检查返回 joining / not ready，避免误接业务流量。

实现状态：当前实现先在 `Start()` 内同步重试 JoinCluster，成功后才启动应用 HTTP/gateway 生命周期；`/readyz` 可见的 joining / not ready 语义留作后续生命周期改造。

## 5. DynamicDiscovery

新增 `DynamicDiscovery` 替代单纯 `StaticDiscovery`：

```go
type DynamicDiscovery struct {
    seeds map[uint64]string
    nodes atomic.Value // map[uint64]string
}
```

行为：

- 启动时只包含 seeds 或兼容的 `WK_CLUSTER_NODES`。
- Join 成功后用 Controller 返回的成员表更新 `nodes`。
- 后续通过 heartbeat response、observation delta 或专门 membership delta 持续更新。
- `Resolve(nodeID)` 先查动态成员表，再查 seeds。
- 当节点被移除或地址变更时，关闭旧连接池中的对应连接，避免继续使用旧地址。

当前 `transport.Pool` 已经通过 discovery 做 `Resolve`，因此替换 discovery 后，Raft、Controller RPC、ManagedSlot RPC、业务 data-plane pool 都应共享同一动态源。

## 6. Planner 与调度语义

Planner 候选节点必须来自 Controller 权威 membership，并满足：

- `JoinState == Active`
- `Status == Alive`
- `Role` 包含 data 能力
- 非 draining

新节点加入后不会立即抢占流量。它只会在以下情况下被使用：

1. 有 dead / draining 副本时，作为 repair target。
2. 负载倾斜超过阈值时，作为 rebalance target。
3. 新增 physical Slot 时，作为 bootstrap peer 候选。

Slot 副本迁移仍复用现有安全链路，不引入批量替换。

## 7. Controller voter 扩容单独处理

普通 Join 不自动改变 Controller Raft membership。

原因：

- Controller quorum 变更是控制面高风险操作。
- 新 data 节点不可达或配置错误不应影响 Controller 可用性。
- 当前 Controller Raft 缺少完整 voter add/remove 运维 API。

后续可单独设计：

- `POST /manager/controller/voters/:node_id`
- `DELETE /manager/controller/voters/:node_id`
- 单步 ConfChange、catch-up 校验、回滚和观测指标

在本设计中，新节点默认 `Role=Data`。

## 8. Manager API 与运维体验

建议新增或扩展：

- `POST /manager/cluster/join-tokens`：生成短期 join token。
- `GET /manager/nodes`：展示 `role`、`join_state`、`advertise_addr`。
- `GET /manager/nodes/:node_id`：展示 join 详情与最近 join 错误。
- `POST /manager/nodes/:node_id/draining`：继续用于迁出节点。
- `POST /manager/nodes/:node_id/resume`：继续用于恢复可调度。

推荐用户流程：

```bash
# 在任意已有节点生成 token
curl -X POST /manager/cluster/join-tokens

# 在新节点启动
wukongim join --seed wk-node1:7000 --token xxx --node-id 4 --advertise-addr wk-node4:7000
wukongim start
```

也可以先只支持配置文件自动 join，CLI 作为第二阶段体验优化。

## 9. 配置兼容

兼容策略：

- 单节点集群继续允许只配置 `WK_CLUSTER_NODES=[{"id":1,...}]`。
- 多节点新部署推荐使用 `WK_CLUSTER_SEEDS`。
- 已有 `WK_CLUSTER_NODES` 可作为初始 seeds 迁移来源。
- 当同时配置 `WK_CLUSTER_NODES` 和 `WK_CLUSTER_SEEDS` 时，seeds 作为 discovery bootstrap，Controller membership 作为运行时权威。
- `WK_CLUSTER_CONTROLLER_REPLICA_N` 仍只用于首次 Controller bootstrap；已持久化后不得通过配置漂移静默改变 Controller voter 集合。

## 10. 错误处理

Join 错误分类：

| 错误 | 行为 |
|------|------|
| seed 不可达 | 新节点退避重试，保持 not ready |
| 无 Controller leader | 退避重试 |
| token 无效 | 失败并停止自动重试，除非配置更新 |
| NodeID 冲突 | 失败并停止自动重试 |
| 地址冲突 | 失败并停止自动重试 |
| 版本不兼容 | 失败并提示升级 / 降级 |
| membership 提交超时 | 可重试，Join RPC 必须幂等 |

## 11. 测试计划

## 11.1 单元测试

- `DynamicDiscovery.Resolve` 支持 seed、membership 更新、删除、地址变更。
- Join request 校验覆盖 token、NodeID、地址、幂等和冲突。
- StateMachine 应用 `CommandKindNodeJoin` 后持久化 membership。
- Planner 不选择 `Joining / Rejected / Dead / Draining` 节点。
- Config 校验覆盖 `WK_CLUSTER_SEEDS`、`WK_CLUSTER_ADVERTISE_ADDR`、兼容 `WK_CLUSTER_NODES`。

## 11.2 集成测试

- 三节点集群启动后，第四节点只带 seed 加入。
- 旧节点不重启也能通过 DynamicDiscovery 连接 node4。
- Mark node1 draining 后，node4 被选为 repair / rebalance target。
- node4 重启后重复 join 幂等成功。
- token 错误、NodeID 冲突、地址冲突均不会写入 active membership。

## 11.3 e2e 测试

- 真实进程三节点集群启动。
- 启动第四节点，仅配置 seed。
- 通过 manager API 观察 node4 从 joining 到 alive / active。
- 触发 draining 并验证消息发送仍能跨节点成功。

## 12. 分阶段落地

## 12.1 Phase 1：动态发现基础

- 增加 `DynamicDiscovery`。
- 让 cluster transport pool 和 data-plane pool 共享动态 discovery。
- Controller observation delta 携带节点 membership 快照或增量。
- 不改 Join RPC，先允许通过 manager / store 注入节点验证 discovery 更新链路。

## 12.2 Phase 2：Join RPC

- 增加配置：`WK_CLUSTER_SEEDS`、`WK_CLUSTER_ADVERTISE_ADDR`、`WK_CLUSTER_JOIN_TOKEN`。
- 增加 `JoinCluster` RPC。
- 增加 `CommandKindNodeJoin`。
- 新节点启动加入 `joining` 状态机。

## 12.3 Phase 3：运维体验

- Manager API 增加 join token 管理。
- 节点详情展示 role、join_state、advertise_addr。
- 可选增加 `wukongim join` CLI。

## 12.4 Phase 4：Controller voter 运维（单独设计）

- 显式 add/remove Controller voter。
- 单步 Raft ConfChange。
- 强校验、可观测、可回滚。

## 13. 开放问题

1. `NodeID` 是由运维显式指定，还是允许 Controller 分配？推荐第一阶段显式指定，避免和 message ID snowflake 约束冲突。
2. Join token 是全局共享 secret，还是短期一次性 token？推荐先支持短期 token，配置共享 secret 作为开发模式。
3. 新节点 `Active` 的条件是 join 提交即 active，还是首次 full sync 后 active？推荐首次 heartbeat + full sync 后 active。
4. 是否允许节点地址变更？推荐第一阶段不自动允许，需要显式 manager 操作。

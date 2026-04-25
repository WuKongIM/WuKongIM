# Manager Slot Recover And Rebalance 设计

- 日期：2026-04-22
- 范围：manager slot 写接口 `POST /manager/slots/:slot_id/recover`、`POST /manager/slots/rebalance`
- 关联目录：
  - `internal/access/manager`
  - `internal/usecase/management`
  - `internal/app`
  - `pkg/cluster`
  - `pkg/controller`
  - `docs/superpowers/specs/2026-04-21-manager-api-foundation-design.md`
  - `docs/superpowers/specs/2026-04-22-manager-slot-leader-transfer-design.md`

## 1. 背景

当前 manager 已经具备以下 slot 能力：

- `GET /manager/slots`
- `GET /manager/slots/:slot_id`
- `POST /manager/slots/:slot_id/leader/transfer`

同时，底层 cluster 已经具备更多 slot 运维原语：

- `RecoverSlot(ctx, slotID, strategy)`
- `Rebalance(ctx)`

但这两个能力还没有通过 manager API 暴露给后端管理系统。对于 slot 运维来说，它们分别代表两类非常关键的控制面动作：

- `recover`：针对单 slot 的恢复路径
- `rebalance`：针对全局 hash slot 分布的均衡路径

本次设计把这两类动作一起纳入 manager API，但仍然坚持第一版最小化原则：

- recover 只支持一个显式策略 `latest_live_replica`
- rebalance 只做“触发当前 table 上的迁移计划”
- 两个接口都必须保持跨节点一致的 leader 视图语义

## 2. 目标与非目标

### 2.1 目标

1. 提供 `POST /manager/slots/:slot_id/recover`
2. 提供 `POST /manager/slots/rebalance`
3. 两个接口都采用 JWT + 权限校验
4. 两个接口在任意 manager 节点上都基于同一份 controller leader 视图执行
5. recover 返回显式 outcome，而不是把结果硬塞进通用 `SlotDetailDTO`
6. rebalance 返回本次启动的 migration plan 列表
7. manager 层不引入本地 fallback，保持 controller/cluster 一致视图

### 2.2 非目标

当前阶段明确不做以下内容：

- 不实现 `add slot` / `remove slot`
- 不实现 hash slot migration 的暂停、继续、撤销等细粒度接口
- 不实现多种 recover 策略
- 不实现异步任务化 rebalance API
- 不实现部分成功 / 部分失败的复合结果模型
- 不在本次中重做底层 recover 算法，只对 manager 所需的一致性入口做必要收口

## 3. 设计原则

### 3.1 所有 cluster manager 写接口都以 leader 视图为准

用户已经明确要求：

- 在 node1 获取 cluster 相关 manager 数据
- 与在 node2 获取同一资源
- 应该得到一致结果

因此本次两个写接口都必须遵循和现有 strict read 一样的规则：

- pre-check 走 controller leader 一致读
- operator 所依赖的关键 cluster 视图也必须与 controller leader 对齐
- 不允许 manager API 因为 fallback 到本地陈旧状态而出现跨节点差异

### 3.2 recover 返回 outcome，rebalance 返回 plan

这两个动作虽然都属于 slot 写接口，但成功结果类型不同：

- recover 更适合返回“本次 recover 请求的结果 + 当前 slot 详情”
- rebalance 更适合返回“本次启动的迁移计划列表”

如果两者都硬套成 `SlotDetailDTO`，会丢失操作结果语义，也不利于后端管理系统直接展示。

### 3.3 recover 第一版只暴露一个显式策略

当前底层 `pkg/cluster.RecoverSlot()` 只支持：

- `RecoverStrategyLatestLiveReplica`

manager 第一版继续保持同样约束，不扩展伪泛化策略。HTTP 层显式要求：

```json
{ "strategy": "latest_live_replica" }
```

这样未来如果需要新增策略，也可以平滑扩展而不破坏接口结构。

### 3.4 rebalance 在已有迁移进行中直接拒绝

rebalance 操作本质上会基于当前 hash slot table 启动一批 migration。如果当前已经有 active migrations，再次触发 rebalance 很容易把控制面状态叠加复杂化。

因此第一版设计为：

- 如果当前 table 已存在 active migrations
- manager rebalance 直接返回 `409`
- 不尝试合并新旧计划

## 4. HTTP 接口设计

## 4.1 `POST /manager/slots/:slot_id/recover`

- 方法：`POST`
- 路径：`/manager/slots/:slot_id/recover`
- 认证：JWT
- 权限：`cluster.slot:w`

请求体：

```json
{
  "strategy": "latest_live_replica"
}
```

成功响应：

```json
{
  "strategy": "latest_live_replica",
  "result": "quorum_reachable",
  "slot": {
    "slot_id": 2,
    "state": {
      "quorum": "ready",
      "sync": "matched"
    },
    "assignment": {
      "desired_peers": [1, 2, 3],
      "config_epoch": 8,
      "balance_version": 3
    },
    "runtime": {
      "current_peers": [1, 2, 3],
      "leader_id": 3,
      "healthy_voters": 3,
      "has_quorum": true,
      "observed_config_epoch": 8,
      "last_report_at": "2026-04-22T12:00:00Z"
    },
    "task": null
  }
}
```

`result` 第一版固定为：

- `quorum_reachable`

它表示在 `latest_live_replica` 策略下，本次 recover 请求没有进入 `manual recovery required` 错误态。

## 4.2 `POST /manager/slots/rebalance`

- 方法：`POST`
- 路径：`/manager/slots/rebalance`
- 认证：JWT
- 权限：`cluster.slot:w`
- 请求体：无

成功响应：

```json
{
  "total": 2,
  "items": [
    {
      "hash_slot": 4,
      "from_slot_id": 1,
      "to_slot_id": 2
    },
    {
      "hash_slot": 5,
      "from_slot_id": 1,
      "to_slot_id": 3
    }
  ]
}
```

如果当前已经平衡，没有需要启动的迁移，返回：

```json
{
  "total": 0,
  "items": []
}
```

## 4.3 错误语义

### recover

- `400`
  - `slot_id` 非法
  - body 非法
  - `strategy` 非法或不支持
- `401`
  - 未登录 / token 非法 / token 过期
- `403`
  - 缺少 `cluster.slot:w`
- `404`
  - slot 不存在
- `409`
  - `manual recovery required`
- `503`
  - controller leader strict read 不可用
  - slot operator 不可用
- `500`
  - 其他未预期错误

### rebalance

- `401`
  - 未登录 / token 非法 / token 过期
- `403`
  - 缺少 `cluster.slot:w`
- `409`
  - 当前已存在 active migrations
- `503`
  - controller leader strict read 不可用
  - rebalance operator 不可用
- `500`
  - 其他未预期错误

## 5. 一致性与分层设计

## 5.1 recover 需要 strict cluster 入口

当前底层 `pkg/cluster.RecoverSlot()` 内部使用：

- `ListSlotAssignments(ctx)`

这个入口允许 fallback 到本地 store / cache，不完全满足 manager cluster 接口对“一致 leader 视图”的要求。

因此本次建议在 `pkg/cluster` 中新增 strict recover 入口，例如：

- `RecoverSlotStrict(ctx, slotID, strategy) error`

其逻辑与当前 `RecoverSlot()` 保持一致，但 assignments 来源必须是：

- `ListSlotAssignmentsStrict(ctx)`

manager usecase 只调用 strict recover 入口。

## 5.2 rebalance 先 strict sync，再执行 operator

`pkg/cluster.Rebalance()` 当前依赖本地 router 上的 hash slot table。为了保证各 manager 节点调用时使用同一份 leader 视图，本次设计要求：

1. manager usecase 先调用 `ListSlotAssignmentsStrict(ctx)`
2. 该调用会通过 `controllerClient.RefreshAssignments()` 同步 controller leader 的最新 assignments 和 hash slot table 到本地 router
3. 然后再读取当前迁移状态并执行 `Rebalance(ctx)`

这样：

- 在不同 manager 节点上触发 rebalance
- 都会先对齐到 controller leader 的同一份 table
- `ComputeRebalancePlan()` 基于同一份 table 计算，结果保持一致

## 5.3 rebalance 前检查 active migrations

在 strict sync 完成后，manager usecase 读取：

- `cluster.GetMigrationStatus()`

如果已存在 active migrations，则直接返回领域错误：

- `ErrSlotMigrationsInProgress`

HTTP 层映射：

- `409 conflict`
- message: `hash slot migrations already in progress`

## 6. Usecase 设计

在 `internal/usecase/management` 中新增：

- `RecoverSlot(ctx, slotID, strategy) (SlotRecoverResult, error)`
- `RebalanceSlots(ctx) (SlotRebalanceResult, error)`

建议放到新文件：

- `internal/usecase/management/slot_operations.go`

### 6.1 Cluster 依赖扩展

`management.ClusterReader` 增加：

- `RecoverSlotStrict(ctx context.Context, slotID uint32, strategy raftcluster.RecoverStrategy) error`
- `Rebalance(ctx context.Context) ([]raftcluster.MigrationPlan, error)`
- `GetMigrationStatus() []raftcluster.HashSlotMigration`

### 6.2 recover 流程

1. `GetSlot(ctx, slotID)` 做 strict read pre-check
2. 校验策略，只允许 `latest_live_replica`
3. 调用 `cluster.RecoverSlotStrict(...)`
4. 成功后再次 `GetSlot(ctx, slotID)`
5. 返回：
   - `strategy = latest_live_replica`
   - `result = quorum_reachable`
   - `slot = latest SlotDetail`

### 6.3 rebalance 流程

1. 先调用 `ListSlotAssignmentsStrict(ctx)` 强制同步 leader 视图到本地 router
2. 调用 `GetMigrationStatus()` 检查 active migrations
3. 若存在 active migrations，返回 `ErrSlotMigrationsInProgress`
4. 调用 `cluster.Rebalance(ctx)`
5. 将返回的 `[]MigrationPlan` 转成 manager DTO
6. 空 plan 视为成功，返回 `total=0, items=[]`

## 7. DTO 设计

### 7.1 recover DTO

```go
type SlotRecoverStrategy string

const SlotRecoverStrategyLatestLiveReplica SlotRecoverStrategy = "latest_live_replica"

type SlotRecoverResult struct {
    Strategy string
    Result   string
    Slot     SlotDetail
}
```

### 7.2 rebalance DTO

```go
type SlotRebalancePlanItem struct {
    HashSlot   uint16
    FromSlotID uint32
    ToSlotID   uint32
}

type SlotRebalanceResult struct {
    Total int
    Items []SlotRebalancePlanItem
}
```

## 8. Access 层设计

在 `internal/access/manager` 中新增 handler 文件，例如：

- `slot_recover.go`
- `slot_rebalance.go`

也可以继续沿用一个文件承载多个 slot 写接口，但需要保持职责清晰。

### 8.1 recover request / response DTO

```go
type SlotRecoverRequest struct {
    Strategy string `json:"strategy"`
}

type SlotRecoverResponse struct {
    Strategy string        `json:"strategy"`
    Result   string        `json:"result"`
    Slot     SlotDetailDTO `json:"slot"`
}
```

### 8.2 rebalance response DTO

```go
type SlotRebalanceResponse struct {
    Total int                    `json:"total"`
    Items []SlotRebalancePlanDTO `json:"items"`
}

type SlotRebalancePlanDTO struct {
    HashSlot   uint16 `json:"hash_slot"`
    FromSlotID uint32 `json:"from_slot_id"`
    ToSlotID   uint32 `json:"to_slot_id"`
}
```

### 8.3 路由

在 `routes.go` 中新增 `cluster.slot:w` 路由：

- `POST /manager/slots/:slot_id/recover`
- `POST /manager/slots/rebalance`

与现有 `GET /manager/slots*` 的 `cluster.slot:r` 继续分组隔离。

## 9. 测试设计

## 9.1 Usecase 测试

新增 recover / rebalance 用例测试，至少覆盖：

### recover

1. slot 不存在返回 `ErrNotFound`
2. strategy 非法返回 `ErrUnsupportedRecoverStrategy`
3. strict recover 返回 `ErrManualRecoveryRequired`
4. strict recover 成功后回读 slot detail
5. strict read / operator unavailable 错误按原样透传

### rebalance

1. strict sync 失败直接返回错误
2. active migrations 存在时返回 `ErrSlotMigrationsInProgress`
3. rebalance 返回空计划时成功且 items 为空数组
4. rebalance 返回 migration plan 时正确映射 DTO
5. operator unavailable 错误按原样透传

## 9.2 Access 测试

至少覆盖：

### recover

1. 未登录 `401`
2. 权限不足 `403`
3. `slot_id` 非法 `400`
4. body 非法 `400`
5. strategy 非法 `400`
6. slot 不存在 `404`
7. `manual recovery required` `409`
8. strict read/operator unavailable `503`
9. 成功返回 `SlotRecoverResponse`

### rebalance

1. 未登录 `401`
2. 权限不足 `403`
3. active migrations `409`
4. strict read/operator unavailable `503`
5. 返回空 plan
6. 返回非空 migration plan

## 10. 对现有行为的影响

本次改动不会改变：

- 现有只读 slot API 行为
- 现有 slot leader transfer API 行为
- 现有 controller planner / state machine 逻辑

唯一的底层收口是：

- 为 manager recover 引入 strict cluster 入口

该入口不改变 recover 算法，只改变 manager 使用时的数据来源一致性。

## 11. 后续演进

本批完成后，slot manager 写接口可以继续按下面顺序演进：

1. `add slot`
2. `remove slot`
3. hash slot migration 细粒度运维接口
4. 更丰富的 recover 策略
5. 异步任务化 slot operations

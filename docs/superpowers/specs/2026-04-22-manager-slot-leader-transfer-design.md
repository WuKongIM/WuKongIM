# Manager Slot Leader Transfer 设计

- 日期：2026-04-22
- 范围：manager slot 写接口 `POST /manager/slots/:slot_id/leader/transfer`
- 关联目录：
  - `internal/access/manager`
  - `internal/usecase/management`
  - `internal/app`
  - `pkg/cluster`
  - `docs/superpowers/specs/2026-04-21-manager-api-foundation-design.md`
  - `docs/superpowers/specs/2026-04-22-manager-node-operator-actions-design.md`

## 1. 背景

当前 manager API 已经提供了 slot 只读能力：

- `GET /manager/slots`
- `GET /manager/slots/:slot_id`

同时，底层 cluster 已经具备 slot 级运维操作原语：

- `TransferSlotLeader(ctx, slotID, nodeID)`

后台管理系统下一批写接口如果直接进入：

- slot 恢复
- slot 增删
- rebalance

会马上碰到更大的影响面、更复杂的前置校验和更重的错误语义。相比之下，slot leader transfer 具备更适合作为第一批 slot 写接口的特征：

- 底层已有稳定 operator
- 参数只有 `slot_id` + `target_node_id`
- 成功结果可以自然复用 slot 详情 DTO
- 与现有 slot 列表/详情只读接口强关联，便于前后端联调

因此，本次设计聚焦于把 slot leader transfer 先通过 manager API 稳定暴露出来，作为 `cluster.slot:w` 的最小写闭环。

## 2. 目标与非目标

### 2.1 目标

1. 提供 `POST /manager/slots/:slot_id/leader/transfer`
2. 接口采用 JWT + 权限校验
3. 请求体仅包含 `target_node_id`
4. 写前通过 strict read 校验 slot 是否存在、目标节点是否合法
5. 写后再 strict read 回读 slot 详情
6. 成功响应直接复用 `GET /manager/slots/:slot_id` 的 `SlotDetailDTO`
7. 对“目标 leader 已经是当前 leader”的情况提供幂等成功语义

### 2.2 非目标

当前阶段明确不做以下内容：

- 不实现 `POST /manager/slots/:slot_id/recover`
- 不实现 `POST /manager/slots/rebalance`
- 不实现 `POST /manager/slots/add` / `POST /manager/slots/:slot_id/remove`
- 不实现批量 leader transfer
- 不实现异步任务化 transfer 接口
- 不新增单独的 operator result DTO
- 不改变现有 slot detail/list 的字段结构

## 3. 设计原则

### 3.1 先做最小 slot 写闭环

slot 写接口最终会覆盖更多控制面动作，但第一批应尽量选择：

- 参数最少
- 行为最直接
- 验证最清晰
- 回归面最小

slot leader transfer 满足这四个条件，因此优先于 recover / add-remove / rebalance。

### 3.2 manager API 保持 strict read 视角

和现有 manager cluster 接口一致，本接口前后都使用 strict read：

- 写前：校验 slot 存在并读取当前详情
- 写后：读取最新详情作为返回值

这样可以保证：

- 在 node1 和 node2 上访问 manager 接口时，响应都以 controller leader 的一致视图为准
- 不会因为本地 fallback 导致写前判断和写后返回不一致

### 3.3 action 接口优先于通用 PATCH

本次不设计：

- `PATCH /manager/slots/:slot_id`
- `PATCH /manager/slots/:slot_id/leader`

而采用：

- `POST /manager/slots/:slot_id/leader/transfer`

原因是底层语义本身就是显式 action，manager API 直接沿用动作语义，更清晰、更利于鉴权和测试。

### 3.4 幂等地表达目标 leader

接口语义定义为“把 slot leader 迁移到目标节点”，而不是“强制执行一次迁移动作”。

因此：

- 如果当前 leader 已经是 `target_node_id`，接口直接返回成功
- 成功体仍返回最新 `SlotDetailDTO`
- 不再额外调用底层 transfer operator

## 4. HTTP 接口设计

### 4.1 路由

- 方法：`POST`
- 路径：`/manager/slots/:slot_id/leader/transfer`
- 认证：JWT
- 权限：`cluster.slot:w`

### 4.2 请求体

```json
{
  "target_node_id": 3
}
```

约束：

- 必填
- 必须为正整数
- 必须属于该 slot 当前 `assignment.desired_peers`

### 4.3 成功响应

成功时直接返回 `GET /manager/slots/:slot_id` 相同结构，例如：

```json
{
  "slot_id": 2,
  "state": {
    "quorum": "ready",
    "sync": "matched"
  },
  "assignment": {
    "desired_peers": [1, 2, 3],
    "config_epoch": 8,
    "balance_version": 11
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
```

说明：

- 不新增新 DTO
- 便于前端在操作成功后直接刷新当前详情面板

### 4.4 错误语义

- `400`
  - `slot_id` 非法
  - 请求体非法
  - `target_node_id` 非法
  - `target_node_id` 不属于该 slot 的 `desired_peers`
- `401`
  - 未登录 / token 非法 / token 过期
- `403`
  - 缺少 `cluster.slot:w`
- `404`
  - slot 不存在
- `503`
  - controller leader strict read 不可用
  - slot leader/operator 不可用
- `500`
  - 其他未预期错误

错误体继续复用统一结构：

```json
{
  "error": "bad_request",
  "message": "target_node_id is not a desired peer"
}
```

## 5. Usecase 设计

在 `internal/usecase/management` 中新增：

- `TransferSlotLeader(ctx, slotID, targetNodeID) (SlotDetail, error)`

建议放在新文件：

- `internal/usecase/management/slot_operator.go`

### 5.1 依赖扩展

`management.ClusterReader` 增加：

- `TransferSlotLeader(ctx context.Context, slotID uint32, nodeID multiraft.NodeID) error`

### 5.2 执行流程

统一流程如下：

1. 先调用 `GetSlot(ctx, slotID)` 做 strict read
2. 如果 slot 不存在，直接返回 `controllermeta.ErrNotFound`
3. 校验 `target_node_id` 是否在 `detail.Assignment.DesiredPeers` 中
4. 如果不在，返回一个 usecase 层可识别的参数错误
5. 如果 `detail.Runtime.LeaderID == target_node_id`，直接幂等返回当前 `detail`
6. 否则调用 `a.cluster.TransferSlotLeader(ctx, slotID, multiraft.NodeID(targetNodeID))`
7. operator 成功后再次调用 `GetSlot(ctx, slotID)`
8. 返回回读后的最新 `SlotDetail`

### 5.3 领域错误

usecase 层建议新增一个明确错误：

- `ErrTargetNodeNotAssigned`

用于表达：

- slot 存在
- 但目标节点不在该 slot 的 `desired_peers` 中

这样 access 层可以稳定映射成 `400`，避免直接暴露底层 operator 错误字符串。

## 6. Access 层设计

在 `internal/access/manager` 中新增文件：

- `slot_operator.go`

### 6.1 Handler

新增 handler：

- `handleSlotLeaderTransfer`

处理逻辑：

1. 解析 `slot_id`
2. 解析 JSON body
3. 调用 `s.management.TransferSlotLeader(...)`
4. 根据错误类型映射 HTTP 状态码
5. 成功时复用 `slotDetailDTO(...)`

### 6.2 路由

在 `routes.go` 中增加单独写权限组：

- `cluster.slot:r` 继续保护只读接口
- `cluster.slot:w` 保护 transfer 写接口

即：

- `POST /manager/slots/:slot_id/leader/transfer`

不要求再叠加 `cluster.slot:r`。

## 7. 测试设计

### 7.1 Usecase 测试

新增 `internal/usecase/management/slot_operator_test.go`，覆盖：

1. slot 不存在返回 `ErrNotFound`
2. `target_node_id` 不在 `desired_peers` 中返回 `ErrTargetNodeNotAssigned`
3. 当前 leader 已是目标节点时幂等成功且不调用 operator
4. 正常调用 operator 后重新回读详情
5. strict read 错误、operator 错误按原样透传

### 7.2 Access 测试

在 `internal/access/manager/server_test.go` 中覆盖：

1. 未登录返回 `401`
2. 权限不足返回 `403`
3. `slot_id` 非法返回 `400`
4. body 非法返回 `400`
5. `target_node_id` 非法返回 `400`
6. slot 不存在返回 `404`
7. `target_node_id` 不在分配中返回 `400`
8. strict read/operator unavailable 返回 `503`
9. 成功返回 `200` + `SlotDetailDTO`

## 8. 对现有行为的影响

本次改动不会改变：

- 现有只读 slot API 行为
- 节点 operator 行为
- cluster controller planner 行为

本次只是把底层已有的 slot leader transfer 操作通过 manager API 以受控、可鉴权、可测试的方式暴露出来。

## 9. 后续演进

这一批完成后，后续 slot 写接口可以按风险从低到高继续扩展：

1. `POST /manager/slots/:slot_id/recover`
2. `POST /manager/slots/rebalance`
3. `POST /manager/slots/add`
4. `POST /manager/slots/:slot_id/remove`

其中 recover 属于单 slot 修复动作，仍适合沿用“strict read pre-check + operator + strict read reload”这一模式。

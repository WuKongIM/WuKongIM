# Manager Node Operator Actions 设计

- 日期：2026-04-22
- 范围：manager 节点写接口 `POST /manager/nodes/:node_id/draining`、`POST /manager/nodes/:node_id/resume`
- 关联目录：
  - `internal/access/manager`
  - `internal/usecase/management`
  - `internal/app`
  - `pkg/cluster`
  - `pkg/controller/plane`
  - `docs/superpowers/specs/2026-04-21-manager-api-foundation-design.md`

## 1. 背景

当前 manager API 已具备以下节点只读能力：

- `GET /manager/nodes`
- `GET /manager/nodes/:node_id`

同时，底层 cluster 已经具备节点操作能力：

- `MarkNodeDraining(ctx, nodeID)`
- `ResumeNode(ctx, nodeID)`

但是后台管理系统还无法通过 manager API 对节点执行显式运维动作，只能观察，不能操作。

对于节点管理来说，`draining` 和 `resume` 是最自然的一组第一批写接口，因为：

- 它们已经有明确的底层 operator 语义
- 它们与当前节点列表 / 详情接口天然配套
- 它们不需要引入 slot 级参数、复杂表单或额外资源模型

因此，本次设计聚焦于把这两个已有 operator 通过 manager API 以稳定、可鉴权、可测试的方式暴露出来。

## 2. 目标与非目标

## 2.1 目标

本次设计目标如下：

1. 提供 `POST /manager/nodes/:node_id/draining`
2. 提供 `POST /manager/nodes/:node_id/resume`
3. 两个接口均采用 JWT + 权限校验
4. 两个接口在成功时直接返回最新节点详情 DTO，而不是 `204`
5. 写操作前后都通过 controller leader strict read 做节点存在校验与结果回读
6. 避免未知 `node_id` 被底层 operator 意外写成 placeholder 节点

## 2.2 非目标

当前阶段明确不做以下内容：

- 不实现通用 `PATCH /manager/nodes/:node_id/state`
- 不实现批量节点操作
- 不实现节点删除、节点新增、容量权重修改等其他 node operator
- 不实现审计日志、操作人记录、审批流
- 不实现异步任务化写接口
- 不额外新增“操作结果”DTO，成功时直接复用节点详情 DTO

## 3. 设计原则

## 3.1 显式 action 接口优先于通用状态 PATCH

本次不采用：

- `PATCH /manager/nodes/:node_id/state`

而采用：

- `POST /manager/nodes/:node_id/draining`
- `POST /manager/nodes/:node_id/resume`

原因是当前底层能力本身就是显式 operator：

- `MarkNodeDraining`
- `ResumeNode`

manager API 直接保持同构语义，更容易理解、测试、审计和扩展。

## 3.2 写前校验、写后回读，避免 placeholder 节点副作用

当前 controller state machine 在处理 operator request 时，如果节点不存在，会生成带 placeholder 地址的默认节点记录。这个行为对底层 controller 是合理的兼容手段，但 manager API 不应把“未知节点”操作默默转化为“创建 placeholder 节点”。

因此 manager 写接口必须遵循：

1. 先做 strict read，确认节点存在
2. 节点不存在则直接返回 `404`
3. 节点存在时才下发 operator
4. operator 成功后再次 strict read
5. 返回最新节点详情 DTO

这样可以保证 manager API 的外部语义稳定、显式、可预期。

## 3.3 与现有 strict read 失败语义保持一致

节点写接口虽然是写操作，但它依赖：

- 写前节点存在校验
- 写后节点详情回读

两步都必须继续遵循现有 manager cluster 接口的 strict read 约束：

- 数据来源以 controller leader 为准
- 不允许 fallback 到本地滞后状态
- leader 不可达、无 leader、未启动、超时等情况统一失败

也就是说，节点写接口不做“尽量成功”的本地降级。

## 3.4 幂等地表达目标状态

本次接口语义按“使节点进入目标状态”来定义，而不是“强制做一次状态切换”：

- 对已是 `draining` 的节点再次调用 `draining`，视为成功
- 对已是 `alive` 的节点再次调用 `resume`，视为成功

成功时都返回当前最新节点详情 DTO。

## 4. HTTP 接口设计

## 4.1 `POST /manager/nodes/:node_id/draining`

- 方法：`POST`
- 路径：`/manager/nodes/:node_id/draining`
- 认证：JWT
- 权限：`cluster.node:w`
- 请求体：无

成功响应：

```json
{
  "node_id": 2,
  "addr": "127.0.0.1:7002",
  "status": "draining",
  "last_heartbeat_at": "2026-04-22T09:00:00Z",
  "is_local": false,
  "capacity_weight": 1,
  "controller": {
    "role": "follower"
  },
  "slot_stats": {
    "count": 32,
    "leader_count": 0
  },
  "slots": {
    "hosted_ids": [1, 4, 8],
    "leader_ids": []
  }
}
```

说明：

- 返回结构与 `GET /manager/nodes/:node_id` 完全一致
- 后台执行成功后无需额外再发一次详情查询

## 4.2 `POST /manager/nodes/:node_id/resume`

- 方法：`POST`
- 路径：`/manager/nodes/:node_id/resume`
- 认证：JWT
- 权限：`cluster.node:w`
- 请求体：无

成功响应同样复用 `GET /manager/nodes/:node_id` 的详情 DTO，只是预期状态变为：

- `status = alive`

## 4.3 错误语义

两个接口统一采用以下错误语义：

- `400`
  - `node_id` 非法
- `401`
  - 未带 token / token 非法 / token 过期
- `403`
  - 缺少 `cluster.node:w`
- `404`
  - 节点不存在
- `503`
  - controller leader 一致读不可用
  - controller operator 无法路由到 leader
- `500`
  - 其他未预期错误

错误体保持现有统一格式：

```json
{
  "error": "not_found",
  "message": "node not found"
}
```

## 4.4 权限设计

这两个接口仅要求：

- `cluster.node:w`

不额外叠加 `cluster.node:r`。

原因是：

- 接口本身是 node operator
- 成功时返回最新详情只是写接口结果的一部分
- 保持权限模型简单，避免把首批写接口复杂化

## 5. Usecase 设计

在 `internal/usecase/management` 中新增节点 operator 用例，例如：

- `MarkNodeDraining(ctx, nodeID) (NodeDetail, error)`
- `ResumeNode(ctx, nodeID) (NodeDetail, error)`

建议放在新文件：

- `internal/usecase/management/node_operator.go`

实现步骤统一如下：

1. 调用 `GetNode(ctx, nodeID)` 做 strict read 预检查
2. 若返回 `ErrNotFound`，直接透传 `404`
3. 如果当前状态已经是目标状态，直接返回当前详情
4. 否则调用底层 cluster operator：
   - `cluster.MarkNodeDraining(ctx, nodeID)` 或
   - `cluster.ResumeNode(ctx, nodeID)`
5. operator 成功后再次调用 `GetNode(ctx, nodeID)`
6. 返回最新节点详情 DTO

这样可以最大复用现有节点详情聚合逻辑，不需要单独维护写接口的响应拼装代码。

## 5.1 management 依赖接口调整

当前 `internal/usecase/management` 里的 cluster 依赖已经包含 strict read 方法；本次需要最小幅度扩展，补充：

- `MarkNodeDraining(ctx, nodeID) error`
- `ResumeNode(ctx, nodeID) error`

为了最小改动，当前接口可以继续保留在同一个 cluster dependency 上，不必为了本次写接口再拆新的 operator provider。

## 6. Access 设计

在 `internal/access/manager` 中新增：

- `internal/access/manager/node_operator.go`

负责：

- 解析 `node_id`
- 做 JWT 与权限校验
- 调用 management usecase
- 将 usecase 错误映射到 HTTP 状态码
- 输出节点详情 DTO

路由建议如下：

```go
nodeWrites := s.engine.Group("/manager")
if s.auth.enabled() {
    nodeWrites.Use(s.requirePermission("cluster.node", "w"))
}
nodeWrites.POST("/nodes/:node_id/draining", s.handleNodeDraining)
nodeWrites.POST("/nodes/:node_id/resume", s.handleNodeResume)
```

保持当前只读路由组不变：

- `cluster.node:r` 只覆盖 GET
- `cluster.node:w` 只覆盖 POST

这样权限边界最清晰。

## 7. DTO 设计

本次不新增独立成功响应 DTO，直接复用：

- `NodeDetailDTO`

也就是说，两个写接口成功时的响应结构与 `GET /manager/nodes/:node_id` 完全一致。

这样做的好处是：

- 前端数据结构稳定
- 不需要“操作结果 DTO”和“节点详情 DTO”来回转换
- 节点详情页面执行操作后可直接用返回值刷新本地状态

## 8. 错误映射设计

usecase / access 层建议沿用当前节点读接口错误映射方式：

- `controllermeta.ErrNotFound` -> `404`
- `raftcluster.ErrNoLeader` -> `503`
- `raftcluster.ErrNotLeader` -> `503`
- `raftcluster.ErrNotStarted` -> `503`
- `context.DeadlineExceeded` -> `503`

对于写接口底层 operator 返回的 leader 路由类错误，也同样归并到：

- `503 service_unavailable`

消息文案建议保持现有风格，例如：

- `controller leader consistent read unavailable`
- `controller leader operator unavailable`

如果实现时希望减少前端分支，也可以统一成更宽泛但稳定的：

- `controller leader unavailable`

第一版建议优先保持简单，只要错误码稳定即可。

## 9. 测试设计

## 9.1 usecase 测试

至少覆盖：

1. 节点不存在时返回 `ErrNotFound`，且不调用 operator
2. 节点已是 `draining` 时，`MarkNodeDraining` 幂等成功
3. 节点已是 `alive` 时，`ResumeNode` 幂等成功
4. 节点状态切换成功后返回最新 `NodeDetail`
5. strict read 不可用时，错误按原样透传
6. operator 返回 leader 路由类错误时，错误按原样透传

## 9.2 access 测试

至少覆盖：

1. 缺少 token -> `401`
2. 缺少 `cluster.node:w` -> `403`
3. 非法 `node_id` -> `400`
4. 节点不存在 -> `404`
5. strict read / operator unavailable -> `503`
6. `draining` 成功返回节点详情 DTO
7. `resume` 成功返回节点详情 DTO

## 10. 风险与边界

## 10.1 写后立即回读仍可能遇到 leader 切换

即使 operator 已提交成功，写后回读仍可能因为 leader 切换、超时或暂时不可用而失败。第一版不做“写成功但回读失败”的部分成功语义，统一 fail closed，返回错误。

这样虽然会让少量成功操作看起来像失败，但可以保持 manager API 的对外契约简单明确。

## 10.2 `resume` 的状态目标只定义为 `alive`

底层 `ResumeNode` 当前语义是把节点状态恢复为 `alive`，并清理相关 repair task。manager API 第一版直接继承这个语义，不引入额外“恢复到操作前状态”或“恢复到健康推断状态”的复杂逻辑。

## 11. 结论

本次建议以最小闭环方式补齐第一批 manager 节点写接口：

- `POST /manager/nodes/:node_id/draining`
- `POST /manager/nodes/:node_id/resume`

它们：

- 直接映射现有底层 cluster operator
- 写前做 strict read 存在校验，避免 placeholder 节点副作用
- 写后回读并返回最新 `NodeDetailDTO`
- 继续沿用现有 manager JWT / 权限 / strict read / 错误映射模式

这能在不引入过多抽象和新概念的前提下，为后台管理系统提供第一个真正可操作的 node 管理闭环。

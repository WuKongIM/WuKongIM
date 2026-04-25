# Manager Overview 设计

- 日期：2026-04-21
- 范围：manager 首页总览接口 `GET /manager/overview`
- 关联目录：
  - `internal/access/manager`
  - `internal/usecase/management`
  - `internal/app`
  - `pkg/cluster`
  - `docs/superpowers/specs/2026-04-21-manager-api-foundation-design.md`

## 1. 背景

在 manager API 基础框架已经具备以下能力后：

- `POST /manager/login`
- `GET /manager/nodes`
- `GET /manager/slots`
- `GET /manager/slots/:slot_id`
- `GET /manager/tasks`
- `GET /manager/tasks/:slot_id`
- `GET /manager/channel-runtime-meta`
- `GET /manager/channel-runtime-meta/:channel_type/:channel_id`

后台管理系统仍缺一个“首页总览”入口，用于回答以下问题：

- 当前集群整体是否健康
- 当前 slot 是否存在 quorum 丢失、leader 缺失或同步异常
- 当前 reconcile task 是否存在失败或重试中的任务

这个首页总览接口不应替代现有列表和详情接口，而应提供稳定、轻量、可直接渲染后台首屏的统计块与异常摘要块。

## 2. 目标与非目标

## 2.1 目标

本次设计目标如下：

1. 提供 `GET /manager/overview` 作为 manager 首页总览接口
2. 第一版返回 `nodes / slots / tasks` 的汇总统计
3. 第一版返回 `slots / tasks` 的少量异常摘要样本，便于首页首屏告警
4. 所有 overview 数据继续遵循 controller leader strict read 语义，不允许本地 fallback
5. 维持现有分层边界：access 负责协议与鉴权，usecase 负责聚合，app 负责装配

## 2.2 非目标

当前阶段明确不做以下内容：

- 不把 `GET /manager/overview` 做成大而全仪表盘接口
- 不包含 `channel runtime meta` 汇总或异常摘要
- 不包含时间序列、趋势图、历史窗口统计
- 不包含分页、筛选、排序参数
- 不返回全量异常明细，只返回固定上限的摘要样本
- 不复用本地 observation snapshot 或本地缓存做降级展示

## 3. 设计原则

## 3.1 首页总览优先，异常可见

`overview` 第一版定位为：首页首屏。它需要优先表达“系统整体健康度”，同时让异常一眼可见。因此第一版采用：

- 汇总统计块：`nodes / slots / tasks`
- 异常摘要块：`slots / tasks`

而不是直接暴露大段列表或趋势图。

## 3.2 权威读失败时整体失败

`overview` 涉及的是 cluster 管理面聚合读，因此应继续遵循 manager foundation 中已经确定的约束：

- 所有数据源统一基于 controller leader strict read
- 任一关键 strict read 失败时，整个 overview 请求失败
- 不返回“统计有值但异常摘要缺失”的部分成功结果

## 3.3 样本有上限，不替代列表页

异常摘要中的 `items` 只用于首页提示，不替代 `slots` / `tasks` 列表页：

- 每类异常样本固定最多返回 `5` 条
- `count` 表示全量异常数，不等于 `items` 数量
- 需要完整明细时，后台跳转到现有对应列表或详情接口

## 4. HTTP 接口设计

## 4.1 `GET /manager/overview`

- 方法：`GET`
- 路径：`/manager/overview`
- 认证：JWT
- 权限：`cluster.overview:r`

第一版不支持任何查询参数。

## 4.2 成功响应结构

成功时返回单对象，而不是 `total/items` 外壳。建议结构如下：

```json
{
  "generated_at": "2026-04-21T21:50:00Z",
  "cluster": {
    "controller_leader_id": 1
  },
  "nodes": {
    "total": 5,
    "alive": 4,
    "suspect": 0,
    "dead": 0,
    "draining": 1
  },
  "slots": {
    "total": 64,
    "ready": 60,
    "quorum_lost": 1,
    "leader_missing": 1,
    "unreported": 1,
    "peer_mismatch": 1,
    "epoch_lag": 2
  },
  "tasks": {
    "total": 3,
    "pending": 1,
    "retrying": 1,
    "failed": 1
  },
  "anomalies": {
    "slots": {
      "quorum_lost": {
        "count": 1,
        "items": []
      },
      "leader_missing": {
        "count": 1,
        "items": []
      },
      "sync_mismatch": {
        "count": 3,
        "items": []
      }
    },
    "tasks": {
      "failed": {
        "count": 1,
        "items": []
      },
      "retrying": {
        "count": 1,
        "items": []
      }
    }
  }
}
```

说明：

- `generated_at`
  - 本次 overview 聚合生成时间
  - 仅表示这次响应的生成时刻，不表示全局事务快照时间
- `cluster.controller_leader_id`
  - 当前 controller leader 节点 ID
- `nodes / slots / tasks`
  - 首页卡片可直接渲染的稳定计数
- `anomalies`
  - 首页异常摘要块
  - 每类异常带全量 `count` 和少量 `items`

## 5. DTO 设计

## 5.1 汇总统计 DTO

### `cluster`

- `controller_leader_id`

### `nodes`

- `total`
- `alive`
- `suspect`
- `dead`
- `draining`

### `slots`

- `total`
- `ready`
- `quorum_lost`
- `leader_missing`
- `unreported`
- `peer_mismatch`
- `epoch_lag`

### `tasks`

- `total`
- `pending`
- `retrying`
- `failed`

## 5.2 Slot 异常摘要 DTO

第一版只返回三类 slot 异常摘要：

- `quorum_lost`
- `leader_missing`
- `sync_mismatch`

每类结构统一为：

- `count`
- `items`

其中 `items[]` 字段如下：

- `slot_id`
- `quorum`
- `sync`
- `leader_id`
- `desired_peers`
- `current_peers`
- `last_report_at`

说明：

- `sync_mismatch` 的 `count` 为 `peer_mismatch + epoch_lag` 的合计
- `sync_mismatch.items[].sync` 仍保留每条 slot 自己的真实值，允许前端区分：
  - `peer_mismatch`
  - `epoch_lag`

## 5.3 Task 异常摘要 DTO

第一版只返回两类 task 异常摘要：

- `failed`
- `retrying`

每类结构统一为：

- `count`
- `items`

其中 `items[]` 字段直接复用现有 task 核心字段：

- `slot_id`
- `kind`
- `step`
- `status`
- `source_node`
- `target_node`
- `attempt`
- `next_run_at`
- `last_error`

## 6. 数据来源与聚合设计

## 6.1 数据来源

`internal/usecase/management` 的 overview 聚合建议直接使用以下权威读：

- `cluster.ControllerLeaderID()`
- `cluster.SlotIDs()`
- `cluster.ListNodesStrict(ctx)`
- `cluster.ListSlotAssignmentsStrict(ctx)`
- `cluster.ListObservedRuntimeViewsStrict(ctx)`
- `cluster.ListTasksStrict(ctx)`

不通过串调现有 `ListNodes / ListSlots / ListTasks` usecase 结果来构建 overview，原因是：

- 可以避免重复 strict read
- 可以保证一次 overview 响应中的统计和异常摘要来自同一轮聚合过程
- 仍可复用现有 slot/task 状态推导逻辑和 DTO 映射规则

## 6.2 `generated_at` 聚合规则

- 在 overview usecase 开始聚合时取一次时间戳
- 输出为 UTC RFC3339 格式

## 6.3 `nodes` 聚合规则

基于 `ListNodesStrict(ctx)` 聚合：

- `total = len(nodes)`
- `alive / suspect / dead / draining` 按节点稳定字符串状态聚合
- 第一版不额外输出 `unknown` 计数；未知状态只计入 `total`

## 6.4 `slots` 聚合规则

slot 统计以 `cluster.SlotIDs()` 作为 physical slot 全集：

- `total = len(cluster.SlotIDs())`

再将以下结果按 `slot_id` 做 join：

- `ListSlotAssignmentsStrict(ctx)`
- `ListObservedRuntimeViewsStrict(ctx)`

每个 slot 的 `quorum` 与 `sync` 推导规则继续复用现有 slot list 语义：

- `quorum`
  - `ready`
  - `lost`
  - `unknown`
- `sync`
  - `matched`
  - `peer_mismatch`
  - `epoch_lag`
  - `unreported`

首页统计按独立计数聚合：

- `ready`: `quorum == ready`
- `quorum_lost`: `quorum == lost`
- `leader_missing`: 存在 runtime view 且 `leader_id == 0`
- `unreported`: `sync == unreported`
- `peer_mismatch`: `sync == peer_mismatch`
- `epoch_lag`: `sync == epoch_lag`

说明：

- 这些计数不要求互斥
- `leader_missing` 只统计“有 runtime view 但 leader 缺失”的 slot
- `unreported` 不计入 `leader_missing`

## 6.5 `tasks` 聚合规则

基于 `ListTasksStrict(ctx)` 聚合：

- `total = len(tasks)`
- `pending / retrying / failed` 按现有稳定 task 状态字符串聚合

## 6.6 Slot 异常摘要聚合规则

第一版只生成以下三类异常摘要：

### `quorum_lost`

筛选条件：

- `quorum == lost`

### `leader_missing`

筛选条件：

- 存在 runtime view
- `leader_id == 0`

### `sync_mismatch`

筛选条件：

- `sync == peer_mismatch`
- 或 `sync == epoch_lag`

聚合约束：

- `count` 为该类异常的全量数量
- `items` 按 `slot_id ASC`
- `items` 固定最多返回 `5` 条

第一版不为 `unreported` 单独生成异常样本块；它先只出现在汇总统计中。

## 6.7 Task 异常摘要聚合规则

第一版只生成以下两类异常摘要：

- `failed`
- `retrying`

聚合约束：

- `count` 为该类异常的全量数量
- `items` 按 `slot_id ASC`
- `items` 固定最多返回 `5` 条

## 7. 错误语义

`GET /manager/overview` 的错误语义建议如下：

- `401`
  - 缺少 token
  - token 非法
  - token 过期
- `403`
  - token 有效但无 `cluster.overview:r`
- `503`
  - 任一 controller leader strict read 不可用
  - 包括：
    - `raftcluster.ErrNoLeader`
    - `raftcluster.ErrNotLeader`
    - `raftcluster.ErrNotStarted`
    - `context.DeadlineExceeded`
    - controller strict read RPC 失败
- `500`
  - 其余未归类内部错误

不做部分成功返回。

## 8. 分层与实现建议

## 8.1 access 层

`internal/access/manager` 负责：

- 注册 `GET /manager/overview`
- JWT 与权限校验
- 调用 management usecase
- 输出 overview JSON
- 将 strict read 不可用错误映射为 `503`

## 8.2 usecase 层

建议在 `internal/usecase/management` 中新增：

- `GetOverview(ctx)` 或 `Overview(ctx)` 形式的聚合方法
- overview 专用 DTO
- slot/task 异常摘要的截断与排序逻辑

## 8.3 app 层

`internal/app` 继续只负责装配，不新增新的底层 cluster 读边界。

## 9. 测试建议

## 9.1 usecase 测试

至少覆盖：

- `nodes / slots / tasks` 统计数字正确
- `slots.total` 基于 `cluster.SlotIDs()`，而不是 assignment/runtime 子集
- `leader_missing / quorum_lost / peer_mismatch / epoch_lag` 计数正确
- `anomalies.slots.quorum_lost.items` 按 `slot_id` 升序且最多 `5` 条
- `anomalies.slots.sync_mismatch.count` 为两类合计，但 `items[].sync` 保留真实值
- `tasks.pending / retrying / failed` 计数正确
- `anomalies.tasks.failed / retrying` 截断与排序正确
- 任一 strict read 失败时整体返回错误

## 9.2 access 测试

至少覆盖：

- 缺少 token 返回 `401`
- 缺少 `cluster.overview:r` 返回 `403`
- strict read 不可用返回 `503`
- 成功返回固定 JSON 结构
- 响应中不存在分页字段和查询参数语义

## 10. 决策摘要

本次设计最终确定以下关键决策：

1. manager 首页总览接口定为 `GET /manager/overview`
2. 第一版定位为“总览优先 + 异常可见”，而不是大而全仪表盘
3. 第一版只包含 `nodes / slots / tasks` 汇总统计
4. 第一版异常摘要只包含 `slots / tasks`
5. `channel runtime meta` 暂不进入 `overview v1`
6. overview 继续统一走 controller leader strict read，不允许本地 fallback
7. 任一关键 strict read 失败时，overview 整体失败
8. 每类异常样本固定最多返回 `5` 条，按 `slot_id` 升序
9. overview 使用独立权限 `cluster.overview:r`

这套方案优先解决后台首页“先看到集群整体健康和关键异常”的需求，同时避免把首页接口做成一次高成本、难维护的大型聚合面板。

# 管理 API 基础框架设计

- 日期：2026-04-21
- 范围：独立 manager API 服务、JWT 登录与权限、manager 只读接口（nodes / slots / slot detail / tasks / task detail / channel runtime meta list）
- 关联目录：
  - `internal/access/api`
  - `internal/app`
  - `cmd/wukongim`
  - `pkg/cluster`
  - `learn_project/WuKongIM/internal/api/server_manager.go`
  - `learn_project/WuKongIM/pkg/auth`

## 1. 背景

当前 `internal/access/api` 暴露的 HTTP API 主要面向第三方内部业务服务，例如路由查询、token 更新、消息发送、会话同步。这类 API 的特点是：

- 入口开放，默认无 JWT 登录态
- 关注消息和接入侧业务动作
- 与后台管理系统的访问模型不同

本次需要为后台管理系统新增一套独立的 manager API 服务，满足以下前提：

- 与现有业务 API 不同端口
- 使用 JWT 登录态
- 支持资源级权限校验
- 先建立节点、slot、reconcile task 这三类 cluster 只读接口骨架
- 在此基础上补一个分页的 `channel runtime meta` 管理查询入口

当前阶段的目标不是一次性补齐所有管理能力，而是先把“独立端口 + 登录 + 权限 + 管理接口聚合查询 + app 装配”这条链路建立起来，并在此骨架上验证一个真正可分页的 `channel runtime meta` 查询接口，为后续扩展 `cluster / slot / channel runtime meta` 管理接口提供稳定基础。

## 2. 目标与非目标

## 2.1 目标

本次设计目标如下：

1. 新增独立的 manager API 服务，不复用当前 `internal/access/api` 的服务实例与监听端口
2. 提供 `POST /manager/login`，基于配置中的静态用户签发 JWT
3. 提供 `GET /manager/nodes`、`GET /manager/slots`、`GET /manager/slots/:slot_id`、`GET /manager/tasks`、`GET /manager/tasks/:slot_id`、`GET /manager/channel-runtime-meta`，要求 JWT 与权限校验通过后才能访问
4. manager 只读接口返回适合后台展示的稳定 DTO，而不是直接透传内部控制面模型
5. manager 读接口从任意节点访问时，都必须返回同一个权威数据源视角的数据，或显式失败；其中 cluster 控制面接口以 controller leader 为准，`channel runtime meta` 以对应 slot leader 为准
6. 在 `internal/app` 中将 manager 服务作为独立入口装配进应用生命周期
7. 在 `cmd/wukongim/config.go` 和 `wukongim.conf.example` 中补齐 manager 配置

## 2.2 非目标

当前阶段明确不做以下内容：

- 不实现 `GET /manager/channel-runtime-meta/:channel_id`、`overview` 等更多 manager 只读接口
- 不为 `GET /manager/channel-runtime-meta` 增加筛选条件（如 `slot_id`、`channel_type`）或全量 `total`
- 不实现基于数据库或外部 IAM 的动态账号体系
- 不实现刷新 token、登出、黑名单、会话吊销
- 不实现细粒度的写操作权限校验链路
- 不实现 `ForceReconcile`、`MarkNodeDraining`、`ResumeNode`、`TransferSlotLeader`、`RecoverSlot` 等 manager 写操作
- 不兼容旧版 manager server 的全部功能，只借鉴其“登录 + JWT + 权限”模式

## 3. 设计原则

## 3.1 管理入口与业务入口分离

`internal/access/api` 继续服务第三方内部业务系统；manager API 专门服务后台管理系统。两者必须：

- 使用不同 HTTP server
- 使用不同监听地址
- 使用不同认证方式
- 保持各自职责边界清晰

## 3.2 继续遵守现有分层约定

遵循仓库现有约定：

- `internal/access/*` 只做入口协议适配
- `internal/usecase/*` 承载可复用业务编排
- `internal/app` 继续作为唯一组合根

因此 manager 的聚合查询逻辑不直接堆进 HTTP handler，而是下沉到新的 `internal/usecase/management`。

## 3.3 DTO 面向管理系统稳定，而不是面向内部模型稳定

后台管理系统需要的是稳定的展示与管理接口，而不是内部控制面结构的直接镜像。当前阶段虽然以 nodes / slots / tasks 以及 `channel runtime meta` 列表这几类基础只读接口为主，也应定义独立 DTO，避免把 manager API 与内部模型直接耦合。

## 3.4 第一版只搭骨架，不做过度抽象

当前阶段只做：

- 静态用户认证
- JWT 签发与解析
- 资源/动作权限校验
- `nodes / slots / tasks` 只读接口
- 一个真正按页读取的 `channel runtime meta` 列表接口

不为了未来所有管理能力提前抽出过重的统一鉴权框架或通用插件体系。

## 3.5 Cluster 级 manager 读接口必须跨节点一致

所有 manager 分布式读接口都需要满足一个额外约束：

- 从任意节点访问同一个 manager cluster 接口，返回的数据来源必须一致
- 控制面类接口统一以 controller leader 视角作为真相源
- `channel runtime meta` 统一以对应 slot leader 视角作为真相源
- 当对应的权威 leader 不可达、无 leader、重定向失败或读超时时，接口应直接失败
- 不允许为了“尽量返回结果”而降级到本地 `controllerMeta`、本地 Pebble 快照或本地滞后 observation snapshot

这条约束适用于：

- `GET /manager/nodes`
- `GET /manager/slots`
- `GET /manager/slots/:slot_id`
- `GET /manager/tasks`
- `GET /manager/tasks/:slot_id`
- `GET /manager/channel-runtime-meta`
- 后续所有 `cluster / slot / channel runtime meta` 的 manager 只读接口

## 4. 包结构设计

新增以下结构：

```text
internal/
  access/
    manager/            manager HTTP 入口、路由、中间件
  usecase/
    management/         manager 聚合查询用例
```

其中职责约束如下。

## 4.1 `internal/access/manager`

负责：

- 创建独立 gin server
- 注册 manager 路由
- 解析登录请求
- JWT 中间件
- 权限中间件
- 调用 management 用例并输出 JSON

不负责：

- 节点角色统计逻辑
- slot 统计逻辑
- task 类型/步骤/状态映射逻辑
- controller 角色推导逻辑

## 4.2 `internal/usecase/management`

当前阶段在 `internal/usecase/management` 中维护一组很薄的 manager 聚合查询用例，例如：

- `ListNodes(ctx)`
- `ListSlots(ctx)`
- `GetSlot(ctx, slotID)`
- `ListTasks(ctx)`
- `GetTask(ctx, slotID)`
- `ListChannelRuntimeMeta(ctx, req)`

负责：

- 调用 cluster 控制面只读接口与 slot 权威读接口
- 聚合节点、slot runtime view、task、controller 角色与 channel runtime meta 分页结果
- 输出 manager 稳定 DTO

这样后续继续扩展 `slot detail`、`channel runtime meta` 管理接口时，仍能延续同一个 usecase 包，而不会把查询聚合逻辑散落在 access 层。

## 5. 配置设计

## 5.1 新增配置项

第一版新增以下配置：

```conf
WK_MANAGER_LISTEN_ADDR=0.0.0.0:5301
WK_MANAGER_AUTH_ON=true
WK_MANAGER_JWT_SECRET=change-me
WK_MANAGER_JWT_ISSUER=wukongim-manager
WK_MANAGER_JWT_EXPIRE=24h
WK_MANAGER_USERS=[{"username":"admin","password":"admin123","permissions":[{"resource":"cluster.node","actions":["r"]}]}]
```

配置语义：

- `WK_MANAGER_LISTEN_ADDR`
  - manager 服务监听地址
  - 为空表示 manager 服务关闭
- `WK_MANAGER_AUTH_ON`
  - 是否启用 manager 鉴权
  - 第一版默认建议开启
- `WK_MANAGER_JWT_SECRET`
  - JWT 签名密钥
- `WK_MANAGER_JWT_ISSUER`
  - JWT `iss`
- `WK_MANAGER_JWT_EXPIRE`
  - JWT 过期时间
- `WK_MANAGER_USERS`
  - 静态用户列表，包含用户名、密码、权限

## 5.2 配置模型

建议在 `internal/app/config.go` 中新增：

- `ManagerConfig`
- `ManagerAuthConfig`
- `ManagerUserConfig`
- `ManagerPermissionConfig`

其中 `ManagerConfig` 至少包含：

- `ListenAddr string`
- `AuthOn bool`
- `JWTSecret string`
- `JWTIssuer string`
- `JWTExpire time.Duration`
- `Users []ManagerUserConfig`

`ManagerUserConfig` 至少包含：

- `Username string`
- `Password string`
- `Permissions []ManagerPermissionConfig`

`ManagerPermissionConfig` 至少包含：

- `Resource string`
- `Actions []string`

第一版不额外设计数据库模型或外部 provider。

## 5.3 启用与校验规则

建议规则：

- `WK_MANAGER_LISTEN_ADDR` 为空时：manager 服务关闭，不做 manager 配置强校验
- `WK_MANAGER_LISTEN_ADDR` 非空且 `WK_MANAGER_AUTH_ON=true` 时：
  - `JWTSecret` 必须非空
  - `Users` 必须非空
- `JWTExpire` 必须大于 0
- 用户名不能为空
- 密码不能为空
- 权限资源不能为空
- 权限动作必须属于 `r`、`w`、`*`

## 5.4 示例配置

`wukongim.conf.example` 需要新增 manager 示例段，且强调：

- manager 是独立服务
- 端口不同于 `WK_API_LISTEN_ADDR`
- 生产环境必须替换 JWT secret 与默认密码

## 6. 认证与权限模型

## 6.1 登录模式

第一版沿用旧项目的模式：

- manager 用户来自配置文件
- `POST /manager/login` 提交用户名/密码
- 服务端认证成功后签发 JWT

不采用“外部已签 JWT 直接透传”的模式，因为本次目标之一就是把 manager 服务自己的认证链路搭建完整。

## 6.2 JWT Claims

第一版 JWT claims 保持最小集：

- `iss`
- `iat`
- `exp`
- `username`

不在 JWT 中固化权限快照。

## 6.3 权限校验策略

当前阶段权限模型保留最小可扩展能力：

- 动作：`r`、`w`、`*`
- 资源：当前至少定义 `cluster.node`、`cluster.slot`、`cluster.task`、`cluster.channel`

接口与权限关系：

- `GET /manager/nodes` -> `cluster.node:r`
- `GET /manager/slots` -> `cluster.slot:r`
- `GET /manager/slots/:slot_id` -> `cluster.slot:r`
- `GET /manager/tasks` -> `cluster.task:r`
- `GET /manager/tasks/:slot_id` -> `cluster.task:r`
- `GET /manager/channel-runtime-meta` -> `cluster.channel:r`

校验过程：

1. JWT 中间件验证签名与时效
2. 从 claims 提取 `username`
3. 权限中间件根据当前内存配置查询该用户权限
4. 判断是否具备目标资源的目标动作

注意：服务端不直接信任 token 内自带权限，避免权限配置更新后旧 token 仍长期保留旧权限快照。

## 6.4 资源与动作常量

建议在 manager 包内定义最小权限常量，例如：

- 资源：`cluster.node`、`cluster.slot`、`cluster.task`、`cluster.channel`
- 动作：`r`、`w`、`*`

第一版不抽出全仓库共享的 `pkg/auth`，避免在只有一个 manager 接口的阶段引入过重改造。待 manager 能力扩展后再评估是否上提为共用包。

## 7. HTTP 接口设计

## 7.1 `POST /manager/login`

请求：

```json
{
  "username": "admin",
  "password": "admin123"
}
```

成功响应：

```json
{
  "username": "admin",
  "token_type": "Bearer",
  "access_token": "<jwt>",
  "expires_in": 86400,
  "expires_at": "2026-04-22T10:00:00+08:00",
  "permissions": [
    {
      "resource": "cluster.node",
      "actions": ["r"]
    }
  ]
}
```

错误语义：

- 用户名或密码错误：`401`
- 请求体非法：`400`
- manager 鉴权未正确配置：`500`

错误体格式统一为：

```json
{
  "error": "invalid_credentials",
  "message": "invalid credentials"
}
```

## 7.2 `GET /manager/nodes`

请求头：

```text
Authorization: Bearer <jwt>
```

成功响应：

```json
{
  "total": 1,
  "items": [
    {
      "node_id": 1,
      "addr": "127.0.0.1:7000",
      "status": "alive",
      "last_heartbeat_at": "2026-04-21T10:00:00+08:00",
      "is_local": true,
      "capacity_weight": 1,
      "controller": {
        "role": "leader"
      },
      "slot_stats": {
        "count": 32,
        "leader_count": 12
      }
    }
  ]
}
```

错误语义：

- 未带 token / token 非法 / token 过期：`401`
- 权限不足：`403`
- controller leader 一致读不可用：`503`

错误体同样使用：

```json
{
  "error": "forbidden",
  "message": "forbidden"
}
```

第一版不做分页、过滤、排序参数，避免在框架搭建阶段引入不必要复杂度。

## 7.3 `GET /manager/slots`

请求头：

```text
Authorization: Bearer <jwt>
```

成功响应：

```json
{
  "total": 1,
  "items": [
    {
      "slot_id": 1,
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
        "leader_id": 1,
        "healthy_voters": 3,
        "has_quorum": true,
        "observed_config_epoch": 8,
        "last_report_at": "2026-04-21T10:00:00+08:00"
      }
    }
  ]
}
```

错误语义：

- 未带 token / token 非法 / token 过期：`401`
- 权限不足：`403`
- controller leader 一致读不可用：`503`

第一版不做分页、过滤、排序参数，列表按 `slot_id` 升序返回。

## 7.4 `GET /manager/slots/:slot_id`

请求头：

```text
Authorization: Bearer <jwt>
```

成功响应：

```json
{
  "slot_id": 2,
  "state": {
    "quorum": "ready",
    "sync": "matched"
  },
  "assignment": {
    "desired_peers": [2, 3, 5],
    "config_epoch": 8,
    "balance_version": 3
  },
  "runtime": {
    "current_peers": [2, 3, 5],
    "leader_id": 2,
    "healthy_voters": 3,
    "has_quorum": true,
    "observed_config_epoch": 8,
    "last_report_at": "2026-04-21T10:00:00+08:00"
  },
  "task": {
    "kind": "repair",
    "step": "catch_up",
    "status": "retrying",
    "source_node": 3,
    "target_node": 5,
    "attempt": 1,
    "next_run_at": "2026-04-21T10:05:00+08:00",
    "last_error": "learner catch-up timeout"
  }
}
```

错误语义：

- `slot_id` 非法：`400`
- 未带 token / token 非法 / token 过期：`401`
- 权限不足：`403`
- 指定 `slot_id` 不存在 slot：`404`
- controller leader 一致读不可用：`503`

路径参数规则：

- `slot_id` 必须是正整数
- 第一版直接按单个 `slot_id` 查询，不支持批量查询

补充约束：

- `task` 表示当前 slot 的 reconcile task 摘要
- 若该 slot 当前没有 reconcile task，则返回 `null`
- `task` 为空不应让 slot 详情接口返回 `404`

## 7.5 `GET /manager/tasks`

请求头：

```text
Authorization: Bearer <jwt>
```

成功响应：

```json
{
  "total": 1,
  "items": [
    {
      "slot_id": 2,
      "kind": "repair",
      "step": "catch_up",
      "status": "retrying",
      "source_node": 3,
      "target_node": 5,
      "attempt": 1,
      "next_run_at": "2026-04-21T10:05:00+08:00",
      "last_error": "learner catch-up timeout"
    }
  ]
}
```

错误语义：

- 未带 token / token 非法 / token 过期：`401`
- 权限不足：`403`
- controller leader 一致读不可用：`503`

第一版不做分页、过滤、排序参数，列表按 `slot_id` 升序返回。

## 7.6 `GET /manager/tasks/:slot_id`

请求头：

```text
Authorization: Bearer <jwt>
```

成功响应：

```json
{
  "slot_id": 2,
  "kind": "repair",
  "step": "catch_up",
  "status": "retrying",
  "source_node": 3,
  "target_node": 5,
  "attempt": 1,
  "next_run_at": "2026-04-21T10:05:00+08:00",
  "last_error": "learner catch-up timeout",
  "slot": {
    "state": {
      "quorum": "ready",
      "sync": "matched"
    },
    "assignment": {
      "desired_peers": [2, 3, 5],
      "config_epoch": 8,
      "balance_version": 3
    },
    "runtime": {
      "current_peers": [2, 3, 5],
      "leader_id": 2,
      "healthy_voters": 3,
      "has_quorum": true,
      "observed_config_epoch": 8,
      "last_report_at": "2026-04-21T10:00:00+08:00"
    }
  }
}
```

错误语义：

- `slot_id` 非法：`400`
- 未带 token / token 非法 / token 过期：`401`
- 权限不足：`403`
- 指定 `slot_id` 不存在 reconcile task：`404`
- controller leader 一致读不可用：`503`

路径参数规则：

- `slot_id` 必须是正整数
- 第一版直接按单个 `slot_id` 查询，不支持批量查询

## 7.7 `GET /manager/channel-runtime-meta`

请求头：

```text
Authorization: Bearer <jwt>
```

查询参数：

- `limit`
  - 可选
  - 默认 `50`
  - 最大 `200`
- `cursor`
  - 可选
  - opaque cursor
  - 当前版本内部使用 base64(JSON) 编码 `{v, slot_id, channel_id, channel_type}`

成功响应：

```json
{
  "limit": 2,
  "has_more": true,
  "next_cursor": "eyJ2IjoxLCJzbG90X2lkIjo3LCJjaGFubmVsX2lkIjoiZzIiLCJjaGFubmVsX3R5cGUiOjJ9",
  "items": [
    {
      "channel_id": "g1",
      "channel_type": 2,
      "slot_id": 7,
      "channel_epoch": 11,
      "leader_epoch": 5,
      "leader": 3,
      "replicas": [3, 5, 8],
      "isr": [3, 5],
      "min_isr": 2,
      "status": "active"
    },
    {
      "channel_id": "g2",
      "channel_type": 2,
      "slot_id": 7,
      "channel_epoch": 12,
      "leader_epoch": 6,
      "leader": 5,
      "replicas": [3, 5, 8],
      "isr": [3, 5, 8],
      "min_isr": 2,
      "status": "active"
    }
  ]
}
```

错误语义：

- `limit` 非法：`400`
- `cursor` 非法或 cursor 版本不支持：`400`
- 未带 token / token 非法 / token 过期：`401`
- 权限不足：`403`
- 当前页所需 slot leader 权威读不可用：`503`

分页约束：

- 返回体不提供 `total`
- 排序固定为 `slot_id ASC`、`channel_id ASC`、`channel_type ASC`
- `next_cursor` 为空字符串表示没有下一页
- 不允许通过全量聚合后再截页来实现分页

## 7.8 `GET /manager/channel-runtime-meta/:channel_type/:channel_id`

请求头：

```text
Authorization: Bearer <jwt>
```

路径参数：

- `channel_type`
  - 必填
  - 正整数
- `channel_id`
  - 必填
  - 频道 ID

成功响应：

```json
{
  "channel_id": "g1",
  "channel_type": 2,
  "slot_id": 7,
  "hash_slot": 129,
  "channel_epoch": 12,
  "leader_epoch": 6,
  "leader": 3,
  "replicas": [3, 5, 8],
  "isr": [3, 5],
  "min_isr": 2,
  "status": "active",
  "features": 1,
  "lease_until_ms": 1700000000000
}
```

错误语义：

- `channel_type` 非法：`400`
- 未带 token / token 非法 / token 过期：`401`
- 权限不足：`403`
- 该 `(channel_type, channel_id)` 不存在：`404`
- 目标 slot leader 权威读不可用：`503`

详情一致性约束：

- 该接口不通过扫描或分页链路定位目标记录
- 该接口必须基于 `(channel_type, channel_id)` 做精确单条权威读取
- 从任意节点访问同一频道详情时，应路由到同一个 slot leader 数据源
- 不允许退回本地 DB 或本地缓存视图

## 8. 节点、Slot、Task 与 Channel Runtime Meta DTO 设计

节点列表第一版返回如下字段：

- `total`
- `items[].node_id`
- `items[].addr`
- `items[].status`
- `items[].last_heartbeat_at`
- `items[].is_local`
- `items[].capacity_weight`
- `items[].controller.role`
- `items[].slot_stats.count`
- `items[].slot_stats.leader_count`

字段说明如下。

## 8.1 基础字段

- `node_id`
  - 节点 ID
- `addr`
  - 集群监听地址
- `status`
  - 节点状态，字符串化输出，建议值：
    - `unknown`
    - `alive`
    - `suspect`
    - `dead`
    - `draining`
- `last_heartbeat_at`
  - 最近心跳时间，RFC3339 格式输出
- `capacity_weight`
  - 节点容量权重

## 8.2 角色字段

不建议直接在顶层平铺多个角色字段，因为后续管理接口可能还会继续扩展控制面信息。第一版将控制面角色收敛为 `controller.role`：

- `controller.role`
  - `leader`
  - `follower`
  - `none`

这样后续若需要补充 controller peer、leader 任期或其他控制面信息时，可以继续挂在 `controller` 对象下，而不破坏顶层字段布局。

## 8.3 统计字段

- `slot_stats.count`
  - 节点出现在多少个 slot 的 `CurrentPeers` 中
- `slot_stats.leader_count`
  - 节点在多少个 slot runtime view 中是 `LeaderID`
- `is_local`
  - 是否为当前节点
- `total`
  - 当前返回的节点总数；第一版虽然不做分页，但先固定外层计数字段，便于后续扩展分页和筛选参数

## 8.4 Task DTO 设计

task 列表与详情第一版返回如下核心字段：

- `slot_id`
- `kind`
- `step`
- `status`
- `source_node`
- `target_node`
- `attempt`
- `next_run_at`
- `last_error`

字段说明如下：

- `kind`
  - 任务类型，字符串化输出，建议值：
    - `bootstrap`
    - `repair`
    - `rebalance`
- `step`
  - 当前任务步骤，字符串化输出，建议值：
    - `add_learner`
    - `catch_up`
    - `promote`
    - `transfer_leader`
    - `remove_old`
- `status`
  - 任务状态，字符串化输出，建议值：
    - `pending`
    - `retrying`
    - `failed`
- `source_node`
  - 源节点 ID；对 `bootstrap` 等没有源节点语义的任务允许返回 `0`
- `target_node`
  - 目标节点 ID；若当前步骤还未绑定目标节点则允许返回 `0`
- `attempt`
  - 当前任务尝试次数
- `next_run_at`
  - 下次重试时间；仅 `retrying` 状态有值，其余状态返回 `null`
- `last_error`
  - 最近一次任务错误；没有错误时返回空字符串

`GET /manager/tasks/:slot_id` 在这些 task 核心字段之外，再补一份轻量 slot 上下文：

- `slot.state`
- `slot.assignment`
- `slot.runtime`

这样后台管理系统点击单个 task 时，可以同时看到它关联的 slot 期望副本与当前 runtime 观测。

## 8.5 Slot 详情 DTO 设计

`GET /manager/slots/:slot_id` 沿用 slot 列表里的主体字段：

- `slot_id`
- `state`
- `assignment`
- `runtime`

并补一个可空的 `task` 摘要对象：

- `task.kind`
- `task.step`
- `task.status`
- `task.source_node`
- `task.target_node`
- `task.attempt`
- `task.next_run_at`
- `task.last_error`

设计约束：

- `task` 只表示“当前 slot 是否存在正在跟踪的 reconcile task”
- 若 task 不存在，`task = null`
- slot 本身存在但 task 不存在时，接口仍返回 `200`

## 8.6 Channel Runtime Meta DTO 设计

`GET /manager/channel-runtime-meta` 第一版返回如下核心字段：

- `channel_id`
- `channel_type`
- `slot_id`
- `channel_epoch`
- `leader_epoch`
- `leader`
- `replicas`
- `isr`
- `min_isr`
- `status`

设计约束：

- `slot_id` 表示该 channel 当前归属的 physical slot
- `status` 必须输出稳定字符串，而不是直接暴露底层数值
- 第一版不返回 `features`、`lease_until_ms`

`status` 建议值：

- `creating`
- `active`
- `deleting`
- `deleted`
- `unknown`

## 8.7 Channel Runtime Meta 详情 DTO 设计

`GET /manager/channel-runtime-meta/:channel_type/:channel_id` 在列表 DTO 基础上增加以下字段：

- `hash_slot`
  - 当前频道 ID 映射到的逻辑 hash slot
- `features`
  - 当前 runtime meta 中的 feature bitset，先按数值直接透出
- `lease_until_ms`
  - 当前 leader lease 到期时间，毫秒时间戳

detail DTO 仍保持“稳定 manager 字段”原则：

- 不直接暴露底层内部结构体名
- 不新增 replica 逐节点状态明细
- 不把 detail 接口做成 list 响应外壳，而是直接返回单对象

## 9. 节点、Slot、Task 与 Channel Runtime Meta 数据聚合设计

## 9.1 数据来源

`internal/usecase/management` 的节点聚合查询需要以下输入：

- `cluster.ListNodesStrict(ctx)`
- `cluster.ListObservedRuntimeViewsStrict(ctx)`
- 当前本地节点 ID
- controller peer 列表
- controller leader ID

## 9.2 controller 角色推导

controller 角色按以下规则计算：

- 若节点 ID 等于 controller leader ID，则 `controller_role = leader`
- 否则若节点 ID 在 controller peer 列表中，则 `controller_role = follower`
- 否则为 `none`

第一版 controller peer 列表可直接基于 `cfg.Cluster.DerivedControllerNodes()` 注入 usecase，而不额外扩展更多 cluster 读接口。

## 9.3 slot 统计推导

对 `cluster.ListObservedRuntimeViewsStrict(ctx)` 返回的每个 view：

- 对 `CurrentPeers` 中的每个 peer，`slot_count++`
- 若 `LeaderID != 0`，则对应节点 `leader_slot_count++`

若某节点存在于 `ListNodesStrict()` 中但未出现在任何 runtime view 中，则其 `slot_count` 和 `leader_slot_count` 为 0。

## 9.4 排序规则

节点列表按 `node_id` 升序返回，保证后台管理系统能得到稳定展示顺序。

## 9.5 Slot 列表数据来源

`internal/usecase/management` 的 slot 聚合查询需要以下输入：

- `cluster.ListSlotAssignmentsStrict(ctx)`
- `cluster.ListObservedRuntimeViewsStrict(ctx)`

两类数据按 `slot_id` 聚合：

- assignment 侧提供期望副本与配置 epoch
- runtime 侧提供当前副本、leader、quorum 与观测时间

## 9.6 Slot 状态字段设计

第一版为 slot 列表补两个轻量派生状态，方便后台直接渲染列表：

- `state.quorum`
  - `ready`：存在 runtime view 且 `has_quorum=true`
  - `lost`：存在 runtime view 且 `has_quorum=false`
  - `unknown`：不存在 runtime view
- `state.sync`
  - `matched`：`desired_peers` 与 `current_peers` 一致，且 `config_epoch == observed_config_epoch`
  - `peer_mismatch`：期望副本与当前副本不一致
  - `epoch_lag`：副本一致，但 `observed_config_epoch` 落后于 `config_epoch`
  - `unreported`：不存在 runtime view

## 9.7 Slot 详情数据来源

`internal/usecase/management` 的 slot 详情查询需要以下输入：

- `cluster.ListSlotAssignmentsStrict(ctx)`
- `cluster.ListObservedRuntimeViewsStrict(ctx)`
- `cluster.GetReconcileTaskStrict(ctx, slotID)`

聚合规则：

- assignment 与 runtime 继续按 `slot_id` 聚合生成 slot 主体字段
- 若 `GetReconcileTaskStrict` 返回 task，则将其转换成 `task` 摘要对象
- 若 `GetReconcileTaskStrict` 返回 `ErrNotFound`，则视为“slot 存在但当前没有 reconcile task”，返回 `task=null`
- 若 assignment 与 runtime 都查不到该 `slot_id`，则 slot detail 返回 `404`

第一版 slot detail 不为了拿 task 摘要而额外新增 cluster strict read 边界，直接复用已有的 strict slot/task 读接口。

## 9.8 Task 列表数据来源

`internal/usecase/management` 的 task 列表查询需要以下输入：

- `cluster.ListTasksStrict(ctx)`

task 列表直接基于 controller leader 的 task snapshot 输出，并按 `slot_id` 升序返回。

## 9.9 Task 详情数据来源

`internal/usecase/management` 的 task 详情查询需要以下输入：

- `cluster.GetReconcileTaskStrict(ctx, slotID)`
- `cluster.ListSlotAssignmentsStrict(ctx)`
- `cluster.ListObservedRuntimeViewsStrict(ctx)`

聚合规则：

- `GetReconcileTaskStrict` 提供单个 task 的核心字段
- assignment 侧提供该 slot 的期望副本与配置 epoch
- runtime 侧提供该 slot 的当前副本、leader、quorum 与观测时间

这样 manager task 详情接口可以在一个响应里同时表达“当前 controller 正在做什么”与“这个 slot 现在是什么状态”。

## 9.10 Task 字段字符串化规则

manager task DTO 不直接暴露 `pkg/controller/meta` 的枚举值，而是统一转换为稳定字符串：

- `kind`
  - `bootstrap`
  - `repair`
  - `rebalance`
- `step`
  - `add_learner`
  - `catch_up`
  - `promote`
  - `transfer_leader`
  - `remove_old`
- `status`
  - `pending`
  - `retrying`
  - `failed`

## 9.11 Channel Runtime Meta 列表数据来源

`internal/usecase/management` 的 channel runtime meta 列表查询需要以下输入：

- `slot/proxy.Store` 提供的权威分页读取能力
- `cluster.SlotIDs()`
- `cluster.HashSlotsOf(slotID)`

权威数据源规则：

- `channel runtime meta` 不以 controller leader 为真相源
- 每个 physical slot 的分页读取都必须走对应 slot leader 的权威读路径
- 从任意节点访问同一个分页请求时，应路由到同一组 slot leader 数据源
- 若当前页需要访问的任一 slot leader 不可用，则整个请求失败，不返回部分结果

## 9.12 Channel Runtime Meta 分页与游标规则

分页顺序固定为：

- `slot_id ASC`
- `channel_id ASC`
- `channel_type ASC`

实现约束：

- manager 层的全局分页按 physical slot 递进，不允许先全量拉取所有 channel 再截页
- 每个 physical slot 内部按其当前拥有的 hash slot 做增量分页扫描与 k-way merge
- slot 内部排序为 `channel_id ASC`、`channel_type ASC`
- 当前 slot 扫描完成且页还未满时，才继续下一个 `slot_id`

cursor 规则：

- 对外保持 opaque
- 当前版本内部编码字段为：
  - `v`
  - `slot_id`
  - `channel_id`
  - `channel_type`
- 语义为“下一页从该全局排序位置之后开始”

分页一致性边界：

- 该接口保证“每条记录来自对应 slot leader 的权威视图”
- 该接口不保证“跨所有 slot 的全局事务快照”
- 因此分页过程中若 channel runtime 正在变化，不同页之间允许看到前后变化

## 9.13 Channel Runtime Meta 详情数据来源

`internal/usecase/management` 的 channel runtime meta 详情查询需要以下输入：

- `cluster.SlotForKey(channelID)`
- `cluster.HashSlotForKey(channelID)`
- `slot/proxy.Store.GetChannelRuntimeMeta(ctx, channelID, channelType)`

职责划分如下：

- `cluster` 只负责 key 路由，不承接 channel runtime meta 业务读取
- `slot/proxy.Store` 负责按频道路由到对应 slot leader 做单条权威读
- `management` usecase 负责补齐 `slot_id`、`hash_slot` 和稳定 DTO 字段映射

实现约束：

- 不新增 `pkg/cluster.API` 的 channel runtime meta detail 读取能力
- 不复用 list 的 scan / k-way merge 链路来查找单条记录
- 详情接口命中不存在记录时返回 `ErrNotFound`
- 详情接口命中 slot leader 不可用时直接失败，不返回局部降级结果

## 10. Cluster / Slot API 边界调整

为避免 `management` 用例依赖具体 `*cluster.Cluster` 实现，建议给 `pkg/cluster.API` 新增一个很小的只读接口：

- `ControllerLeaderID() uint64`

原因：

- 第一版 manager 用例需要稳定拿到 controller leader
- 现有 `API` 已暴露多个只读运维接口，新增一个 leader 只读查询符合当前接口演进方向
- 可避免在 usecase 层做具体实现类型断言

controller peer 列表则不需要进 `pkg/cluster.API`，第一版直接从 `app.Config` 注入即可，减少改动面。

此外需要为 manager 聚合查询增加一组显式的“严格一致读”边界：

- `ListNodesStrict(ctx)`
- `ListSlotAssignmentsStrict(ctx)`
- `ListObservedRuntimeViewsStrict(ctx)`
- `ListTasksStrict(ctx)`
- `GetReconcileTaskStrict(ctx, slotID)`

这些接口的语义是：

- 必须通过 controller leader 返回统一结果
- 不允许 fallback 到本地 `controllerMeta`
- leader 不可达、无 leader、读超时或重定向失败时直接返回错误
- `GetReconcileTaskStrict` 仍然保留 `ErrNotFound` 语义，用于 manager detail 接口返回 `404`

manager usecase 只使用这组 strict read 接口，不再复用“允许本地降级”的通用运维读语义。

其中 `GET /manager/slots/:slot_id` 直接复用：

- `ListSlotAssignmentsStrict(ctx)`
- `ListObservedRuntimeViewsStrict(ctx)`
- `GetReconcileTaskStrict(ctx, slotID)`

不再额外新增新的 cluster strict read 方法。

`GET /manager/channel-runtime-meta` 不应把 `ListChannelRuntimeMeta()` 直接塞进 `pkg/cluster.API`，原因是：

- `channel runtime meta` 属于 slot 分布式元数据，而不是 controller 控制面元数据
- 其权威读路径已经存在于 `pkg/slot/proxy.Store`
- 若把整套 channel runtime meta 读能力塞进 `pkg/cluster.API`，会扩大 `cluster` 对 channel 业务元数据的边界耦合

因此当前阶段采用两条依赖边界：

- `internal/usecase/management` 继续依赖 `cluster` 提供 nodes / slots / tasks 的 strict read
- `internal/usecase/management` 额外依赖一个 `ChannelRuntimeMetaReader`

为了支持 slot 维度的权威分页扫描，建议仅给 `pkg/cluster.API` 增加一个很小的只读方法：

- `HashSlotsOf(slotID multiraft.SlotID) []uint16`

并在 slot 元数据层增加两级分页能力：

- `pkg/slot/meta`
  - `ShardStore.ListChannelRuntimeMetaPage(ctx, after, limit)`
- `pkg/slot/proxy`
  - `Store.ScanChannelRuntimeMetaSlotPage(ctx, slotID, after, limit)`

其中 `pkg/slot/proxy` 负责：

- 路由到对应 slot leader
- 在 slot leader 本地对该 physical slot 的多个 hash slot 做分页 k-way merge
- 对 manager 层输出“单个 physical slot 的下一页”

## 11. `internal/app` 装配设计

## 11.1 App 结构

建议在 `internal/app.App` 中新增：

- manager server 字段
- manager start/stop 生命周期钩子
- 对应运行状态标记

manager 服务与现有业务 API 服务并列存在，互不替代。

## 11.2 Build 装配

在 `internal/app/build.go` 中：

1. 构建 management usecase
2. 若 `cfg.Manager.ListenAddr != ""`，则构建 manager access server
3. 注入：
   - management usecase
   - channel runtime meta reader（复用 `app.store`）
   - manager 鉴权配置
   - logger

## 11.3 生命周期

在 `internal/app/lifecycle.go` 中：

- `Start()` 需要独立启动 manager 服务
- `Stop()` 需要独立关闭 manager 服务
- manager 服务失败不应影响业务 API 的边界定义，但在当前应用启动流程中，只要某个入口显式配置开启且启动失败，应整体启动失败

这与当前 API/gateway 的装配语义一致。

## 12. 错误处理策略

当前阶段保持简单明确，不做部分成功降级：

- 登录失败返回 `401`
- 参数非法返回 `400`
- `channel-runtime-meta` 的 `limit/cursor` 非法返回 `400`
- token 缺失或非法返回 `401`
- 权限不足返回 `403`
- 指定 task 不存在返回 `404`
- controller leader 一致读不可用返回 `503`
- 当前页所需 slot leader 权威读不可用返回 `503`
- 其他 cluster 查询失败返回 `500`

节点列表、slot 列表、slot 详情、task 列表、task 详情与 `channel runtime meta` 分页接口在底层数据源查询失败时整体失败，不返回“部分成功”的响应，避免把零值或本地滞后快照误导为真实状态。

## 13. 测试设计

## 13.1 `internal/access/manager`

至少覆盖：

- `POST /manager/login` 成功
- `POST /manager/login` 用户名错误
- `POST /manager/login` 密码错误
- `GET /manager/nodes` 未带 token
- `GET /manager/nodes` token 非法
- `GET /manager/nodes` token 过期
- `GET /manager/nodes` 权限不足
- `GET /manager/nodes` 成功返回节点列表
- `GET /manager/nodes` controller leader 一致读不可用
- `GET /manager/slots` 未带 token
- `GET /manager/slots` 权限不足
- `GET /manager/slots` 成功返回 slot 列表
- `GET /manager/slots` controller leader 一致读不可用
- `GET /manager/slots/:slot_id` 未带 token
- `GET /manager/slots/:slot_id` `slot_id` 非法
- `GET /manager/slots/:slot_id` 权限不足
- `GET /manager/slots/:slot_id` 成功返回 slot 详情
- `GET /manager/slots/:slot_id` slot 存在但 task 为空
- `GET /manager/slots/:slot_id` slot 不存在
- `GET /manager/slots/:slot_id` controller leader 一致读不可用
- `GET /manager/tasks` 未带 token
- `GET /manager/tasks` 权限不足
- `GET /manager/tasks` 成功返回 task 列表
- `GET /manager/tasks` controller leader 一致读不可用
- `GET /manager/tasks/:slot_id` 未带 token
- `GET /manager/tasks/:slot_id` `slot_id` 非法
- `GET /manager/tasks/:slot_id` 权限不足
- `GET /manager/tasks/:slot_id` 成功返回 task 详情
- `GET /manager/tasks/:slot_id` task 不存在
- `GET /manager/tasks/:slot_id` controller leader 一致读不可用
- `GET /manager/channel-runtime-meta` 未带 token
- `GET /manager/channel-runtime-meta` 权限不足
- `GET /manager/channel-runtime-meta` `limit` 非法
- `GET /manager/channel-runtime-meta` `cursor` 非法
- `GET /manager/channel-runtime-meta` 成功返回第一页
- `GET /manager/channel-runtime-meta` 使用 `next_cursor` 成功返回第二页
- `GET /manager/channel-runtime-meta` slot leader 权威读不可用
- `GET /manager/channel-runtime-meta/:channel_type/:channel_id` 未带 token
- `GET /manager/channel-runtime-meta/:channel_type/:channel_id` 权限不足
- `GET /manager/channel-runtime-meta/:channel_type/:channel_id` `channel_type` 非法
- `GET /manager/channel-runtime-meta/:channel_type/:channel_id` 记录不存在
- `GET /manager/channel-runtime-meta/:channel_type/:channel_id` 成功返回详情对象
- `GET /manager/channel-runtime-meta/:channel_type/:channel_id` slot leader 权威读不可用

## 13.2 `internal/usecase/management`

至少覆盖：

- controller role 计算正确
- `slot_count` 统计正确
- `leader_slot_count` 统计正确
- 结果按 `node_id` 排序
- 无 runtime view 时统计为 0
- task 列表按 `slot_id` 排序
- task `kind/step/status` 枚举字符串映射正确
- `retrying` 任务的 `next_run_at` 映射正确，其他状态返回 `nil`
- task 详情能聚合 slot assignment/runtime 上下文
- task 不存在时正确透传 `ErrNotFound`
- slot 详情能聚合 assignment/runtime 与可空 task 摘要
- slot 不存在时正确返回 `ErrNotFound`
- channel runtime meta 全局分页按 `slot_id/channel_id/channel_type` 顺序输出
- channel runtime meta 使用 `next_cursor` 能正确续扫
- channel runtime meta `status` 数值到稳定字符串映射正确
- channel runtime meta 详情能正确补齐 `slot_id/hash_slot`
- channel runtime meta 详情正确透传 `features/lease_until_ms`
- channel runtime meta 详情不存在时正确透传 `ErrNotFound`
- channel runtime meta 详情权威读错误正确透传

## 13.3 `pkg/slot/meta`

至少覆盖：

- 单个 hash slot 内 channel runtime meta 分页顺序正确
- `after` cursor 能正确续扫
- `limit` 边界行为正确

## 13.4 `pkg/slot/proxy`

至少覆盖：

- 单个 physical slot 下多个 hash slot 的分页 k-way merge 顺序正确
- slot leader 权威分页能连续取到下一页
- slot leader 不可用时返回错误，不 fallback 到本地

## 13.5 `internal/app`

至少覆盖：

- 配置 manager listen addr 时构建 manager server
- manager 生命周期纳入 `App.Start()` / `App.Stop()`
- manager 关闭时不会影响现有 API/gateway 构建
- manager usecase 注入 channel runtime meta reader

## 13.6 `cmd/wukongim`

至少覆盖：

- `WK_MANAGER_*` 配置解析
- manager 开启时的配置校验
- `WK_MANAGER_USERS` JSON 解析

## 14. 风险与后续扩展

## 14.1 当前风险

- 静态用户密码直接放配置，适合第一版框架验证，但不适合作为长期企业认证方案
- JWT 不做吊销与黑名单，权限收紧只能依赖 token 到期或服务端实时按用户名重新判权
- 节点列表中的 slot 统计与 slot 列表中的 runtime 信息依赖 controller leader 的 observation snapshot，展示会随 leader observation 延迟而变化
- task 详情中的 slot runtime 上下文同样依赖 controller leader 的 observation snapshot，展示会随 leader observation 延迟而变化
- manager cluster 接口不再为了可用性退回本地视图；当 leader 一致读不可用时，接口直接失败
- `channel runtime meta` 分页保证的是“每条记录来自对应 slot leader 的权威视图”，但不保证跨所有 slot 的全局事务快照；分页过程中数据可能前后变化

## 14.2 扩展路径

在此骨架上，后续可以按同一模式继续扩展：

- `GET /manager/overview`
- `GET /manager/channel-runtime-meta` 的筛选条件（如 `slot_id`、`channel_type`）
- `GET /manager/channel-runtime-meta` 详情的 replica 明细视图
- 写操作接口与 `w` 权限

这些后续接口仍应遵循：

- access 层只负责协议与鉴权
- usecase 层负责聚合查询或管理编排
- app 层统一装配

## 15. 决策摘要

本次设计最终确定以下关键决策：

1. manager API 使用独立端口与独立 server，不复用当前业务 API
2. 先补齐 `nodes / slots / tasks` 三类 manager 只读接口，并在此基础上增加分页的 `GET /manager/channel-runtime-meta`
3. 鉴权采用“静态用户 + 登录签发 JWT + 服务端按用户名实时判权”
4. 新增 `internal/usecase/management`，避免把聚合逻辑堆进 access 层
5. 控制面类 manager 读接口统一走 controller leader 一致读，不允许 fallback 到本地滞后视图
6. `channel runtime meta` 列表统一走 slot leader 权威分页读，不允许全量聚合后再截页
7. `channel runtime meta` 详情采用 `GET /manager/channel-runtime-meta/:channel_type/:channel_id`，继续走 slot leader 单条权威读
8. 节点列表、slot 列表、slot 详情、task 列表/详情与 channel runtime meta 列表/详情都返回稳定 DTO，字段覆盖后台首屏与调度排障所需的基础信息
9. `pkg/cluster.API` 增加最小只读能力 `ControllerLeaderID() uint64`、strict read 接口边界与 `HashSlotsOf(slotID)`；channel runtime meta 具体读能力继续留在 `pkg/slot/proxy`

这套方案优先解决“管理面入口框架缺失”的问题，并为后续分布式数据管理接口扩展提供稳定基础。

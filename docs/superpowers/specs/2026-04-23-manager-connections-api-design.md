# Manager Connections API 设计

- 日期：2026-04-23
- 范围：为 `internal/access/manager/routes.go` 增加 Connections 读接口，并将 `web/` 中的 `Connections` 页面从“未暴露 API”占位态升级为真实的列表 + 详情页
- 关联目录：
  - `internal/access/manager/`
  - `internal/usecase/management/`
  - `internal/runtime/online/`
  - `internal/gateway/session/`
  - `internal/app/`
  - `web/src/lib/`
  - `web/src/pages/connections/`
  - `web/src/pages/page-shells.test.tsx`

## 1. 背景

当前 manager 已经提供了 `overview`、`nodes`、`slots`、`tasks`、`channel-runtime-meta` 等读写接口，前端 `Dashboard`、`Nodes`、`Slots`、`Channels` 也已经完成对接。

但 `Connections` 页面仍然是显式的占位说明，原因不是前端未实现，而是 manager 后端尚未暴露任何连接清单接口。

与此同时，仓库内已经存在可以支撑“本地连接视图”的运行时数据：

- `internal/runtime/online.Registry` 保存本地在线连接注册表
- `internal/runtime/online.OnlineConn` 已包含：
  - `session_id`
  - `uid`
  - `device_id`
  - `device_flag`
  - `device_level`
  - `slot_id`
  - `listener`
  - `state`
  - `connected_at`
- `internal/gateway/session.Session` 已暴露：
  - `RemoteAddr()`
  - `LocalAddr()`

因此本次设计不需要先发明新的连接运行时，只需要把现有本地在线连接信息通过 management usecase 和 manager access 层整理成稳定 DTO。

## 2. 目标与非目标

### 2.1 目标

本次设计目标如下：

1. 在 manager 后端新增 Connections 只读接口：
   - `GET /manager/connections`
   - `GET /manager/connections/:session_id`
2. 前端 `Connections` 页改为真实的列表 + 详情交互
3. 保持“前端字段以后端 API 接口为准”，但允许前端内部保留 camelCase DTO 映射风格
4. 沿用当前 manager 页面已有的加载、空态、错误态、详情 sheet 交互模式
5. 补齐后端 handler 测试、usecase 测试、前端页面测试

### 2.2 非目标

本次明确不做以下事情：

1. 不实现连接写操作，例如强制断开、踢线、迁移
2. 不实现跨节点全局连接聚合
3. 不新增独立的 Connections 控制面服务对象
4. 不为了前端字段去改造现有网关/在线运行时内部存储模型

## 3. 方案对比

### 方案 A：本地连接列表 + 本地连接详情（推荐）

基于当前节点的 `online.Registry` 暴露本地在线连接清单和单连接详情。

优点：

- 与当前运行时边界最一致
- 改动集中且最小
- 能快速把前端 `Connections` 页面变成真实可用页面
- 后续若要做全局聚合，可以在外层继续扩展，而不是推翻现有 UI

缺点：

- 页面展示的是“当前节点连接”，不是整个集群的全局连接

### 方案 B：聚合视图 + 本地详情

列表先返回按节点聚合的连接总览，再通过二级查询看本地详情。

优点：

- 更接近 manager 全局视角

缺点：

- 需要引入新的跨节点收集语义
- 远超当前 `Connections` 页面“列表 + 详情”的实现范围

### 方案 C：仅支持条件查询，不提供完整列表

只允许按 `uid` / `session_id` 查询，不开放完整连接表。

优点：

- 返回量更可控

缺点：

- 不能满足当前页面目标
- 会把 `Connections` 页变成查询工具，而不是运行时列表页

### 结论

选择方案 A。当前 manager 最合理的语义是“本地连接视图”，与现有 `runtime/online` 的数据边界完全一致。

## 4. 后端设计

### 4.1 路由

在 `internal/access/manager/routes.go` 增加一个新的读资源组：

- 权限资源名：`cluster.connection`
- 读动作：`r`

路由如下：

- `GET /manager/connections`
- `GET /manager/connections/:session_id`

不新增写接口。

### 4.2 Management 接口

在 `internal/access/manager/server.go` 的 `Management` 接口中新增：

- `ListConnections(ctx context.Context) ([]managementusecase.Connection, error)`
- `GetConnection(ctx context.Context, sessionID uint64) (managementusecase.ConnectionDetail, error)`

这样 access 层继续只做 HTTP 适配，不直接依赖 `online.Registry` 的内部实现。

### 4.3 usecase DTO

在 `internal/usecase/management/` 中新增 manager-facing DTO：

- `Connection`
- `ConnectionDetail`

列表字段：

- `session_id`
- `uid`
- `device_id`
- `device_flag`
- `device_level`
- `slot_id`
- `state`
- `listener`
- `connected_at`
- `remote_addr`
- `local_addr`

详情字段先与列表一致。只有在不引入额外运行时耦合的情况下，才附加稳定元数据。没有可靠来源的字段不返回。

### 4.4 数据来源

usecase 从 `online.Registry` 拉取本地在线连接，再从每个连接内的 `Session` 提取：

- `RemoteAddr()`
- `LocalAddr()`

排序策略：

- 列表默认按 `connected_at` 倒序
- 同时间戳时按 `session_id` 升序作为稳定 tie-breaker

### 4.5 错误语义

- `ListConnections`
  - 运行时未配置：`503 service_unavailable`
- `GetConnection`
  - `session_id` 非法：`400 bad_request`
  - 连接不存在：`404 not_found`
  - 运行时未配置：`503 service_unavailable`

## 5. 前端设计

### 5.1 API client

在 `web/src/lib/manager-api.ts` 和 `web/src/lib/manager-api.types.ts` 中新增：

- `getConnections()`
- `getConnection(sessionId)`

前端展示字段以后端响应为准；如有页面内部需要的命名转换，维持 camelCase 包装方式，但不能反推后端改字段。

### 5.2 页面行为

`web/src/pages/connections/page.tsx` 改造成与 `Nodes` / `Channels` 一致的模式：

- 顶部刷新按钮
- 表格展示连接列表
- 点击 `Inspect` 打开右侧详情 sheet
- 列表失败时显示 `ResourceState`
- 空列表时显示 `empty`

表格列建议为：

- Session ID
- UID
- Device ID
- Device
- Listener
- State
- Connected At
- Actions

详情 sheet 展示：

- UID
- Session ID
- Device ID
- Device flag
- Device level
- Slot ID
- Listener
- State
- Connected At
- Remote address
- Local address

## 6. 测试策略

### 6.1 后端

新增测试覆盖：

- manager handler 列表成功
- manager handler 详情成功
- `session_id` 非法返回 `400`
- 详情不存在返回 `404`
- runtime 未配置返回 `503`

新增 usecase 测试覆盖：

- 列表从 `online.Registry` 正确映射 DTO
- 排序稳定
- 详情读取正确

### 6.2 前端

新增 `web/src/pages/connections/page.test.tsx`，至少覆盖：

- 列表成功渲染
- 打开详情
- 刷新
- `503` 不可用态
- 空列表态

同时更新 `web/src/pages/page-shells.test.tsx`，把 `Connections` 从“未暴露 API”占位预期，调整为真实页面预期。

## 7. 分层约束

- `internal/access/manager/*` 只做 HTTP 适配和错误码映射
- `internal/usecase/management/*` 负责把 online runtime 整理成 manager-facing DTO
- `internal/runtime/online/*` 不为 manager 特地引入反向耦合
- `internal/app/*` 负责把 online runtime 注入 management usecase

## 8. 验收标准

完成后应满足：

1. `GET /manager/connections` 与 `GET /manager/connections/:session_id` 可用
2. `Connections` 页面不再显示“未暴露 API”占位
3. 页面能展示真实连接列表并打开详情
4. 后端与前端对应测试通过
5. 不修改现有节点、槽位、频道等 manager API 契约

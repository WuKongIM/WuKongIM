# Web Manager API Integration 设计

- 日期：2026-04-22
- 范围：将 `web/` 现有管理台页面对接到 `internal/access/manager/routes.go` 已提供的管理接口，覆盖可读页面数据、节点与槽位管理动作，以及与之配套的前端状态、权限反馈和测试
- 关联目录：
  - `web/src/lib/manager-api.ts`
  - `web/src/auth/`
  - `web/src/app/router.tsx`
  - `web/src/lib/navigation.ts`
  - `web/src/pages/dashboard/`
  - `web/src/pages/nodes/`
  - `web/src/pages/slots/`
  - `web/src/pages/channels/`
  - `web/src/pages/connections/`
  - `web/src/pages/network/`
  - `web/src/pages/topology/`
  - `web/src/components/`
  - `web/src/test/`
  - `internal/access/manager/routes.go`
  - `internal/access/manager/overview.go`
  - `internal/access/manager/nodes.go`
  - `internal/access/manager/node_detail.go`
  - `internal/access/manager/node_operator.go`
  - `internal/access/manager/slots.go`
  - `internal/access/manager/slot_operator.go`
  - `internal/access/manager/tasks.go`
  - `internal/access/manager/channel_runtime_meta.go`
  - `docs/superpowers/specs/2026-04-21-manager-api-foundation-design.md`
  - `docs/superpowers/specs/2026-04-21-manager-overview-design.md`
  - `docs/superpowers/specs/2026-04-22-manager-node-operator-actions-design.md`
  - `docs/superpowers/specs/2026-04-22-manager-slot-leader-transfer-design.md`
  - `docs/superpowers/specs/2026-04-22-manager-slot-recover-rebalance-design.md`
  - `docs/superpowers/specs/2026-04-22-web-admin-shell-design.md`
  - `docs/superpowers/specs/2026-04-22-web-manager-login-auth-design.md`

## 1. 背景

当前 `web/` 已具备以下基础能力：

- 统一的 `AppShell + SidebarNav + Topbar` 管理台骨架
- 登录页、JWT 登录态保存、路由保护与 `POST /manager/login` 对接
- `dashboard`、`nodes`、`channels`、`slots` 等页面骨架
- Vitest + Testing Library 单元测试基线

但除登录外，其余页面仍以占位内容为主。与此同时，manager 后端已经在 `internal/access/manager/routes.go` 中提供了一组真实管理接口：

- `GET /manager/overview`
- `GET /manager/nodes`
- `GET /manager/nodes/:node_id`
- `POST /manager/nodes/:node_id/draining`
- `POST /manager/nodes/:node_id/resume`
- `GET /manager/slots`
- `GET /manager/slots/:slot_id`
- `POST /manager/slots/:slot_id/leader/transfer`
- `POST /manager/slots/:slot_id/recover`
- `POST /manager/slots/rebalance`
- `GET /manager/tasks`
- `GET /manager/tasks/:slot_id`
- `GET /manager/channel-runtime-meta`
- `GET /manager/channel-runtime-meta/:channel_type/:channel_id`

因此本轮的重点不是新增后端接口，而是把现有管理台壳子变成一个“以后端 manager API 为准”的真实操作台。

## 2. 目标与非目标

### 2.1 目标

本次设计目标如下：

1. 保留当前前端导航结构，不额外新增 `Tasks` 独立导航页
2. 让 `Dashboard` 对接 `overview` 与 `tasks` 总览数据
3. 让 `Nodes` 对接节点列表、节点详情、`draining`、`resume`
4. 让 `Slots` 对接槽位列表、槽位详情、leader transfer、recover、rebalance
5. 让 `Channels` 对接 `channel-runtime-meta` 列表与详情
6. 保留 `Connections`、`Network`、`Topology` 路由，但明确显示“当前无对应 manager API 接口”的占位状态
7. 为读请求、详情请求、写请求建立统一的加载、错误、权限不足、冲突反馈模式
8. 为关键行为补齐前端单元测试
9. 在对接过程中以前端展示内容和管理动作以后端现有 API 为准，不为了前端对接去反推后端接口改动

### 2.2 非目标

当前阶段明确不做以下内容：

- 不新增 manager 后端接口
- 不修改现有 manager 接口路径、权限资源名和响应语义，除非联调证明后端响应与现有测试/实现不一致
- 不为 `Connections`、`Network`、`Topology` 编造假数据或影射无关接口
- 不引入全局异步数据缓存框架，例如 `tanstack query`
- 不新增自动轮询、WebSocket 实时推送或复杂图表系统
- 不新增导航裁剪逻辑；即使权限不足，也保持当前导航结构稳定
- 不在本轮处理最终 `web/dist` 与 Go 进程静态托管的发布集成

## 3. 关键约束

### 3.1 前端展示以后端现有 API 为准，后端接口保持不动

这是本轮的硬约束。

这里的含义是：

- 前端页面表格、详情区、操作入口展示什么内容，以当前后端已经提供的 manager API 为准
- 本轮不为了“前端想显示某个字段”去反向修改 `internal/access/manager/*` 的接口路径、响应结构或权限模型
- 前端内部是否保留 camelCase DTO 映射是实现选择，不是本轮被禁止的事情

也就是说，前端需要围绕后端现有能力组织页面，例如直接消费这些接口已经给出的信息：

- `generated_at`
- `controller_leader_id`
- `node_id`
- `last_heartbeat_at`
- `leader_count`
- `desired_peers`
- `next_run_at`
- `next_cursor`
- `token_type`
- `access_token`
- `expires_at`

这样做的原因是：

- 当前 manager API 已经在 Go 侧通过结构体 tag 明确了 JSON 契约
- 用户要求“前端字段以后端 API 为准”的核心诉求，是保持后端接口不动，而不是让前端去驱动后端改接口
- 管理台的本质是运维界面，优先应把页面内容建立在现有后端能力之上
- 即使前端内部使用 DTO 映射，也必须由测试锁定它和后端契约之间的一致性

允许前端做的转换包括但不限于：

1. 展示层派生值，例如把时间字符串格式化成人类可读文本
2. 前端内部 DTO 映射，例如把接口响应整理成组件更易消费的 camelCase 结构
3. 表单提交体，例如 `target_node_id`、`strategy` 这类请求字段的输入收集

但这些转换不能反过来要求后端改接口，也不能让前端脱离现有 manager API 自行发明展示字段。

### 3.2 保持现有导航结构稳定

用户已明确要求保留现有导航，因此信息架构采用：

- `Dashboard`：真实对接 `overview` 与 `tasks`
- `Nodes`：真实对接节点列表、详情和动作
- `Channels`：真实对接 `channel-runtime-meta`
- `Slots`：真实对接槽位列表、详情和动作
- `Connections` / `Network` / `Topology`：保留占位，但状态文案明确说明当前无对应 manager API

这样既不强行篡改已存在的壳子，又能把有后端支撑的页面做实。

### 3.3 写操作采用标准控制台交互

用户要求写操作采用标准控制台交互，因此所有管理动作遵循同一模式：

1. 用户在列表行或详情头部点击动作按钮
2. 打开确认弹窗或带字段表单弹窗
3. 用户确认后执行请求
4. 请求中显示提交中状态并防止重复提交
5. 成功后关闭弹窗并刷新当前页面相关数据
6. 失败后保留上下文并展示明确错误

这套模式适用于：

- `node draining`
- `node resume`
- `slot leader transfer`
- `slot recover`
- `slot rebalance`

## 4. 页面与接口映射

## 4.1 Dashboard

`Dashboard` 对接以下接口：

- `GET /manager/overview`
- `GET /manager/tasks`

页面布局保持现有概览风格，但替换静态占位为真实数据：

- 顶部指标卡展示节点、槽位、任务等 headline 数字
- `Operations Summary` 展示 cluster、nodes、slots、tasks 的核心统计
- `Alert List` 展示 overview anomalies 中的异常样本
- `Control Queue` 展示任务列表摘要，作为任务总览入口

本轮不新增 `/tasks` 导航页。任务信息作为 `Dashboard` 的操作队列区块存在，必要时允许在该区块中提供“按 slot_id 查看详情”的跳转或抽屉入口。

## 4.2 Nodes

`Nodes` 对接以下接口：

- `GET /manager/nodes`
- `GET /manager/nodes/:node_id`
- `POST /manager/nodes/:node_id/draining`
- `POST /manager/nodes/:node_id/resume`

页面结构：

- 首屏加载节点列表
- 行内显示 `node_id`、`addr`、`status`、`last_heartbeat_at`、`controller.role`、`slot_stats.count`、`slot_stats.leader_count`
- 点击 `Inspect` 或行项目打开详情抽屉
- 详情抽屉展示原始节点信息与 `slots.hosted_ids`、`slots.leader_ids`

动作规则：

- `draining`：确认弹窗，成功后刷新列表和当前详情
- `resume`：确认弹窗，成功后刷新列表和当前详情
- 按钮可同时出现在列表行和详情头部，但调用同一动作逻辑

## 4.3 Slots

`Slots` 对接以下接口：

- `GET /manager/slots`
- `GET /manager/slots/:slot_id`
- `POST /manager/slots/:slot_id/leader/transfer`
- `POST /manager/slots/:slot_id/recover`
- `POST /manager/slots/rebalance`

页面结构：

- 首屏加载槽位列表
- 指标卡展示 `ready`、`quorum_lost`、`leader_missing`、`peer_mismatch` 等可从列表与 overview 推导出的状态摘要
- 主表格展示 `slot_id`、`state.quorum`、`state.sync`、`assignment.desired_peers`、`runtime.current_peers`、`runtime.leader_id`、`runtime.last_report_at`
- 点击 `Inspect` 或行项目打开详情抽屉
- 详情抽屉补充 `assignment.config_epoch`、`assignment.balance_version`、`runtime.observed_config_epoch`、`task` 等字段

动作规则：

- `leader transfer`：表单弹窗，输入 `target_node_id`
- `recover`：表单弹窗，输入 `strategy`
- `rebalance`：确认弹窗，无额外输入，成功后展示返回的 plan item 列表

其中 `rebalance` 返回的是计划结果，不是单个槽位详情，因此成功态需要展示一份结果面板或结果对话框，并在关闭后刷新槽位列表。

## 4.4 Channels

`Channels` 对接以下接口：

- `GET /manager/channel-runtime-meta`
- `GET /manager/channel-runtime-meta/:channel_type/:channel_id`

页面结构：

- 首屏请求第一页列表
- 主表格展示 `channel_id`、`channel_type`、`slot_id`、`leader`、`replicas`、`isr`、`min_isr`、`status`
- 底部分页使用后端 `has_more + next_cursor` 模式，不自行构造页码
- 点击 `Inspect` 或行项目打开详情抽屉
- 详情抽屉展示 `hash_slot`、`features`、`lease_until_ms`、`channel_epoch`、`leader_epoch`

这里不额外增加前端分页模型，只把后端 cursor 当作翻页令牌保存和传递。

## 4.5 Connections / Network / Topology

这三个页面在本轮不接不存在的接口，但要从“静态占位”升级为“明确未接入状态”：

- 标题说明当前页面尚未有对应 manager API
- 主卡片展示原因说明，而不是普通骨架块
- 不显示误导性的 `Status: static`、`Feed: placeholders only` 之类文案
- 若未来有后端接口，可以在不改路由结构的前提下接入

## 5. 前端数据层设计

## 5.1 `manager-api.ts` 作为唯一 HTTP 契约入口

所有 manager 请求统一收口到 `web/src/lib/manager-api.ts`。该文件负责：

- 构造 URL
- 注入认证头
- 发送请求
- 解析错误响应
- 暴露 typed API 函数

本轮预计至少新增或调整以下函数：

- `loginManager`
- `getOverview`
- `getNodes`
- `getNode`
- `markNodeDraining`
- `resumeNode`
- `getSlots`
- `getSlot`
- `transferSlotLeader`
- `recoverSlot`
- `rebalanceSlots`
- `getTasks`
- `getTask`
- `getChannelRuntimeMeta`
- `getChannelRuntimeMetaDetail`

如果类型数量明显增多，可以拆出 `web/src/lib/manager-api.types.ts`，但仍保持 manager API 契约集中定义。

## 5.2 页面级资源状态，不引入全局异步缓存层

本轮不引入 `tanstack query` 或统一异步 store，而采用“页面自己持有资源状态”的方式：

- 页面首屏请求自己的列表数据
- 详情抽屉按需请求详情数据
- 动作弹窗维护独立的提交中状态
- 动作成功后仅刷新当前页面必要资源

可复用的不是全局缓存，而是小型通用辅助能力，例如：

- `useAsyncResource`
- `useActionState`
- `ResourceState` 组件
- 统一错误提示组件

这样可以在不扩大状态复杂度的前提下，复用加载/失败/重试模式。

## 5.3 允许前端内部 DTO 映射，但接口契约仍以后端为准

本轮如果触及登录态、manager client 类型或页面资源类型，允许前端继续保留或新增 camelCase DTO 映射，但需要满足两个条件：

1. manager API 请求和响应的测试仍以后端真实接口字段为准
2. 前端内部映射只是消费层整理，不能成为推动后端改接口的理由

例如，前端可以把登录响应中的：

- `token_type`
- `access_token`
- `expires_at`
- `permissions`

映射成内部使用的 `tokenType`、`accessToken`、`expiresAt` 等字段，只要：

- `manager-api.test.ts` 仍锁定后端响应契约
- `auth-store` 和页面层不会因此要求改动后端 JSON 结构
- 页面最终展示的数据项和操作能力仍然来自后端已经提供的接口

## 6. 通用交互与组件拆分

## 6.1 共享组件

建议在 `web/src/components/manager/` 下抽取以下可复用组件：

- `resource-state.tsx`
  - 统一处理 loading / error / empty / forbidden / unavailable
- `detail-sheet.tsx`
  - 统一详情抽屉骨架
- `confirm-dialog.tsx`
  - 统一确认型操作弹窗
- `action-form-dialog.tsx`
  - 统一带表单字段的操作弹窗
- `status-badge.tsx`
  - 统一状态色与标签风格
- `key-value-list.tsx`
  - 统一详情区键值展示
- `table-toolbar.tsx`
  - 统一刷新、辅助操作入口

这些组件只服务 manager 页面，不污染更底层的 `components/ui/`。

## 6.2 列表到详情的交互

`Nodes`、`Slots`、`Channels` 一律采用“双层视图”：

- 列表页负责概览
- 详情抽屉负责深挖单个资源

这样可以：

- 控制首屏请求量
- 保留当前管理台的桌面端操作节奏
- 让写操作天然依附在具体资源详情上下文中

## 6.3 刷新策略

本轮刷新策略保持保守：

- 页面顶部保留手动 `Refresh`
- 列表和详情可独立刷新
- 写操作成功后自动刷新当前页面必要资源
- 不做自动轮询

这样可以避免在第一版中处理轮询与弹窗/抽屉状态同步的复杂竞态。

## 7. 错误、权限与状态反馈

## 7.1 状态码映射

前端对 manager 错误响应的处理应尽量保留后端语义：

- `400`：请求参数或 cursor 非法，展示表单或请求错误
- `401`：沿用现有逻辑，清空登录态并回到 `/login`
- `403`：显示权限不足，不隐藏页面导航
- `404`：资源不存在，详情抽屉显示 not found 状态
- `409`：操作冲突，例如 `manual recovery required` 或 `slot migrations already in progress`
- `503`：显示 controller leader 或 slot leader unavailable 的运维语义提示
- `500`：显示通用内部错误，并保留重试入口

## 7.2 权限反馈

当前不做导航裁剪，因此权限反馈规则如下：

- 页面进入后若列表请求返回 `403`，页面主体显示权限不足态
- 写按钮在无写权限时直接禁用，并显示所需权限说明
- 若用户通过其他入口仍触发动作且后端返回 `403`，弹窗内展示权限不足错误

## 7.3 空状态

需要区分三类空状态：

1. 接口未对接：仅用于 `Connections`、`Network`、`Topology`
2. 资源为空：例如真实返回 `items: []`
3. 权限不足或接口不可用：不可与空资源混淆

## 8. 测试策略

本轮实现必须遵循测试先行的原则，至少覆盖以下层次：

## 8.1 `manager-api` 单元测试

优先补和调整 `web/src/lib/manager-api.test.ts`，覆盖：

- 新增 endpoint 的 URL 与 method
- 请求头与 body 构造
- 列表与详情响应的原样字段契约
- `401` 自动登出
- `403` / `404` / `409` / `503` 的错误映射
- `channel-runtime-meta` cursor 传参与返回

测试断言同样以后端字段为准，不在测试里把响应改写成另一套前端命名。

## 8.2 页面测试

新增或调整页面测试，至少覆盖：

- `Dashboard` 成功渲染 overview 和 tasks 摘要
- `Nodes` 成功渲染列表并可打开详情抽屉
- `Nodes` 触发 `draining` / `resume` 后成功刷新
- `Slots` 成功渲染列表并可打开详情抽屉
- `Slots` 的 `leader transfer` / `recover` / `rebalance` 交互
- `Channels` 成功渲染列表、使用 `next_cursor` 翻页并打开详情
- `403`、`409`、`503` 等关键错误态展示
- 无对应接口的三个页面显示明确未接入状态

## 8.3 回归测试

需要确保现有登录与路由保护不回退，至少覆盖：

- 登录后仍能进入受保护页面
- `401` 仍能触发登出
- 新增页面请求不会破坏现有 `AppShell` 和 router 行为

## 9. 实施边界与风险

## 9.1 主要改动边界在 `web/`

本轮默认以后端已存在接口为准，主要改动边界放在前端：

- 页面结构
- manager API client
- 前端测试
- 少量导航和占位文案

除非联调或测试明确发现后端契约实现与声明不一致，否则不主动修改 Go 侧 handler。

## 9.2 最大风险是接口契约漂移

当前最大的工程风险不是视觉实现，而是契约漂移：

- 前端本地类型与后端 JSON tag 不一致
- 前端内部 DTO 映射与真实后端响应脱节
- 写操作返回结构与列表/详情刷新策略没有对齐

因此本设计把“页面展示以后端现有接口为准、后端接口不为前端倒推改动”作为首要约束，就是为了把这类风险压到最低。

## 10. 结果要求

完成本轮后，管理台应达到以下状态：

- 登录后可看到真实 `Dashboard`
- `Nodes`、`Slots`、`Channels` 页面都能读取真实数据
- 节点和槽位管理动作可以通过标准控制台交互执行
- `Connections`、`Network`、`Topology` 不再伪装成静态业务页，而是明确告知当前未接入接口
- 前端测试对主要读写路径和错误语义有基本覆盖
- 前端展示的数据来源与动作能力都建立在现有后端 API 之上，不要求改动后端接口

## 11. 下一步

这份 spec 经用户 review 确认后，下一步应编写 implementation plan，明确：

1. 先改哪些测试来锁定接口契约
2. 再按什么顺序接 `Dashboard`、`Nodes`、`Slots`、`Channels`
3. 如何在不破坏现有登录态的前提下，让前端内部 DTO 映射与后端现有契约保持清晰边界
4. 最后如何验证页面、交互与测试结果

在 implementation plan 落地之前，不开始具体实现代码。

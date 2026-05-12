# WuKongIM Web 后台管理重构方案

## 1. 背景

WuKongIM 是一个高性能分布式即时通讯引擎，采用自研的 ISR（In-Sync Replicas）机制实现频道级别的数据复制。当前 web 后台管理面板主要覆盖集群运维场景（节点、Slot、Controller Raft），缺少：

- **频道集群管理** — 每个频道本身就是一个 ISR 集群，需要独立的运维视图
- **业务管理** — 用户、频道 CRUD、消息审计等日常运营能力
- **系统设置** — 权限、Webhook、API 密钥等平台配置

## 2. 架构概述

### 2.1 集群两层模型

```
┌─────────────────────────────────────────────────┐
│              Controller Raft                      │
│         （全局元数据共识层）                        │
├─────────────────────────────────────────────────┤
│  Slot 0   │  Slot 1   │  Slot 2   │  Slot N    │
│  (Raft)   │  (Raft)   │  (Raft)   │  (Raft)    │
├───────────┼───────────┼───────────┼────────────┤
│  Ch A     │  Ch C     │  Ch E     │  Ch G      │
│  Ch B     │  Ch D     │  Ch F     │  Ch H      │
│  (ISR)    │  (ISR)    │  (ISR)    │  (ISR)     │
└───────────┴───────────┴───────────┴────────────┘
```

- **第一层：Slot 集群** — Controller Raft 管理全局 Slot 分片，每个 Slot 是一个 Raft 组
- **第二层：频道集群** — 每个频道独立的 ISR 副本组，有自己的 Leader/Follower、Epoch、Lease

### 2.2 频道 ISR 模型

每个频道的元数据（`channel.Meta`）包含：

| 字段 | 说明 |
|------|------|
| Key | 频道唯一标识 |
| Epoch | 频道纪元（元数据版本） |
| LeaderEpoch | Leader 纪元 |
| Leader | 当前 Leader 节点 ID |
| Replicas | 副本集（所有持有该频道数据的节点） |
| ISR | 同步副本集（与 Leader 保持同步的节点） |
| MinISR | 最小同步副本数（写入 Quorum 要求） |
| LeaseUntil | Leader 租约到期时间 |
| Status | 状态（Creating/Active/Deleting/Deleted） |
| Features | 特性位（消息序号格式等） |

## 3. 导航结构

```
📊 总览 (Overview)
  ├── 仪表盘          /dashboard
  └── 实时监控         /monitor

🖥️ 全局集群 (Global Cluster)
  ├── 节点管理         /nodes
  ├── Slot 管理        /slots
  ├── 节点扩容         /onboarding
  ├── Controller       /controller
  └── 拓扑视图         /topology

📡 频道集群 (Channel Cluster)
  ├── 集群总览         /channel-cluster
  ├── 频道列表         /channel-cluster/list
  └── 异常频道         /channel-cluster/unhealthy

👥 业务管理 (Business)
  ├── 用户管理         /users
  ├── 频道管理         /channels-biz
  ├── 消息管理         /messages
  └── 系统用户         /system-users

🔧 诊断工具 (Diagnostics)
  ├── 消息追踪         /diagnostics
  ├── 网络诊断         /network
  ├── 连接管理         /connections
  └── 槽位日志         /slot-logs

⚙️ 系统设置 (Settings)
  ├── 权限管理         /settings/permissions
  └── Webhook          /settings/webhooks
```

## 4. 模块详细设计

### 4.1 总览模块

#### 仪表盘 `/dashboard`（已有，增强）

在现有基础上增加：
- 在线用户数、消息 TPS 指标卡片
- 频道集群健康度摘要（健康/ISR 不足/无 Leader）— **已完成**
- 最近 24h 消息量趋势图

#### 实时监控 `/monitor`（新增）

- 消息发送/接收速率实时曲线
- WebSocket 连接数趋势
- 延迟分位数（P50/P95/P99）
- 节点负载热力图

### 4.2 全局集群模块（已有，保持）

| 页面 | 功能 |
|------|------|
| 节点管理 | 节点列表、排水/恢复、缩容流程 |
| Slot 管理 | Slot 分布、Rebalance、Leader 转移、恢复 |
| 节点扩容 | 新节点上线、Slot 分配计划 |
| Controller | Raft 日志查看、压缩操作 |
| 拓扑视图 | 集群节点关系可视化 |

### 4.3 频道集群模块（核心新增）

#### 集群总览 `/channel-cluster`

健康度面板：
- 频道总数 — **已完成**
- 健康频道数（ISR >= MinISR 且有 Leader）— **已完成**
- ISR 不足频道数 — **已完成**
- 无 Leader 频道数 — **已完成**
- 平均副本数 / 平均 ISR 数 — **已完成**
- 按节点的 Leader 分布 — **已完成**

#### 频道列表 `/channel-cluster/list`（迁移自原 /channels）

表格字段：
- Channel ID / Type
- 所属 Slot
- Leader 节点
- Replicas 列表
- ISR 列表
- MaxMessageSeq
- Status
- Channel Epoch / Leader Epoch

支持：
- 按 Channel ID 搜索
- 按节点筛选（某节点上的所有频道）
- 按状态筛选
- 点击进入详情（副本同步进度、Lease 信息）

#### 异常频道 `/channel-cluster/unhealthy`

自动筛选条件：
- ISR 数量 < MinISR
- Status != Active
- Leader == 0（无 Leader）

展示：
- 异常原因标签 — **已完成**
- 持续时间 — 待补充，需要 runtime 暴露异常起始时间
- 建议操作（查看副本/安全修复无 Leader/显式安全 Leader 转移）— **已完成**；批量 leader drain 待后续设计

#### 需要新增的后端 API

```
GET  /manager/channel-cluster/summary
     返回：total, healthy, isr_insufficient, no_leader, avg_replicas, avg_isr
     状态：已完成

GET  /manager/channel-cluster/unhealthy?limit=50&cursor=xxx
     返回：异常频道列表（含异常原因）
     状态：已完成

GET  /manager/channel-cluster/:type/:id/replicas
     返回：权威元数据 + 已证明的运行时状态；未知 follower commit_seq/lag 用 null，不推断
     状态：已完成（P0.5）

POST /manager/channel-cluster/:type/:id/leader/transfer
     Body: { target_node_id: uint64 }
     状态：已完成（P0.6，单频道显式安全 target transfer）

POST /manager/channel-cluster/:type/:id/repair
     触发策略驱动的安全 Leader repair（当前仅暴露 no_leader 修复入口）
     状态：已完成（P0.5）

POST /manager/channel-cluster/batch/leader-drain/:node_id
     批量迁移某节点上所有频道的 Leader
     状态：待实现（后续批量编排，复用单频道安全 transfer 原语）
```

### 4.4 业务管理模块（新增）

#### 用户管理 `/users`

- 用户列表（分页、搜索）
- 用户详情：UID、在线状态、设备列表、Token 信息
- 操作：封禁/解封、强制下线、Token 重置

需要新增 API：
```
GET  /manager/users?keyword=xxx&limit=50&cursor=xxx
GET  /manager/users/:uid
POST /manager/users/:uid/ban
POST /manager/users/:uid/unban
POST /manager/users/:uid/kick
```

#### 频道管理 `/channels-biz`

- 频道列表（按类型筛选：个人/群组/客服/社区）
- 频道详情：基本信息、订阅者列表、黑白名单
- 操作：创建频道、添加/移除订阅者、管理黑白名单

需要新增 API：
```
GET  /manager/channels?type=x&keyword=xxx&limit=50&cursor=xxx
GET  /manager/channels/:type/:id
GET  /manager/channels/:type/:id/subscribers
POST /manager/channels/:type/:id/subscribers/add
POST /manager/channels/:type/:id/subscribers/remove
GET  /manager/channels/:type/:id/denylist
GET  /manager/channels/:type/:id/allowlist
```

#### 消息管理 `/messages`（已有，增强）

在现有消息查询基础上增加：
- 消息保留策略配置界面
- 批量操作能力

#### 系统用户 `/system-users`

- System UID 列表
- 添加/移除系统 UID
- 说明各系统 UID 的用途

对应已有 API：
```
GET  /user/systemuids
POST /user/systemuids_add
POST /user/systemuids_remove
```

### 4.5 诊断工具模块（已有，重组）

| 页面 | 来源 | 说明 |
|------|------|------|
| 消息追踪 | 原 diagnostics | Trace ID / clientMsgNo 追踪 |
| 网络诊断 | 原 network | RPC 延迟、流量、节点健康 |
| 连接管理 | 原 connections（从运行时组移入） | 活跃连接列表 |
| 槽位日志 | 原 slot-logs（从观测组移入） | Slot Raft 日志 |

### 4.6 系统设置模块（新增）

#### 权限管理 `/settings/permissions`

- 管理员账号列表
- 角色定义（基于现有 permission resource 模型）
- 权限分配矩阵

现有权限资源：
- `cluster.node` (r/w)
- `cluster.slot` (r/w)
- `cluster.controller` (r/w)
- `cluster.task` (r)
- `cluster.overview` (r)
- `cluster.network` (r)
- `cluster.diagnostics` (r)
- `cluster.connection` (r)
- `cluster.channel` (r/w)

#### Webhook 配置 `/settings/webhooks`

- 回调地址配置
- 事件类型筛选（消息发送、用户上线、频道创建等）
- 回调日志查看
- 重试配置

## 5. 技术栈

| 层面 | 技术 |
|------|------|
| 框架 | React 19 + TypeScript |
| 路由 | React Router DOM 7 |
| UI | Tailwind CSS 4 + shadcn/ui (Radix) |
| 状态 | Zustand |
| 图表 | Recharts |
| 国际化 | react-intl（中/英） |
| 构建 | Vite 8 |
| 测试 | Vitest |

## 6. 实现优先级

### P0 — 核心缺失（已完成 read path）

1. **频道集群总览** — 已新增 `/manager/channel-cluster/summary` 聚合 API 与页面
2. **异常频道** — 已新增 `/manager/channel-cluster/unhealthy` 分页 API 与页面
3. **Dashboard 增强** — 已加入频道集群健康度卡片

### P0.5 — 频道集群操作（已完成安全子集）

- 副本详情读取：已通过 `GET /manager/channel-cluster/:type/:id/replicas` 暴露权威元数据与已证明的 leader/local runtime 状态；未证明的 follower 数值保持 unknown/null。
- 安全修复：已通过 `POST /manager/channel-cluster/:type/:id/repair` 接入 `internal/usecase/management` 和现有 channelmeta leader repairer；当前 UI 仅对 `no_leader` 行显示修复。

### P0.6 — 频道集群显式 Leader 转移（已完成单频道安全子集）

- 单频道显式转移：已通过 `POST /manager/channel-cluster/:type/:id/leader/transfer` 暴露，目标节点必须是副本、在 ISR 中，且由后端通过现有 promotion evaluation 证明可安全成为 Leader。
- 元数据写入约束：transfer 只允许更新 `Leader`、`LeaderEpoch` 和 `LeaseUntilMS`，不修改副本集合、ISR、MinISR、频道状态、特性、保留边界或 channel epoch。
- 批量 leader drain：仍待后续实现，应复用单频道安全 transfer 原语并补充批量限流/历史记录。

### P1 — 运营必需

4. **用户管理** — 需要新增后端 API
5. **频道业务管理** — 需要新增后端 API（部分可复用 client API）
6. **权限管理** — 后端已有 permission 中间件，需要管理界面

### P2 — 体验提升

7. **实时监控** — 需要 WebSocket 推送或轮询机制
8. **Webhook 配置** — 需要后端配置持久化
9. **系统用户管理** — 已有 API，前端包装即可

## 7. 当前实现状态

已完成：

- [x] 导航结构重组为 6 组
- [x] 路由注册（含 `/channels` → `/channel-cluster/list` 重定向）
- [x] 新模块占位页面与 P0 频道集群实页面
- [x] 中英文 i18n 消息
- [x] 后端新增 channel-cluster read API：summary / unhealthy
- [x] 频道集群总览页面（聚合 channel runtime meta 数据）
- [x] 异常频道页面（后端过滤，前端分页展示）
- [x] Dashboard 频道集群健康度卡片
- [x] 频道集群 P0.5 安全操作：副本详情、no_leader repair
- [x] 频道集群 P0.6 单频道显式安全 Leader transfer

待实现：
- [ ] 频道集群批量 leader drain
- [ ] 用户管理后端 API + 前端页面
- [ ] 频道业务管理后端 API + 前端页面
- [ ] 权限管理页面
- [ ] 实时监控页面
- [ ] Webhook 配置页面
- [ ] 系统用户管理页面

# Distributed Admin UI Phase 1 Design

## Overview

为当前 `WuKongIM v3` 主仓设计一套分布式后台管理高保真 UI/UX 第一阶段原型，交付物保存到项目 `ui/` 目录下。

这一阶段只做：

- 全局主体骨架
- 左侧菜单
- `Dashboard`
- `节点管理`

其他页面先保留菜单入口和轻量占位，不在本轮实现完整内容。

设计方向基于用户确认的约束：

- 视觉采用“冷静控制台”
- 风格参考现有后台截图，保持克制、清晰、高信息密度
- 文案采用“中文为主，关键技术词保留英文”
- 交付采用可直接打开的静态页面，不引入前端构建工具
- 同时提供 `ui/index.html` 总入口和独立页面文件
- 图标统一使用 Lucide Static CDN，而不是手写内联 SVG

## Goals

- 在仓库内落地一套可直接打开的分布式后台高保真静态原型
- 建立统一的后台 shell，包括侧边导航、顶部状态栏和主内容区节奏
- 为后续扩展 `分区管理`、`网络监控`、`拓扑视图`、`在线连接` 提供稳定骨架
- 先把 `Dashboard` 和 `节点管理` 两个主路径页面做准，后续页面在同一体系内扩展
- 页面语义贴合当前仓库的 cluster-first 模型，避免使用与项目不一致的旧术语

## Non-Goals

- 本轮不接入真实 API
- 本轮不引入 React、Vite、Next.js 或完整 `shadcn/ui` 工程
- 本轮不实现真实搜索、排序、筛选、图表计算或登录流程
- 本轮不实现危险操作、配置编辑、节点摘除、迁移等控制能力
- 本轮不完成 `分区管理`、`网络监控`、`拓扑视图`、`在线连接` 的正式页面内容
- 本轮不为移动端重新设计信息架构；布局以桌面端和常见笔记本宽度优先

## Existing Context

仓库当前没有现成的前端工程，也没有 `ui/` 目录下的后台页面基础设施，因此第一阶段应选择静态页面原型而不是立即接入构建链。

仓库中的集群语义约束明确：

- 单节点部署也统一按“单节点集群”描述
- 配置结构中已存在 `WK_CLUSTER_NODES` 和 `WK_CLUSTER_GROUPS`
- 当前配置示例天然适合提供 `3` 节点、`Leader + Follower`、`Group` 分布等高保真占位数据语境

后台信息架构需与当前项目核心概念一致：

- 集群
- 节点
- Group
- Gateway / 网络
- 在线连接

因此菜单中不再使用旧的“Slot 管理”作为本轮范围定义，而统一采用“分区管理（Group）”。

## Product Scope

第一阶段交付范围固定为：

1. 全局 shell
2. 左侧菜单
3. `Dashboard`
4. `节点管理`
5. 其他页面入口占位

### Menu Structure

- 概览
  - `Dashboard`
- 集群
  - `节点管理`
  - `分区管理`
  - `网络监控`
  - `拓扑视图`
- 连接
  - `在线连接`

规则：

- 当前页面菜单项高亮
- 未完成页面保留菜单入口，但主内容仅显示统一占位说明
- 菜单层级在 `index.html` 和独立页面中保持一致

## Recommended Approach

采用“巡检控制台”路线，并局部吸收监控视图能力：

- 主体仍是后台控制台，而不是监控大盘
- 首页强调“概况 -> 风险 -> 趋势/快照 -> 下钻”
- 节点页强调“筛选 -> 浏览 -> 详情抽屉”
- 图形表达克制，只为判断服务，不追求大面积可视化装饰

选择该路线的原因：

- 最贴近用户提供的参考图
- 适合分布式后台日常巡检场景
- 容易在静态原型阶段做到可信且稳定
- 后续扩展更多页面时，不需要推翻已建立的 shell

## Visual Direction

### Visual Character

整体视觉采用“冷静控制台”：

- 浅色大背景
- 白色内容面板
- 石墨灰文字
- 冷蓝色作为主强调色
- 状态色只服务健康语义，不做装饰性铺陈

避免出现以下偏差：

- 营销页式大 Hero 和强装饰背景
- 监控大屏式高饱和图形
- 过深的暗色界面
- 过度圆润或过度卡通化组件

### Tokens

建议在静态页面中通过 CSS 变量建立最小 token 集：

- `--bg-canvas: #f4f7fb`
- `--bg-panel: #ffffff`
- `--bg-subtle: #f7f9fc`
- `--text-primary: #111827`
- `--text-secondary: #4b5563`
- `--text-tertiary: #6b7280`
- `--border-default: #d9e1eb`
- `--accent-primary: #2d6cdf`
- `--accent-soft: #e9f0ff`
- `--state-online: #111827`
- `--state-degraded-bg: #fff7e6`
- `--state-degraded-text: #9a6700`
- `--state-offline-bg: #fef2f2`
- `--state-offline-text: #b91c1c`
- `--radius-card: 20px`
- `--radius-pill: 999px`
- `--shadow-soft: 0 14px 30px rgba(15, 23, 42, 0.06)`

### Typography

文案以中文为主，技术词保留英文，例如：

- `Dashboard`
- `Leader`
- `Follower`
- `Group`
- `RPC Latency`
- `online / degraded / offline`

标题、正文、表格文字都应稳定、克制，不使用过于强烈的展示性字形。

## Delivery Architecture

### Output Files

第一阶段建议输出以下文件：

- `ui/index.html`
- `ui/dashboard.html`
- `ui/nodes.html`
- `ui/placeholder/groups.html`
- `ui/placeholder/network.html`
- `ui/placeholder/topology.html`
- `ui/placeholder/connections.html`
- `ui/assets/styles.css`
- `ui/assets/app.js`
- `ui/assets/data.js`

如果实现阶段发现公共片段难以维护，可以增加：

- `ui/assets/components.js`

### Technology Choice

交付采用静态 HTML 原型：

- HTML 页面可直接双击或本地浏览器打开
- Tailwind 采用 CDN 方式引入，并通过页面内配置扩展颜色 token
- `shadcn/ui` 采用组件审美和结构模式，而不是安装完整依赖链
- 组件风格参考 `card / badge / button / input / table / sheet`
- 图标统一使用 `https://unpkg.com/lucide-static@latest/icons/xxx.svg`

### Shared Components

第一阶段至少沉淀以下可复用片段：

- `AppSidebar`
- `AppTopbar`
- `StatusBadge`
- `NodeRoleBadge`
- `MetricCard`
- `PanelSection`
- `TableToolbar`
- `EmptyPlaceholder`
- `RiskListItem`

这些片段可以通过重复 HTML + 公共 class、或轻量字符串模板实现；关键目标是统一视觉和减少后续页面扩展成本。

## Shell Design

### App Shell

所有页面共用相同骨架：

- 左侧固定导航
- 顶部状态栏
- 主内容区

布局原则：

- 左侧导航宽度固定
- 顶部状态栏保持轻量，但必须体现集群上下文
- 主内容区留白统一
- 页面切换时只替换主内容，不改变全局布局节奏

### Sidebar

侧栏承担稳定导航，而不是展示大量状态。

要求：

- 顶部展示 `WuKongIM`
- 菜单按“概览 / 集群 / 连接”分组
- 当前项高亮
- 图标使用 Lucide Static CDN
- 底部保留 `退出登录` 入口样式，但不做真实流程

### Topbar

顶部状态栏承担“当前集群上下文 + 轻操作入口”：

- 集群名称
- 部署说明，例如 `单节点集群`
- 时间窗提示，例如 `最近 15 分钟`
- 自动刷新提示
- 搜索或全局筛选占位
- 轻量操作按钮占位

第一阶段顶部栏只需具备视觉和信息定位作用，不承载复杂交互。

## Dashboard Design

### Purpose

`Dashboard` 是巡检首页，不是监控大盘。

它的目标是让用户在一个首屏中完成：

1. 快速判断集群是否健康
2. 看见当前最重要的风险线索
3. 识别异常节点或负载偏移
4. 进入节点管理页继续下钻

### Structure

首屏建议采用三段式结构：

1. 顶部 KPI 卡带
2. 中部风险与集群快照双列区
3. 下部趋势和排行区

#### KPI Cards

建议包含：

- `在线节点数`
- `异常节点数`
- `总 Group 数`
- `当前连接数`

规则：

- 卡片支持 hover 态
- 数据值之间要互相能解释
- 卡片视觉强调数据，但不使用大面积状态色

#### Risk Section

风险区优先展示当前最值得处理的问题，例如：

- 某节点 `RPC Latency` 升高
- 某 `Follower` 复制 lag 异常
- 某节点连接负载偏高

规则：

- 风险按 `critical / warning / info` 排序
- 每条风险都要能映射到下一步动作或下钻目标
- 不出现泛泛空洞文案

#### Cluster Snapshot

使用热度矩阵、小型状态块或轻量分布视图表达：

- Group 分布
- 节点负载偏移
- 健康状态差异

约束：

- 不做复杂拓扑图
- 不用装饰性 3D 或大面积图表
- 只表达“哪里异常”“哪里不均衡”

#### Trend and Ranking

下部可放置：

- 最近时间窗内连接趋势
- 异常节点排行
- 异常 Group 排行

这部分用于补充上下文，不应压过风险区和节点入口。

### Dashboard Interactions

- 时间窗按钮仅做视觉切换位，提供 `15m / 1h / 6h`
- KPI 卡 hover 时边框加深、阴影轻抬
- 风险项可点击跳转至 `nodes.html` 并附带模拟筛选态
- 相关入口都服务于“下钻到节点页”

## Nodes Page Design

### Purpose

`节点管理` 是第一阶段最主要的明细页面。

它需要支持：

- 浏览所有节点
- 快速判断 `Leader / Follower`
- 判断 `online / degraded / offline`
- 对比每个节点承载的 Group 数、连接数和 `RPC Latency`
- 进入节点详情抽屉继续查看上下文

### Structure

页面结构建议为：

1. 页面标题与说明
2. 工具条
3. 主表格
4. 详情抽屉

#### Toolbar

工具条包含：

- 搜索框
- 角色筛选
- 状态筛选
- 排序按钮

原则：

- 第一阶段只做轻交互壳
- 不加入批量操作
- 不加入危险按钮

#### Main Table

建议字段：

- `节点ID`
- `地址`
- `角色`
- `状态`
- `Group 数`
- `连接数`
- `RPC Latency`
- `操作`

表格要求：

- 行 hover 时背景轻微变化
- `Leader` 与 `Follower` 使用不同样式 badge
- 状态 badge 统一使用 `online / degraded / offline`
- 每行右侧提供 `查看详情`
- 当节点为 `degraded` 时，增加一句次级提示，例如 `Follower replication lag > threshold`

#### Detail Drawer

点击 `查看详情` 打开静态详情抽屉。

抽屉内容建议包括：

- 基础信息
- 最近 15 分钟负载概览
- 参与的重点 Group
- 最近异常事件
- 网络指标摘要

第一阶段抽屉只需具备高保真表现和静态交互，不接真实数据接口。

### Nodes Interactions

- 搜索、筛选、排序均使用静态模拟状态
- 表格行 hover 明确可点击感
- 详情抽屉支持打开/关闭
- 与 `Dashboard` 共用同一份占位数据源

## Static Data Rules

占位数据必须可信、统一、可交叉解释。

第一阶段建议采用 `3` 节点示例：

- `1` 个 `Leader`
- `2` 个 `Follower`
- `Group` 分布接近均衡，例如 `22 / 21 / 21`

节点状态建议：

- 节点 `1`: `Leader`, `online`, 负载稳定
- 节点 `2`: `Follower`, `online`, 轻微正常波动
- 节点 `3`: `Follower`, `degraded`, `RPC Latency` 偏高

这样可以保证：

- `Dashboard` 的风险摘要有明确来源
- `节点管理` 的状态差异合理
- 趋势与排行区的数据不显得随机拼凑

所有页面共用同一个静态数据源，避免同一节点在不同页面表现互相冲突。

## State Semantics

### Role Badges

- `Leader`
- `Follower`

角色 badge 主要通过明暗和边框区分，不依赖过多颜色。

### Health Badges

- `online`
  - 深色实心 badge
  - 表示节点健康在线
- `degraded`
  - 浅暖色 badge
  - 表示在线但存在延迟、复制或负载风险
- `offline`
  - 浅红色 badge
  - 表示不可达或服务不可用

### Risk Levels

仅保留三档：

- `critical`
- `warning`
- `info`

风险等级主要用于 Dashboard 排序和强调，不扩展更多复杂状态体系。

## Placeholder Pages

以下页面在第一阶段只保留占位：

- `分区管理`
- `网络监控`
- `拓扑视图`
- `在线连接`

占位页要求：

- 保留完整 shell
- 主内容区展示统一说明：
  - `该页面将在下一阶段扩展`
- 视觉完成度不能像“报错页”或“废页”

## Acceptance Criteria

第一阶段实现完成时，必须满足：

- `ui/index.html`、`ui/dashboard.html`、`ui/nodes.html` 可以直接打开
- `index.html` 同时具备总入口与页面跳转能力
- 左侧导航、顶部状态栏、主内容容器在不同页面中保持一致
- `Dashboard` 清楚表达“概况 -> 风险 -> 趋势/快照 -> 下钻”
- `节点管理` 清楚表达“筛选 -> 浏览 -> 详情抽屉”
- 样式整体贴近用户确认的“冷静控制台”方向
- 页面气质接近参考后台，而不是营销页或监控大盘
- 图标全部来自 Lucide Static CDN
- 未完成页面有统一占位，不破坏全局导航闭环
- 所有实现都落在 `ui/` 目录下，后续继续扩展时不需要推翻当前骨架

## Planning Notes

实现计划应优先拆成以下顺序：

1. 建立 `ui/` 目录和公共资产
2. 完成共享 shell、菜单和 token
3. 完成 `Dashboard`
4. 完成 `节点管理`
5. 补占位页
6. 做本地打开验证和样式一致性检查

计划阶段应继续坚持：

- 不引入无必要的前端工程化
- 不扩 scope 到剩余页面正式内容
- 不在第一阶段加入真实 API 适配

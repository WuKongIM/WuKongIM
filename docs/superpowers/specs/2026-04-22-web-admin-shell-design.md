# Web Admin Shell 设计

- 日期：2026-04-22
- 范围：新增根目录 `web/` 管理界面前端工程第一版骨架
- 关联目录：
  - `web/`
  - `ui/`
  - `docs/superpowers/specs/2026-04-08-distributed-admin-ui-design.md`

## 1. 背景

仓库当前已经存在一套静态管理界面原型，位于 `ui/` 目录，包含：

- `dashboard`
- `nodes`
- `channels`
- `connections`
- `groups`
- `network`
- `topology`

这套原型适合快速表达页面结构，但不适合作为后续持续演进的管理端基础设施，主要原因是：

- 目录仍是多页静态 HTML 组织，缺少统一的应用壳子与路由层
- 无类型系统与组件复用边界，后续维护成本会快速上升
- 无法自然承载后续的 manager API、状态管理、权限、导航状态与构建产物集成

因此第一版需要在仓库根目录新增独立的 `web/` 前端工程，使用 `bun + TypeScript + React + Tailwind CSS v4 + shadcn-ui` 搭建新的管理界面应用壳子。

同时，本轮明确只做“骨架”，不接真实 API，不实现复杂业务交互，不处理最终发布集成。

## 2. 目标与非目标

## 2.1 目标

本次设计目标如下：

1. 在仓库根目录新增独立前端工程 `web/`
2. 使用 `bun + TypeScript + React + Vite` 建立标准 SPA 管理台骨架
3. 使用 `Tailwind CSS v4 + shadcn-ui` 建立可持续扩展的基础 UI 层
4. 提供固定左侧导航、顶部栏、主内容区的统一应用壳子
5. 提供以下七个页面的路由级骨架：
   - `dashboard`
   - `nodes`
   - `channels`
   - `connections`
   - `slots`
   - `network`
   - `topology`
6. 保持视觉方向与 WuKongIM 的运维/控制台语义一致，而不是普通通用企业后台
7. 保留 `ui/` 目录作为旧静态原型参考，不在本轮删除或迁移

## 2.2 非目标

当前阶段明确不做以下内容：

- 不接入真实 manager API
- 不实现登录、鉴权、权限控制、用户态全局会话
- 不实现复杂筛选、搜索、分页、排序、批量操作
- 不实现真实图表、真实拓扑渲染、实时轮询或 WebSocket 数据
- 不引入全局状态库或数据请求库，例如 `zustand`、`redux`、`tanstack query`
- 不处理 `web/dist` 与 Go 服务静态资源托管的最终集成方式
- 不删除现有 `ui/` 静态原型
- 不在第一版中提供完整深色模式或大规模主题切换

## 3. 设计原则

## 3.1 先建立稳定壳子，再填充业务内容

第一版的重点不是业务能力，而是建立稳定的“管理台骨架”：

- 导航位置固定
- 页面节奏统一
- 标题区和内容区结构统一
- 复用组件边界清晰

这样后续接入 manager API 和页面细节时，不需要推翻整体结构。

## 3.2 保持 cluster-first / runtime-first 语义

页面命名、导航分组和空态文案都需要贴合当前项目的语义，避免引入与项目模型不一致的后台术语。

因此在第一版中：

- `groups` 页面统一调整为 `slots`
- 导航按“总览 / 运行时 / 观测”组织
- 页面骨架优先体现节点、连接、channel、slot、网络、拓扑等运行时实体

## 3.3 只抽取骨架级复用组件

第一版不构建业务组件库，只沉淀“应用壳子层”的复用单元：

- `AppShell`
- `SidebarNav`
- `Topbar`
- `PageHeader`
- `SectionCard`
- `PlaceholderBlock`
- `MetricPlaceholder`

目标是减少样式散落，同时避免在尚未明确业务细节时过早抽象。

## 3.4 轻依赖、低耦合、后续可扩展

为了服务“第一版只做骨架”的范围，本轮依赖选择应保持克制：

- 构建与运行只依赖 Bun 常规能力
- UI 层只引入骨架真正需要的 `shadcn-ui` 基础组件
- 后续需要数据与状态能力时，再按页面复杂度增量引入

## 4. 目录与工程结构

## 4.1 根目录新增 `web/`

新管理界面工程放在仓库根目录 `web/`：

```text
web/
  src/
    app/
      layout/
        app-shell.tsx
        sidebar-nav.tsx
        topbar.tsx
      providers.tsx
      router.tsx
    components/
      shell/
        page-container.tsx
        page-header.tsx
        section-card.tsx
        placeholder-block.tsx
        metric-placeholder.tsx
      ui/
        ...shadcn generated components
    lib/
      cn.ts
      navigation.ts
    pages/
      dashboard/
      nodes/
      channels/
      connections/
      slots/
      network/
      topology/
    styles/
      globals.css
    main.tsx
  components.json
  index.html
  package.json
  tsconfig.json
```

说明：

- `app/` 只放应用入口、全局 provider、路由和壳子布局
- `components/shell/` 只放骨架级复用组件
- `components/ui/` 放 `shadcn-ui` 生成的基础组件
- `pages/` 以页面为单位组织路由骨架
- `lib/navigation.ts` 负责导航定义与页面元信息

## 4.2 `ui/` 目录的定位

当前 `ui/` 目录保留为旧静态原型参考：

- 不作为新前端工程目录
- 不在本轮迁移内容到 `web/`
- 继续用于比对页面名称、信息结构和空态节奏

后续如需删除或归档 `ui/`，应在新工程稳定并完成发布集成后再单独决策。

## 5. 技术选型

## 5.1 核心依赖

第一版建议使用：

- 包管理与脚本：`bun`
- 构建工具：`vite`
- 语言：`TypeScript`
- 视图框架：`react`、`react-dom`
- 路由：`react-router-dom`
- 样式：`tailwindcss v4`
- 组件基础：`shadcn-ui`
- 图标：`lucide-react`
- 类名工具：`clsx`、`tailwind-merge`

## 5.2 暂不引入的能力

本轮显式不引入：

- `zustand` / `redux`
- `tanstack query`
- 图表库
- 拓扑渲染库
- i18n 框架
- 端到端测试平台

原因是这些能力都不是“第一版壳子成立”的必要条件。

## 6. 信息架构与路由

## 6.1 导航分组

左侧导航按运行视角组织：

- `Overview`
  - `Dashboard`
- `Runtime`
  - `Nodes`
  - `Channels`
  - `Connections`
  - `Slots`
- `Observability`
  - `Network`
  - `Topology`

该分组的意义如下：

- `Dashboard` 承担全局进入点角色
- `Nodes / Channels / Connections / Slots` 都是运行时实体
- `Network / Topology` 更偏关系观测和整体可视化

后续若新增 `Logs`、`Metrics`、`Tracing` 等页面，也能自然扩展到 `Observability`。

## 6.2 路由规则

第一版使用标准 SPA 路由：

- `/dashboard`
- `/nodes`
- `/channels`
- `/connections`
- `/slots`
- `/network`
- `/topology`

规则：

- `/` 默认重定向到 `/dashboard`
- 左侧导航根据当前路由高亮
- 顶部栏标题与副标题由路由元信息驱动
- 第一版不实现路由级权限控制

## 7. 页面骨架设计

## 7.1 全局应用壳子

所有页面共用统一结构：

- 左侧固定导航
- 顶部栏
- 主内容区

### 左侧固定导航

- 显示产品标识与简短说明
- 展示三组导航
- 当前页面高亮清晰
- 第一版不做折叠、拖拽、分组展开动画

### 顶部栏

- 左侧显示当前页面标题和简短描述
- 右侧保留少量全局操作位：
  - 搜索入口占位
  - 刷新按钮占位
  - 用户/主题区域占位

### 主内容区

- 使用统一 `PageContainer`
- 控制页面宽度、内边距、上下节奏
- 每个页面都从 `PageHeader` 开始，再进入主体占位区

## 7.2 各页面第一版骨架

### `Dashboard`

包含：

- 页面标题区
- 一行全局指标占位卡片
- 两块主要内容占位区域，例如：
  - 集群状态概览
  - 最近事件 / 告警

### `Nodes`

包含：

- 页面标题区
- 顶部筛选/工具条占位
- 主表格占位

### `Channels`

包含：

- 页面标题区
- 顶部筛选/工具条占位
- 主表格占位

### `Connections`

包含：

- 页面标题区
- 顶部筛选/工具条占位
- 主表格占位

### `Slots`

包含：

- 页面标题区
- 一组状态指标占位
- 列表或表格占位

### `Network`

包含：

- 页面标题区
- 两到三块网络观测卡片占位
- 下方趋势/明细区域占位

### `Topology`

包含：

- 页面标题区
- 大型画布区域占位
- 详情面板或说明区域占位

## 7.3 页面统一约束

所有页面保持以下规则：

- 每页都有统一的 `PageHeader`
- 第一版只做结构，不填充真实接口数据
- 优先使用卡片、表格、面板、画布占位表达未来承载能力
- 只保留最基础的导航切换和当前页信息映射

## 8. 视觉方向

## 8.1 风格定位

整体视觉采用“浅色基础的运维控制台”：

- 不做纯白通用企业后台
- 不做重暗黑监控大屏
- 强调结构清晰、状态明确、节奏统一

这一风格适合当前项目的节点、channel、slot、topology、network 等语义，也更利于后续逐步填入状态卡片、观测图块和详情面板。

## 8.2 视觉表达原则

- 背景、侧栏、卡片层次分明，但对比克制
- 保留 success / warning / danger 的状态位
- 圆角、阴影、边框均控制在中等偏弱强度
- 不做营销站式装饰，不做夸张动画

## 8.3 `shadcn-ui` 使用范围

第一版只引入壳子真正需要的基础组件，例如：

- `button`
- `card`
- `separator`
- `scroll-area`
- `tooltip`
- `sheet`
- `breadcrumb`

不在第一版中预生成大量暂时用不到的组件，以避免目录膨胀。

## 9. 组件边界

第一版建议沉淀以下骨架级组件：

- `AppShell`
  - 负责整页布局与左右区域分割
- `SidebarNav`
  - 负责产品区、导航分组、当前项高亮
- `Topbar`
  - 负责当前页标题映射与全局占位动作
- `PageContainer`
  - 负责页面边距、主轴节奏
- `PageHeader`
  - 负责标题、说明、副操作区
- `SectionCard`
  - 负责统一内容分组容器
- `PlaceholderBlock`
  - 负责表格、图表、详情区、拓扑画布等占位形态
- `MetricPlaceholder`
  - 负责状态卡片与指标卡片占位

这些组件都应尽量保持单一职责，页面文件只负责组合，不堆积大量重复样式。

## 10. 测试与质量门槛

第一版只要求最小化测试，但必须覆盖壳子关键路径：

1. 路由是否能渲染到对应页面骨架
2. 左侧导航是否能正确高亮当前页
3. 应用壳子是否稳定渲染顶部栏与主内容区

建议使用前端常规单元/组件测试手段覆盖这些行为；不在本轮接入重型视觉回归或端到端测试。

## 11. 交付与验收标准

当以下条件满足时，视为第一版骨架完成：

1. 根目录存在可独立运行的 `web/` 前端工程
2. 可以通过 `bun install` 完成依赖安装
3. 可以通过 `bun run dev` 启动本地开发服务
4. 可以通过 `bun run build` 完成生产构建
5. 应用包含统一壳子：固定左侧导航、顶部栏、主内容区
6. 七个页面都具备可访问的路由级骨架
7. 页面之间共享统一风格的壳子级复用组件
8. `ui/` 目录仍保留为旧静态参考，不影响本轮新工程落地

## 12. 推荐实现路径

推荐采用“标准 SPA 壳子”方案：

- `bun` 管理依赖与脚本
- `React + TypeScript` 构建单页应用
- `react-router-dom` 管理 7 个页面
- `Tailwind CSS v4 + shadcn-ui` 提供样式体系与基础组件

不推荐继续沿用多页 HTML 模式，也不建议在第一版引入文件路由框架或局部挂载方案，因为这些路径都不如标准 SPA 更适合作为后续持续演进的基础。

## 13. 后续阶段衔接

在第一版骨架稳定后，后续工作可按以下顺序推进：

1. 接入 manager API 与数据请求层
2. 为页面补充真实筛选、列表、详情、异常摘要
3. 决定 `web/dist` 与 Go 服务的静态托管/发布集成方式
4. 评估是否归档或移除旧的 `ui/` 静态原型

本设计文档只覆盖第一阶段“骨架工程 + 统一应用壳子”。

# Distributed Admin UI Pencil Design Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 `WuKongIM` 产出一套基于 pencil `.pen` 文件的分布式后台管理系统 UI 初版设计，覆盖单集群 SRE 巡检工作台的核心页面。

**Architecture:** 设计成果落在单独的 `.pen` 文件中，包含共享组件区与 7 个主页面 Frame。先建立统一视觉 token 与布局骨架，再按 `Dashboard -> Nodes -> Groups -> Events -> Gateway -> Traffic -> Topology` 的顺序增量搭建页面，并在每个阶段用 screenshot / layout snapshot 校验视觉与布局正确性。

**Tech Stack:** Pencil MCP、`.pen` 文件、`Web App` guide、`Table` guide、Pencil variables/themes、PNG 导出校验。

**Spec:** `docs/wiki/2026-04-08-distributed-admin-ui-design.md`

---

## 执行说明

- 设计对象是 `单集群`、`SRE`、`观测优先` 的高信息密度后台，不得滑向营销页或大屏风格。
- 视觉方向以中性石墨灰 + 冷蓝强调为主，状态色只服务健康语义，不做装饰性色块泛滥。
- 页面必须桌面端优先，默认宽视口；不为了移动端压低信息密度。
- 所有页面都必须保留一致的左侧导航、顶部状态栏、内容区节奏和抽屉式详情模式。
- 优先做工作台主路径，不在第一版加入任何危险操作按钮。
- 每个页面完成后都要执行 screenshot 校验；最终统一导出 PNG 供审阅。

## 文件映射

| 路径 | 责任 |
|------|------|
| `docs/wiki/2026-04-08-distributed-admin-ui-design.md` | 已确认的产品/UI 设计规格，作为页面范围与信息架构基线 |
| `docs/wiki/2026-04-08-distributed-admin-ui.pen` | 最终的 pencil 设计文件 |
| `docs/wiki/exports/distributed-admin-ui/` | 最终导出的页面 PNG 预览 |
| `untitled.pen` | 当前活动画布来源文件，仅用于初始化复制，不作为最终交付物 |

## 设计基线

### 视觉 token

在 `.pen` 文件内建立最小变量集，避免全程硬编码：

- 颜色
  - `surface.canvas = #F3F5F7`
  - `surface.panel = #FFFFFF`
  - `surface.subtle = #E9EEF3`
  - `ink.primary = #111827`
  - `ink.secondary = #4B5563`
  - `ink.tertiary = #6B7280`
  - `border.default = #D7DEE7`
  - `accent.primary = #2D6CDF`
  - `state.success = #1F8F5F`
  - `state.warning = #D38A14`
  - `state.danger = #D64545`
  - `state.info = #2563EB`
- 字体
  - `font.heading = Geist`
  - `font.body = Geist`
  - `font.mono = IBM Plex Mono`
- 间距
  - `space.8 = 8`
  - `space.12 = 12`
  - `space.16 = 16`
  - `space.20 = 20`
  - `space.24 = 24`
  - `space.32 = 32`
- 圆角与阴影
  - `radius.card = 12`
  - `radius.badge = 999`
  - 阴影仅用于面板与抽屉，强度保持克制

### 页面清单

最终至少包含 7 个顶级页面 Frame：

1. `Dashboard`
2. `Nodes`
3. `Groups`
4. `Events`
5. `Gateway`
6. `Traffic`
7. `Topology`

另设一个单独的 `Components` 区用于共享 UI 原子件。

### 共享组件范围

至少沉淀以下 reusable 组件：

- 侧边栏导航项
- 顶部状态条片段
- KPI 指标卡
- 状态 badge
- 风险列表项
- 表格表头行
- 表格数据行
- 分区标题栏
- 小型趋势卡

## Task 1: 建立专用设计文件和视觉 token

**Files:**
- Create: `docs/wiki/2026-04-08-distributed-admin-ui.pen`
- Reference: `docs/wiki/2026-04-08-distributed-admin-ui-design.md`

- [ ] **Step 1: 从当前活动画布复制出正式设计文件**

Run:

```bash
mkdir -p docs/wiki
cp untitled.pen docs/wiki/2026-04-08-distributed-admin-ui.pen
```

Expected: `docs/wiki/2026-04-08-distributed-admin-ui.pen` 存在，后续 pencil 仅操作该文件。

- [ ] **Step 2: 在 pencil 中打开正式设计文件**

Use:

```text
mcp__pencil__open_document({ filePathOrTemplate: "/Users/tt/Desktop/work/go/WuKongIM-v3.1/docs/wiki/2026-04-08-distributed-admin-ui.pen" })
```

Expected: 活动编辑器切换到正式设计文件，不再使用 `untitled.pen`。

- [ ] **Step 3: 读取布局与指南上下文**

Use:

```text
mcp__pencil__get_editor_state({ include_schema: true })
mcp__pencil__get_guidelines({ category: "guide", name: "Web App" })
mcp__pencil__get_guidelines({ category: "guide", name: "Table" })
```

Expected: 获得页面结构、表格规则和 web app 布局约束。

- [ ] **Step 4: 写入全局变量**

Use:

```text
mcp__pencil__set_variables({ filePath, variables: { ...设计基线中的颜色/字体/间距变量... } })
```

Expected: `.pen` 文件拥有统一 token，后续节点尽量引用变量而不是重复硬编码。

- [ ] **Step 5: 预留画布区域并建立 placeholder 页面骨架**

Use `batch_design()` 一次创建：

- 左侧 `Components` 区 placeholder frame
- 一排 7 个页面 placeholder frame
- 所有页面 frame 命名清晰并摆在不重叠的空白区域

Expected: 画布上出现完整页面骨架，且每个待设计页面都带 `placeholder: true`。

- [ ] **Step 6: 截图校验骨架**

Use:

```text
mcp__pencil__get_screenshot({ filePath, nodeId: "<每个顶级页面 frame id>" })
```

Expected: 页面没有重叠，布局留白足够，后续可在各页面内部安全施工。

## Task 2: 先做共享组件与工作台基础布局

**Files:**
- Modify: `docs/wiki/2026-04-08-distributed-admin-ui.pen`

- [ ] **Step 1: 在 Components 区创建 reusable 组件**

至少创建：

- `NavItem`
- `StatusBadge`
- `MetricCard`
- `SectionHeader`
- `RiskItem`
- `DataTableHeaderRow`
- `DataTableRow`
- `MiniTrendCard`

要求：

- 组件命名使用可读英文
- 组件树尽量小而稳定
- 文本与填充全部使用变量

- [ ] **Step 2: 建立全局工作台 shell**

把 shell 结构应用到 `Dashboard / Nodes / Groups / Events / Gateway / Traffic / Topology`：

- 左侧导航
- 顶部状态栏
- 主内容容器

要求：

- 左侧导航宽度固定
- 顶部状态栏包含集群名、时间窗、刷新与搜索占位
- 主内容容器使用统一 padding / gap

- [ ] **Step 3: 用 screenshot 校验组件和 shell**

Use:

```text
mcp__pencil__get_screenshot({ filePath, nodeId: "<Components 区 id>" })
mcp__pencil__get_screenshot({ filePath, nodeId: "<Dashboard frame id>" })
```

Expected: 组件区整齐可复用，Dashboard 已有统一框架但仍未填充业务内容。

- [ ] **Step 4: 解除 Components placeholder，保留页面 placeholder**

Use `batch_design()` 更新 `Components` frame：

- `placeholder: false`

Expected: 组件区完成并锁定，页面 frame 继续保留 placeholder。

## Task 3: 完成 Dashboard 页面

**Files:**
- Modify: `docs/wiki/2026-04-08-distributed-admin-ui.pen`

- [ ] **Step 1: 先搭建 Dashboard 的 3 段主结构**

必须包含：

- 顶部 KPI 状态卡带
- 中部风险清单 + 集群结构快照矩阵
- 下部短趋势区 + 异常节点排行 + 异常 groups 排行

要求：

- 主页焦点是风险与巡检，不要做炫技图形
- 结构快照使用矩阵或热度表达，不做装饰性拓扑

- [ ] **Step 2: 给表格和趋势区填入可信的占位数据**

要求：

- 节点名、group id、lag、吞吐、连接数都有示例值
- 状态值之间要彼此合理，不要随机乱填

- [ ] **Step 3: 补充状态语义与交互占位**

加入：

- `healthy / degraded / critical` badge
- 可点击卡片和详情入口的视觉 affordance
- 风险项严重级别标识

- [ ] **Step 4: 截图并修正**

Use:

```text
mcp__pencil__get_screenshot({ filePath, nodeId: "<Dashboard frame id>" })
mcp__pencil__snapshot_layout({ filePath, parentId: "<Dashboard frame id>", maxDepth: 4, problemsOnly: true })
```

Expected: 无明显截断、重叠、不可见文本或密度失衡问题。

- [ ] **Step 5: Dashboard 完成后取消 placeholder**

Expected: `Dashboard` frame 的 `placeholder` 被移除。

## Task 4: 完成 Nodes 与 Groups 两个主巡检页

**Files:**
- Modify: `docs/wiki/2026-04-08-distributed-admin-ui.pen`

- [ ] **Step 1: 完成 Nodes 页面**

必须包含：

- 顶部汇总指标
- 高密度节点表
- 右侧详情抽屉展开态示例

节点表建议展示：

- Node ID
- 状态
- Leader/Follower 摘要
- Groups 数
- 连接数
- 在线会话
- 吞吐
- 复制异常
- Listener 状态
- CPU / 内存 / 磁盘
- 最后心跳

- [ ] **Step 2: 完成 Groups 页面**

必须包含：

- 顶部汇总指标
- 高密度 group 表
- 右侧详情抽屉展开态示例

group 表建议展示：

- Group ID
- Leader
- Peers
- 健康状态
- Commit Lag
- 异常摘要
- 最近 leader 变更
- 热点节点
- 写入速率
- 风险标签

- [ ] **Step 3: 保持两个页面的抽屉模式一致**

抽屉内 tab 至少体现：

- 概况
- 趋势
- 事件
- 关联对象

Expected: 两个主巡检页结构一致，但字段与诊断重心不同。

- [ ] **Step 4: 截图并修正**

Use:

```text
mcp__pencil__get_screenshot({ filePath, nodeId: "<Nodes frame id>" })
mcp__pencil__get_screenshot({ filePath, nodeId: "<Groups frame id>" })
mcp__pencil__snapshot_layout({ filePath, parentId: "<Nodes frame id>", maxDepth: 4, problemsOnly: true })
mcp__pencil__snapshot_layout({ filePath, parentId: "<Groups frame id>", maxDepth: 4, problemsOnly: true })
```

- [ ] **Step 5: 取消 Nodes / Groups placeholder**

Expected: 这两个主页面转为完成态。

## Task 5: 完成 Events、Gateway、Traffic、Topology 四个辅助页面

**Files:**
- Modify: `docs/wiki/2026-04-08-distributed-admin-ui.pen`

- [ ] **Step 1: 完成 Events 页面**

必须包含：

- 事件时间线
- 可过滤事件表
- 严重级别、节点、group、时间窗等筛选条

- [ ] **Step 2: 完成 Gateway 页面**

必须包含：

- listener 概览卡
- listener 表格
- 按节点接入压力区
- 短趋势

- [ ] **Step 3: 完成 Traffic 页面**

必须包含：

- 发送/投递/ack/失败指标卡
- 4 张短趋势
- 热点节点表
- 热点 groups/channels 表

- [ ] **Step 4: 完成 Topology 页面**

必须包含：

- 节点配置清单
- group 分布视图
- listener 配置区
- 参数对照表

要求：

- `Topology` 明确保持只读，不出现危险操作按钮

- [ ] **Step 5: 截图并修正**

对 4 个页面逐一执行：

```text
mcp__pencil__get_screenshot(...)
mcp__pencil__snapshot_layout(..., problemsOnly: true)
```

Expected: 页面完整、视觉一致、无明显布局问题。

- [ ] **Step 6: 取消这 4 个页面 placeholder**

Expected: 所有页面 frame 全部进入完成态。

## Task 6: 全局视觉一致性回查与导出

**Files:**
- Modify: `docs/wiki/2026-04-08-distributed-admin-ui.pen`
- Create: `docs/wiki/exports/distributed-admin-ui/`

- [ ] **Step 1: 全局回查变量、间距和状态色**

检查项：

- 是否存在大量重复硬编码颜色
- badge / card / table 的圆角是否一致
- 状态色是否只用在健康语义
- 页面之间导航与状态栏是否统一

- [ ] **Step 2: 检查所有页面的布局问题**

Use:

```text
mcp__pencil__snapshot_layout({ filePath, problemsOnly: true, maxDepth: 5 })
```

Expected: 没有被裁剪、重叠、尺寸异常的节点；若有问题，回到对应页面修复。

- [ ] **Step 3: 导出所有页面 PNG**

Run:

```bash
mkdir -p docs/wiki/exports/distributed-admin-ui
```

Use:

```text
mcp__pencil__export_nodes({
  filePath,
  nodeIds: ["<Dashboard>", "<Nodes>", "<Groups>", "<Events>", "<Gateway>", "<Traffic>", "<Topology>"],
  outputDir: "/Users/tt/Desktop/work/go/WuKongIM-v3.1/docs/wiki/exports/distributed-admin-ui",
  format: "png",
  scale: 2
})
```

Expected: 导出 7 张页面 PNG，供最终审阅。

- [ ] **Step 4: 逐张查看导出效果或截图**

重点检查：

- 文本是否可见
- 表格列宽是否合理
- 详情抽屉是否挤压主体
- 风险色和彩色重点是否过度
- 首页是否能在第一眼完成巡检判断

- [ ] **Step 5: 最终整理**

确保：

- 正式产物位于 `docs/wiki/2026-04-08-distributed-admin-ui.pen`
- 导出预览位于 `docs/wiki/exports/distributed-admin-ui/`
- 不再依赖 `untitled.pen` 作为最终文件


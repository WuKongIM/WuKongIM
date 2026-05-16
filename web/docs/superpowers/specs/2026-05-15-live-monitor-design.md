# Live Monitor 实时监控界面设计

## Summary

为 WuKongIM Web 管理面板的 Monitor 页面（`/monitor`）设计并实现实时监控界面。该页面展示消息流量和连接状态的实时时序图，支持按节点筛选、时间范围选择和暂停/恢复功能。v1 使用 mock 数据，不对接真实 API。

## Background

现有 Dashboard 页面提供快照式的系统概览（健康状态 + Pulse 卡片），但不支持自动刷新和详细的时序图展示。Monitor 页面目前是空壳占位页面，需要实现为持续刷新的实时监控面板，满足运维人员日常巡检和问题排查的需求。

## Design Decisions

### 核心场景
混合场景，侧重实时巡检：
- 默认展示聚合数据的实时时序图，自动刷新
- 支持按节点筛选，联动所有图表
- 支持暂停/恢复，方便截图和对比分析
- 大屏盯屏模式留待 v2

### 页面布局
- **4 列网格**：信息密度高，一屏展示所有核心指标
- **展开图表卡片**：带 Y 轴刻度和时间轴，图表高度约 180px，适合仔细看趋势
- **顶部筛选栏**：节点下拉选择器 + 时间范围选择器（5m/15m/30m/1h）+ Live/Pause 切换按钮

### 指标范围（v1）
分为两个区块：

**1. 消息流量（6 个图表）**
- Send Rate (msg/s) — 发送消息速率
- Deliver Rate (msg/s) — 投递消息速率
- Send Latency P99 (ms) — 发送延迟 P99
- Delivery Latency P99 (ms) — 投递延迟 P99
- Send Fail Rate (%) — 发送失败率
- Retry Queue Depth — 重试队列深度

**2. 连接状态（4 个图表）**
- Online Connections — 在线连接总数
- Active Channels — 活跃频道数
- Connection Rate (conn/s) — 连接建立速率
- Disconnection Rate (conn/s) — 连接断开速率

### 行为规格
- **自动刷新频率**：每 5 秒
- **节点筛选**：默认 "All Nodes"，选择具体节点后所有图表联动过滤
- **时间范围**：默认 5 分钟，可选 15m/30m/1h
- **Pause/Resume**：暂停时冻结数据更新，图表不再滚动
- **Mock 数据**：3-5 个节点，时序数据随机生成但符合真实波动特征

## Technical Approach

### 组件结构
```
src/pages/monitor/
├── page.tsx                    # 主页面容器
├── components/
│   ├── monitor-controls.tsx    # 顶部筛选栏（节点/时间/pause）
│   ├── metric-chart.tsx        # 单个指标图表卡片
│   └── chart-grid.tsx          # 4 列网格容器
├── use-monitor-data.ts         # mock 数据生成 hook
└── types.ts                    # 类型定义
```

### 图表库
使用现有的 `recharts`（Dashboard 已使用），配置：
- `AreaChart` + `Area` 组件（填充面积图，视觉效果更丰富）
- `XAxis` 显示时间刻度（格式：HH:mm:ss）
- `YAxis` 显示数值刻度
- `Tooltip` 显示悬停详情
- `ResponsiveContainer` 自适应容器宽度

### Mock 数据策略
- 初始化时生成 5 分钟的历史数据（60 个数据点，每 5 秒一个）
- 每 5 秒追加一个新数据点，移除最旧的数据点（滑动窗口）
- 数据点包含时间戳 + 各节点的指标值
- 节点筛选时前端过滤数据，不重新生成

### 样式规范
- 卡片：`rounded-2xl border border-border/80 bg-card/88 shadow-[inset_0_1px_0_rgba(255,255,255,0.035)]`（与 PulseTile 一致）
- 标题：`font-mono text-[11px] font-semibold uppercase tracking-[0.14em] text-muted-foreground`
- 图表配色：使用 CSS 变量 `--chart-1` 到 `--chart-5`（与 Dashboard 一致）
- 网格：`grid grid-cols-4 gap-4`

## Implementation Plan

1. **创建类型定义** (`types.ts`)
   - `MetricDataPoint`、`NodeMetric`、`TimeRange` 等

2. **实现 mock 数据 hook** (`use-monitor-data.ts`)
   - 生成初始数据
   - 定时追加新数据点
   - 支持节点筛选和时间范围切换

3. **实现顶部控制栏** (`monitor-controls.tsx`)
   - 节点下拉选择器（使用 shadcn Select）
   - 时间范围按钮组（使用 shadcn ToggleGroup）
   - Live/Pause 切换按钮（使用 shadcn Button + icon）

4. **实现图表卡片** (`metric-chart.tsx`)
   - 接收数据和配置 props
   - 使用 recharts AreaChart 渲染
   - 响应式容器

5. **实现主页面** (`page.tsx`)
   - 组装所有组件
   - 管理全局状态（节点筛选、时间范围、pause 状态）
   - 4 列网格布局

6. **添加 i18n keys**
   - `monitor.controls.*`
   - `monitor.metrics.*`

## Out of Scope (v2+)

- 真实 API 对接
- 节点健康指标（CPU/内存/Goroutine）
- 存储 I/O 指标
- 大屏全屏模式
- 图表缩放和时间范围拖拽
- 告警阈值标线
- 导出图表数据

## Files Changed

### New Files
- `src/pages/monitor/components/monitor-controls.tsx`
- `src/pages/monitor/components/metric-chart.tsx`
- `src/pages/monitor/components/chart-grid.tsx`
- `src/pages/monitor/use-monitor-data.ts`
- `src/pages/monitor/types.ts`

### Modified Files
- `src/pages/monitor/page.tsx` — 替换占位内容为完整实现
- `src/i18n/messages/zh-CN.ts` — 添加 monitor.controls.* 和 monitor.metrics.* keys
- `src/i18n/messages/en.ts` — 添加对应英文翻译

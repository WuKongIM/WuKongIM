# Distributed Admin UI Phase 1 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 `WuKongIM` 在 `ui/` 目录下交付一套可直接打开的分布式后台管理静态高保真原型，第一阶段只覆盖共享骨架、菜单、`Dashboard` 和 `节点管理`。

**Architecture:** 不引入前端构建工具，直接用静态 `HTML + Tailwind CDN + 共享 CSS/JS` 组织页面。`ui/assets` 提供统一 token、数据和页面交互；`ui/index.html` 作为默认入口并复用 `Dashboard` 视图，`ui/dashboard.html`、`ui/nodes.html` 和占位页共享同一套 sidebar / topbar / visual tokens。由于既要支持 `file://` 直接打开，又要支持本地静态服务预览，页面通过 `data-base="." | ".."` 提供相对路径前缀，`app.js` 统一拼接菜单 href。为保证“先验证再实现”，新增一个轻量 smoke 校验脚本，锁定文件结构、Lucide CDN 用法、无内联 SVG、关键页面结构和文案标记。

**Tech Stack:** 静态 HTML、Tailwind CDN、原生 CSS、原生 JavaScript、Lucide Static CDN、POSIX shell、`python3 -m http.server`。

**Spec:** `docs/superpowers/specs/2026-04-08-distributed-admin-ui-design.md`

---

## 执行说明

- 每个任务都按 `@superpowers/test-driven-development` 的精神执行：先补 fail 的 smoke 检查，再写最小实现，再重新验证。
- 不引入 React、Vite、Next.js、npm 依赖或完整 `shadcn/ui` 工程；本次只采用其组件风格和信息组织方式。
- 所有图标都必须来自 `https://unpkg.com/lucide-static@latest/icons/`；禁止手写内联 `<svg>`。
- 第一阶段只做 `Dashboard`、`节点管理` 和占位页，不扩 scope 到 `分区管理`、`网络监控`、`拓扑视图`、`在线连接` 的正式内容。
- 所有页面默认桌面端优先，但必须在常见笔记本宽度下不破版。
- 最终交付前运行 `@superpowers/verification-before-completion` 的等价核验：完整 smoke 校验、本地静态服务预览、人工检查页面一致性。

## 文件映射

| 路径 | 责任 |
|------|------|
| `ui/index.html` | 默认入口页，直接展示 `Dashboard` 主视图，并作为全局菜单跳转起点 |
| `ui/dashboard.html` | 独立 `Dashboard` 页面 |
| `ui/nodes.html` | 独立 `节点管理` 页面 |
| `ui/placeholder/groups.html` | `分区管理` 占位页 |
| `ui/placeholder/network.html` | `网络监控` 占位页 |
| `ui/placeholder/topology.html` | `拓扑视图` 占位页 |
| `ui/placeholder/connections.html` | `在线连接` 占位页 |
| `ui/assets/styles.css` | 全局 token、布局、组件样式、页面级视觉规则 |
| `ui/assets/data.js` | 静态占位数据，统一定义集群摘要、风险项、节点列表和详情抽屉数据 |
| `ui/assets/app.js` | 共享 shell 渲染、菜单高亮、轻量筛选态、详情抽屉开关和公共 DOM 初始化 |
| `scripts/verify-admin-ui.sh` | 轻量 smoke 校验脚本：文件存在、关键结构、Lucide CDN、禁止内联 SVG |

## 实现边界

- `ui/index.html` 不是额外的新信息架构页，它就是默认入口，内容与 `Dashboard` 对齐。
- 所有页面共用相同的左侧导航和顶部状态栏；差异只出现在主内容区。
- `Dashboard` 的重点是概况、风险、趋势和集群快照，不做复杂拓扑。
- `节点管理` 的重点是表格浏览和详情抽屉，不做真实搜索与危险操作。
- 占位页必须保留完整 shell 和统一提示文案：`该页面将在下一阶段扩展`。

## Task 1: 建立 `ui/` 目录骨架和 smoke 校验脚本

**Files:**
- Create: `scripts/verify-admin-ui.sh`
- Create: `ui/index.html`
- Create: `ui/dashboard.html`
- Create: `ui/nodes.html`
- Create: `ui/placeholder/groups.html`
- Create: `ui/placeholder/network.html`
- Create: `ui/placeholder/topology.html`
- Create: `ui/placeholder/connections.html`
- Create: `ui/assets/styles.css`
- Create: `ui/assets/data.js`
- Create: `ui/assets/app.js`

- [ ] **Step 1: 先写失败的 smoke 校验脚本**

在 `scripts/verify-admin-ui.sh` 写一个最小可执行脚本，先锁定第一阶段必须存在的文件：

```bash
#!/usr/bin/env bash
set -euo pipefail

required_files=(
  "ui/index.html"
  "ui/dashboard.html"
  "ui/nodes.html"
  "ui/placeholder/groups.html"
  "ui/placeholder/network.html"
  "ui/placeholder/topology.html"
  "ui/placeholder/connections.html"
  "ui/assets/styles.css"
  "ui/assets/data.js"
  "ui/assets/app.js"
)

for file in "${required_files[@]}"; do
  [[ -f "$file" ]] || { echo "missing: $file" >&2; exit 1; }
done
```

- [ ] **Step 2: 运行脚本，确认当前失败**

Run: `bash scripts/verify-admin-ui.sh`

Expected: FAIL，并输出缺失的 `ui/...` 文件路径。

- [ ] **Step 3: 创建最小目录和空文件骨架**

创建目录：

```bash
mkdir -p ui/assets ui/placeholder scripts
```

创建最小骨架文件，先只放最小内容，确保后续任务可以逐步填充：

```html
<!-- ui/index.html -->
<!doctype html>
<html lang="zh-CN">
  <head><meta charset="utf-8"><title>WuKongIM Admin</title></head>
  <body data-page="dashboard" data-base="."></body>
</html>
```

```js
// ui/assets/app.js
document.addEventListener("DOMContentLoaded", () => {
  document.body.dataset.boot = "pending";
});
```

- [ ] **Step 4: 重新运行 smoke 校验，确认骨架存在**

Run: `bash scripts/verify-admin-ui.sh`

Expected: PASS，无输出。

- [ ] **Step 5: 提交骨架**

```bash
git add scripts/verify-admin-ui.sh ui
git commit -m "chore(ui): scaffold admin ui phase 1"
```

## Task 2: 完成共享 shell、菜单、token 和占位页

**Files:**
- Modify: `scripts/verify-admin-ui.sh`
- Modify: `ui/index.html`
- Modify: `ui/dashboard.html`
- Modify: `ui/nodes.html`
- Modify: `ui/placeholder/groups.html`
- Modify: `ui/placeholder/network.html`
- Modify: `ui/placeholder/topology.html`
- Modify: `ui/placeholder/connections.html`
- Modify: `ui/assets/styles.css`
- Modify: `ui/assets/data.js`
- Modify: `ui/assets/app.js`

- [ ] **Step 1: 扩展 smoke 校验，先锁定共享 shell 约束**

在 `scripts/verify-admin-ui.sh` 增加以下检查：

```bash
html_files=(
  "ui/index.html"
  "ui/dashboard.html"
  "ui/nodes.html"
  "ui/placeholder/groups.html"
  "ui/placeholder/network.html"
  "ui/placeholder/topology.html"
  "ui/placeholder/connections.html"
)

for file in "${html_files[@]}"; do
  grep -q 'tailwindcss.com' "$file" || { echo "missing tailwind cdn: $file" >&2; exit 1; }
  grep -q '\./assets/styles.css\|../assets/styles.css' "$file" || { echo "missing styles reference: $file" >&2; exit 1; }
  grep -q '\./assets/app.js\|../assets/app.js' "$file" || { echo "missing app.js reference: $file" >&2; exit 1; }
done

! rg -n "<svg" ui >/dev/null || { echo "inline svg is forbidden" >&2; exit 1; }
rg -n "unpkg.com/lucide-static@latest/icons/" ui >/dev/null || { echo "missing lucide static cdn usage" >&2; exit 1; }
```

并检查占位文案：

```bash
for file in ui/placeholder/*.html; do
  grep -q '该页面将在下一阶段扩展' "$file" || { echo "missing placeholder copy: $file" >&2; exit 1; }
done
```

- [ ] **Step 2: 运行校验，确认当前失败**

Run: `bash scripts/verify-admin-ui.sh`

Expected: FAIL，因为页面还没有 Tailwind CDN、共享资产引用、Lucide CDN 图标和占位文案。

- [ ] **Step 3: 实现共享视觉和骨架**

在 `ui/assets/styles.css` 建立 token 和组件基础：

```css
:root {
  --bg-canvas: #f4f7fb;
  --bg-panel: #ffffff;
  --text-primary: #111827;
  --text-secondary: #4b5563;
  --text-tertiary: #6b7280;
  --border-default: #d9e1eb;
  --accent-primary: #2d6cdf;
  --accent-soft: #e9f0ff;
  --radius-card: 20px;
  --radius-pill: 999px;
  --shadow-soft: 0 14px 30px rgba(15, 23, 42, 0.06);
}

body {
  margin: 0;
  background: var(--bg-canvas);
  color: var(--text-primary);
}
```

在 `ui/assets/data.js` 固定第一阶段静态数据：

```js
window.ADMIN_UI_DATA = {
  cluster: {
    name: "cluster-alpha",
    deployment: "单节点集群",
    window: "最近 15 分钟",
    refresh: "自动刷新 10s",
  },
  nav: [
    { label: "概览", items: [{ key: "dashboard", text: "Dashboard", path: "dashboard.html", icon: "layout-dashboard" }] },
    { label: "集群", items: [
      { key: "nodes", text: "节点管理", path: "nodes.html", icon: "server" },
      { key: "groups", text: "分区管理", path: "placeholder/groups.html", icon: "blocks" },
      { key: "network", text: "网络监控", path: "placeholder/network.html", icon: "waypoints" },
      { key: "topology", text: "拓扑视图", path: "placeholder/topology.html", icon: "share-2" },
    ]},
    { label: "连接", items: [{ key: "connections", text: "在线连接", path: "placeholder/connections.html", icon: "plug-zap" }] },
  ],
};
```

在 `ui/assets/app.js` 提供共享渲染函数：

```js
function icon(name) {
  return `https://unpkg.com/lucide-static@latest/icons/${name}.svg`;
}

function pageHref(path) {
  const base = document.body.dataset.base || ".";
  return `${base}/${path}`;
}

function renderSidebar(currentKey) { /* 渲染 logo、分组、当前项高亮、退出登录 */ }
function renderTopbar(meta) { /* 渲染 cluster 名称、时间窗、刷新提示、全局搜索占位 */ }
function renderPlaceholder(title, copy) { /* 统一占位主内容 */ }

document.addEventListener("DOMContentLoaded", () => {
  const page = document.body.dataset.page;
  document.querySelector("[data-sidebar]").innerHTML = renderSidebar(page);
  document.querySelector("[data-topbar]").innerHTML = renderTopbar(window.ADMIN_UI_DATA.cluster);
});
```

在所有 HTML 页面落地统一骨架。根页面使用 `./assets/*`，占位页使用 `../assets/*`：

```html
<!doctype html>
<html lang="zh-CN">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>WuKongIM Admin</title>
    <script src="https://cdn.tailwindcss.com"></script>
    <link rel="stylesheet" href="./assets/styles.css" />
  </head>
  <body data-page="dashboard" data-base=".">
    <div class="app-shell">
      <aside data-sidebar></aside>
      <main>
        <header data-topbar></header>
        <section data-page-root></section>
      </main>
    </div>
    <script src="./assets/data.js"></script>
    <script src="./assets/app.js"></script>
  </body>
</html>
```

占位页的 `data-page-root` 直接渲染：

```html
<section class="placeholder-panel">
  <h1>分区管理</h1>
  <p>该页面将在下一阶段扩展</p>
</section>
```

- [ ] **Step 4: 重新运行 smoke 校验**

Run: `bash scripts/verify-admin-ui.sh`

Expected: PASS，且 `rg -n "<svg" ui` 无输出。

- [ ] **Step 5: 提交共享 shell**

```bash
git add scripts/verify-admin-ui.sh ui
git commit -m "feat(ui): add shared admin shell and placeholders"
```

## Task 3: 完成 `Dashboard` 高保真首页

**Files:**
- Modify: `scripts/verify-admin-ui.sh`
- Modify: `ui/index.html`
- Modify: `ui/dashboard.html`
- Modify: `ui/assets/styles.css`
- Modify: `ui/assets/data.js`
- Modify: `ui/assets/app.js`

- [ ] **Step 1: 先扩展 smoke 校验，锁定 `Dashboard` 结构**

在 `scripts/verify-admin-ui.sh` 增加 `Dashboard` 页面关键标记检查：

```bash
dashboard_files=("ui/index.html" "ui/dashboard.html")
for file in "${dashboard_files[@]}"; do
  grep -q 'data-page="dashboard"' "$file" || { echo "missing dashboard page marker: $file" >&2; exit 1; }
done

grep -q 'renderDashboard' ui/assets/app.js || { echo "missing renderDashboard" >&2; exit 1; }
grep -q '在线节点数' ui/assets/data.js || { echo "missing dashboard metric copy" >&2; exit 1; }
grep -q '风险摘要' ui/assets/app.js || { echo "missing risk section" >&2; exit 1; }
grep -q '集群快照' ui/assets/app.js || { echo "missing cluster snapshot" >&2; exit 1; }
```

- [ ] **Step 2: 运行校验，确认当前失败**

Run: `bash scripts/verify-admin-ui.sh`

Expected: FAIL，因为 `Dashboard` 还没有完整 KPI、风险区和快照结构。

- [ ] **Step 3: 写最小但完整的 `Dashboard` 实现**

在 `ui/assets/data.js` 增加 Dashboard 数据：

```js
window.ADMIN_UI_DATA.dashboard = {
  metrics: [
    { label: "在线节点数", value: "3 / 3", hint: "Healthy 2 · Degraded 1" },
    { label: "异常节点数", value: "1", hint: "Follower latency elevated" },
    { label: "总 Group 数", value: "64", hint: "Leader 分布接近均衡" },
    { label: "当前连接数", value: "34,195", hint: "较 15m 前 +4.8%" },
  ],
  risks: [
    { level: "critical", title: "wk-node3 RPC Latency 持续抬升", link: "./nodes.html?status=degraded" },
    { level: "warning", title: "Follower replication lag 超过阈值", link: "./nodes.html?status=degraded" },
    { level: "info", title: "wk-node1 连接数高于集群均值", link: "./nodes.html" },
  ],
};
```

在 `ui/assets/app.js` 提供 `renderDashboard()`：

```js
function renderDashboard() {
  const { metrics, risks } = window.ADMIN_UI_DATA.dashboard;
  return `
    <section data-view="dashboard" class="page-stack">
      <header class="page-heading">
        <h1>Dashboard</h1>
        <p>把集群健康、异常线索和节点承载情况放在一个首屏里。</p>
      </header>
      <section class="metric-grid">...</section>
      <section class="dashboard-grid">
        <article class="panel">
          <h2>风险摘要</h2>
          ...
        </article>
        <article class="panel">
          <h2>集群快照</h2>
          ...
        </article>
      </section>
    </section>
  `;
}
```

在 `ui/assets/styles.css` 增加：

```css
.metric-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 16px;
}

.dashboard-grid {
  display: grid;
  grid-template-columns: 1.2fr 0.8fr;
  gap: 16px;
}
```

要求：

- `ui/index.html` 和 `ui/dashboard.html` 都渲染 `Dashboard`
- 首页保留 `15m / 1h / 6h` 的时间窗按钮占位
- 风险项可跳转 `nodes.html`
- 集群快照采用热度矩阵或状态块，不画复杂拓扑

- [ ] **Step 4: 重新运行 smoke 校验**

Run: `bash scripts/verify-admin-ui.sh`

Expected: PASS。

- [ ] **Step 5: 提交 `Dashboard`**

```bash
git add scripts/verify-admin-ui.sh ui
git commit -m "feat(ui): add dashboard prototype"
```

## Task 4: 完成 `节点管理` 列表页和详情抽屉

**Files:**
- Modify: `scripts/verify-admin-ui.sh`
- Modify: `ui/nodes.html`
- Modify: `ui/assets/styles.css`
- Modify: `ui/assets/data.js`
- Modify: `ui/assets/app.js`

- [ ] **Step 1: 先扩展 smoke 校验，锁定节点页表格和抽屉结构**

在 `scripts/verify-admin-ui.sh` 增加：

```bash
grep -q 'data-page="nodes"' ui/nodes.html || { echo "missing nodes marker" >&2; exit 1; }
grep -q '节点ID' ui/assets/app.js || { echo "missing node id header" >&2; exit 1; }
grep -q 'RPC Latency' ui/assets/app.js || { echo "missing latency header" >&2; exit 1; }
grep -q '查看详情' ui/assets/app.js || { echo "missing drawer trigger" >&2; exit 1; }
grep -q 'data-node-drawer' ui/assets/app.js || { echo "missing drawer container" >&2; exit 1; }
grep -q 'Follower replication lag > threshold' ui/assets/data.js || { echo "missing degraded hint" >&2; exit 1; }
```

- [ ] **Step 2: 运行校验，确认当前失败**

Run: `bash scripts/verify-admin-ui.sh`

Expected: FAIL，因为 `ui/nodes.html` 还没有表格字段和详情抽屉。

- [ ] **Step 3: 写最小但完整的节点页实现**

在 `ui/assets/data.js` 增加节点数据：

```js
window.ADMIN_UI_DATA.nodes = [
  {
    id: 1,
    name: "wk-node1",
    address: "wk-node1:5400",
    role: "Leader",
    status: "online",
    groups: 22,
    connections: 12480,
    latency: "4.8ms",
  },
  {
    id: 3,
    name: "wk-node3",
    address: "wk-node3:5400",
    role: "Follower",
    status: "degraded",
    groups: 21,
    connections: 9812,
    latency: "18.4ms",
    note: "Follower replication lag > threshold",
  },
];
```

在 `ui/assets/app.js` 增加：

```js
function renderNodes() {
  return `
    <section data-view="nodes" class="page-stack">
      <header class="page-heading">
        <h1>节点管理</h1>
        <p>主表就是入口，先看状态，再看承载，再进入节点详情抽屉。</p>
      </header>
      <section class="table-toolbar">...</section>
      <section class="table-panel">
        <table>...</table>
      </section>
      <aside data-node-drawer class="drawer hidden">...</aside>
    </section>
  `;
}

function bindNodeDrawer() {
  document.querySelectorAll("[data-open-node]").forEach((button) => {
    button.addEventListener("click", () => { /* 渲染静态详情并打开抽屉 */ });
  });
}
```

在 `ui/assets/styles.css` 增加：

```css
.table-panel {
  overflow: hidden;
  border: 1px solid var(--border-default);
  border-radius: var(--radius-card);
  background: var(--bg-panel);
}

.drawer {
  position: fixed;
  inset: 24px 24px 24px auto;
  width: min(420px, 92vw);
}
```

要求：

- 工具条包含搜索框、角色筛选、状态筛选、排序按钮
- 表格列包含 spec 中锁定的 8 个字段
- `degraded` 行展示额外提示
- 抽屉展示基础信息、近 15 分钟负载、重点 Group、最近异常事件和网络指标摘要

- [ ] **Step 4: 重新运行 smoke 校验**

Run: `bash scripts/verify-admin-ui.sh`

Expected: PASS。

- [ ] **Step 5: 提交节点页**

```bash
git add scripts/verify-admin-ui.sh ui
git commit -m "feat(ui): add nodes prototype"
```

## Task 5: 收尾占位页、统一 polish 和本地预览验证

**Files:**
- Modify: `scripts/verify-admin-ui.sh`
- Modify: `ui/index.html`
- Modify: `ui/dashboard.html`
- Modify: `ui/nodes.html`
- Modify: `ui/placeholder/groups.html`
- Modify: `ui/placeholder/network.html`
- Modify: `ui/placeholder/topology.html`
- Modify: `ui/placeholder/connections.html`
- Modify: `ui/assets/styles.css`
- Modify: `ui/assets/data.js`
- Modify: `ui/assets/app.js`

- [ ] **Step 1: 扩展最终 smoke 校验，锁定导航闭环和页面一致性**

在 `scripts/verify-admin-ui.sh` 增加：

```bash
grep -q 'pageHref' ui/assets/app.js || { echo "missing relative path helper" >&2; exit 1; }
grep -q 'dashboard.html' ui/assets/data.js || { echo "missing dashboard nav target" >&2; exit 1; }
grep -q 'nodes.html' ui/assets/data.js || { echo "missing nodes nav target" >&2; exit 1; }
grep -q 'placeholder/groups.html' ui/assets/data.js || { echo "missing groups nav target" >&2; exit 1; }
grep -q 'data-base="."' ui/index.html || { echo "index missing base marker" >&2; exit 1; }
grep -q 'data-base="."' ui/dashboard.html || { echo "dashboard missing base marker" >&2; exit 1; }
grep -q 'data-base="."' ui/nodes.html || { echo "nodes missing base marker" >&2; exit 1; }
for file in ui/placeholder/*.html; do
  grep -q 'data-base=".."' "$file" || { echo "placeholder missing base marker: $file" >&2; exit 1; }
done
```

再补一个样式完整性检查：

```bash
grep -q '\-\-accent-primary' ui/assets/styles.css || { echo "missing accent token" >&2; exit 1; }
grep -q 'drawer' ui/assets/styles.css || { echo "missing drawer styles" >&2; exit 1; }
```

- [ ] **Step 2: 运行校验，确认还有收尾项时先失败**

Run: `bash scripts/verify-admin-ui.sh`

Expected: 如果导航闭环或 token/抽屉样式还不完整则 FAIL；否则继续补齐再进入下一步。

- [ ] **Step 3: 做最终 polish**

收尾要求：

- 所有页面 title 合理命名
- `index.html`、`dashboard.html`、`nodes.html` 的菜单高亮一致
- 占位页文案和布局统一
- `Dashboard` 与 `节点管理` 的间距、阴影、边框、badge 风格一致
- 页面内所有图标都改为 Lucide Static CDN `<img>`
- 若有重复 DOM 结构，收敛到 `ui/assets/app.js` 的渲染函数中

- [ ] **Step 4: 跑完整 smoke 校验并本地预览**

Run: `bash scripts/verify-admin-ui.sh`

Expected: PASS

Run:

```bash
python3 -m http.server 5301 >/tmp/wukong-admin-ui-http.log 2>&1 &
server_pid=$!
trap 'kill $server_pid' EXIT
sleep 1
curl -I http://localhost:5301/ui/
curl -I http://localhost:5301/ui/dashboard.html
curl -I http://localhost:5301/ui/nodes.html
kill $server_pid
trap - EXIT
```

Expected: `curl` 返回 `200 OK`，浏览器可打开：

- `http://localhost:5301/ui/`
- `http://localhost:5301/ui/dashboard.html`
- `http://localhost:5301/ui/nodes.html`

人工检查：

- 侧栏、顶部栏、主内容区节奏一致
- `Dashboard` 首屏是“概况 -> 风险 -> 快照/趋势”
- `节点管理` 首屏是“筛选 -> 表格 -> 抽屉”
- 占位页不显得像废页

- [ ] **Step 5: 运行最终核验并提交**

Run:

```bash
bash scripts/verify-admin-ui.sh
python3 -m http.server 5301 >/tmp/wukong-admin-ui-http.log 2>&1 &
server_pid=$!
trap 'kill $server_pid' EXIT
sleep 1
curl -I http://localhost:5301/ui/
curl -I http://localhost:5301/ui/nodes.html
kill $server_pid
trap - EXIT
```

Expected: smoke PASS，静态服务可启动，关键页面返回 `200 OK`。

```bash
git add scripts/verify-admin-ui.sh ui
git commit -m "feat(ui): finish distributed admin ui phase 1"
```

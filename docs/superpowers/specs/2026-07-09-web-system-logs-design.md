# Web System Logs Design

## Summary

在 `web/` 的集群运维导航下新增“系统日志”菜单和页面，用于按节点查看 WuKongIM 普通进程日志。页面复用现有日志读取能力，支持固定日志源 `app`、`warn`、`error`、`debug`，并继续提供关键字、级别筛选、分页加载和尾部跟随。

## Background

后端已经提供普通进程日志链路：

- `GET /manager/app-logs/sources`
- `GET /manager/app-logs`
- `GET /manager/app-logs/stream`

该链路通过 `internal/usecase/management` 与 `internal/infra/cluster` 选择本地或远端节点读取，并由 `internal/log.AppLogReader` 限定固定文件源，避免暴露任意文件路径。前端已经有 `AppLogsPage` 和 `AppLogsPanel`，但当前未作为集群运维的独立菜单入口暴露，旧 `/app-logs` 路径也被重定向到诊断追踪页。

## Goals

- 在“集群运维”侧边栏增加“系统日志”菜单。
- 新增 `/cluster/system-logs` 页面，直接展示现有日志面板。
- 页面文案使用“系统日志 / System Logs”，说明其读取的是节点普通进程日志。
- 保留旧 `/app-logs` 入口，重定向到 `/cluster/system-logs`。
- 用前端测试覆盖导航、路由、页面标题和日志读取参数。

## Non-Goals

- 不新增后端日志读取接口。
- 不开放任意日志文件路径。
- 不读取 Controller、Slot、Channel、Raft 分布式日志；这些仍由各自页面负责。
- 不改变 manager 权限模型，继续使用现有 `cluster.log:r` 后端权限。

## Design

### Navigation

在 `web/src/lib/navigation.ts` 的 cluster section 下新增一项：

- `href`: `/cluster/system-logs`
- title: `nav.systemLogs.title`
- description: `nav.systemLogs.description`
- path label: `nav.path.cluster.systemLogs`
- icon: 使用日志/文件类 `lucide-react` 图标
- legacy alias: `/app-logs`

菜单位置放在 `Config` 与 `Diagnostics` 附近，因为系统日志是运维排障入口，不属于业务管理或系统设置。

### Routing

在 `web/src/app/router.tsx` 注册：

- `/cluster/system-logs` -> `AppLogsPage`
- `/app-logs` -> `/cluster/system-logs`

诊断页继续只保留 tracing tab，不重新引入日志 tab。

### Page Behavior

复用 `web/src/pages/app-logs/page.tsx`：

- `AppLogsPage` 页面标题和 `PageHeader` 改为“系统日志”。
- `AppLogsPanel` 继续加载节点、日志源、日志条目和尾部跟随。
- 默认读取本地节点优先，日志源仍由后端返回的 `app/warn/error/debug` 控制。
- 级别筛选仍发送 `levels=INFO/WARN/ERROR/DEBUG`，不在前端推断文件语义。
- 日志输出区域采用黑底 terminal console 风格，筛选区继续保持现有浅色运维控制台风格。
- Console 行内使用等宽字体和稳定列：时间、级别、消息/模块/调用点；`ERROR/WARN/INFO/DEBUG` 用低饱和色区分。
- 原始 `raw` 和结构化 `fields` 继续展示在日志行下方，使用 secondary 文本，便于排障复制。

### i18n

新增或调整中英文文案：

- `nav.systemLogs.*`
- `nav.path.cluster.systemLogs`
- `appLogs.title`
- `appLogs.description`
- `diagnostics.tabs.appLogs` 可保留但不展示。

### Tests

前端测试覆盖：

- navigation metadata 包含 `/cluster/system-logs`，旧 `/app-logs` 指向新路径。
- router 能渲染 `/cluster/system-logs` 页面。
- sidebar 在集群运维下显示“系统日志”。
- app logs 页面默认按选中节点读取 `app` 源，并仍支持日志源、级别、尾部跟随。

## Verification

至少运行：

- `cd web && bun run test -- src/lib/navigation.test.ts src/app/router.test.tsx src/app/layout/sidebar-nav.test.tsx src/pages/app-logs/page.test.tsx`
- `cd web && bunx tsc -b`
- `cd web && bun run build`
- `git diff --check`

若 `bun run build` 造成 `web/dist/index.html` hash 变更，而本次目标是源码变更，则恢复该生成文件。

import { AlertTriangle, Clock3, Cpu, Filter, Layers3, RefreshCw, Search, TrendingUp } from "lucide-react"
import { Area, AreaChart, Bar, BarChart, CartesianGrid, Cell, Line, LineChart, Pie, PieChart, XAxis, YAxis } from "recharts"

import { ChartContainer, ChartLegend, ChartLegendContent, ChartTooltip, ChartTooltipContent, type ChartConfig } from "@/components/ui/chart"

const summaryMetrics = [
  { label: "Total goroutines", value: "1,284", detail: "+38 last sample", tone: "text-foreground" },
  { label: "Runnable", value: "186", detail: "14.5% of total", tone: "text-success" },
  { label: "Waiting", value: "921", detail: "I/O, channel, timer", tone: "text-muted-foreground" },
  { label: "Hot groups", value: "7", detail: "above baseline", tone: "text-warning" },
]

// 时间序列数据 - Goroutine 数量趋势
const trendData = [
  { time: "00:00", total: 1156, runnable: 142, waiting: 1014 },
  { time: "00:10", total: 1189, runnable: 156, waiting: 1033 },
  { time: "00:20", total: 1223, runnable: 168, waiting: 1055 },
  { time: "00:30", total: 1198, runnable: 151, waiting: 1047 },
  { time: "00:40", total: 1245, runnable: 174, waiting: 1071 },
  { time: "00:50", total: 1268, runnable: 182, waiting: 1086 },
  { time: "01:00", total: 1284, runnable: 186, waiting: 1098 },
]

const trendChartConfig = {
  total: {
    label: "Total",
    color: "hsl(158, 64%, 28%)",
  },
  runnable: {
    label: "Runnable",
    color: "hsl(140, 64%, 32%)",
  },
  waiting: {
    label: "Waiting",
    color: "hsl(158, 6%, 46%)",
  },
} satisfies ChartConfig

// 子系统分布数据
const subsystemData = [
  { name: "gateway/session", count: 438, fill: "hsl(158, 64%, 28%)" },
  { name: "cluster/transport", count: 282, fill: "hsl(203, 55%, 40%)" },
  { name: "raft/control", count: 218, fill: "hsl(256, 35%, 56%)" },
  { name: "storage/pebble", count: 154, fill: "hsl(28, 75%, 38%)" },
  { name: "runtime/delivery", count: 116, fill: "hsl(20, 33%, 42%)" },
  { name: "other", count: 76, fill: "hsl(220, 8%, 55%)" },
]

const subsystemChartConfig = {
  count: {
    label: "Goroutines",
  },
  gateway: {
    label: "gateway/session",
    color: "hsl(158, 64%, 28%)",
  },
  cluster: {
    label: "cluster/transport",
    color: "hsl(203, 55%, 40%)",
  },
  raft: {
    label: "raft/control",
    color: "hsl(256, 35%, 56%)",
  },
  storage: {
    label: "storage/pebble",
    color: "hsl(28, 75%, 38%)",
  },
  delivery: {
    label: "runtime/delivery",
    color: "hsl(20, 33%, 42%)",
  },
  other: {
    label: "other",
    color: "hsl(220, 8%, 55%)",
  },
} satisfies ChartConfig

// 状态分布数据 - 饼图
const stateData = [
  { state: "I/O wait", count: 462, fill: "hsl(158, 64%, 28%)" },
  { state: "chan receive", count: 308, fill: "hsl(203, 55%, 40%)" },
  { state: "runnable", count: 186, fill: "hsl(140, 64%, 32%)" },
  { state: "select", count: 154, fill: "hsl(256, 35%, 56%)" },
  { state: "syscall", count: 103, fill: "hsl(28, 75%, 38%)" },
  { state: "semacquire", count: 71, fill: "hsl(220, 8%, 55%)" },
]

const stateChartConfig = {
  count: {
    label: "Count",
  },
  ioWait: {
    label: "I/O wait",
    color: "hsl(158, 64%, 28%)",
  },
  chanReceive: {
    label: "chan receive",
    color: "hsl(203, 55%, 40%)",
  },
  runnable: {
    label: "runnable",
    color: "hsl(140, 64%, 32%)",
  },
  select: {
    label: "select",
    color: "hsl(256, 35%, 56%)",
  },
  syscall: {
    label: "syscall",
    color: "hsl(28, 75%, 38%)",
  },
  semacquire: {
    label: "semacquire",
    color: "hsl(220, 8%, 55%)",
  },
} satisfies ChartConfig

// Top groups 趋势对比
const topGroupsTrendData = [
  { time: "00:00", session: 202, reactor: 164, flush: 129, mailbox: 77 },
  { time: "00:10", session: 206, reactor: 166, flush: 127, mailbox: 81 },
  { time: "00:20", session: 208, reactor: 165, flush: 125, mailbox: 85 },
  { time: "00:30", session: 210, reactor: 167, flush: 123, mailbox: 89 },
  { time: "00:40", session: 212, reactor: 168, flush: 122, mailbox: 92 },
  { time: "00:50", session: 213, reactor: 168, flush: 121, mailbox: 95 },
  { time: "01:00", session: 214, reactor: 168, flush: 121, mailbox: 96 },
]

const topGroupsTrendConfig = {
  session: {
    label: "session.writeLoop",
    color: "hsl(158, 64%, 28%)",
  },
  reactor: {
    label: "Reactor.run",
    color: "hsl(203, 55%, 40%)",
  },
  flush: {
    label: "SegmentWriter.flushLoop",
    color: "hsl(256, 35%, 56%)",
  },
  mailbox: {
    label: "Mailbox.worker",
    color: "hsl(28, 75%, 38%)",
  },
} satisfies ChartConfig

const topGroups = [
  {
    name: "pkg/gateway/session.(*Session).writeLoop",
    count: 214,
    state: "I/O wait",
    owner: "gateway",
    trend: "+12",
  },
  {
    name: "pkg/clusterv2/slots.(*Reactor).run",
    count: 168,
    state: "select",
    owner: "cluster",
    trend: "+4",
  },
  {
    name: "pkg/raftlog.(*SegmentWriter).flushLoop",
    count: 121,
    state: "chan receive",
    owner: "storage",
    trend: "-8",
  },
  {
    name: "internal/runtime/delivery.(*Mailbox).worker",
    count: 96,
    state: "runnable",
    owner: "delivery",
    trend: "+19",
  },
]

const captureChecklist = [
  "Group by normalized stack root",
  "Compare against previous sample",
  "Flag long-lived runnable groups",
  "Keep raw stack sample expandable",
]

export function GoroutinesPage() {
  return (
    <section className="space-y-5 py-6" aria-labelledby="goroutines-title">
      {/* Summary metrics */}
      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
        {summaryMetrics.map((metric) => (
          <article className="content-panel" key={metric.label}>
            <div className="text-xs font-medium text-muted-foreground">{metric.label}</div>
            <div className={`mt-3 text-2xl font-semibold ${metric.tone}`}>{metric.value}</div>
            <div className="mt-2 text-xs text-muted-foreground">{metric.detail}</div>
          </article>
        ))}
      </div>

      {/* Goroutine count trend chart */}
      <section className="content-panel" aria-labelledby="trend-title">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
          <div>
            <h2 className="text-base font-semibold text-foreground" id="trend-title">
              Goroutine count trend
            </h2>
            <p className="mt-1 text-sm text-muted-foreground">
              Real-time monitoring of total, runnable, and waiting goroutines over the last hour.
            </p>
          </div>
          <span className="inline-flex w-fit items-center gap-2 rounded-md border border-border bg-muted px-2.5 py-1 text-xs font-medium text-muted-foreground">
            <TrendingUp className="size-3.5" aria-hidden />
            Live data
          </span>
        </div>
        <ChartContainer config={trendChartConfig} className="mt-5 h-[300px] w-full">
          <AreaChart data={trendData}>
            <defs>
              <linearGradient id="fillTotal" x1="0" y1="0" x2="0" y2="1">
                <stop offset="5%" stopColor="hsl(158, 64%, 28%)" stopOpacity={0.3} />
                <stop offset="95%" stopColor="hsl(158, 64%, 28%)" stopOpacity={0} />
              </linearGradient>
              <linearGradient id="fillRunnable" x1="0" y1="0" x2="0" y2="1">
                <stop offset="5%" stopColor="hsl(140, 64%, 32%)" stopOpacity={0.3} />
                <stop offset="95%" stopColor="hsl(140, 64%, 32%)" stopOpacity={0} />
              </linearGradient>
            </defs>
            <CartesianGrid strokeDasharray="3 3" stroke="hsl(var(--color-border))" vertical={false} />
            <XAxis
              dataKey="time"
              tickLine={false}
              axisLine={false}
              tickMargin={8}
              tick={{ fill: "hsl(var(--color-muted-foreground))", fontSize: 12 }}
            />
            <YAxis
              tickLine={false}
              axisLine={false}
              tickMargin={8}
              tick={{ fill: "hsl(var(--color-muted-foreground))", fontSize: 12 }}
            />
            <ChartTooltip cursor={false} content={<ChartTooltipContent />} />
            <Area
              type="monotone"
              dataKey="total"
              stroke="hsl(158, 64%, 28%)"
              strokeWidth={2}
              fill="url(#fillTotal)"
              fillOpacity={0.4}
            />
            <Area
              type="monotone"
              dataKey="runnable"
              stroke="hsl(140, 64%, 32%)"
              strokeWidth={2}
              fill="url(#fillRunnable)"
              fillOpacity={0.4}
            />
          </AreaChart>
        </ChartContainer>
      </section>

      <div className="grid gap-5 xl:grid-cols-2">
        {/* Subsystem distribution - Bar chart */}
        <section className="content-panel" aria-labelledby="subsystem-title">
          <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
            <div>
              <h2 className="text-base font-semibold text-foreground" id="subsystem-title">
                By subsystem
              </h2>
              <p className="mt-1 text-sm text-muted-foreground">
                Distribution across runtime subsystems.
              </p>
            </div>
            <Layers3 className="size-5 text-muted-foreground" aria-hidden />
          </div>
          <ChartContainer config={subsystemChartConfig} className="mt-5 h-[320px] w-full">
            <BarChart data={subsystemData} layout="vertical">
              <CartesianGrid strokeDasharray="3 3" stroke="hsl(var(--color-border))" horizontal={false} />
              <XAxis
                type="number"
                tickLine={false}
                axisLine={false}
                tick={{ fill: "hsl(var(--color-muted-foreground))", fontSize: 12 }}
              />
              <YAxis
                type="category"
                dataKey="name"
                tickLine={false}
                axisLine={false}
                tick={{ fill: "hsl(var(--color-muted-foreground))", fontSize: 12 }}
                width={140}
              />
              <ChartTooltip cursor={false} content={<ChartTooltipContent hideLabel />} />
              <Bar dataKey="count" radius={[0, 4, 4, 0]}>
                {subsystemData.map((entry, index) => (
                  <Cell key={`cell-${index}`} fill={entry.fill} />
                ))}
              </Bar>
            </BarChart>
          </ChartContainer>
        </section>

        {/* State distribution - Pie chart */}
        <section className="content-panel" aria-labelledby="state-title">
          <div className="flex items-center justify-between gap-3">
            <div>
              <h2 className="text-base font-semibold text-foreground" id="state-title">
                State distribution
              </h2>
              <p className="mt-1 text-sm text-muted-foreground">
                Runtime state breakdown for all goroutines.
              </p>
            </div>
            <Cpu className="size-5 text-muted-foreground" aria-hidden />
          </div>
          <ChartContainer config={stateChartConfig} className="mt-5 h-[320px] w-full">
            <PieChart>
              <ChartTooltip content={<ChartTooltipContent hideLabel />} />
              <Pie
                data={stateData}
                dataKey="count"
                nameKey="state"
                cx="50%"
                cy="50%"
                innerRadius={60}
                outerRadius={100}
                strokeWidth={2}
                stroke="hsl(var(--color-background))"
              >
                {stateData.map((entry, index) => (
                  <Cell key={`cell-${index}`} fill={entry.fill} />
                ))}
              </Pie>
              <ChartLegend content={<ChartLegendContent />} />
            </PieChart>
          </ChartContainer>
        </section>
      </div>

      {/* Top groups trend */}
      <section className="content-panel" aria-labelledby="groups-trend-title">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
          <div>
            <h2 className="text-base font-semibold text-foreground" id="groups-trend-title">
              Top groups trend
            </h2>
            <p className="mt-1 text-sm text-muted-foreground">
              Historical growth patterns of the most active goroutine groups.
            </p>
          </div>
          <div className="flex items-center gap-2">
            <button className="icon-button" type="button" aria-label="Filter goroutine groups">
              <Filter className="size-4" aria-hidden />
            </button>
            <button className="icon-button" type="button" aria-label="Search goroutine groups">
              <Search className="size-4" aria-hidden />
            </button>
          </div>
        </div>
        <ChartContainer config={topGroupsTrendConfig} className="mt-5 h-[280px] w-full">
          <LineChart data={topGroupsTrendData}>
            <CartesianGrid strokeDasharray="3 3" stroke="hsl(var(--color-border))" vertical={false} />
            <XAxis
              dataKey="time"
              tickLine={false}
              axisLine={false}
              tickMargin={8}
              tick={{ fill: "hsl(var(--color-muted-foreground))", fontSize: 12 }}
            />
            <YAxis
              tickLine={false}
              axisLine={false}
              tickMargin={8}
              tick={{ fill: "hsl(var(--color-muted-foreground))", fontSize: 12 }}
            />
            <ChartTooltip content={<ChartTooltipContent />} />
            <ChartLegend content={<ChartLegendContent />} />
            <Line type="monotone" dataKey="session" stroke="hsl(158, 64%, 28%)" strokeWidth={2} dot={false} />
            <Line type="monotone" dataKey="reactor" stroke="hsl(203, 55%, 40%)" strokeWidth={2} dot={false} />
            <Line type="monotone" dataKey="flush" stroke="hsl(256, 35%, 56%)" strokeWidth={2} dot={false} />
            <Line type="monotone" dataKey="mailbox" stroke="hsl(28, 75%, 38%)" strokeWidth={2} dot={false} />
          </LineChart>
        </ChartContainer>
      </section>

      <div className="grid gap-5 xl:grid-cols-[minmax(0,1fr)_340px]">
        <div className="min-w-0 space-y-5">
          {/* Top goroutine groups table */}
          <section className="content-panel" aria-labelledby="groups-title">
            <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
              <div>
                <h2 className="text-base font-semibold text-foreground" id="groups-title">
                  Top goroutine groups
                </h2>
                <p className="mt-1 text-sm text-muted-foreground">
                  Normalized stack roots ranked by current sample size.
                </p>
              </div>
            </div>
            <div className="mt-5 overflow-hidden rounded-lg border border-border">
              <div className="grid grid-cols-[minmax(0,1fr)_88px_112px_72px] border-b border-border bg-muted/60 px-3 py-2 text-xs font-medium text-muted-foreground">
                <span>Stack root</span>
                <span className="text-right">Count</span>
                <span className="text-right">State</span>
                <span className="text-right">Delta</span>
              </div>
              {topGroups.map((group) => (
                <div
                  className="grid grid-cols-[minmax(0,1fr)_88px_112px_72px] items-center border-b border-border px-3 py-3 text-sm last:border-b-0"
                  key={group.name}
                >
                  <div className="min-w-0">
                    <div className="truncate font-medium text-foreground">{group.name}</div>
                    <div className="mt-1 text-xs text-muted-foreground">{group.owner}</div>
                  </div>
                  <div className="text-right font-medium text-foreground">{group.count}</div>
                  <div className="text-right text-muted-foreground">{group.state}</div>
                  <div className={group.trend.startsWith("+") ? "text-right text-warning" : "text-right text-success"}>
                    {group.trend}
                  </div>
                </div>
              ))}
            </div>
          </section>
        </div>

        <aside className="space-y-5" aria-label="Goroutines diagnostics context">
          <section className="content-panel">
            <div className="flex items-center justify-between gap-3">
              <h2 className="text-sm font-semibold text-foreground">Sample status</h2>
              <RefreshCw className="size-4 text-muted-foreground" aria-hidden />
            </div>
            <div className="mt-4 space-y-3 text-sm">
              <div className="flex items-center justify-between gap-3">
                <span className="text-muted-foreground">Source</span>
                <span className="font-medium text-foreground">Mock data</span>
              </div>
              <div className="flex items-center justify-between gap-3">
                <span className="text-muted-foreground">Window</span>
                <span className="font-medium text-foreground">Last 60s</span>
              </div>
              <div className="flex items-center justify-between gap-3">
                <span className="text-muted-foreground">Baseline</span>
                <span className="font-medium text-muted-foreground">Not connected</span>
              </div>
            </div>
          </section>

          <section className="content-panel">
            <div className="flex items-start gap-3">
              <div className="rounded-lg border border-warning/30 bg-warning/10 p-2 text-warning">
                <AlertTriangle className="size-4" aria-hidden />
              </div>
              <div>
                <h2 className="text-sm font-semibold text-foreground">Attention pattern</h2>
                <p className="mt-2 text-sm leading-6 text-muted-foreground">
                  Delivery workers are growing faster than the sample baseline. When the API is wired, this area should
                  link directly to representative stacks.
                </p>
              </div>
            </div>
          </section>

          <section className="content-panel">
            <h2 className="text-sm font-semibold text-foreground">Capture plan</h2>
            <div className="mt-4 space-y-3">
              {captureChecklist.map((item) => (
                <div className="flex items-start gap-2 text-sm" key={item}>
                  <div className="mt-1 size-2.5 shrink-0 rounded-full bg-primary" aria-hidden />
                  <span className="text-muted-foreground">{item}</span>
                </div>
              ))}
            </div>
          </section>

          <section className="content-panel">
            <div className="flex items-center gap-2 text-sm font-semibold text-foreground">
              <Clock3 className="size-4 text-muted-foreground" aria-hidden />
              Future controls
            </div>
            <p className="mt-2 text-sm leading-6 text-muted-foreground">
              Keep this page static for now. Later controls can add sample interval, node selector, state filter, and raw
              stack export.
            </p>
          </section>
        </aside>
      </div>
    </section>
  )
}

import { useState } from "react"
import { useIntl } from "react-intl"

import type { GoroutineModuleSnapshot, RealtimeMonitorGoroutines } from "@/lib/manager-api.types"
import { cn } from "@/lib/utils"

type GoroutineMonitorTableProps = {
  data: RealtimeMonitorGoroutines
  showComplete: boolean
}

const numberCell = "px-3 py-2 text-right font-mono text-xs tabular-nums text-foreground"

export function GoroutineMonitorTable({ data, showComplete }: GoroutineMonitorTableProps) {
  const intl = useIntl()

  return (
    <section className="space-y-4" data-cluster-monitor-surface="goroutines">
      <div>
        <h2 className="text-lg font-semibold text-foreground">{intl.formatMessage({ id: "clusterMonitor.goroutines.title" })}</h2>
        <p className="mt-1 text-sm text-muted-foreground">{intl.formatMessage({ id: "clusterMonitor.goroutines.description" })}</p>
      </div>

      {data.nodes.map((node) => (
        <article className="overflow-hidden rounded-lg border border-border bg-card" key={node.node_id}>
          <header className="flex flex-wrap items-center justify-between gap-3 border-b border-border bg-muted/35 px-4 py-3">
            <div>
              <div className="flex items-center gap-2">
                <h3 className="font-semibold text-foreground">{node.name || `node-${node.node_id}`}</h3>
                <span className="font-mono text-xs text-muted-foreground">#{node.node_id}</span>
                <span className="rounded-full border border-border px-2 py-0.5 text-[11px] text-muted-foreground">{node.status}</span>
              </div>
              {node.snapshot ? (
                <p className="mt-1 font-mono text-[11px] text-muted-foreground">
                  {intl.formatMessage({ id: "clusterMonitor.goroutines.boot" }, { boot: node.snapshot.boot_id })}
                </p>
              ) : null}
            </div>
            {node.snapshot ? (
              <div className="flex flex-wrap gap-3 text-xs text-muted-foreground">
                <NodeTotal
                  label={intl.formatMessage({
                    id: "clusterMonitor.goroutines.processTotal",
                  })}
                  value={node.snapshot.process_total}
                />
                <NodeTotal
                  label={intl.formatMessage({
                    id: "clusterMonitor.goroutines.managedTotal",
                  })}
                  value={node.snapshot.managed_total}
                />
                <NodeTotal
                  label={intl.formatMessage({
                    id: "clusterMonitor.goroutines.unmanagedTotal",
                  })}
                  value={node.snapshot.unmanaged_total}
                />
              </div>
            ) : (
              <span className="text-sm text-warning">
                {node.error ||
                  intl.formatMessage({
                    id: "clusterMonitor.goroutines.unsupported",
                  })}
              </span>
            )}
          </header>

          {node.snapshot ? <ModuleTable modules={node.snapshot.modules} showComplete={showComplete} /> : null}
        </article>
      ))}
    </section>
  )
}

function NodeTotal({ label, value, className }: { label: string; value: number; className?: string }) {
  return (
    <span className={cn("inline-flex items-baseline gap-1", className)}>
      <span>{label}</span>
      <strong className="font-mono text-sm tabular-nums text-foreground">{value}</strong>
    </span>
  )
}

function ModuleTable({ modules, showComplete }: { modules: GoroutineModuleSnapshot[]; showComplete: boolean }) {
  const intl = useIntl()
  const visible = showComplete
    ? modules
    : modules.filter(
        (module) => module.active > 0 || (module.pool_capacity ?? 0) > 0 || module.panics > 0 || (module.rejected_total ?? 0) > 0,
      )

  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[900px] border-collapse">
        <thead>
          <tr className="border-b border-border text-left text-[11px] uppercase tracking-wide text-muted-foreground">
            <th className="px-4 py-2 font-medium">
              {intl.formatMessage({
                id: "clusterMonitor.goroutines.moduleTask",
              })}
            </th>
            <NumericHeader id="clusterMonitor.goroutines.active" />
            <NumericHeader id="clusterMonitor.goroutines.peak" />
            <NumericHeader id="clusterMonitor.goroutines.busyCapacity" />
            <NumericHeader id="clusterMonitor.goroutines.queue" />
            <NumericHeader id="clusterMonitor.goroutines.rejected" />
            <NumericHeader id="clusterMonitor.goroutines.panics" />
            <th className="px-3 py-2 text-left font-medium">{intl.formatMessage({ id: "clusterMonitor.goroutines.health" })}</th>
          </tr>
        </thead>
        <tbody>
          {visible.map((module) => (
            <ModuleRows key={module.module} module={module} showComplete={showComplete} />
          ))}
        </tbody>
      </table>
      {visible.length === 0 ? (
        <p className="px-4 py-6 text-sm text-muted-foreground">{intl.formatMessage({ id: "clusterMonitor.goroutines.noManaged" })}</p>
      ) : null}
    </div>
  )
}

function NumericHeader({ id }: { id: string }) {
  const intl = useIntl()
  return <th className="px-3 py-2 text-right font-medium">{intl.formatMessage({ id })}</th>
}

function ModuleRows({ module, showComplete }: { module: GoroutineModuleSnapshot; showComplete: boolean }) {
  const intl = useIntl()
  const [expanded, setExpanded] = useState(false)
  const tasks = showComplete
    ? module.tasks
    : module.tasks.filter((task) => task.active > 0 || (task.pool_capacity ?? 0) > 0 || task.panics > 0 || (task.rejected_total ?? 0) > 0)
  return (
    <>
      <tr className="border-b border-border/70 bg-background/40">
        <td className="px-4 py-2 text-sm font-semibold text-foreground">
          <button
            aria-expanded={expanded}
            aria-label={intl.formatMessage(
              {
                id: expanded ? "clusterMonitor.goroutines.collapse" : "clusterMonitor.goroutines.expand",
              },
              { module: module.module },
            )}
            className="inline-flex items-center gap-2 hover:text-primary"
            onClick={() => setExpanded((current) => !current)}
            type="button"
          >
            <span className="font-mono text-xs">{expanded ? "−" : "+"}</span>
            {module.module}
          </button>
        </td>
        <MetricCells item={module} />
        <HealthCell health={module.health} />
      </tr>
      {expanded
        ? tasks.map((task) => (
            <tr className="border-b border-border/45 last:border-b-0" key={task.task}>
              <td className="px-4 py-2 pl-8">
                <div className="flex items-center gap-2">
                  <span className="font-mono text-xs text-foreground">{task.name}</span>
                  <span className="rounded border border-border px-1.5 py-0.5 text-[10px] text-muted-foreground">{task.kind}</span>
                  {task.critical ? <span className="size-1.5 rounded-full bg-destructive" title="critical" /> : null}
                </div>
              </td>
              <MetricCells item={task} />
              <HealthCell health={task.health} reason={task.health_reason} />
            </tr>
          ))
        : null}
    </>
  )
}

function HealthCell({ health, reason }: { health: "normal" | "warning" | "critical"; reason?: string }) {
  const intl = useIntl()
  return (
    <td className="px-3 py-2 text-left text-xs">
      <span
        className={cn(
          "rounded-full px-2 py-0.5",
          health === "critical" && "bg-destructive/15 text-destructive",
          health === "warning" && "bg-warning/15 text-warning",
          health === "normal" && "bg-success/15 text-success",
        )}
        title={reason}
      >
        {intl.formatMessage({
          id: `clusterMonitor.goroutines.health.${health}`,
        })}
      </span>
    </td>
  )
}

function MetricCells({
  item,
}: {
  item: {
    active: number
    process_peak: number
    busy_tasks?: number
    pool_capacity?: number
    queue_depth?: number
    queue_capacity?: number
    rejected_total?: number
    panics: number
  }
}) {
  return (
    <>
      <td className={numberCell}>{item.active}</td>
      <td className={numberCell}>{item.process_peak}</td>
      <td className={numberCell}>{item.pool_capacity ? `${item.busy_tasks ?? 0}/${item.pool_capacity}` : "—"}</td>
      <td className={numberCell}>
        {item.queue_capacity ? `${item.queue_depth ?? 0}/${item.queue_capacity}` : (item.queue_depth ?? 0)}
      </td>
      <td className={numberCell}>{item.rejected_total ?? 0}</td>
      <td className={cn(numberCell, item.panics > 0 && "text-destructive")}>{item.panics}</td>
    </>
  )
}

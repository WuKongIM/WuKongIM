import { Cell, Pie, PieChart, ResponsiveContainer } from "recharts"
import { useIntl } from "react-intl"
import { Link } from "react-router-dom"

import { Button } from "@/components/ui/button"
import { SectionCard } from "@/components/shell/section-card"
import { cn } from "@/lib/utils"
import type {
  ManagerChannelClusterSummaryResponse,
  ManagerOverviewResponse,
} from "@/lib/manager-api.types"

type SlotChannelHealthProps = {
  slots: ManagerOverviewResponse["slots"]
  channelCluster: ManagerChannelClusterSummaryResponse
}

export function SlotChannelHealth({ slots, channelCluster }: SlotChannelHealthProps) {
  const intl = useIntl()

  const total = slots.total
  const ready = slots.ready
  const percent = total > 0 ? Math.round((ready / total) * 100) : 0

  const rawSegments = [
    { name: "ready", value: slots.ready, fill: "var(--chart-1)" },
    { name: "quorum_lost", value: slots.quorum_lost, fill: "var(--chart-4)" },
    { name: "leader_missing", value: slots.leader_missing, fill: "var(--chart-3)" },
    { name: "unreported", value: slots.unreported, fill: "var(--chart-5)" },
  ]
  const donutData = rawSegments.filter((s) => s.value > 0)

  return (
    <SectionCard title={intl.formatMessage({ id: "dashboard.health.cardTitle" })}>
      {/* Donut chart */}
      <div className="flex justify-center">
        <div className="relative h-36 w-36">
          <ResponsiveContainer width="100%" height="100%">
            <PieChart>
              <Pie
                data={donutData}
                dataKey="value"
                innerRadius={42}
                outerRadius={62}
                paddingAngle={2}
                strokeWidth={0}
                isAnimationActive={false}
              >
                {donutData.map((entry) => (
                  <Cell key={entry.name} fill={entry.fill} />
                ))}
              </Pie>
            </PieChart>
          </ResponsiveContainer>
          {/* Center overlay */}
          <div className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center">
            <span className="font-mono text-lg font-semibold leading-tight">
              {intl.formatMessage(
                { id: "dashboard.health.slotsCenter" },
                { ready, total },
              )}
            </span>
            <span className="text-xs text-muted-foreground">
              {intl.formatMessage(
                { id: "dashboard.health.slotsCenterSub" },
                { percent },
              )}
            </span>
          </div>
        </div>
      </div>

      {/* Slot counters */}
      <div className="mt-3 grid gap-1">
        <div className="flex items-center justify-between text-sm">
          <span className="text-muted-foreground">
            {intl.formatMessage(
              { id: "dashboard.health.quorumLost" },
              { count: slots.quorum_lost },
            )}
          </span>
          <span className={cn(slots.quorum_lost > 0 ? "text-warning" : "text-muted-foreground")}>
            {slots.quorum_lost}
          </span>
        </div>
        <div className="flex items-center justify-between text-sm">
          <span className="text-muted-foreground">
            {intl.formatMessage(
              { id: "dashboard.health.leaderMissing" },
              { count: slots.leader_missing },
            )}
          </span>
          <span className={cn(slots.leader_missing > 0 ? "text-warning" : "text-muted-foreground")}>
            {slots.leader_missing}
          </span>
        </div>
        <div className="flex items-center justify-between text-sm">
          <span className="text-muted-foreground">
            {intl.formatMessage(
              { id: "dashboard.health.unreported" },
              { count: slots.unreported },
            )}
          </span>
          <span className="text-muted-foreground">{slots.unreported}</span>
        </div>
      </div>

      {/* Channel health section */}
      <div className="mt-3 border-t border-border/80 pt-3">
        <p className="mb-2 text-xs uppercase tracking-[0.14em] text-muted-foreground">
          {intl.formatMessage({ id: "dashboard.channelHealthTitle" })}
        </p>
        <div className="grid gap-1">
          <div className="flex items-center justify-between text-sm">
            <span className="text-muted-foreground">
              {intl.formatMessage(
                { id: "dashboard.channelHealthHealthy" },
                { count: channelCluster.healthy },
              )}
            </span>
            <span className="text-success">{channelCluster.healthy}</span>
          </div>
          <div className="flex items-center justify-between text-sm">
            <span className="text-muted-foreground">
              {intl.formatMessage(
                { id: "dashboard.channelHealthIsrInsufficient" },
                { count: channelCluster.isr_insufficient },
              )}
            </span>
            <span
              className={cn(
                channelCluster.isr_insufficient > 0
                  ? "text-warning"
                  : "text-muted-foreground",
              )}
            >
              {channelCluster.isr_insufficient}
            </span>
          </div>
          <div className="flex items-center justify-between text-sm">
            <span className="text-muted-foreground">
              {intl.formatMessage(
                { id: "dashboard.channelHealthNoLeader" },
                { count: channelCluster.no_leader },
              )}
            </span>
            <span
              className={cn(
                channelCluster.no_leader > 0
                  ? "text-destructive"
                  : "text-muted-foreground",
              )}
            >
              {channelCluster.no_leader}
            </span>
          </div>
        </div>
        <div className="mt-3 flex justify-end">
          <Button asChild size="sm" variant="outline">
            <Link
              to="/cluster/channels?tab=overview"
              aria-label={intl.formatMessage({ id: "dashboard.channelHealthOpen" })}
            >
              {intl.formatMessage({ id: "common.inspect" })}
            </Link>
          </Button>
        </div>
      </div>
    </SectionCard>
  )
}

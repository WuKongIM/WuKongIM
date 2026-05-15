import { Link } from "react-router-dom"
import { Cell, Pie, PieChart, ResponsiveContainer } from "recharts"
import { useIntl } from "react-intl"

import { SectionCard } from "@/components/shell/section-card"
import { Button } from "@/components/ui/button"
import type { ManagerChannelClusterSummaryResponse, ManagerOverviewResponse } from "@/lib/manager-api.types"

type ClusterReplicationHealthProps = {
  slots: ManagerOverviewResponse["slots"]
  channelCluster: ManagerChannelClusterSummaryResponse
}

export function ClusterReplicationHealth({ slots, channelCluster }: ClusterReplicationHealthProps) {
  const intl = useIntl()
  const donutData = [
    { name: "ready", value: slots.ready, fill: "var(--chart-1)" },
    { name: "quorum_lost", value: slots.quorum_lost, fill: "var(--chart-4)" },
    { name: "leader_missing", value: slots.leader_missing, fill: "var(--chart-3)" },
    { name: "unreported", value: slots.unreported, fill: "var(--chart-5)" },
  ].filter((item) => item.value > 0)

  return (
    <SectionCard title={intl.formatMessage({ id: "clusterDashboard.replication.title" })}>
      <div className="grid gap-4 sm:grid-cols-[10rem_1fr] sm:items-center">
        <div className="relative h-36 w-36 justify-self-center">
          <ResponsiveContainer height="100%" width="100%">
            <PieChart>
              <Pie data={donutData} dataKey="value" innerRadius={42} isAnimationActive={false} outerRadius={62} paddingAngle={2} strokeWidth={0}>
                {donutData.map((entry) => <Cell fill={entry.fill} key={entry.name} />)}
              </Pie>
            </PieChart>
          </ResponsiveContainer>
          <div className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center">
            <span className="font-mono text-lg font-semibold leading-tight">{slots.ready}/{slots.total}</span>
            <span className="text-xs text-muted-foreground">slots ready</span>
          </div>
        </div>
        <div className="space-y-2 text-sm">
          <Row label="Quorum lost" value={slots.quorum_lost} />
          <Row label="Leader missing" value={slots.leader_missing} />
          <Row label="ISR insufficient" value={channelCluster.isr_insufficient} />
          <Row label="No leader" value={channelCluster.no_leader} />
          <div className="flex justify-end gap-2 pt-2">
            <Button asChild size="sm" variant="outline"><Link to="/cluster/slots">Slots</Link></Button>
            <Button asChild size="sm" variant="outline"><Link to="/cluster/channels?tab=overview">Channels</Link></Button>
          </div>
        </div>
      </div>
    </SectionCard>
  )
}

function Row({ label, value }: { label: string; value: number }) {
  return <div className="flex items-center justify-between"><span className="text-muted-foreground">{label}</span><span className="font-mono text-foreground">{value}</span></div>
}

import type { ClusterMonitorMetricCard as ClusterMonitorMetricCardModel } from "../types"
import { ClusterMonitorMetricCard } from "./cluster-monitor-metric-card"

type ClusterMonitorCardGridProps = {
  cards: ClusterMonitorMetricCardModel[]
}

export function ClusterMonitorCardGrid({ cards }: ClusterMonitorCardGridProps) {
  return (
    <section className="grid gap-4 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
      {cards.map((card) => (
        <ClusterMonitorMetricCard card={card} key={card.key} />
      ))}
    </section>
  )
}

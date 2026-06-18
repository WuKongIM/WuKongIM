import type { MonitorMetricCard as MonitorMetricCardModel } from "../types"
import { MonitorMetricCard } from "./monitor-metric-card"

type MonitorCardGridProps = {
  cards: MonitorMetricCardModel[]
}

export function MonitorCardGrid({ cards }: MonitorCardGridProps) {
  return (
    <section className="grid gap-4 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
      {cards.map((card) => (
        <MonitorMetricCard card={card} key={card.key} />
      ))}
    </section>
  )
}

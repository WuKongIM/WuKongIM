import { PageContainer } from "@/components/shell/page-container"

import { BusinessEntryCards } from "./components/business-entry-cards"
import { BusinessHealthHero } from "./components/business-health-hero"
import { BusinessMessageTrends } from "./components/business-message-trends"
import { BusinessMetricStrip } from "./components/business-metric-strip"
import { BusinessRiskList } from "./components/business-risk-list"
import { buildSampleBusinessDashboardModel } from "./view-model"

export function BusinessDashboardPage() {
  const model = buildSampleBusinessDashboardModel()

  return (
    <PageContainer className="max-w-[1600px] gap-4">
      <BusinessHealthHero
        generatedAt={model.metrics.generated_at}
        metrics={model.metrics}
        verdict={model.verdict}
      />
      <BusinessMetricStrip groups={model.groups} />
      <section className="grid gap-4 xl:grid-cols-[minmax(0,1.55fr)_minmax(320px,0.75fr)]">
        <BusinessMessageTrends trends={model.trends} />
        <BusinessRiskList risks={model.risks} />
      </section>
      <BusinessEntryCards cards={model.entryCards} />
    </PageContainer>
  )
}

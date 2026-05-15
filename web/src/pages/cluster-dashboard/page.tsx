import { useIntl } from "react-intl"

import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"

export function ClusterDashboardPage() {
  const intl = useIntl()

  return (
    <PageContainer>
      <PageHeader
        title={intl.formatMessage({ id: "clusterDashboard.title" })}
        description={intl.formatMessage({ id: "clusterDashboard.description" })}
      />
      <SectionCard title={intl.formatMessage({ id: "clusterDashboard.links.title" })}>
        <p className="text-sm text-muted-foreground">
          {intl.formatMessage({ id: "clusterDashboard.loading" })}
        </p>
      </SectionCard>
    </PageContainer>
  )
}

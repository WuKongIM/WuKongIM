import { useIntl } from "react-intl"

import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"

export function BusinessDashboardPage() {
  const intl = useIntl()

  return (
    <PageContainer>
      <PageHeader
        title={intl.formatMessage({ id: "businessDashboard.title" })}
        description={intl.formatMessage({ id: "businessDashboard.description" })}
      />
      <SectionCard title={intl.formatMessage({ id: "businessDashboard.trends.title" })}>
        <p className="text-sm text-muted-foreground">
          {intl.formatMessage({ id: "businessDashboard.loading" })}
        </p>
      </SectionCard>
    </PageContainer>
  )
}

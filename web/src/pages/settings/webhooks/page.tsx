import { useIntl } from "react-intl"

import { ResourceState } from "@/components/manager/resource-state"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"

export function WebhooksPage() {
  const intl = useIntl()

  return (
    <PageContainer>
      <PageHeader
        title={intl.formatMessage({ id: "webhooks.title" })}
        description={intl.formatMessage({ id: "webhooks.description" })}
      />
      <SectionCard
        description={intl.formatMessage({ id: "common.comingSoonDescription" })}
        title={intl.formatMessage({ id: "common.comingSoon" })}
      >
        <ResourceState kind="empty" title={intl.formatMessage({ id: "webhooks.title" })} />
      </SectionCard>
    </PageContainer>
  )
}

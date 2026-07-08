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
      <div
        className="rounded-md border border-border bg-card"
        data-testid="webhooks-placeholder-surface"
      >
        <SectionCard
          className="rounded-md border-0 bg-transparent"
          description={intl.formatMessage({ id: "common.comingSoonDescription" })}
          title={intl.formatMessage({ id: "common.comingSoon" })}
        >
          <div className="rounded-md border border-border bg-card p-3">
            <ResourceState kind="empty" title={intl.formatMessage({ id: "webhooks.title" })} />
          </div>
        </SectionCard>
      </div>
    </PageContainer>
  )
}

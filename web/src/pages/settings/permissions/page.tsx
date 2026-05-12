import { useIntl } from "react-intl"

import { ResourceState } from "@/components/manager/resource-state"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"

export function PermissionsPage() {
  const intl = useIntl()

  return (
    <PageContainer>
      <PageHeader
        title={intl.formatMessage({ id: "permissions.title" })}
        description={intl.formatMessage({ id: "permissions.description" })}
      />
      <SectionCard
        description={intl.formatMessage({ id: "common.comingSoonDescription" })}
        title={intl.formatMessage({ id: "common.comingSoon" })}
      >
        <ResourceState kind="empty" title={intl.formatMessage({ id: "permissions.title" })} />
      </SectionCard>
    </PageContainer>
  )
}

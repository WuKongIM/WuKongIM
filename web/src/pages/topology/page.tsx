import { useIntl } from "react-intl"

import { ResourceState } from "@/components/manager/resource-state"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"

export function TopologyPage() {
  const intl = useIntl()

  return (
    <PageContainer>
      <PageHeader
        title={intl.formatMessage({ id: "nav.topology.title" })}
        description={intl.formatMessage({ id: "nav.topology.description" })}
      >
        <div className="flex flex-wrap gap-2 text-xs text-muted-foreground">
          <div className="rounded-md border border-border bg-background px-3 py-2">
            {intl.formatMessage({ id: "topology.scopeClusterGraph" })}
          </div>
          <div className="rounded-md border border-border bg-background px-3 py-2">
            {intl.formatMessage({ id: "topology.statusNotExposed" })}
          </div>
        </div>
      </PageHeader>
      <SectionCard
        description={intl.formatMessage({ id: "topology.coverageDescription" })}
        title={intl.formatMessage({ id: "topology.coverageTitle" })}
      >
        <ResourceState
          description={intl.formatMessage({ id: "topology.coverageEmpty" })}
          kind="empty"
          title={intl.formatMessage({ id: "nav.topology.title" })}
        />
      </SectionCard>
    </PageContainer>
  )
}

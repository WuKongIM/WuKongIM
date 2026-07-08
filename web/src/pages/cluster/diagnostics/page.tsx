import { useIntl } from "react-intl"
import { useSearchParams } from "react-router-dom"

import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { PageTabs } from "@/components/shell/page-tabs"
import { DiagnosticsTracePanel } from "@/pages/diagnostics/page"

const tabs = [
  { id: "trace", labelMessageId: "diagnostics.tabs.trace" },
] as const

type DiagnosticsTab = (typeof tabs)[number]["id"]

function normalizeTab(value: string | null): DiagnosticsTab {
  return tabs.some((tab) => tab.id === value) ? (value as DiagnosticsTab) : "trace"
}

export function ClusterDiagnosticsPage() {
  const intl = useIntl()
  const [searchParams, setSearchParams] = useSearchParams()
  const activeTab = normalizeTab(searchParams.get("tab"))

  function setTab(tab: string) {
    const next = new URLSearchParams(searchParams)
    next.set("tab", tab)
    setSearchParams(next)
  }

  return (
    <PageContainer>
      <PageHeader
        eyebrow={intl.formatMessage({ id: "nav.path.cluster.diagnostics" })}
        title={intl.formatMessage({ id: "nav.diagnostics.title" })}
        description={intl.formatMessage({ id: "nav.diagnostics.description" })}
      />
      <div className="space-y-4" data-cluster-diagnostics-surface="trace">
        <PageTabs
          activeTab={activeTab}
          className="px-0 pt-0"
          tabs={tabs.map((tab) => ({ id: tab.id, label: intl.formatMessage({ id: tab.labelMessageId }) }))}
          onTabChange={setTab}
        />
        {activeTab === "trace" ? <DiagnosticsTracePanel /> : null}
      </div>
    </PageContainer>
  )
}

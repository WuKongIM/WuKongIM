import { useIntl } from "react-intl"
import { useSearchParams } from "react-router-dom"

import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { PageTabs } from "@/components/shell/page-tabs"
import { AppLogsPanel } from "@/pages/app-logs/page"
import { ControllerLogsPanel } from "@/pages/controller/page"
import { DiagnosticsTracePanel } from "@/pages/diagnostics/page"
import { DiagnosticsNetworkPanel } from "@/pages/network/page"
import { SlotLogsPanel } from "@/pages/slot-logs/page"

const tabs = [
  { id: "trace", labelMessageId: "diagnostics.tabs.trace" },
  { id: "network", labelMessageId: "diagnostics.tabs.network" },
  { id: "controller-logs", labelMessageId: "diagnostics.tabs.controllerLogs" },
  { id: "slot-logs", labelMessageId: "diagnostics.tabs.slotLogs" },
  { id: "app-logs", labelMessageId: "diagnostics.tabs.appLogs" },
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
      <PageTabs
        activeTab={activeTab}
        className="px-0 pt-0"
        tabs={tabs.map((tab) => ({ id: tab.id, label: intl.formatMessage({ id: tab.labelMessageId }) }))}
        onTabChange={setTab}
      />
      {activeTab === "trace" ? <DiagnosticsTracePanel /> : null}
      {activeTab === "network" ? <DiagnosticsNetworkPanel /> : null}
      {activeTab === "controller-logs" ? <ControllerLogsPanel /> : null}
      {activeTab === "slot-logs" ? <SlotLogsPanel /> : null}
      {activeTab === "app-logs" ? <AppLogsPanel /> : null}
    </PageContainer>
  )
}

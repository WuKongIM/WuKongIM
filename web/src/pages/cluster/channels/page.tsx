import { useIntl } from "react-intl"
import { useSearchParams } from "react-router-dom"

import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { PageTabs } from "@/components/shell/page-tabs"
import { ChannelClusterOverviewPanel } from "@/pages/channel-cluster/page"
import { ChannelClusterUnhealthyPanel } from "@/pages/channel-cluster/unhealthy/page"
import { ChannelClusterListPanel } from "@/pages/channels/page"

const tabs = [
  { id: "overview", labelMessageId: "channelCluster.tabs.overview" },
  { id: "list", labelMessageId: "channelCluster.tabs.list" },
  { id: "unhealthy", labelMessageId: "channelCluster.tabs.unhealthy" },
] as const

type ChannelClusterTab = (typeof tabs)[number]["id"]

function normalizeTab(value: string | null): ChannelClusterTab {
  return tabs.some((tab) => tab.id === value) ? (value as ChannelClusterTab) : "overview"
}

export function ClusterChannelsPage() {
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
        eyebrow={intl.formatMessage({ id: "nav.path.cluster.channels" })}
        title={intl.formatMessage({ id: "channelCluster.title" })}
        description={intl.formatMessage({ id: "channelCluster.description" })}
      />
      <PageTabs
        activeTab={activeTab}
        className="px-0 pt-0"
        tabs={tabs.map((tab) => ({ id: tab.id, label: intl.formatMessage({ id: tab.labelMessageId }) }))}
        onTabChange={setTab}
      />
      {activeTab === "overview" ? <ChannelClusterOverviewPanel unhealthyHref="/cluster/channels?tab=unhealthy" /> : null}
      {activeTab === "list" ? <ChannelClusterListPanel messagesHref="/business/messages" /> : null}
      {activeTab === "unhealthy" ? <ChannelClusterUnhealthyPanel listHref="/cluster/channels?tab=list" /> : null}
    </PageContainer>
  )
}

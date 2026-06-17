import { useIntl } from "react-intl"

import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { ChannelClusterListPanel } from "@/pages/channels/page"

export function ClusterChannelsPage() {
  const intl = useIntl()

  return (
    <PageContainer>
      <PageHeader
        eyebrow={intl.formatMessage({ id: "nav.path.cluster.channels" })}
        title={intl.formatMessage({ id: "channelCluster.title" })}
        description={intl.formatMessage({ id: "channelCluster.description" })}
      />
      <ChannelClusterListPanel messagesHref="/business/messages" />
    </PageContainer>
  )
}

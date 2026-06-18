import { useMemo, useState } from "react"
import { useIntl } from "react-intl"

import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"

import { ClusterMonitorCardGrid } from "./components/cluster-monitor-card-grid"
import { ClusterMonitorSnapshotStrip } from "./components/cluster-monitor-snapshot-strip"
import { ClusterMonitorToolbar } from "./components/cluster-monitor-toolbar"
import { buildPreviewClusterMonitorModel } from "./preview-data"
import type { ClusterMonitorTimeRange } from "./types"

export function ClusterMonitorPage() {
  const intl = useIntl()
  const [timeRange, setTimeRange] = useState<ClusterMonitorTimeRange>("15m")
  const [isPaused, setIsPaused] = useState(false)
  const model = useMemo(() => buildPreviewClusterMonitorModel(timeRange, isPaused), [isPaused, timeRange])

  return (
    <PageContainer className="max-w-[1600px] gap-4">
      <PageHeader
        description={intl.formatMessage({ id: "clusterMonitor.description" })}
        eyebrow={intl.formatMessage({ id: "clusterMonitor.previewBadge" })}
        title={intl.formatMessage({ id: "clusterMonitor.title" })}
      />

      <ClusterMonitorToolbar
        generatedAt={model.generatedAt}
        isPaused={model.isPaused}
        onPauseToggle={() => setIsPaused((current) => !current)}
        onTimeRangeChange={setTimeRange}
        scopeLabelId={model.scopeLabelId}
        timeRange={model.timeRange}
      />
      <ClusterMonitorSnapshotStrip entries={model.snapshot} />
      <ClusterMonitorCardGrid cards={model.cards} />
    </PageContainer>
  )
}

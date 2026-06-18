import { useMemo, useState } from "react"
import { useIntl } from "react-intl"

import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"

import { MonitorCardGrid } from "./components/monitor-card-grid"
import { MonitorSnapshotStrip } from "./components/monitor-snapshot-strip"
import { MonitorToolbar } from "./components/monitor-toolbar"
import { buildPreviewMonitorModel } from "./preview-data"
import type { TimeRange } from "./types"

export function MonitorPage() {
  const intl = useIntl()
  const [timeRange, setTimeRange] = useState<TimeRange>("15m")
  const [isPaused, setIsPaused] = useState(false)
  const model = useMemo(() => buildPreviewMonitorModel(timeRange, isPaused), [isPaused, timeRange])

  return (
    <PageContainer className="max-w-[1600px] gap-4">
      <PageHeader
        description={intl.formatMessage({ id: "monitor.cardWallDescription" })}
        eyebrow={intl.formatMessage({ id: "monitor.previewBadge" })}
        title={intl.formatMessage({ id: "monitor.title" })}
      />

      <MonitorToolbar
        generatedAt={model.generatedAt}
        isPaused={model.isPaused}
        onPauseToggle={() => setIsPaused((current) => !current)}
        onTimeRangeChange={setTimeRange}
        scopeLabelId={model.scopeLabelId}
        timeRange={model.timeRange}
      />
      <MonitorSnapshotStrip entries={model.snapshot} />
      <MonitorCardGrid cards={model.cards} />
    </PageContainer>
  )
}

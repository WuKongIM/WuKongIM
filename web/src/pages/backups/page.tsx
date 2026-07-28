import { useCallback, useEffect, useRef, useState, type ReactNode } from "react"
import { useIntl, type IntlShape } from "react-intl"
import { useSearchParams } from "react-router-dom"

import { useAuthStore } from "@/auth/auth-store"
import { hasManagerPermission } from "@/auth/permissions"
import { ConfirmDialog } from "@/components/manager/confirm-dialog"
import { ResourceState } from "@/components/manager/resource-state"
import { StatusBadge } from "@/components/manager/status-badge"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { PageTabs } from "@/components/shell/page-tabs"
import { SectionCard } from "@/components/shell/section-card"
import { Button } from "@/components/ui/button"
import {
  getBackupCheckpoints,
  getBackupStatus,
  ManagerApiError,
  publishBackupCheckpoint,
} from "@/lib/manager-api"
import type {
  ManagerBackupCheckpoint,
  ManagerBackupStatusResponse,
} from "@/lib/manager-api.types"

type BackupTab = "overview" | "checkpoints"

const tabs: BackupTab[] = ["overview", "checkpoints"]

function formatDuration(seconds: number | null | undefined) {
  if (seconds === null || seconds === undefined) return "—"
  if (seconds < 60) return `${seconds}s`
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`
  if (seconds < 86_400) return `${Math.floor(seconds / 3600)}h`
  return `${Math.floor(seconds / 86_400)}d`
}

function formatBytes(bytes: number) {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 ** 2) return `${(bytes / 1024).toFixed(1)} KiB`
  if (bytes < 1024 ** 3) return `${(bytes / 1024 ** 2).toFixed(1)} MiB`
  return `${(bytes / 1024 ** 3).toFixed(1)} GiB`
}

function LocalTime({ value }: { value?: number }) {
  if (!value) return <span>—</span>
  const date = new Date(value)
  return <time dateTime={date.toISOString()} title={`${date.toISOString()} (UTC)`}>{date.toLocaleString()}</time>
}

function errorMessage(error: unknown, intl: IntlShape) {
  if (!(error instanceof ManagerApiError)) return intl.formatMessage({ id: "backups.error.serviceUnavailable" })
  const messages: Record<string, string> = {
    backup_disabled: "backups.error.disabled",
    backup_doctor_unhealthy: "backups.error.doctorUnhealthy",
    controller_leader_unavailable: "backups.error.leaderUnavailable",
    state_conflict: "backups.error.stateConflict",
    checkpoint_not_found: "backups.error.checkpointNotFound",
    permission_denied: "backups.error.permissionDenied",
  }
  return intl.formatMessage({ id: messages[error.error] ?? "backups.error.serviceUnavailable" })
}

export function BackupsPage() {
  const intl = useIntl()
  const permissions = useAuthStore((state) => state.permissions)
  const canRead = hasManagerPermission(permissions, "cluster.backup", "r")
  const permissionWrite = hasManagerPermission(permissions, "cluster.backup", "w")
  const [searchParams, setSearchParams] = useSearchParams()
  const requestedTab = searchParams.get("tab") as BackupTab | null
  const activeTab = requestedTab && tabs.includes(requestedTab) ? requestedTab : "overview"

  const [status, setStatus] = useState<ManagerBackupStatusResponse | null>(null)
  const [checkpoints, setCheckpoints] = useState<ManagerBackupCheckpoint[]>([])
  const [nextCursor, setNextCursor] = useState("")
  const [total, setTotal] = useState(0)
  const [idQuery, setIDQuery] = useState("")
  const [appliedIDQuery, setAppliedIDQuery] = useState("")
  const [loading, setLoading] = useState(canRead)
  const [statusError, setStatusError] = useState<Error | null>(null)
  const [catalogError, setCatalogError] = useState<Error | null>(null)
  const [pending, setPending] = useState(false)
  const [mutationError, setMutationError] = useState("")
  const [confirmPublish, setConfirmPublish] = useState(false)
  const statusInFlight = useRef(false)
  const listInFlight = useRef(false)

  const canWrite = permissionWrite && status?.auth_enabled === true
  const writeDisabledReason = !permissionWrite
    ? intl.formatMessage({ id: "backups.write.permission" })
    : status?.auth_enabled === false
      ? intl.formatMessage({ id: "backups.write.authDisabled" })
      : status?.enabled === false
        ? intl.formatMessage({ id: "backups.error.disabled" })
        : ""

  const loadCheckpoints = useCallback(async (append = false, cursor = "") => {
    if (!canRead || listInFlight.current) return
    listInFlight.current = true
    try {
      const page = await getBackupCheckpoints({
        limit: 50,
        cursor,
        id: appliedIDQuery || undefined,
      })
      setCheckpoints((current) => append ? [...current, ...page.items] : page.items)
      setNextCursor(page.next_cursor ?? "")
      setTotal(page.total)
      setCatalogError(null)
    } catch (requestError) {
      setCatalogError(requestError instanceof Error ? requestError : new Error("backup checkpoint request failed"))
    } finally {
      listInFlight.current = false
    }
  }, [appliedIDQuery, canRead])

  const loadStatus = useCallback(async () => {
    if (!canRead || statusInFlight.current) return
    statusInFlight.current = true
    try {
      setStatus(await getBackupStatus())
      setStatusError(null)
    } catch (requestError) {
      setStatusError(requestError instanceof Error ? requestError : new Error("backup status request failed"))
    } finally {
      statusInFlight.current = false
    }
  }, [canRead])

  const refreshAll = useCallback(async () => {
    if (!canRead) return
    try {
      await Promise.all([loadStatus(), loadCheckpoints(false)])
    } finally {
      setLoading(false)
    }
  }, [canRead, loadCheckpoints, loadStatus])

  useEffect(() => {
    const timer = window.setTimeout(() => void refreshAll(), 0)
    return () => window.clearTimeout(timer)
  }, [refreshAll])

  useEffect(() => {
    if (!canRead) return
    const timer = window.setInterval(() => {
      if (document.visibilityState === "visible") void loadStatus()
    }, 15_000)
    return () => window.clearInterval(timer)
  }, [canRead, loadStatus])

  const publish = async () => {
    setPending(true)
    setMutationError("")
    try {
      await publishBackupCheckpoint()
      setConfirmPublish(false)
      await Promise.all([loadStatus(), loadCheckpoints(false)])
    } catch (requestError) {
      setMutationError(errorMessage(requestError, intl))
    } finally {
      setPending(false)
    }
  }

  if (!canRead) {
    return (
      <PageContainer>
        <PageHeader title={intl.formatMessage({ id: "backups.title" })} description={intl.formatMessage({ id: "backups.description" })} />
        <ResourceState kind="forbidden" title={intl.formatMessage({ id: "backups.forbidden" })} />
      </PageContainer>
    )
  }

  return (
    <PageContainer>
      <PageHeader
        title={intl.formatMessage({ id: "backups.title" })}
        description={intl.formatMessage({ id: "backups.description" })}
        actions={
          <>
            <Button onClick={() => void refreshAll()} size="sm" variant="outline">
              {intl.formatMessage({ id: "common.refresh" })}
            </Button>
            <Button
              aria-label={intl.formatMessage({ id: "backups.publish" })}
              disabled={!canWrite || status?.enabled !== true || pending}
              onClick={() => setConfirmPublish(true)}
              size="sm"
              title={writeDisabledReason}
            >
              {intl.formatMessage({ id: "backups.publish" })}
            </Button>
          </>
        }
      >
        <PageTabs
          activeTab={activeTab}
          onTabChange={(tab) => setSearchParams(tab === "overview" ? {} : { tab })}
          tabs={[
            { id: "overview", label: intl.formatMessage({ id: "backups.tabs.overview" }) },
            { id: "checkpoints", label: intl.formatMessage({ id: "backups.tabs.checkpoints" }) },
          ]}
        />
      </PageHeader>

      {loading && !status ? <ResourceState kind="loading" title={intl.formatMessage({ id: "backups.title" })} /> : null}
      {statusError && !status ? (
        <ResourceState kind={statusError instanceof ManagerApiError && statusError.status === 403 ? "forbidden" : "unavailable"} onRetry={() => void refreshAll()} title={intl.formatMessage({ id: "backups.title" })} />
      ) : null}

      {status && activeTab === "overview" ? <BackupOverview status={status} /> : null}
      {status && activeTab === "checkpoints" ? (
        catalogError ? (
          <ResourceState
            kind={catalogError instanceof ManagerApiError && catalogError.status === 403 ? "forbidden" : "unavailable"}
            onRetry={() => void loadCheckpoints(false)}
            title={intl.formatMessage({ id: "backups.checkpoints.title" })}
          />
        ) : (
          <CheckpointCatalog
            checkpoints={checkpoints}
            idQuery={idQuery}
            nextCursor={nextCursor}
            onIDQuery={setIDQuery}
            onLoadMore={() => void loadCheckpoints(true, nextCursor)}
            onSearch={() => setAppliedIDQuery(idQuery.trim())}
            total={total}
          />
        )
      ) : null}

      <ConfirmDialog
        confirmLabel={intl.formatMessage({ id: "backups.publish.confirm" })}
        description={intl.formatMessage({ id: "backups.publish.warning" })}
        error={mutationError}
        onConfirm={() => void publish()}
        onOpenChange={setConfirmPublish}
        open={confirmPublish}
        pending={pending}
        title={intl.formatMessage({ id: "backups.publish.title" })}
      />
    </PageContainer>
  )
}

function BackupOverview({ status }: { status: ManagerBackupStatusResponse }) {
  const intl = useIntl()
  const healthySlots = status.capture_statuses.filter((slot) =>
    slot.lease_current && !slot.failure_category && ["idle", "reconciling"].includes(slot.state),
  ).length
  const pendingErasures = status.erasure_streams.filter((stream) => stream.pending).length
  const maxMetadataLag = Math.max(0, ...status.capture_statuses.map((slot) => slot.metadata_lag))
  const maxMessageLag = Math.max(0, ...status.capture_statuses.map((slot) => slot.message_lag))
  const repairSlots = status.integrity_audit.slots.filter((slot) => slot.health !== "healthy").length
  const expectedCaptureSlots = status.capture_leases.length || status.capture_statuses.length

  return (
    <>
      {!status.auth_enabled ? <ResourceState kind="unavailable" title={intl.formatMessage({ id: "backups.authReadonly" })} /> : null}
      {status.enabled && !status.capture_status_complete ? (
        <ResourceState
          description={intl.formatMessage(
            { id: "backups.capture.incomplete.description" },
            {
              nodes: status.capture_status_missing_node_ids.length,
              slots: status.capture_status_missing_slots.length,
            },
          )}
          kind="unavailable"
          title={intl.formatMessage({ id: "backups.capture.incomplete" })}
        />
      ) : null}
      <SectionCard title={intl.formatMessage({ id: "backups.overview.status" })}>
        <div className="grid overflow-hidden rounded-md border border-border md:grid-cols-4">
          <Summary label={intl.formatMessage({ id: "backups.health" })} value={<StatusBadge value={status.health} />} />
          <Summary label={intl.formatMessage({ id: "backups.checkpointAge" })} value={`${formatDuration(status.checkpoint_age_seconds)} / ${formatDuration(status.max_checkpoint_age_seconds)}`} />
          <Summary label={intl.formatMessage({ id: "backups.coordinator" })} value={status.coordinator_node_id ? `#${status.coordinator_node_id}` : "—"} />
          <Summary label={intl.formatMessage({ id: "backups.captureSlots" })} value={`${healthySlots} / ${expectedCaptureSlots}`} />
        </div>
        <p className="mt-3 text-xs text-muted-foreground">
          {intl.formatMessage({ id: "backups.observed" })}: <LocalTime value={status.observed_at_unix_millis} />
        </p>
        {status.failure_category ? <p className="mt-3 text-sm text-destructive">{status.failure_category}</p> : null}
      </SectionCard>

      <SectionCard title={intl.formatMessage({ id: "backups.latest" })}>
        {status.latest_checkpoint ? (
          <dl className="grid gap-3 text-sm md:grid-cols-4">
            <div><dt className="text-muted-foreground">ID</dt><dd className="font-mono">{status.latest_checkpoint.id}</dd></div>
            <div><dt className="text-muted-foreground">{intl.formatMessage({ id: "backups.checkpoints.effective" })}</dt><dd><LocalTime value={status.latest_checkpoint.effective_at_unix_millis} /></dd></div>
            <div><dt className="text-muted-foreground">{intl.formatMessage({ id: "backups.checkpoints.created" })}</dt><dd><LocalTime value={status.latest_checkpoint.created_at_unix_millis} /></dd></div>
            <div><dt className="text-muted-foreground">{intl.formatMessage({ id: "backups.checkpoints.hold" })}</dt><dd>{status.latest_checkpoint.held ? intl.formatMessage({ id: "backups.checkpoints.heldYes" }) : intl.formatMessage({ id: "backups.checkpoints.heldNo" })}</dd></div>
          </dl>
        ) : <ResourceState kind="empty" title={intl.formatMessage({ id: "backups.latest.empty" })} />}
      </SectionCard>

      <SectionCard title={intl.formatMessage({ id: "backups.policy" })}>
        <dl className="grid gap-3 text-sm sm:grid-cols-2 lg:grid-cols-3">
          <Policy label={intl.formatMessage({ id: "backups.policy.reconcile" })} value={formatDuration(status.policy.capture_reconcile_interval_seconds)} />
          <Policy label={intl.formatMessage({ id: "backups.policy.checkpoint" })} value={formatDuration(status.policy.checkpoint_interval_seconds)} />
          <Policy label={intl.formatMessage({ id: "backups.policy.workers" })} value={String(status.policy.capture_worker_count)} />
          <Policy label={intl.formatMessage({ id: "backups.policy.segmentTarget" })} value={formatBytes(status.policy.target_segment_bytes)} />
          <Policy label={intl.formatMessage({ id: "backups.policy.segmentMax" })} value={formatBytes(status.policy.max_segment_bytes)} />
          <Policy label={intl.formatMessage({ id: "backups.policy.segmentOpen" })} value={formatDuration(status.policy.max_segment_open_duration_seconds)} />
          <Policy label={intl.formatMessage({ id: "backups.policy.stagingQuota" })} value={formatBytes(status.policy.staging_max_bytes)} />
          <Policy label={intl.formatMessage({ id: "backups.policy.pinAge" })} value={formatDuration(status.policy.source_pin_max_age_seconds)} />
          <Policy label={intl.formatMessage({ id: "backups.policy.pinBytes" })} value={formatBytes(status.policy.max_source_pinned_bytes)} />
        </dl>
      </SectionCard>

      <SectionCard title={intl.formatMessage({ id: "backups.capture" })}>
        <dl className="grid gap-3 text-sm sm:grid-cols-3">
          <Policy label={intl.formatMessage({ id: "backups.capture.leases" })} value={String(status.capture_leases.length)} />
          <Policy label={intl.formatMessage({ id: "backups.capture.local" })} value={String(status.capture_statuses.length)} />
          <Policy label={intl.formatMessage({ id: "backups.capture.pendingErasures" })} value={String(pendingErasures)} />
        </dl>
      </SectionCard>

      <SectionCard title={intl.formatMessage({ id: "backups.operations" })}>
        <dl className="grid gap-3 text-sm sm:grid-cols-2 lg:grid-cols-3">
          <Policy label={intl.formatMessage({ id: "backups.operations.captureLag" })} value={`${maxMetadataLag} / ${maxMessageLag}`} />
          <Policy label={intl.formatMessage({ id: "backups.operations.audit" })} value={`${status.integrity_audit.cursor?.phase ?? "idle"} · ${status.integrity_audit.debt_objects}`} />
          <Policy label={intl.formatMessage({ id: "backups.operations.repair" })} value={String(repairSlots)} />
          <Policy label={intl.formatMessage({ id: "backups.operations.compaction" })} value={String(status.compaction.debt_slots)} />
          <Policy label={intl.formatMessage({ id: "backups.operations.gc" })} value={String(status.garbage_collection.debt_repositories)} />
          <Policy label={intl.formatMessage({ id: "backups.operations.restore" })} value={status.restore ? `${status.restore.status} · ${formatBytes(status.restore.throughput_bytes_per_second)}/s` : "—"} />
        </dl>
      </SectionCard>
    </>
  )
}

function Summary({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div className="border-b border-border p-3 text-sm last:border-b-0 md:border-r md:border-b-0">
      <div className="text-muted-foreground">{label}</div>
      <div className="mt-1 font-semibold">{value}</div>
    </div>
  )
}

function Policy({ label, value }: { label: string; value: string }) {
  return <div><dt className="text-muted-foreground">{label}</dt><dd>{value}</dd></div>
}

function CheckpointCatalog(props: {
  checkpoints: ManagerBackupCheckpoint[]
  idQuery: string
  nextCursor: string
  onIDQuery: (value: string) => void
  onLoadMore: () => void
  onSearch: () => void
  total: number
}) {
  const intl = useIntl()
  return (
    <SectionCard title={intl.formatMessage({ id: "backups.checkpoints.title" })} description={intl.formatMessage({ id: "backups.checkpoints.count" }, { count: props.total })}>
      <div className="mb-3 flex flex-wrap gap-2">
        <input
          aria-label={intl.formatMessage({ id: "backups.checkpoints.search" })}
          className="h-8 min-w-60 rounded-md border border-border bg-background px-3 text-sm"
          onChange={(event) => props.onIDQuery(event.target.value)}
          placeholder={intl.formatMessage({ id: "backups.checkpoints.search" })}
          value={props.idQuery}
        />
        <Button onClick={props.onSearch} size="sm" variant="outline">{intl.formatMessage({ id: "common.search" })}</Button>
      </div>
      <div className="overflow-x-auto rounded-md border border-border">
        <table className="w-full min-w-[760px] text-left text-sm">
          <thead className="bg-muted/40 text-xs uppercase text-muted-foreground">
            <tr>
              <th className="p-3">ID</th>
              <th className="p-3">{intl.formatMessage({ id: "backups.checkpoints.effective" })}</th>
              <th className="p-3">{intl.formatMessage({ id: "backups.checkpoints.created" })}</th>
              <th className="p-3">{intl.formatMessage({ id: "backups.checkpoints.hold" })}</th>
            </tr>
          </thead>
          <tbody>
            {props.checkpoints.map((checkpoint) => (
              <tr className="border-t border-border" key={checkpoint.id}>
                <td className="p-3 font-mono text-xs">{checkpoint.id}</td>
                <td className="p-3"><LocalTime value={checkpoint.effective_at_unix_millis} /></td>
                <td className="p-3"><LocalTime value={checkpoint.created_at_unix_millis} /></td>
                <td className="p-3">{checkpoint.held ? intl.formatMessage({ id: "backups.checkpoints.heldYes" }) : "—"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {props.checkpoints.length === 0 ? <ResourceState kind="empty" title={intl.formatMessage({ id: "backups.checkpoints.empty" })} /> : null}
      {props.nextCursor ? <Button className="mt-3" onClick={props.onLoadMore} size="sm" variant="outline">{intl.formatMessage({ id: "common.loadMore" })}</Button> : null}
    </SectionCard>
  )
}

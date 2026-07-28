import { useCallback, useEffect, useRef, useState } from "react"
import { MoreHorizontalIcon } from "lucide-react"
import { useIntl, type IntlShape } from "react-intl"
import { useNavigate, useSearchParams } from "react-router-dom"

import { useAuthStore } from "@/auth/auth-store"
import { hasManagerPermission } from "@/auth/permissions"
import { ActionFormDialog } from "@/components/manager/action-form-dialog"
import { ConfirmDialog } from "@/components/manager/confirm-dialog"
import { DetailSheet } from "@/components/manager/detail-sheet"
import { ResourceState } from "@/components/manager/resource-state"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { PageTabs } from "@/components/shell/page-tabs"
import { SectionCard } from "@/components/shell/section-card"
import { Button } from "@/components/ui/button"
import {
  getBackupCheckpoint,
  getBackupCheckpoints,
  getBackupStatus,
  ManagerApiError,
  publishBackupCheckpoint,
  setBackupCheckpointHold,
} from "@/lib/manager-api"
import type {
  ManagerBackupCheckpoint,
  ManagerBackupCheckpointDetail,
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

function formatRelativeDuration(seconds: number | null | undefined, intl: IntlShape) {
  if (seconds === null || seconds === undefined) {
    return intl.formatMessage({ id: "backups.relative.unknown" })
  }
  if (seconds < 60) {
    return intl.formatMessage({ id: "backups.relative.justNow" })
  }
  if (seconds < 3600) {
    return intl.formatMessage(
      { id: "backups.relative.minutes" },
      { count: Math.floor(seconds / 60) },
    )
  }
  if (seconds < 86_400) {
    return intl.formatMessage(
      { id: "backups.relative.hours" },
      { count: Math.floor(seconds / 3600) },
    )
  }
  return intl.formatMessage(
    { id: "backups.relative.days" },
    { count: Math.floor(seconds / 86_400) },
  )
}

function formatDurationWords(seconds: number, intl: IntlShape) {
  if (seconds < 60) {
    return intl.formatMessage(
      { id: "backups.duration.seconds" },
      { count: Math.max(1, Math.ceil(seconds)) },
    )
  }
  if (seconds < 3600) {
    return intl.formatMessage(
      { id: "backups.duration.minutes" },
      { count: Math.ceil(seconds / 60) },
    )
  }
  if (seconds < 86_400) {
    return intl.formatMessage(
      { id: "backups.duration.hours" },
      { count: Math.ceil(seconds / 3600) },
    )
  }
  return intl.formatMessage(
    { id: "backups.duration.days" },
    { count: Math.ceil(seconds / 86_400) },
  )
}

function LocalTime({ value }: { value?: number }) {
  if (!value) return <span>—</span>
  const date = new Date(value)
  return <time dateTime={date.toISOString()} title={`${date.toISOString()} (UTC)`}>{date.toLocaleString()}</time>
}

function dateStartMillis(value: string) {
  if (!value) return undefined
  return new Date(`${value}T00:00:00`).getTime()
}

function dateEndMillis(value: string) {
  if (!value) return undefined
  const nextDay = new Date(`${value}T00:00:00`)
  nextDay.setDate(nextDay.getDate() + 1)
  return nextDay.getTime() - 1
}

function shortCheckpointID(checkpointID: string) {
  return checkpointID.length > 18 ? `${checkpointID.slice(0, 14)}…` : checkpointID
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
  const navigate = useNavigate()
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
  const [heldQuery, setHeldQuery] = useState("")
  const [appliedHeldQuery, setAppliedHeldQuery] = useState("")
  const [effectiveFromQuery, setEffectiveFromQuery] = useState("")
  const [appliedEffectiveFromQuery, setAppliedEffectiveFromQuery] = useState("")
  const [effectiveToQuery, setEffectiveToQuery] = useState("")
  const [appliedEffectiveToQuery, setAppliedEffectiveToQuery] = useState("")
  const [loading, setLoading] = useState(canRead)
  const [statusError, setStatusError] = useState<Error | null>(null)
  const [catalogError, setCatalogError] = useState<Error | null>(null)
  const [pending, setPending] = useState(false)
  const [mutationError, setMutationError] = useState("")
  const [confirmPublish, setConfirmPublish] = useState(false)
  const [selectedCheckpointID, setSelectedCheckpointID] = useState("")
  const [checkpointDetail, setCheckpointDetail] = useState<ManagerBackupCheckpointDetail | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const [detailError, setDetailError] = useState<Error | null>(null)
  const [confirmRelease, setConfirmRelease] = useState(false)
  const [releaseConfirmation, setReleaseConfirmation] = useState("")
  const statusInFlight = useRef(false)
  const listRequestID = useRef(0)
  const detailRequestID = useRef(0)
  const displayedTab = status?.enabled === false ? "overview" : activeTab

  const canWrite = permissionWrite && status?.auth_enabled === true

  const loadCheckpoints = useCallback(async (append = false, cursor = "") => {
    if (!canRead) return
    const requestID = ++listRequestID.current
    try {
      const page = await getBackupCheckpoints({
        limit: 50,
        cursor,
        id: appliedIDQuery || undefined,
        held: appliedHeldQuery ? appliedHeldQuery === "true" : undefined,
        effectiveFrom: dateStartMillis(appliedEffectiveFromQuery),
        effectiveTo: dateEndMillis(appliedEffectiveToQuery),
      })
      if (requestID !== listRequestID.current) return
      setCheckpoints((current) => append ? [...current, ...page.items] : page.items)
      setNextCursor(page.next_cursor ?? "")
      setTotal(page.total)
      setCatalogError(null)
    } catch (requestError) {
      if (requestID !== listRequestID.current) return
      setCatalogError(requestError instanceof Error ? requestError : new Error("backup checkpoint request failed"))
    }
  }, [
    appliedEffectiveFromQuery,
    appliedEffectiveToQuery,
    appliedHeldQuery,
    appliedIDQuery,
    canRead,
  ])

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

  const applyCheckpointFilters = () => {
    setAppliedIDQuery(idQuery.trim())
    setAppliedHeldQuery(heldQuery)
    setAppliedEffectiveFromQuery(effectiveFromQuery)
    setAppliedEffectiveToQuery(effectiveToQuery)
  }

  const clearCheckpointFilters = () => {
    setIDQuery("")
    setHeldQuery("")
    setEffectiveFromQuery("")
    setEffectiveToQuery("")
    setAppliedIDQuery("")
    setAppliedHeldQuery("")
    setAppliedEffectiveFromQuery("")
    setAppliedEffectiveToQuery("")
  }

  const openCheckpointDetail = async (checkpointID: string) => {
    const requestID = ++detailRequestID.current
    setSelectedCheckpointID(checkpointID)
    setCheckpointDetail(null)
    setDetailError(null)
    setMutationError("")
    setDetailLoading(true)
    try {
      const detail = await getBackupCheckpoint(checkpointID)
      if (requestID !== detailRequestID.current) return
      setCheckpointDetail(detail)
    } catch (requestError) {
      if (requestID !== detailRequestID.current) return
      setDetailError(requestError instanceof Error ? requestError : new Error("backup checkpoint detail request failed"))
    } finally {
      if (requestID === detailRequestID.current) {
        setDetailLoading(false)
      }
    }
  }

  const closeCheckpointDetail = () => {
    detailRequestID.current++
    setSelectedCheckpointID("")
    setCheckpointDetail(null)
    setDetailError(null)
    setDetailLoading(false)
  }

  const updateCheckpointHold = async (held: boolean) => {
    if (!checkpointDetail) return false
    setPending(true)
    setMutationError("")
    try {
      const updated = await setBackupCheckpointHold(checkpointDetail.id, held)
      setCheckpointDetail((current) => current ? { ...current, ...updated } : current)
      await loadCheckpoints(false)
      return true
    } catch (requestError) {
      setMutationError(errorMessage(requestError, intl))
      return false
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
            {status && !canWrite ? (
              <span className="rounded-full border border-border px-3 py-1 text-xs font-medium text-muted-foreground">
                {intl.formatMessage({ id: "backups.readOnly" })}
              </span>
            ) : null}
            <Button onClick={() => void refreshAll()} size="sm" variant="outline">
              {intl.formatMessage({ id: "common.refresh" })}
            </Button>
            {canWrite && status?.enabled ? (
              <details className="relative">
                <summary
                  aria-label={intl.formatMessage({ id: "backups.actions.more" })}
                  className="inline-flex h-8 cursor-pointer list-none items-center gap-1.5 rounded-full border border-border bg-background px-3 text-[0.8rem] font-medium text-foreground transition-colors hover:bg-muted [&::-webkit-details-marker]:hidden"
                  role="button"
                >
                  <MoreHorizontalIcon aria-hidden="true" className="size-3.5" />
                  {intl.formatMessage({ id: "backups.actions.more" })}
                </summary>
                <div className="absolute right-0 z-20 mt-1 min-w-64 rounded-lg border border-border bg-popover p-1 text-popover-foreground shadow-lg">
                  <Button
                    className="w-full justify-start"
                    onClick={() => setConfirmPublish(true)}
                    size="sm"
                    type="button"
                    variant="ghost"
                  >
                    {intl.formatMessage({ id: "backups.publish" })}
                  </Button>
                </div>
              </details>
            ) : null}
          </>
        }
      >
        {status?.enabled !== false ? (
          <PageTabs
            activeTab={activeTab}
            onTabChange={(tab) => setSearchParams(tab === "overview" ? {} : { tab })}
            tabs={[
              { id: "overview", label: intl.formatMessage({ id: "backups.tabs.overview" }) },
              { id: "checkpoints", label: intl.formatMessage({ id: "backups.tabs.checkpoints" }) },
            ]}
          />
        ) : null}
      </PageHeader>

      {loading && !status ? <ResourceState kind="loading" title={intl.formatMessage({ id: "backups.title" })} /> : null}
      {statusError && !status ? (
        <ResourceState kind={statusError instanceof ManagerApiError && statusError.status === 403 ? "forbidden" : "unavailable"} onRetry={() => void refreshAll()} title={intl.formatMessage({ id: "backups.title" })} />
      ) : null}
      {(statusError && status) || (catalogError && checkpoints.length > 0) ? (
        <div
          className="rounded-xl border border-warning/30 bg-warning/10 px-4 py-3 text-sm text-foreground"
          role="status"
        >
          {intl.formatMessage({ id: "backups.stale" })}
        </div>
      ) : null}

      {status && displayedTab === "overview" ? (
        <BackupOverview
          checkpoints={checkpoints}
          onViewAll={() => setSearchParams({ tab: "checkpoints" })}
          status={status}
        />
      ) : null}
      {status && displayedTab === "checkpoints" ? (
        catalogError && checkpoints.length === 0 ? (
          <ResourceState
            kind={catalogError instanceof ManagerApiError && catalogError.status === 403 ? "forbidden" : "unavailable"}
            onRetry={() => void loadCheckpoints(false)}
            title={intl.formatMessage({ id: "backups.checkpoints.title" })}
          />
        ) : (
          <CheckpointCatalog
            checkpoints={checkpoints}
            effectiveFromQuery={effectiveFromQuery}
            effectiveToQuery={effectiveToQuery}
            heldQuery={heldQuery}
            idQuery={idQuery}
            nextCursor={nextCursor}
            onClear={clearCheckpointFilters}
            onEffectiveFromQuery={setEffectiveFromQuery}
            onEffectiveToQuery={setEffectiveToQuery}
            onHeldQuery={setHeldQuery}
            onIDQuery={setIDQuery}
            onLoadMore={() => void loadCheckpoints(true, nextCursor)}
            onOpenDetail={(checkpointID) => void openCheckpointDetail(checkpointID)}
            onSearch={applyCheckpointFilters}
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
      <CheckpointDetailSheet
        canWrite={canWrite}
        checkpoint={checkpointDetail}
        error={detailError}
        loading={detailLoading}
        mutationError={mutationError}
        onClose={closeCheckpointDetail}
        onHold={() => void updateCheckpointHold(true)}
        onPrepareRecovery={() => {
          if (checkpointDetail) {
            navigate(`/cluster/backups/recovery/${encodeURIComponent(checkpointDetail.id)}`)
          }
        }}
        onRelease={() => {
          setReleaseConfirmation("")
          setConfirmRelease(true)
        }}
        open={Boolean(selectedCheckpointID)}
        pending={pending}
      />
      <ActionFormDialog
        description={intl.formatMessage(
          { id: "backups.release.description" },
          { id: checkpointDetail?.id ?? "" },
        )}
        error={mutationError}
        onOpenChange={setConfirmRelease}
        onSubmit={(event) => {
          event.preventDefault()
          if (releaseConfirmation !== checkpointDetail?.id) return
          void (async () => {
            if (await updateCheckpointHold(false)) {
              setConfirmRelease(false)
              setReleaseConfirmation("")
            }
          })()
        }}
        open={confirmRelease}
        pending={pending || releaseConfirmation !== checkpointDetail?.id}
        submitLabel={intl.formatMessage({ id: "backups.release.confirm" })}
        title={intl.formatMessage({ id: "backups.release.title" })}
      >
        <label className="grid gap-1 text-sm">
          <span className="text-muted-foreground">
            {intl.formatMessage({ id: "backups.release.idLabel" })}
          </span>
          <input
            aria-label={intl.formatMessage({ id: "backups.release.idLabel" })}
            autoComplete="off"
            className="h-9 rounded-md border border-border bg-background px-3 font-mono text-sm"
            onChange={(event) => setReleaseConfirmation(event.target.value)}
            value={releaseConfirmation}
          />
        </label>
      </ActionFormDialog>
    </PageContainer>
  )
}

function BackupOverview({
  status,
  checkpoints,
  onViewAll,
}: {
  status: ManagerBackupStatusResponse
  checkpoints: ManagerBackupCheckpoint[]
  onViewAll: () => void
}) {
  const intl = useIntl()
  if (!status.enabled) {
    return (
      <ResourceState
        description={intl.formatMessage({ id: "backups.disabled.description" })}
        kind="empty"
        title={intl.formatMessage({ id: "backups.disabled.title" })}
      />
    )
  }
  const repairSlots = status.integrity_audit.slots.filter((slot) => slot.health !== "healthy").length
  const needsAttention = status.health === "degraded" ||
    status.health === "failed" ||
    !status.capture_status_complete ||
    repairSlots > 0
  const syncing = !needsAttention &&
    (status.health === "unknown" || !status.latest_checkpoint)
  const heroTitle = needsAttention
    ? intl.formatMessage({ id: "backups.hero.attention" })
    : syncing
      ? intl.formatMessage({ id: "backups.hero.syncing" })
      : intl.formatMessage({ id: "backups.hero.healthy" })
  const heroClasses = needsAttention
    ? "border-warning/25 bg-warning/10"
    : syncing
      ? "border-border bg-muted/30"
      : "border-success/25 bg-success/10"
  const heroTitleClass = needsAttention
    ? "text-warning"
    : syncing
      ? "text-foreground"
      : "text-success"
  const checkpointOverage = status.checkpoint_age_seconds === null
    ? 0
    : Math.max(
        0,
        status.checkpoint_age_seconds - status.max_checkpoint_age_seconds,
      )
  const recent = checkpoints.slice(0, 3)
  const activity = status.restore
    ? {
        title: intl.formatMessage({ id: "backups.activity.restore" }),
        detail: intl.formatMessage(
          { id: "backups.activity.restoreProgress" },
          {
            completed: status.restore.installed_slots,
            total: status.restore.total_slots,
          },
        ),
      }
    : status.integrity_audit.cursor
      ? {
          title: intl.formatMessage({ id: "backups.activity.audit" }),
          detail: intl.formatMessage(
            { id: "backups.activity.auditProgress" },
            {
              phase: status.integrity_audit.cursor.phase,
              count: status.integrity_audit.debt_objects,
            },
          ),
        }
      : null

  return (
    <>
      <section className={`overflow-hidden rounded-3xl border p-5 sm:p-6 ${heroClasses}`}>
        <p className={`text-lg font-semibold ${heroTitleClass}`}>
          {heroTitle}
        </p>
        <div className="mt-5 grid gap-4 sm:grid-cols-2">
          <div>
            <p className="text-xs font-semibold uppercase tracking-[0.14em] text-muted-foreground">
              {intl.formatMessage({ id: "backups.hero.latest" })}
            </p>
            <p className="mt-1 text-xl font-medium text-foreground">
              <LocalTime value={status.latest_checkpoint?.effective_at_unix_millis} />
            </p>
            <p className="mt-1 text-sm text-muted-foreground">
              {checkpointOverage > 0
                ? intl.formatMessage(
                    { id: "backups.hero.exceeded" },
                    { duration: formatDurationWords(checkpointOverage, intl) },
                  )
                : formatRelativeDuration(status.checkpoint_age_seconds, intl)}
            </p>
          </div>
          <div>
            <p className="text-xs font-semibold uppercase tracking-[0.14em] text-muted-foreground">
              {intl.formatMessage({ id: "backups.hero.target" })}
            </p>
            <p className="mt-1 text-xl font-medium text-foreground">
              {formatDuration(status.max_checkpoint_age_seconds)}
            </p>
          </div>
        </div>
      </section>
      {needsAttention ? (
        <SectionCard title={intl.formatMessage({ id: "backups.issues.title" })}>
          <ul className="space-y-2 text-sm">
            {checkpointOverage > 0 ? (
              <li>{intl.formatMessage(
                { id: "backups.hero.exceeded" },
                { duration: formatDurationWords(checkpointOverage, intl) },
              )}</li>
            ) : null}
            {!status.capture_status_complete ? (
              <li>{intl.formatMessage(
                { id: "backups.capture.incomplete.description" },
                {
                  nodes: status.capture_status_missing_node_ids.length,
                  slots: status.capture_status_missing_slots.length,
                },
              )}</li>
            ) : null}
            {repairSlots > 0 ? (
              <li>{intl.formatMessage(
                { id: "backups.issues.integrity" },
                { count: repairSlots },
              )}</li>
            ) : null}
            {status.failure_category ? <li>{status.failure_category}</li> : null}
          </ul>
        </SectionCard>
      ) : null}
      {activity ? (
        <SectionCard title={intl.formatMessage({ id: "backups.activity.title" })}>
          <p className="font-medium text-foreground">{activity.title}</p>
          <p className="mt-1 text-sm text-muted-foreground">{activity.detail}</p>
        </SectionCard>
      ) : null}
      <SectionCard
        action={
          <Button onClick={onViewAll} size="sm" variant="ghost">
            {intl.formatMessage({ id: "backups.recent.viewAll" })}
          </Button>
        }
        title={intl.formatMessage({ id: "backups.recent.title" })}
      >
        {recent.length > 0 ? (
          <ul className="divide-y divide-border">
            {recent.map((checkpoint) => (
              <li className="flex flex-col gap-2 py-3 first:pt-0 last:pb-0 sm:flex-row sm:items-center sm:justify-between" key={checkpoint.id}>
                <div>
                  <LocalTime value={checkpoint.effective_at_unix_millis} />
                  <p className="mt-1 font-mono text-xs text-muted-foreground">{checkpoint.id}</p>
                </div>
                <span className="text-sm text-muted-foreground">
                  {checkpoint.held
                    ? intl.formatMessage({ id: "backups.checkpoints.protected" })
                    : intl.formatMessage({ id: "backups.checkpoints.published" })}
                </span>
              </li>
            ))}
          </ul>
        ) : (
          <ResourceState kind="empty" title={intl.formatMessage({ id: "backups.latest.empty" })} />
        )}
      </SectionCard>
      <details className="rounded-2xl border border-border bg-card px-4 py-3">
        <summary className="cursor-pointer text-sm font-medium text-foreground">
          {intl.formatMessage({ id: "backups.configuration.title" })}
        </summary>
        <dl className="mt-4 grid gap-3 text-sm sm:grid-cols-2 lg:grid-cols-3">
          <Policy label={intl.formatMessage({ id: "backups.policy.reconcile" })} value={formatDuration(status.policy.capture_reconcile_interval_seconds)} />
          <Policy label={intl.formatMessage({ id: "backups.policy.checkpoint" })} value={formatDuration(status.policy.checkpoint_interval_seconds)} />
          <Policy label={intl.formatMessage({ id: "backups.policy.workers" })} value={String(status.policy.capture_worker_count)} />
          <Policy label={intl.formatMessage({ id: "backups.policy.stagingQuota" })} value={formatBytes(status.policy.staging_max_bytes)} />
          <Policy label={intl.formatMessage({ id: "backups.policy.pinAge" })} value={formatDuration(status.policy.source_pin_max_age_seconds)} />
          <Policy label={intl.formatMessage({ id: "backups.policy.pinBytes" })} value={formatBytes(status.policy.max_source_pinned_bytes)} />
        </dl>
      </details>
    </>
  )
}

function Policy({ label, value }: { label: string; value: string }) {
  return <div><dt className="text-muted-foreground">{label}</dt><dd>{value}</dd></div>
}

function CheckpointCatalog(props: {
  checkpoints: ManagerBackupCheckpoint[]
  effectiveFromQuery: string
  effectiveToQuery: string
  heldQuery: string
  idQuery: string
  nextCursor: string
  onClear: () => void
  onEffectiveFromQuery: (value: string) => void
  onEffectiveToQuery: (value: string) => void
  onHeldQuery: (value: string) => void
  onIDQuery: (value: string) => void
  onLoadMore: () => void
  onOpenDetail: (checkpointID: string) => void
  onSearch: () => void
  total: number
}) {
  const intl = useIntl()
  return (
    <SectionCard title={intl.formatMessage({ id: "backups.checkpoints.title" })} description={intl.formatMessage({ id: "backups.checkpoints.count" }, { count: props.total })}>
      <div className="mb-4 flex flex-col gap-2 lg:flex-row lg:items-start">
        <input
          aria-label={intl.formatMessage({ id: "backups.checkpoints.search" })}
          className="h-9 min-w-64 flex-1 rounded-md border border-border bg-background px-3 text-sm"
          onChange={(event) => props.onIDQuery(event.target.value)}
          placeholder={intl.formatMessage({ id: "backups.checkpoints.search" })}
          value={props.idQuery}
        />
        <details className="relative">
          <summary
            className="inline-flex h-9 w-full cursor-pointer list-none items-center justify-center rounded-full border border-border bg-background px-4 text-sm font-medium text-foreground hover:bg-muted [&::-webkit-details-marker]:hidden lg:w-auto"
            role="button"
          >
            {intl.formatMessage({ id: "backups.filters.title" })}
          </summary>
          <div className="mt-2 grid gap-3 rounded-xl border border-border bg-popover p-4 shadow-lg lg:absolute lg:right-0 lg:z-20 lg:w-[28rem]">
            <label className="grid gap-1 text-sm">
              <span className="text-muted-foreground">{intl.formatMessage({ id: "backups.filters.protection" })}</span>
              <select
                aria-label={intl.formatMessage({ id: "backups.filters.protection" })}
                className="h-9 rounded-md border border-border bg-background px-3"
                onChange={(event) => props.onHeldQuery(event.target.value)}
                value={props.heldQuery}
              >
                <option value="">{intl.formatMessage({ id: "backups.filters.anyProtection" })}</option>
                <option value="true">{intl.formatMessage({ id: "backups.checkpoints.protected" })}</option>
                <option value="false">{intl.formatMessage({ id: "backups.checkpoints.standardRetention" })}</option>
              </select>
            </label>
            <div className="grid gap-3 sm:grid-cols-2">
              <label className="grid gap-1 text-sm">
                <span className="text-muted-foreground">{intl.formatMessage({ id: "backups.filters.from" })}</span>
                <input
                  className="h-9 rounded-md border border-border bg-background px-3"
                  onChange={(event) => props.onEffectiveFromQuery(event.target.value)}
                  type="date"
                  value={props.effectiveFromQuery}
                />
              </label>
              <label className="grid gap-1 text-sm">
                <span className="text-muted-foreground">{intl.formatMessage({ id: "backups.filters.to" })}</span>
                <input
                  className="h-9 rounded-md border border-border bg-background px-3"
                  onChange={(event) => props.onEffectiveToQuery(event.target.value)}
                  type="date"
                  value={props.effectiveToQuery}
                />
              </label>
            </div>
          </div>
        </details>
        <Button onClick={props.onSearch} size="sm">
          {intl.formatMessage({ id: "backups.filters.apply" })}
        </Button>
        <Button onClick={props.onClear} size="sm" variant="ghost">
          {intl.formatMessage({ id: "backups.filters.clear" })}
        </Button>
      </div>
      <div className="hidden overflow-x-auto rounded-md border border-border md:block">
        <table className="w-full min-w-[720px] text-left text-sm">
          <thead className="bg-muted/40 text-xs uppercase text-muted-foreground">
            <tr>
              <th className="p-3">{intl.formatMessage({ id: "backups.checkpoints.restorePoint" })}</th>
              <th className="p-3">{intl.formatMessage({ id: "backups.checkpoints.created" })}</th>
              <th className="p-3">{intl.formatMessage({ id: "backups.checkpoints.protection" })}</th>
              <th className="p-3 text-right">{intl.formatMessage({ id: "backups.checkpoints.action" })}</th>
            </tr>
          </thead>
          <tbody>
            {props.checkpoints.map((checkpoint) => (
              <tr className="border-t border-border" key={checkpoint.id}>
                <td className="p-3">
                  <LocalTime value={checkpoint.effective_at_unix_millis} />
                  <p className="mt-1 font-mono text-xs text-muted-foreground" title={checkpoint.id}>
                    {shortCheckpointID(checkpoint.id)}
                  </p>
                </td>
                <td className="p-3"><LocalTime value={checkpoint.created_at_unix_millis} /></td>
                <td className="p-3">
                  {checkpoint.held
                    ? intl.formatMessage({ id: "backups.checkpoints.protected" })
                    : intl.formatMessage({ id: "backups.checkpoints.standardRetention" })}
                </td>
                <td className="p-3 text-right">
                  <Button
                    aria-label={intl.formatMessage(
                      { id: "backups.checkpoints.viewDetailsFor" },
                      { id: checkpoint.id },
                    )}
                    onClick={() => props.onOpenDetail(checkpoint.id)}
                    size="sm"
                    variant="ghost"
                  >
                    {intl.formatMessage({ id: "backups.checkpoints.viewDetails" })}
                  </Button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <ul className="grid gap-3 md:hidden">
        {props.checkpoints.map((checkpoint) => (
          <li className="rounded-xl border border-border p-4" key={checkpoint.id}>
            <div className="flex items-start justify-between gap-3">
              <div>
                <LocalTime value={checkpoint.effective_at_unix_millis} />
                <p className="mt-1 font-mono text-xs text-muted-foreground" title={checkpoint.id}>
                  {shortCheckpointID(checkpoint.id)}
                </p>
              </div>
              <span className="text-xs text-muted-foreground">
                {checkpoint.held
                  ? intl.formatMessage({ id: "backups.checkpoints.protected" })
                  : intl.formatMessage({ id: "backups.checkpoints.standardRetention" })}
              </span>
            </div>
            <p className="mt-3 text-xs text-muted-foreground">
              {intl.formatMessage({ id: "backups.checkpoints.created" })}:{" "}
              <LocalTime value={checkpoint.created_at_unix_millis} />
            </p>
            <Button
              aria-label={intl.formatMessage(
                { id: "backups.checkpoints.viewDetailsFor" },
                { id: checkpoint.id },
              )}
              className="mt-3 w-full"
              onClick={() => props.onOpenDetail(checkpoint.id)}
              size="sm"
              variant="outline"
            >
              {intl.formatMessage({ id: "backups.checkpoints.viewDetails" })}
            </Button>
          </li>
        ))}
      </ul>
      {props.checkpoints.length === 0 ? <ResourceState kind="empty" title={intl.formatMessage({ id: "backups.checkpoints.empty" })} /> : null}
      {props.nextCursor ? <Button className="mt-3" onClick={props.onLoadMore} size="sm" variant="outline">{intl.formatMessage({ id: "common.loadMore" })}</Button> : null}
    </SectionCard>
  )
}

function CheckpointDetailSheet(props: {
  canWrite: boolean
  checkpoint: ManagerBackupCheckpointDetail | null
  error: Error | null
  loading: boolean
  mutationError: string
  onClose: () => void
  onHold: () => void
  onPrepareRecovery: () => void
  onRelease: () => void
  open: boolean
  pending: boolean
}) {
  const intl = useIntl()
  const checkpoint = props.checkpoint
  return (
    <DetailSheet
      description={checkpoint ? shortCheckpointID(checkpoint.id) : undefined}
      footer={checkpoint ? (
        <div className="flex flex-wrap justify-end gap-2">
          {props.canWrite ? (
            checkpoint.held ? (
              <Button disabled={props.pending} onClick={props.onRelease} size="sm" variant="outline">
                {intl.formatMessage({ id: "backups.detail.release" })}
              </Button>
            ) : (
              <Button disabled={props.pending} onClick={props.onHold} size="sm" variant="outline">
                {intl.formatMessage({ id: "backups.detail.hold" })}
              </Button>
            )
          ) : null}
          <Button onClick={props.onPrepareRecovery} size="sm">
            {intl.formatMessage({ id: "backups.detail.prepare" })}
          </Button>
        </div>
      ) : undefined}
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      open={props.open}
      title={intl.formatMessage({ id: "backups.detail.title" })}
    >
      {props.loading ? (
        <ResourceState kind="loading" title={intl.formatMessage({ id: "backups.detail.title" })} />
      ) : props.error ? (
        <ResourceState kind="unavailable" title={errorMessage(props.error, intl)} />
      ) : checkpoint ? (
        <div className="space-y-4">
          <SectionCard
            description={intl.formatMessage({ id: "backups.detail.recoveryDescription" })}
            title={intl.formatMessage({ id: "backups.detail.recovery" })}
          >
            <dl className="grid gap-3 text-sm sm:grid-cols-2">
              <Policy
                label={intl.formatMessage({ id: "backups.checkpoints.effective" })}
                value={new Date(checkpoint.effective_at_unix_millis).toLocaleString()}
              />
              <Policy
                label={intl.formatMessage({ id: "backups.checkpoints.created" })}
                value={new Date(checkpoint.created_at_unix_millis).toLocaleString()}
              />
            </dl>
          </SectionCard>
          <SectionCard
            description={checkpoint.held
              ? intl.formatMessage({ id: "backups.detail.protectedDescription" })
              : intl.formatMessage({ id: "backups.detail.retentionDescription" })}
            title={intl.formatMessage({ id: "backups.detail.protection" })}
          >
            <p className="text-sm font-medium">
              {checkpoint.held
                ? intl.formatMessage({ id: "backups.checkpoints.protected" })
                : intl.formatMessage({ id: "backups.checkpoints.standardRetention" })}
            </p>
          </SectionCard>
          {props.mutationError ? <p className="text-sm text-destructive">{props.mutationError}</p> : null}
          <details className="rounded-xl border border-border px-4 py-3">
            <summary className="cursor-pointer text-sm font-medium">
              {intl.formatMessage({ id: "backups.detail.technical" })}
            </summary>
            <dl className="mt-4 grid gap-3 text-sm">
              <Policy label={intl.formatMessage({ id: "backups.detail.id" })} value={checkpoint.id} />
              <Policy label={intl.formatMessage({ id: "backups.detail.sourceCluster" })} value={checkpoint.source_cluster_id} />
              <Policy label={intl.formatMessage({ id: "backups.detail.sourceGeneration" })} value={checkpoint.source_generation} />
              <Policy label={intl.formatMessage({ id: "backups.detail.hashSlots" })} value={String(checkpoint.hash_slot_count)} />
            </dl>
          </details>
        </div>
      ) : null}
    </DetailSheet>
  )
}

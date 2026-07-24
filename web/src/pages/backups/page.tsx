import { useCallback, useEffect, useMemo, useRef, useState } from "react"
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
  cancelBackupJob,
  getBackupRestorePoints,
  getBackupStatus,
  ManagerApiError,
  setBackupRestorePointHold,
  triggerMaterializedBackup,
  verifyBackupRestorePoint,
} from "@/lib/manager-api"
import type {
  ManagerBackupRestorePoint,
  ManagerBackupStatusResponse,
} from "@/lib/manager-api.types"
import {
  isBackupOperationActive,
  shouldRefreshRestorePointList,
} from "@/pages/backups/backup-refresh"

type BackupTab = "overview" | "points" | "recovery"
type ConfirmState =
  | { kind: "trigger" }
  | { kind: "cancel" }
  | { kind: "release"; point: ManagerBackupRestorePoint }
  | null

const tabs: BackupTab[] = ["overview", "points", "recovery"]

function isActiveStatus(status: ManagerBackupStatusResponse | null) {
  return isBackupOperationActive(status)
}

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
    backup_job_active: "backups.error.backupActive",
    verification_job_active: "backups.error.verificationActive",
    state_conflict: "backups.error.stateConflict",
    restore_point_not_found: "backups.error.pointNotFound",
    permission_denied: "backups.error.permissionDenied",
  }
  return intl.formatMessage({ id: messages[error.error] ?? "backups.error.serviceUnavailable" })
}

function shellQuote(value: string) {
  return `'${value.replaceAll("'", "'\"'\"'")}'`
}

function validManagerURL(value: string) {
  try {
    const parsed = new URL(value)
    if (parsed.username || parsed.password || parsed.pathname !== "/" || parsed.search || parsed.hash) return false
    if (parsed.protocol === "https:") return true
    return parsed.protocol === "http:" && ["localhost", "127.0.0.1", "[::1]"].includes(parsed.hostname)
  } catch {
    return false
  }
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
  const statusRef = useRef<ManagerBackupStatusResponse | null>(null)
  const [points, setPoints] = useState<ManagerBackupRestorePoint[]>([])
  const [nextCursor, setNextCursor] = useState("")
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(canRead)
  const [error, setError] = useState<Error | null>(null)
  const [pending, setPending] = useState(false)
  const [mutationError, setMutationError] = useState("")
  const [confirm, setConfirm] = useState<ConfirmState>(null)
  const [releaseText, setReleaseText] = useState("")
  const [idQuery, setIDQuery] = useState("")
  const [heldOnly, setHeldOnly] = useState(false)
  const [appliedFilters, setAppliedFilters] = useState({ id: "", held: false })
  const [selectedID, setSelectedID] = useState("")
  const [targetURL, setTargetURL] = useState("")
  const [repository, setRepository] = useState<"primary" | "secondary">("primary")
  const [tokenChoice, setTokenChoice] = useState<"preserve" | "invalidate" | "">("")
  const statusInFlight = useRef(false)
  const listInFlight = useRef(false)
  const previousStatus = useRef<ManagerBackupStatusResponse | null>(null)
  const pointRefreshPending = useRef(false)

  const canWrite = permissionWrite && status?.auth_enabled === true
  const writeDisabledReason = !permissionWrite
    ? intl.formatMessage({ id: "backups.write.permission" })
    : status?.auth_enabled === false
      ? intl.formatMessage({ id: "backups.write.authDisabled" })
      : ""

  const loadPoints = useCallback(async (append = false, cursor = "") => {
    if (!canRead || listInFlight.current) return false
    listInFlight.current = true
    try {
      const page = await getBackupRestorePoints({
        limit: 50,
        cursor,
        id: appliedFilters.id || undefined,
        held: appliedFilters.held,
      })
      setPoints((current) => append ? [...current, ...page.items] : page.items)
      setNextCursor(page.next_cursor ?? "")
      setTotal(page.total)
      setSelectedID((current) => current || page.items[0]?.id || "")
      return true
    } finally {
      listInFlight.current = false
    }
  }, [appliedFilters, canRead])

  const loadStatus = useCallback(async () => {
    if (!canRead || statusInFlight.current) return
    statusInFlight.current = true
    try {
      const snapshot = await getBackupStatus()
      const refreshPoints = shouldRefreshRestorePointList(previousStatus.current, snapshot)
      if (refreshPoints) {
        pointRefreshPending.current = true
      }
      previousStatus.current = snapshot
      statusRef.current = snapshot
      setStatus(snapshot)
      if (pointRefreshPending.current && await loadPoints(false)) {
        pointRefreshPending.current = false
      }
      setError(null)
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError : new Error("backup status request failed"))
    } finally {
      statusInFlight.current = false
    }
  }, [canRead, loadPoints])

  const refreshAll = useCallback(async () => {
    if (!canRead) return
    try {
      await Promise.all([loadStatus(), loadPoints(false)])
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError : new Error("backup request failed"))
    } finally {
      setLoading(false)
    }
  }, [canRead, loadPoints, loadStatus])

  useEffect(() => {
    const timer = window.setTimeout(() => void refreshAll(), 0)
    return () => window.clearTimeout(timer)
  }, [refreshAll])

  useEffect(() => {
    if (!canRead) return
    let timer = 0
    const schedule = () => {
      const delay = isActiveStatus(statusRef.current) ? 2_000 : 15_000
      timer = window.setTimeout(async () => {
        if (document.visibilityState === "visible") await loadStatus()
        schedule()
      }, delay)
    }
    const onVisibility = () => {
      if (document.visibilityState === "visible") void loadStatus()
    }
    schedule()
    document.addEventListener("visibilitychange", onVisibility)
    return () => {
      window.clearTimeout(timer)
      document.removeEventListener("visibilitychange", onVisibility)
    }
  }, [canRead, loadStatus])

  const runMutation = async (operation: () => Promise<unknown>, refreshPoints: boolean) => {
    setPending(true)
    setMutationError("")
    try {
      await operation()
      setConfirm(null)
      setReleaseText("")
      await loadStatus()
      if (refreshPoints) await loadPoints(false)
    } catch (requestError) {
      setMutationError(errorMessage(requestError, intl))
    } finally {
      setPending(false)
    }
  }

  const selectedPoint = points.find((point) => point.id === selectedID) ?? points[0]
  const recoveryBlocked = selectedPoint?.last_verification?.status === "failed"
  const urlValid = validManagerURL(targetURL)
  const recoveryCommands = useMemo(() => {
    if (!selectedPoint || recoveryBlocked || !urlValid || !tokenChoice) return []
    const server = shellQuote(targetURL)
    const invalidate = tokenChoice === "invalidate" ? " --invalidate-tokens" : ""
    return [
      `wkcli backup restore plan --server ${server} --restore-point ${shellQuote(selectedPoint.id)} --repository ${repository}${invalidate} --token "$WK_MANAGER_TOKEN"`,
      `wkcli backup restore start "$RESTORE_PLAN_ID" --server ${server} --token "$WK_MANAGER_TOKEN"`,
      `wkcli backup restore verify "$RESTORE_PLAN_ID" --server ${server} --token "$WK_MANAGER_TOKEN"`,
      `wkcli backup restore activate "$RESTORE_PLAN_ID" --server ${server} --old-cluster-fence-digest "$OLD_CLUSTER_FENCE_SHA256" --token "$WK_MANAGER_TOKEN"`,
    ]
  }, [recoveryBlocked, repository, selectedPoint, targetURL, tokenChoice, urlValid])

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
              aria-label={intl.formatMessage({ id: "backups.trigger" })}
              disabled={!canWrite || Boolean(status?.active) || isActiveStatus(status)}
              onClick={() => setConfirm({ kind: "trigger" })}
              size="sm"
              title={writeDisabledReason}
            >
              {intl.formatMessage({ id: "backups.trigger" })}
            </Button>
          </>
        }
      >
        <PageTabs
          activeTab={activeTab}
          onTabChange={(tab) => setSearchParams(tab === "overview" ? {} : { tab })}
          tabs={[
            { id: "overview", label: intl.formatMessage({ id: "backups.tabs.overview" }) },
            { id: "points", label: intl.formatMessage({ id: "backups.tabs.points" }) },
            { id: "recovery", label: intl.formatMessage({ id: "backups.tabs.recovery" }) },
          ]}
        />
      </PageHeader>

      {loading && !status ? <ResourceState kind="loading" title={intl.formatMessage({ id: "backups.title" })} /> : null}
      {error && !status ? (
        <ResourceState kind={error instanceof ManagerApiError && error.status === 403 ? "forbidden" : "unavailable"} onRetry={() => void refreshAll()} title={intl.formatMessage({ id: "backups.title" })} />
      ) : null}

      {status && activeTab === "overview" ? <BackupOverview status={status} canWrite={canWrite} writeDisabledReason={writeDisabledReason} onCancel={() => setConfirm({ kind: "cancel" })} /> : null}
      {status && activeTab === "points" ? (
        <RestorePoints
          canWrite={canWrite && !pending}
          heldOnly={heldOnly}
          idQuery={idQuery}
          nextCursor={nextCursor}
          onHeldOnly={setHeldOnly}
          onIDQuery={setIDQuery}
          onLoadMore={() => void loadPoints(true, nextCursor)}
          onSearch={() => setAppliedFilters({ id: idQuery.trim(), held: heldOnly })}
          onRelease={(point) => { setReleaseText(""); setConfirm({ kind: "release", point }) }}
          onSelectRecovery={(point) => { setSelectedID(point.id); setSearchParams({ tab: "recovery" }) }}
          onVerify={(point) => void runMutation(() => verifyBackupRestorePoint(point.id), true)}
          onHold={(point) => void runMutation(() => setBackupRestorePointHold(point.id, true), true)}
          points={points}
          total={total}
          writeDisabledReason={writeDisabledReason}
        />
      ) : null}
      {status && activeTab === "recovery" ? (
        <RecoveryGuide
          commands={recoveryCommands}
          point={selectedPoint}
          recoveryBlocked={recoveryBlocked}
          repository={repository}
          setRepository={setRepository}
          setTargetURL={setTargetURL}
          setTokenChoice={setTokenChoice}
          targetURL={targetURL}
          tokenChoice={tokenChoice}
          urlValid={urlValid}
        />
      ) : null}

      <ConfirmDialog
        confirmLabel={confirm?.kind === "trigger" ? intl.formatMessage({ id: "backups.trigger.confirm" }) : intl.formatMessage({ id: "common.confirm" })}
        description={
          confirm?.kind === "trigger"
            ? intl.formatMessage({ id: "backups.trigger.warning" })
            : confirm?.kind === "cancel"
              ? intl.formatMessage({ id: "backups.cancel.warning" }, { id: status?.active?.id ?? "", epoch: status?.active?.epoch ?? 0, completed: status?.active?.completed_partitions ?? 0, total: status?.active?.hash_slot_count ?? 0 })
              : confirm?.kind === "release"
                ? intl.formatMessage({ id: "backups.release.warning" }, { id: confirm.point.id })
                : ""
        }
        error={mutationError}
        onConfirm={() => {
          if (confirm?.kind === "trigger") void runMutation(triggerMaterializedBackup, false)
          if (confirm?.kind === "cancel" && status?.active) void runMutation(() => cancelBackupJob(status.active!.id, status.active!.epoch), false)
          if (confirm?.kind === "release" && releaseText === confirm.point.id) void runMutation(() => setBackupRestorePointHold(confirm.point.id, false), true)
        }}
        onOpenChange={(open) => { if (!open) setConfirm(null) }}
        open={confirm !== null}
        pending={pending || (confirm?.kind === "release" && releaseText !== confirm.point.id)}
        title={confirm?.kind === "trigger" ? intl.formatMessage({ id: "backups.trigger.title" }) : confirm?.kind === "cancel" ? intl.formatMessage({ id: "backups.cancel.title" }) : intl.formatMessage({ id: "backups.release.title" })}
      >
        {confirm?.kind === "release" ? (
          <input aria-label={intl.formatMessage({ id: "backups.release.input" })} className="h-9 w-full rounded-md border border-border bg-background px-3 text-sm" onChange={(event) => setReleaseText(event.target.value)} placeholder={confirm.point.id} value={releaseText} />
        ) : null}
      </ConfirmDialog>
    </PageContainer>
  )
}

function BackupOverview({ status, canWrite, writeDisabledReason, onCancel }: {
  status: ManagerBackupStatusResponse
  canWrite: boolean
  writeDisabledReason: string
  onCancel: () => void
}) {
  const intl = useIntl()
  const metrics = [
    [intl.formatMessage({ id: "backups.health" }), status.health],
    [intl.formatMessage({ id: "backups.rpo" }), `${formatDuration(status.recovery_point_age_seconds)} / ${formatDuration(status.max_recovery_point_age_seconds)}`],
    [intl.formatMessage({ id: "backups.verificationAge" }), `${formatDuration(status.verification_age_seconds)} / ${formatDuration(status.max_verification_age_seconds)}`],
    [intl.formatMessage({ id: "backups.coordinator" }), status.coordinator_node_id ? `#${status.coordinator_node_id}` : "—"],
  ]
  const activeDuration = status.active
    ? Math.max(0, Math.floor((status.observed_at_unix_millis - status.active.started_at_unix_millis) / 1000))
    : null
  const dependencyLabels: Record<string, string> = {
    primary: intl.formatMessage({ id: "backups.dependency.primary" }),
    secondary: intl.formatMessage({ id: "backups.dependency.secondary" }),
    kms: intl.formatMessage({ id: "backups.dependency.kms" }),
    staging: intl.formatMessage({ id: "backups.dependency.staging" }),
    utc: intl.formatMessage({ id: "backups.dependency.utc" }),
  }
  return (
    <>
      {!status.auth_enabled ? <ResourceState kind="unavailable" title={intl.formatMessage({ id: "backups.authReadonly" })} /> : null}
      <SectionCard title={intl.formatMessage({ id: "backups.overview.status" })}>
        <div className="grid overflow-hidden rounded-md border border-border md:grid-cols-4">
          {metrics.map(([label, value]) => (
            <div className="border-b border-border p-3 text-sm last:border-b-0 md:border-r md:border-b-0" key={label}>
              <div className="text-muted-foreground">{label}</div>
              <div className="mt-1 font-semibold">{label === intl.formatMessage({ id: "backups.health" }) ? <StatusBadge value={value} /> : value}</div>
            </div>
          ))}
        </div>
        <p className="mt-3 text-xs text-muted-foreground">
          {intl.formatMessage({ id: "backups.observed" })}: <LocalTime value={status.observed_at_unix_millis} />
        </p>
      </SectionCard>
      {status.active ? (
        <SectionCard
          action={<Button disabled={!canWrite} onClick={onCancel} size="sm" title={writeDisabledReason} variant="destructive">{intl.formatMessage({ id: "backups.cancel" })}</Button>}
          title={intl.formatMessage({ id: "backups.active.title" })}
        >
          <dl className="grid gap-3 text-sm md:grid-cols-5">
            <div><dt className="text-muted-foreground">{intl.formatMessage({ id: "backups.active.idEpoch" })}</dt><dd className="font-mono">{status.active.id} / {status.active.epoch}</dd></div>
            <div><dt className="text-muted-foreground">{intl.formatMessage({ id: "backups.active.phase" })}</dt><dd><StatusBadge value={status.active.status} /></dd></div>
            <div><dt className="text-muted-foreground">{intl.formatMessage({ id: "backups.active.progress" })}</dt><dd>{status.active.completed_partitions} / {status.active.hash_slot_count}</dd></div>
            <div><dt className="text-muted-foreground">{intl.formatMessage({ id: "backups.active.started" })}</dt><dd><LocalTime value={status.active.started_at_unix_millis} /></dd></div>
            <div><dt className="text-muted-foreground">{intl.formatMessage({ id: "backups.active.duration" })}</dt><dd>{formatDuration(activeDuration)}</dd></div>
          </dl>
          {status.active.failure_category ? (
            <p className="mt-3 text-sm text-destructive">
              {intl.formatMessage({ id: "backups.active.failure" })}: {status.active.failure_category}{" "}
              <a className="underline" href={`/cluster/system-logs?keyword=${encodeURIComponent(status.active.id)}`}>
                {intl.formatMessage({ id: "backups.active.logs" })}
              </a>
            </p>
          ) : null}
        </SectionCard>
      ) : null}
      {status.verification ? (
        <SectionCard title={intl.formatMessage({ id: "backups.verification.title" })}>
          <dl className="grid gap-3 text-sm md:grid-cols-5">
            <div><dt className="text-muted-foreground">ID</dt><dd className="font-mono">{status.verification.id}</dd></div>
            <div><dt className="text-muted-foreground">{intl.formatMessage({ id: "backups.verification.point" })}</dt><dd className="font-mono">{status.verification.restore_point_id}</dd></div>
            <div><dt className="text-muted-foreground">{intl.formatMessage({ id: "backups.active.phase" })}</dt><dd><StatusBadge value={status.verification.status} /></dd></div>
            <div><dt className="text-muted-foreground">{intl.formatMessage({ id: "backups.active.started" })}</dt><dd><LocalTime value={status.verification.started_at_unix_millis} /></dd></div>
            <div><dt className="text-muted-foreground">{intl.formatMessage({ id: "backups.active.duration" })}</dt><dd>{formatDuration(Math.max(0, Math.floor(((status.verification.completed_at_unix_millis || status.observed_at_unix_millis) - status.verification.started_at_unix_millis) / 1000)))}</dd></div>
          </dl>
          {status.verification.failure_category ? <p className="mt-3 text-sm text-destructive">{intl.formatMessage({ id: "backups.active.failure" })}: {status.verification.failure_category}</p> : null}
        </SectionCard>
      ) : null}
      <SectionCard title={intl.formatMessage({ id: "backups.dependencies" })}>
        <div className="grid gap-2 sm:grid-cols-5">
          {Object.entries(status.dependencies).filter(([key]) => key !== "checked_at_unix_millis").map(([key, value]) => {
            const dependency = value as { health: string; region?: string }
            return <div className="rounded-md border border-border p-3 text-sm" key={key}><div className="text-muted-foreground">{dependencyLabels[key] ?? key}</div><div className="mt-1"><StatusBadge value={dependency.health} /></div><div className="mt-1 text-xs">{dependency.region ?? "—"}</div></div>
          })}
        </div>
      </SectionCard>
      <SectionCard title={intl.formatMessage({ id: "backups.policy" })}>
        <dl className="grid gap-3 text-sm sm:grid-cols-2 lg:grid-cols-4">
          <div><dt className="text-muted-foreground">{intl.formatMessage({ id: "backups.policy.incrementalInterval" })}</dt><dd>{formatDuration(status.policy.incremental_interval_seconds)}</dd></div>
          <div><dt className="text-muted-foreground">{intl.formatMessage({ id: "backups.policy.restorePointInterval" })}</dt><dd>{formatDuration(status.policy.restore_point_interval_seconds)}</dd></div>
          <div><dt className="text-muted-foreground">{intl.formatMessage({ id: "backups.policy.independentFullInterval" })}</dt><dd>{formatDuration(status.policy.independent_full_interval_seconds)}</dd></div>
          <div><dt className="text-muted-foreground">{intl.formatMessage({ id: "backups.policy.materializedFullInterval" })}</dt><dd>{formatDuration(status.policy.materialized_full_interval_seconds)}</dd></div>
          <div><dt className="text-muted-foreground">{intl.formatMessage({ id: "backups.policy.objectLock" })}</dt><dd>{intl.formatMessage({ id: "backups.days" }, { count: status.policy.object_lock_days })}</dd></div>
          <div><dt className="text-muted-foreground">{intl.formatMessage({ id: "backups.policy.stagingQuota" })}</dt><dd>{formatBytes(status.policy.staging_max_bytes)}</dd></div>
          <div><dt className="text-muted-foreground">{intl.formatMessage({ id: "backups.policy.parallelism" })}</dt><dd>{status.policy.max_parallel_partitions}</dd></div>
          <div><dt className="text-muted-foreground">{intl.formatMessage({ id: "backups.policy.monthlyRetention" })}</dt><dd>{intl.formatMessage({ id: "backups.months" }, { count: status.policy.monthly_retention_months })}</dd></div>
          <div><dt className="text-muted-foreground">{intl.formatMessage({ id: "backups.policy.capacity" })}</dt><dd>{status.capacity.total} / {status.capacity.max} ({status.capacity.level}); {intl.formatMessage({ id: "backups.policy.held" })}: {status.capacity.held}</dd></div>
          <div><dt className="text-muted-foreground">{intl.formatMessage({ id: "backups.policy.pendingGC" })}</dt><dd>{status.capacity.pending}</dd></div>
        </dl>
      </SectionCard>
    </>
  )
}

function RestorePoints(props: {
  canWrite: boolean
  heldOnly: boolean
  idQuery: string
  nextCursor: string
  onHeldOnly: (value: boolean) => void
  onIDQuery: (value: string) => void
  onLoadMore: () => void
  onSearch: () => void
  onRelease: (point: ManagerBackupRestorePoint) => void
  onSelectRecovery: (point: ManagerBackupRestorePoint) => void
  onVerify: (point: ManagerBackupRestorePoint) => void
  onHold: (point: ManagerBackupRestorePoint) => void
  points: ManagerBackupRestorePoint[]
  total: number
  writeDisabledReason: string
}) {
  const intl = useIntl()
  return (
    <SectionCard title={intl.formatMessage({ id: "backups.points.title" })} description={intl.formatMessage({ id: "backups.points.count" }, { count: props.total })}>
      <div className="mb-3 flex flex-wrap gap-2">
        <input aria-label={intl.formatMessage({ id: "backups.points.search" })} className="h-8 min-w-60 rounded-md border border-border bg-background px-3 text-sm" onChange={(event) => props.onIDQuery(event.target.value)} placeholder={intl.formatMessage({ id: "backups.points.search" })} value={props.idQuery} />
        <label className="inline-flex items-center gap-2 text-sm"><input checked={props.heldOnly} onChange={(event) => props.onHeldOnly(event.target.checked)} type="checkbox" />{intl.formatMessage({ id: "backups.points.heldOnly" })}</label>
        <Button onClick={props.onSearch} size="sm" variant="outline">{intl.formatMessage({ id: "common.search" })}</Button>
      </div>
      <div className="overflow-x-auto rounded-md border border-border">
        <table className="w-full min-w-[980px] text-left text-sm">
          <thead className="bg-muted/40 text-xs uppercase text-muted-foreground"><tr><th className="p-3">ID</th><th className="p-3">{intl.formatMessage({ id: "backups.points.kind" })}</th><th className="p-3">{intl.formatMessage({ id: "backups.points.effective" })}</th><th className="p-3">{intl.formatMessage({ id: "backups.points.created" })}</th><th className="p-3">{intl.formatMessage({ id: "backups.points.publication" })}</th><th className="p-3">{intl.formatMessage({ id: "backups.points.audit" })}</th><th className="p-3">{intl.formatMessage({ id: "backups.points.hold" })}</th><th className="p-3">{intl.formatMessage({ id: "backups.points.actions" })}</th></tr></thead>
          <tbody>
            {props.points.map((point) => (
              <tr className="border-t border-border" key={point.id}>
                <td className="p-3 font-mono text-xs">{point.id}</td>
                <td className="p-3">{point.kind}</td>
                <td className="p-3"><LocalTime value={point.effective_at_unix_millis} /></td>
                <td className="p-3"><LocalTime value={point.created_at_unix_millis} /></td>
                <td className="p-3"><div className="flex items-center gap-2"><StatusBadge value={point.primary_verified && point.secondary_verified ? "succeeded" : "failed"} /><span>{intl.formatMessage({ id: point.primary_verified && point.secondary_verified ? "backups.points.publicationVerified" : "backups.points.publicationIncomplete" })}</span></div></td>
                <td className="p-3"><StatusBadge value={point.last_verification?.status ?? "not_run"} /></td>
                <td className="p-3">{point.held ? intl.formatMessage({ id: "backups.points.held" }) : "—"}</td>
                <td className="p-3"><div className="flex flex-wrap gap-1">
                  <Button disabled={!props.canWrite} onClick={() => props.onVerify(point)} size="xs" title={props.writeDisabledReason} variant="outline">{intl.formatMessage({ id: "backups.points.reverify" })}</Button>
                  {point.held
                    ? <Button disabled={!props.canWrite} onClick={() => props.onRelease(point)} size="xs" title={props.writeDisabledReason} variant="outline">{intl.formatMessage({ id: "backups.points.release" })}</Button>
                    : <Button disabled={!props.canWrite} onClick={() => props.onHold(point)} size="xs" title={props.writeDisabledReason} variant="outline">{intl.formatMessage({ id: "backups.points.placeHold" })}</Button>}
                  <Button onClick={() => props.onSelectRecovery(point)} size="xs" variant="ghost">{intl.formatMessage({ id: "backups.points.recovery" })}</Button>
                </div></td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {props.nextCursor ? <Button className="mt-3" onClick={props.onLoadMore} size="sm" variant="outline">{intl.formatMessage({ id: "common.loadMore" })}</Button> : null}
    </SectionCard>
  )
}

function RecoveryGuide(props: {
  commands: string[]
  point?: ManagerBackupRestorePoint
  recoveryBlocked: boolean
  repository: "primary" | "secondary"
  setRepository: (value: "primary" | "secondary") => void
  setTargetURL: (value: string) => void
  setTokenChoice: (value: "preserve" | "invalidate") => void
  targetURL: string
  tokenChoice: "preserve" | "invalidate" | ""
  urlValid: boolean
}) {
  const intl = useIntl()
  const exportCommands = props.commands.map((command) =>
    command.replaceAll(shellQuote(props.targetURL), '"$RESTORE_MANAGER_URL"'),
  )
  const markdown = exportCommands.map((command) => `\`\`\`sh\n${command}\n\`\`\``).join("\n\n")
  return (
    <SectionCard title={intl.formatMessage({ id: "backups.recovery.title" })} description={intl.formatMessage({ id: "backups.recovery.description" })}>
      <div className="mb-4 rounded-md border border-border bg-muted/30 p-3 text-sm">
        <div className="font-semibold">{intl.formatMessage({ id: "backups.recovery.runbook" })}</div>
        <p className="mt-1 text-muted-foreground">{intl.formatMessage({ id: "backups.recovery.runbookDescription" })}</p>
      </div>
      {!props.point ? <ResourceState kind="empty" title={intl.formatMessage({ id: "backups.recovery.noPoint" })} /> : null}
      {props.point ? <div className="space-y-4">
        <div className="rounded-md border border-border p-3 text-sm"><span className="text-muted-foreground">{intl.formatMessage({ id: "backups.recovery.exactPoint" })}: </span><span className="font-mono">{props.point.id}</span></div>
        {props.recoveryBlocked ? <ResourceState kind="error" title={intl.formatMessage({ id: "backups.recovery.failedAudit" })} /> : <>
          <label className="block text-sm">{intl.formatMessage({ id: "backups.recovery.targetURL" })}<input aria-label={intl.formatMessage({ id: "backups.recovery.targetURL" })} className="mt-1 h-9 w-full rounded-md border border-border bg-background px-3" onChange={(event) => props.setTargetURL(event.target.value)} placeholder="https://restore-manager.example.com" value={props.targetURL} /></label>
          {props.targetURL && !props.urlValid ? <p className="text-sm text-destructive">{intl.formatMessage({ id: "backups.recovery.urlInvalid" })}</p> : null}
          <label className="block text-sm">{intl.formatMessage({ id: "backups.recovery.repository" })}<select className="mt-1 h-9 w-full rounded-md border border-border bg-background px-3" onChange={(event) => props.setRepository(event.target.value as "primary" | "secondary")} value={props.repository}><option value="primary">{intl.formatMessage({ id: "backups.recovery.primary" })}</option><option value="secondary">{intl.formatMessage({ id: "backups.recovery.secondary" })}</option></select></label>
          <fieldset className="space-y-2 text-sm"><legend>{intl.formatMessage({ id: "backups.recovery.tokens" })}</legend><label className="flex gap-2"><input checked={props.tokenChoice === "preserve"} name="tokens" onChange={() => props.setTokenChoice("preserve")} type="radio" />{intl.formatMessage({ id: "backups.recovery.preserveTokens" })}</label><label className="flex gap-2"><input checked={props.tokenChoice === "invalidate"} name="tokens" onChange={() => props.setTokenChoice("invalidate")} type="radio" />{intl.formatMessage({ id: "backups.recovery.invalidateTokens" })}</label></fieldset>
          <p className="text-xs text-muted-foreground">{intl.formatMessage({ id: "backups.recovery.browserOnly" })}</p>
          {props.commands.length ? <div className="space-y-3">{props.commands.map((command, index) => <div className="space-y-1" key={command}><pre className="overflow-x-auto rounded-md bg-muted p-3 text-xs"><code>{command}</code></pre><Button aria-label={intl.formatMessage({ id: "backups.recovery.copyCommand" }, { index: index + 1 })} onClick={() => void navigator.clipboard?.writeText(command)} size="xs" variant="outline">{intl.formatMessage({ id: "backups.recovery.copyCommand" }, { index: index + 1 })}</Button></div>)}<div className="flex gap-2"><Button onClick={() => void navigator.clipboard?.writeText(props.commands.join("\n"))} size="sm" variant="outline">{intl.formatMessage({ id: "backups.recovery.copyAll" })}</Button><Button onClick={() => {
            const blob = new Blob([`# WuKongIM recovery guide\n\nCanonical runbook: \`docs/development/BACKUP_AND_RESTORE.md\`\n\nRestore point: \`${props.point!.id}\`\n\nToken policy: ${props.tokenChoice}\n\nSet \`RESTORE_MANAGER_URL\` locally before using these commands. No target URL, token, or fence digest is stored in this export.\n\n${markdown}\n`], { type: "text/markdown" })
            const href = URL.createObjectURL(blob)
            const link = document.createElement("a")
            link.href = href
            link.download = `wukongim-recovery-${props.point!.id}.md`
            link.click()
            URL.revokeObjectURL(href)
          }} size="sm" variant="outline">{intl.formatMessage({ id: "backups.recovery.export" })}</Button></div></div> : null}
        </>}
      </div> : null}
    </SectionCard>
  )
}

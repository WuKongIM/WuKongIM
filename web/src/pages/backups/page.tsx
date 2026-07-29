import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent, type ReactNode } from "react"
import { ArchiveRestore, Pause, Play, RefreshCw, ShieldCheck, Trash2 } from "lucide-react"
import { useIntl } from "react-intl"

import { useAuthStore } from "@/auth/auth-store"
import { hasManagerPermission } from "@/auth/permissions"
import { ActionFormDialog } from "@/components/manager/action-form-dialog"
import { ConfirmDialog } from "@/components/manager/confirm-dialog"
import { ResourceState } from "@/components/manager/resource-state"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"
import { Button } from "@/components/ui/button"
import {
  cancelBackupJob,
  cancelBackupRestore,
  deleteBackupArchive,
  getBackupDashboard,
  ManagerApiError,
  saveBackupPlan,
  setBackupArchiveHold,
  startBackupJob,
  startBackupRestore,
  testBackupRepository,
  verifyBackupArchive,
} from "@/lib/manager-api"
import type {
  ManagerBackupArchive,
  ManagerBackupDashboard,
  ManagerBackupPlan,
  ManagerBackupPlanInput,
  ManagerRestoreSlotProgress,
} from "@/lib/manager-api.types"

type ScheduleMode = "daily" | "half_day" | "custom"

type PlanDraft = {
  enabled: boolean
  scheduleMode: ScheduleMode
  cron: string
  timeZone: string
  storeKind: "file" | "s3"
  endpoint: string
  region: string
  bucket: string
  prefix: string
  pathStyle: boolean
  accessKey: string
  secretKey: string
  retentionCount: number
  rateMiBPerSecond: number
  workersPerNode: number
  maxDurationHours: number
}

const defaultTimeZone = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC"

function scheduleMode(cron: string): ScheduleMode {
  if (cron === "0 1 * * *") return "daily"
  if (cron === "@every 12h") return "half_day"
  return "custom"
}

function draftFromPlan(plan?: ManagerBackupPlan): PlanDraft {
  return {
    enabled: plan?.enabled ?? false,
    scheduleMode: scheduleMode(plan?.cron ?? "0 1 * * *"),
    cron: plan?.cron ?? "0 1 * * *",
    timeZone: plan?.time_zone || defaultTimeZone,
    storeKind: plan?.store.kind ?? "file",
    endpoint: plan?.store.endpoint ?? "",
    region: plan?.store.region ?? "",
    bucket: plan?.store.bucket ?? "",
    prefix: plan?.store.prefix ?? "",
    pathStyle: plan?.store.path_style ?? false,
    accessKey: "",
    secretKey: "",
    retentionCount: plan?.retention_count ?? 7,
    rateMiBPerSecond: plan ? Math.max(1, Math.round(plan.rate_bytes_per_sec / 1_048_576)) : 50,
    workersPerNode: plan?.workers_per_node ?? 1,
    maxDurationHours: plan ? Math.max(1, Math.round(plan.max_duration_ms / 3_600_000)) : 12,
  }
}

function planInput(
  draft: PlanDraft,
  plan?: ManagerBackupPlan,
  expectedRevision = plan?.revision ?? 0,
): ManagerBackupPlanInput {
  const cron = draft.scheduleMode === "daily"
    ? "0 1 * * *"
    : draft.scheduleMode === "half_day"
      ? "@every 12h"
      : draft.cron.trim()
  return {
    expected_revision: expectedRevision,
    enabled: draft.enabled,
    store: {
      kind: draft.storeKind,
      ...(draft.storeKind === "s3" ? {
        endpoint: draft.endpoint.trim(),
        region: draft.region.trim(),
        bucket: draft.bucket.trim(),
        prefix: draft.prefix.trim(),
        path_style: draft.pathStyle,
        access_key: draft.accessKey.trim() || undefined,
        secret_key: draft.secretKey || undefined,
      } : {}),
    },
    cron,
    time_zone: draft.timeZone.trim(),
    retention_count: draft.retentionCount,
    rate_mib_per_second: draft.rateMiBPerSecond,
    workers_per_node: draft.workersPerNode,
    max_duration_hours: draft.maxDurationHours,
  }
}

function errorKind(error: Error | null) {
  if (!(error instanceof ManagerApiError)) return "error" as const
  if (error.status === 403) return "forbidden" as const
  if (error.status === 503) return "unavailable" as const
  return "error" as const
}

function errorMessage(error: unknown, repositoryUnavailable: string) {
  if (error instanceof ManagerApiError &&
    error.error === "backup_store_unreachable") {
    return repositoryUnavailable
  }
  return error instanceof Error ? error.message : "Backup operation failed"
}

function formatBytes(value: number) {
  if (!Number.isFinite(value) || value <= 0) return "0 B"
  const units = ["B", "KiB", "MiB", "GiB", "TiB"]
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1)
  return `${(value / 1024 ** index).toFixed(index === 0 ? 0 : 1)} ${units[index]}`
}

function formatDate(value: number) {
  return value > 0 ? new Date(value).toLocaleString() : "-"
}

function taskProgress(slots: { status: string }[]) {
  const complete = slots.filter((slot) => slot.status === "complete" || slot.status === "verified").length
  return { complete, total: slots.length, percent: slots.length === 0 ? 0 : Math.round((complete / slots.length) * 100) }
}

function restoreNodeProgress(slots: ManagerRestoreSlotProgress[]) {
  const nodes = new Map<number, { total: number; verified: number }>()
  for (const slot of slots) {
    for (const nodeID of slot.replica_node_ids ?? []) {
      const progress = nodes.get(nodeID) ?? { total: 0, verified: 0 }
      progress.total++
      if (slot.status === "verified") progress.verified++
      nodes.set(nodeID, progress)
    }
  }
  return [...nodes.entries()]
    .sort(([left], [right]) => left - right)
    .map(([nodeID, progress]) => ({ nodeID, ...progress }))
}

function restorePhaseMessageID(status: string) {
  const phases: Record<string, string> = {
    preparing: "backups.task.phase.preparing",
    validated: "backups.task.phase.validated",
    maintenance: "backups.task.phase.maintenance",
    staging: "backups.task.phase.staging",
    verifying: "backups.task.phase.verifying",
    switching: "backups.task.phase.switching",
    finalizing: "backups.task.phase.finalizing",
    rolling_back: "backups.task.phase.rollingBack",
  }
  return phases[status]
}

function taskKindMessageID(kind: string) {
  const kinds: Record<string, string> = {
    backup: "backups.task.backup",
    restore: "backups.task.restore",
    verification: "backups.task.verification",
    retention: "backups.task.retention",
  }
  return kinds[kind]
}

function taskStatusMessageID(status: string) {
  const statuses: Record<string, string> = {
    succeeded: "backups.history.succeeded",
    failed: "backups.history.failed",
    canceled: "backups.history.canceled",
  }
  return statuses[status]
}

function exactRestorePermission(permissions: { resource: string; actions: string[] }[]) {
  return permissions.some((permission) =>
    permission.resource === "cluster.restore" &&
    permission.actions.includes("w"))
}

export function BackupsPage() {
  const intl = useIntl()
  const authStatus = useAuthStore((state) => state.status)
  const username = useAuthStore((state) => state.username)
  const permissions = useAuthStore((state) => state.permissions)
  const canRead = useMemo(
    () => hasManagerPermission(permissions, "cluster.backup", "r"),
    [permissions],
  )
  const canWrite = useMemo(
    () => authStatus === "authenticated" &&
      hasManagerPermission(permissions, "cluster.backup", "w"),
    [authStatus, permissions],
  )
  const canRestore = useMemo(
    () => authStatus === "authenticated" && exactRestorePermission(permissions),
    [authStatus, permissions],
  )

  const [dashboard, setDashboard] = useState<ManagerBackupDashboard | null>(null)
  const [draft, setDraft] = useState<PlanDraft>(() => draftFromPlan())
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<Error | null>(null)
  const [notice, setNotice] = useState("")
  const [mutationError, setMutationError] = useState("")
  const [busy, setBusy] = useState("")
  const [deleteArchive, setDeleteArchive] = useState<ManagerBackupArchive | null>(null)
  const [restoreArchive, setRestoreArchive] = useState<ManagerBackupArchive | null>(null)
  const [restorePassword, setRestorePassword] = useState("")
  const [restoreConfirmation, setRestoreConfirmation] = useState("")
  const initialized = useRef(false)
  const draftRevision = useRef(0)

  const load = useCallback(async () => {
    if (!canRead) {
      return
    }
    try {
      const next = await getBackupDashboard()
      setDashboard(next)
      setError(null)
      if (!initialized.current) {
        setDraft(draftFromPlan(next.state.plan))
        draftRevision.current = next.state.plan?.revision ?? 0
        initialized.current = true
      }
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError : new Error("Backup request failed"))
    } finally {
      setLoading(false)
    }
  }, [canRead])

  useEffect(() => {
    if (!canRead) return
    const initial = window.setTimeout(() => { void load() }, 0)
    const timer = window.setInterval(() => { void load() }, 30_000)
    return () => {
      window.clearTimeout(initial)
      window.clearInterval(timer)
    }
  }, [canRead, load])

  const mutate = useCallback(async (
    operation: string,
    action: () => Promise<void>,
    successMessage: string,
  ) => {
    setBusy(operation)
    setMutationError("")
    setNotice("")
    try {
      await action()
      setNotice(successMessage)
      await load()
      return true
    } catch (mutationFailure) {
      setMutationError(errorMessage(
        mutationFailure,
        intl.formatMessage({ id: "backups.error.repositoryUnavailable" }),
      ))
      return false
    } finally {
      setBusy("")
    }
  }, [intl, load])

  if (!canRead) {
    return (
      <PageContainer>
        <PageHeader
          title={intl.formatMessage({ id: "backups.title" })}
          description={intl.formatMessage({ id: "backups.description" })}
        />
        <ResourceState kind="forbidden" title={intl.formatMessage({ id: "backups.forbidden" })} />
      </PageContainer>
    )
  }

  const plan = dashboard?.state.plan
  const activeBackup = dashboard?.state.active_backup
  const activeRestore = dashboard?.state.active_restore
  const activeTask = activeRestore ?? activeBackup
  const backupCanCancel = activeBackup
    ? activeBackup.status !== "publishing" && activeBackup.status !== "cleaning"
    : false
  const restoreCanCancel = activeRestore
    ? ["preparing", "validated", "maintenance", "staging", "verifying"].includes(activeRestore.status)
    : false
  const progress = activeTask ? taskProgress(activeTask.slots) : null
  const restoreNodes = activeRestore ? restoreNodeProgress(activeRestore.slots) : []
  const writeDisabled = !canWrite || busy !== ""

  return (
    <PageContainer>
      <PageHeader
        title={intl.formatMessage({ id: "backups.title" })}
        description={intl.formatMessage({ id: "backups.description" })}
        actions={(
          <Button disabled={loading} onClick={() => { setLoading(true); void load() }} variant="outline">
            <RefreshCw /> {intl.formatMessage({ id: "common.refresh" })}
          </Button>
        )}
      />

      {!canWrite ? (
        <div className="rounded-md border border-amber-500/40 bg-amber-500/10 px-4 py-3 text-sm text-foreground">
          {intl.formatMessage({
            id: authStatus === "readonly" ? "backups.authReadonly" : "backups.write.permission",
          })}
        </div>
      ) : null}
      {notice ? (
        <div className="rounded-md border border-emerald-500/40 bg-emerald-500/10 px-4 py-3 text-sm text-foreground">
          {notice}
        </div>
      ) : null}
      {mutationError ? (
        <div className="rounded-md border border-destructive/40 bg-destructive/10 px-4 py-3 text-sm text-destructive">
          {mutationError}
        </div>
      ) : null}
      {dashboard?.backup_health === "warning" ? (
        <div className="rounded-md border border-amber-500/40 bg-amber-500/10 px-4 py-3 text-sm text-foreground">
          {intl.formatMessage({ id: "backups.health.warning" })}
        </div>
      ) : null}
      {dashboard?.backup_health === "critical" ? (
        <div className="rounded-md border border-destructive/40 bg-destructive/10 px-4 py-3 text-sm text-destructive">
          {intl.formatMessage({ id: "backups.health.critical" })}
        </div>
      ) : null}

      {loading && !dashboard ? (
        <ResourceState kind="loading" title={intl.formatMessage({ id: "backups.title" })} />
      ) : null}
      {!loading && error && !dashboard ? (
        <ResourceState
          kind={errorKind(error)}
          onRetry={() => { setLoading(true); void load() }}
          title={intl.formatMessage({ id: "backups.title" })}
        />
      ) : null}
      {error && dashboard ? (
        <div className="rounded-md border border-amber-500/40 bg-amber-500/10 px-4 py-3 text-sm">
          {intl.formatMessage({ id: "backups.stale" })}
        </div>
      ) : null}
      {dashboard?.repository_error ? (
        <div className="rounded-md border border-amber-500/40 bg-amber-500/10 px-4 py-3 text-sm">
          {intl.formatMessage({ id: "backups.repository.unavailable" })}
        </div>
      ) : null}

      {dashboard ? (
        <>
          <SectionCard
            title={intl.formatMessage({ id: "backups.plan.title" })}
            description={intl.formatMessage({ id: "backups.plan.description" })}
          >
            <div className="grid gap-4 lg:grid-cols-2">
              <label className="flex items-center justify-between gap-4 rounded-md border border-border px-3 py-3 text-sm font-medium">
                <span>
                  <span className="block text-foreground">{intl.formatMessage({ id: "backups.plan.enabled" })}</span>
                  <span className="mt-1 block font-normal text-muted-foreground">
                    {intl.formatMessage({ id: "backups.plan.enabledDescription" })}
                  </span>
                </span>
                <input
                  checked={draft.enabled}
                  disabled={writeDisabled}
                  onChange={(event) => setDraft((current) => ({ ...current, enabled: event.target.checked }))}
                  type="checkbox"
                />
              </label>

              <Field label={intl.formatMessage({ id: "backups.plan.schedule" })}>
                <select
                  className="h-9 rounded-md border border-input bg-background px-3 text-sm"
                  disabled={writeDisabled}
                  onChange={(event) => setDraft((current) => ({
                    ...current, scheduleMode: event.target.value as ScheduleMode,
                  }))}
                  value={draft.scheduleMode}
                >
                  <option value="daily">{intl.formatMessage({ id: "backups.schedule.daily" })}</option>
                  <option value="half_day">{intl.formatMessage({ id: "backups.schedule.halfDay" })}</option>
                  <option value="custom">{intl.formatMessage({ id: "backups.schedule.custom" })}</option>
                </select>
              </Field>

              {draft.scheduleMode === "custom" ? (
                <Field label={intl.formatMessage({ id: "backups.plan.cron" })}>
                  <input
                    className="h-9 rounded-md border border-input bg-background px-3 text-sm"
                    disabled={writeDisabled}
                    onChange={(event) => setDraft((current) => ({ ...current, cron: event.target.value }))}
                    placeholder="0 1 * * * or @every 12h"
                    value={draft.cron}
                  />
                </Field>
              ) : null}

              <Field label={intl.formatMessage({ id: "backups.plan.timeZone" })}>
                <input
                  className="h-9 rounded-md border border-input bg-background px-3 text-sm"
                  disabled={writeDisabled}
                  onChange={(event) => setDraft((current) => ({ ...current, timeZone: event.target.value }))}
                  value={draft.timeZone}
                />
                {dashboard.next_scheduled_unix_ms ? (
                  <span className="text-xs text-muted-foreground">
                    {intl.formatMessage(
                      { id: "backups.plan.nextRun" },
                      { time: formatDate(dashboard.next_scheduled_unix_ms) },
                    )}
                  </span>
                ) : null}
              </Field>

              <Field label={intl.formatMessage({ id: "backups.plan.repository" })}>
                <select
                  className="h-9 rounded-md border border-input bg-background px-3 text-sm"
                  disabled={writeDisabled}
                  onChange={(event) => setDraft((current) => ({
                    ...current, storeKind: event.target.value as "file" | "s3",
                  }))}
                  value={draft.storeKind}
                >
                  <option value="file">{intl.formatMessage({ id: "backups.repository.file" })}</option>
                  <option value="s3">{intl.formatMessage({ id: "backups.repository.s3" })}</option>
                </select>
              </Field>
            </div>

            {draft.storeKind === "file" ? (
              <p className="mt-3 text-sm text-muted-foreground">
                {intl.formatMessage({ id: "backups.repository.fileDescription" })}
              </p>
            ) : (
              <div className="mt-4 grid gap-4 rounded-md border border-border p-4 lg:grid-cols-2">
                <Field label={intl.formatMessage({ id: "backups.repository.endpoint" })}>
                  <input className="h-9 rounded-md border border-input bg-background px-3 text-sm" disabled={writeDisabled}
                    onChange={(event) => setDraft((current) => ({ ...current, endpoint: event.target.value }))} value={draft.endpoint} />
                </Field>
                <Field label={intl.formatMessage({ id: "backups.repository.region" })}>
                  <input className="h-9 rounded-md border border-input bg-background px-3 text-sm" disabled={writeDisabled}
                    onChange={(event) => setDraft((current) => ({ ...current, region: event.target.value }))} value={draft.region} />
                </Field>
                <Field label={intl.formatMessage({ id: "backups.repository.bucket" })}>
                  <input className="h-9 rounded-md border border-input bg-background px-3 text-sm" disabled={writeDisabled}
                    onChange={(event) => setDraft((current) => ({ ...current, bucket: event.target.value }))} value={draft.bucket} />
                </Field>
                <Field label={intl.formatMessage({ id: "backups.repository.prefix" })}>
                  <input className="h-9 rounded-md border border-input bg-background px-3 text-sm" disabled={writeDisabled}
                    onChange={(event) => setDraft((current) => ({ ...current, prefix: event.target.value }))} value={draft.prefix} />
                </Field>
                <Field label={intl.formatMessage({ id: "backups.repository.accessKey" })}>
                  <input className="h-9 rounded-md border border-input bg-background px-3 text-sm" disabled={writeDisabled}
                    onChange={(event) => setDraft((current) => ({ ...current, accessKey: event.target.value }))}
                    placeholder={dashboard.credentials_configured ? intl.formatMessage({ id: "backups.repository.keepCredential" }) : ""}
                    value={draft.accessKey} />
                </Field>
                <Field label={intl.formatMessage({ id: "backups.repository.secretKey" })}>
                  <input className="h-9 rounded-md border border-input bg-background px-3 text-sm" disabled={writeDisabled}
                    onChange={(event) => setDraft((current) => ({ ...current, secretKey: event.target.value }))}
                    placeholder={dashboard.credentials_configured ? intl.formatMessage({ id: "backups.repository.keepCredential" }) : ""}
                    type="password" value={draft.secretKey} />
                </Field>
                <label className="flex items-center gap-2 text-sm">
                  <input checked={draft.pathStyle} disabled={writeDisabled}
                    onChange={(event) => setDraft((current) => ({ ...current, pathStyle: event.target.checked }))} type="checkbox" />
                  {intl.formatMessage({ id: "backups.repository.pathStyle" })}
                </label>
              </div>
            )}
            <p className="mt-3 rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-sm">
              {intl.formatMessage({ id: "backups.repository.encryptionWarning" })}
            </p>

            <details className="mt-4 rounded-md border border-border p-4">
              <summary className="cursor-pointer text-sm font-medium">{intl.formatMessage({ id: "backups.advanced" })}</summary>
              <div className="mt-4 grid gap-4 md:grid-cols-4">
                <NumberField disabled={writeDisabled} label={intl.formatMessage({ id: "backups.plan.retention" })}
                  max={1000} min={1} onChange={(value) => setDraft((current) => ({ ...current, retentionCount: value }))} value={draft.retentionCount} />
                <NumberField disabled={writeDisabled} label={intl.formatMessage({ id: "backups.plan.rate" })}
                  max={10240} min={1} onChange={(value) => setDraft((current) => ({ ...current, rateMiBPerSecond: value }))} value={draft.rateMiBPerSecond} />
                <NumberField disabled={writeDisabled} label={intl.formatMessage({ id: "backups.plan.workers" })}
                  max={4} min={1} onChange={(value) => setDraft((current) => ({ ...current, workersPerNode: value }))} value={draft.workersPerNode} />
                <NumberField disabled={writeDisabled} label={intl.formatMessage({ id: "backups.plan.timeout" })}
                  max={48} min={1} onChange={(value) => setDraft((current) => ({ ...current, maxDurationHours: value }))} value={draft.maxDurationHours} />
              </div>
            </details>

            <div className="mt-4 flex flex-wrap gap-2">
              <Button
                disabled={writeDisabled}
                onClick={() => {
                  void mutate("save", async () => {
                    await saveBackupPlan(planInput(draft, plan, draftRevision.current))
                    initialized.current = false
                  }, intl.formatMessage({ id: "backups.notice.saved" }))
                }}
              >
                {intl.formatMessage({ id: busy === "save" ? "backups.saving" : "backups.save" })}
              </Button>
              <Button
                disabled={writeDisabled}
                onClick={() => {
                  void mutate("test", async () => {
                    await testBackupRepository(planInput(draft, plan))
                  }, intl.formatMessage({ id: "backups.notice.repositoryReady" }))
                }}
                variant="outline"
              >
                <ShieldCheck /> {intl.formatMessage({ id: "backups.testRepository" })}
              </Button>
              <Button
                disabled={writeDisabled || !plan || Boolean(activeBackup) || Boolean(activeRestore)}
                onClick={() => {
                  void mutate("start", async () => { await startBackupJob() },
                    intl.formatMessage({ id: "backups.notice.started" }))
                }}
                variant="outline"
              >
                <Play /> {intl.formatMessage({ id: "backups.startNow" })}
              </Button>
            </div>
          </SectionCard>

          <SectionCard
            title={intl.formatMessage({ id: "backups.task.title" })}
            description={intl.formatMessage({ id: "backups.task.description" })}
            action={activeTask && (restoreCanCancel || backupCanCancel) ? (
              <Button
                disabled={busy !== "" || (activeRestore ? !canRestore : !canWrite)}
                onClick={() => {
                  if (activeRestore) {
                    void mutate("cancel-restore", async () => { await cancelBackupRestore(activeRestore.id) },
                      intl.formatMessage({ id: "backups.notice.cancelRequested" }))
                  } else if (activeBackup) {
                    void mutate("cancel-backup", async () => { await cancelBackupJob(activeBackup.id) },
                      intl.formatMessage({ id: "backups.notice.cancelRequested" }))
                  }
                }}
                size="sm"
                variant="outline"
              >
                <Pause /> {intl.formatMessage({ id: "common.cancel" })}
              </Button>
            ) : null}
          >
            {activeTask && progress ? (
              <div className="space-y-3" data-testid="backup-task-progress">
                <div className="flex flex-wrap items-center justify-between gap-2 text-sm">
                  <span className="font-medium">{activeRestore ? intl.formatMessage({ id: "backups.task.restore" }) : intl.formatMessage({ id: "backups.task.backup" })}</span>
                  <span className="text-muted-foreground">
                    {activeRestore && restorePhaseMessageID(activeRestore.status)
                      ? intl.formatMessage({ id: restorePhaseMessageID(activeRestore.status) })
                      : activeTask.status} · {progress.complete}/{progress.total} Hash Slots
                  </span>
                </div>
                <div className="h-2 overflow-hidden rounded-full bg-muted">
                  <div className="h-full bg-primary transition-all" style={{ width: `${progress.percent}%` }} />
                </div>
                {activeRestore ? (
                  <div className="space-y-1 text-xs text-muted-foreground">
                    <div>{intl.formatMessage(
                      { id: "backups.task.restoredBytes" },
                      { size: formatBytes(activeRestore.logical_bytes ?? 0) },
                    )}</div>
                    {activeRestore.error_code ? (
                      <div className="text-destructive">{intl.formatMessage(
                        { id: "backups.task.failure" },
                        { code: activeRestore.error_code },
                      )}</div>
                    ) : null}
                    {restoreNodes.map((node) => (
                      <div key={node.nodeID}>{intl.formatMessage(
                        { id: "backups.task.nodeProgress" },
                        {
                          node: node.nodeID,
                          verified: node.verified,
                          total: node.total,
                        },
                      )}</div>
                    ))}
                  </div>
                ) : null}
                <div className="font-mono text-xs text-muted-foreground">{activeTask.id}</div>
              </div>
            ) : (
              <p className="text-sm text-muted-foreground">{intl.formatMessage({ id: "backups.task.idle" })}</p>
            )}
          </SectionCard>

          <SectionCard
            title={intl.formatMessage({ id: "backups.history.title" })}
            description={intl.formatMessage({ id: "backups.history.description" })}
          >
            {(dashboard.state.history ?? []).length === 0 ? (
              <p className="text-sm text-muted-foreground">
                {intl.formatMessage({ id: "backups.history.empty" })}
              </p>
            ) : (
              <div className="overflow-x-auto rounded-md border border-border">
                <table className="w-full min-w-[640px] text-left text-sm">
                  <thead className="bg-muted/40 text-muted-foreground">
                    <tr>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "backups.history.operation" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "backups.history.completed" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "backups.history.result" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "backups.history.error" })}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {(dashboard.state.history ?? []).map((record) => (
                      <tr className="border-t border-border" key={`${record.kind}-${record.id}`}>
                        <td className="px-3 py-3">
                          {taskKindMessageID(record.kind)
                            ? intl.formatMessage({ id: taskKindMessageID(record.kind) })
                            : record.kind}
                        </td>
                        <td className="px-3 py-3">{formatDate(record.completed_at_unix_ms)}</td>
                        <td className="px-3 py-3">
                          {taskStatusMessageID(record.status)
                            ? intl.formatMessage({ id: taskStatusMessageID(record.status) })
                            : record.status}
                        </td>
                        <td className="px-3 py-3 font-mono text-xs text-muted-foreground">
                          {record.error_code || "—"}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </SectionCard>

          <SectionCard
            title={intl.formatMessage({ id: "backups.archives.title" })}
            description={intl.formatMessage({ id: "backups.archives.description" })}
          >
            {dashboard.archives.length === 0 ? (
              <p className="text-sm text-muted-foreground">{intl.formatMessage({ id: "backups.archives.empty" })}</p>
            ) : (
              <div className="overflow-x-auto rounded-md border border-border">
                <table className="w-full min-w-[900px] text-left text-sm">
                  <thead className="bg-muted/40 text-muted-foreground">
                    <tr>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "backups.table.archive" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "backups.table.completed" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "backups.table.size" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "backups.table.health" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "backups.table.actions" })}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {dashboard.archives.map((archive) => (
                      <ArchiveRow
                        archive={archive}
                        busy={busy}
                        canRestore={canRestore}
                        canWrite={canWrite}
                        key={archive.id}
                        onDelete={() => setDeleteArchive(archive)}
                        onHold={() => {
                          void mutate(`hold-${archive.id}`, async () => {
                            await setBackupArchiveHold(archive.id, !archive.held, archive.held ? "" : "operator hold")
                          }, intl.formatMessage({ id: archive.held ? "backups.notice.released" : "backups.notice.held" }))
                        }}
                        onRestore={() => {
                          setRestoreArchive(archive)
                          setRestorePassword("")
                          setRestoreConfirmation("")
                          setMutationError("")
                        }}
                        onVerify={() => {
                          void mutate(`verify-${archive.id}`, async () => {
                            await verifyBackupArchive(archive.id)
                          }, intl.formatMessage({ id: "backups.notice.verified" }))
                        }}
                      />
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </SectionCard>
        </>
      ) : null}

      <ConfirmDialog
        confirmLabel={intl.formatMessage({ id: "backups.delete.confirm" })}
        description={intl.formatMessage({ id: "backups.delete.description" }, { id: deleteArchive?.id ?? "" })}
        error={mutationError}
        onConfirm={() => {
          if (!deleteArchive) return
          const archiveID = deleteArchive.id
          void mutate("delete", async () => { await deleteBackupArchive(archiveID) },
            intl.formatMessage({ id: "backups.notice.deleted" })).then((succeeded) => {
              if (succeeded) setDeleteArchive(null)
            })
        }}
        onOpenChange={(open) => { if (!open) setDeleteArchive(null) }}
        open={deleteArchive !== null}
        pending={busy === "delete"}
        title={intl.formatMessage({ id: "backups.delete.title" })}
      />

      <ActionFormDialog
        description={intl.formatMessage({ id: "backups.restore.description" }, { id: restoreArchive?.id ?? "" })}
        error={mutationError}
        onOpenChange={(open) => {
          if (!open) {
            setRestoreArchive(null)
            setRestorePassword("")
            setRestoreConfirmation("")
          }
        }}
        onSubmit={(event: FormEvent<HTMLFormElement>) => {
          event.preventDefault()
          if (!restoreArchive) return
          const archiveID = restoreArchive.id
          void mutate("restore", async () => {
            await startBackupRestore(archiveID, {
              username,
              password: restorePassword,
              confirmation: restoreConfirmation,
            })
          }, intl.formatMessage({ id: "backups.notice.restoreStarted" })).then((succeeded) => {
            if (succeeded) {
              setRestoreArchive(null)
              setRestorePassword("")
              setRestoreConfirmation("")
            }
          })
        }}
        open={restoreArchive !== null}
        pending={busy === "restore"}
        submitLabel={intl.formatMessage({ id: "backups.restore.confirm" })}
        title={intl.formatMessage({ id: "backups.restore.title" })}
      >
        <div className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm">
          {intl.formatMessage(
            { id: "backups.restore.dataLossWarning" },
            {
              cluster: restoreArchive?.source_cluster_id ?? "-",
              size: formatBytes(restoreArchive?.logical_bytes ?? 0),
              completed: formatDate(restoreArchive?.completed_at_unix_ms ?? 0),
            },
          )}
        </div>
        <Field label={intl.formatMessage({ id: "backups.restore.username" })}>
          <input className="h-9 rounded-md border border-input bg-muted px-3 text-sm" disabled value={username} />
        </Field>
        <Field label={intl.formatMessage({ id: "backups.restore.password" })}>
          <input autoComplete="current-password" className="h-9 rounded-md border border-input bg-background px-3 text-sm"
            onChange={(event) => setRestorePassword(event.target.value)} required type="password" value={restorePassword} />
        </Field>
        <Field label={intl.formatMessage({ id: "backups.restore.confirmation" }, { value: `RESTORE ${restoreArchive?.id ?? ""}` })}>
          <input className="h-9 rounded-md border border-input bg-background px-3 font-mono text-sm"
            onChange={(event) => setRestoreConfirmation(event.target.value)} required value={restoreConfirmation} />
        </Field>
      </ActionFormDialog>
    </PageContainer>
  )
}

function Field({ children, label }: { children: ReactNode; label: string }) {
  return (
    <label className="flex flex-col gap-1 text-sm font-medium text-foreground">
      {label}
      {children}
    </label>
  )
}

function NumberField({
  disabled, label, max, min, onChange, value,
}: {
  disabled: boolean
  label: string
  max: number
  min: number
  onChange: (value: number) => void
  value: number
}) {
  return (
    <Field label={label}>
      <input className="h-9 rounded-md border border-input bg-background px-3 text-sm" disabled={disabled}
        max={max} min={min} onChange={(event) => onChange(Number(event.target.value))} type="number" value={value} />
    </Field>
  )
}

function ArchiveRow({
  archive, busy, canRestore, canWrite, onDelete, onHold, onRestore, onVerify,
}: {
  archive: ManagerBackupArchive
  busy: string
  canRestore: boolean
  canWrite: boolean
  onDelete: () => void
  onHold: () => void
  onRestore: () => void
  onVerify: () => void
}) {
  const intl = useIntl()
  const disabled = busy !== ""
  return (
    <tr className="border-t border-border">
      <td className="px-3 py-3">
        <div className="font-mono text-xs text-foreground">{archive.id}</div>
        <div className="mt-1 text-xs text-muted-foreground">{archive.trigger}</div>
      </td>
      <td className="px-3 py-3">{formatDate(archive.completed_at_unix_ms)}</td>
      <td className="px-3 py-3">{formatBytes(archive.stored_bytes)}</td>
      <td className="px-3 py-3">
        <span className={archive.health === "healthy" ? "text-emerald-600" : "text-destructive"}>{archive.health}</span>
        {archive.held ? <span className="ml-2 rounded-full bg-muted px-2 py-1 text-xs">{intl.formatMessage({ id: "backups.held" })}</span> : null}
      </td>
      <td className="px-3 py-3">
        <div className="flex flex-wrap gap-2">
          <Button disabled={!canWrite || disabled} onClick={onVerify} size="xs" variant="outline">
            {intl.formatMessage({ id: "backups.verify" })}
          </Button>
          <Button disabled={!canWrite || disabled} onClick={onHold} size="xs" variant="outline">
            {intl.formatMessage({ id: archive.held ? "backups.release" : "backups.hold" })}
          </Button>
          {canRestore ? (
            <Button disabled={disabled || archive.health !== "healthy"} onClick={onRestore} size="xs" variant="outline">
              <ArchiveRestore /> {intl.formatMessage({ id: "backups.restore.action" })}
            </Button>
          ) : null}
          <Button disabled={!canWrite || disabled || archive.held} onClick={onDelete} size="icon-xs" variant="destructive">
            <Trash2 /><span className="sr-only">{intl.formatMessage({ id: "backups.delete.action" })}</span>
          </Button>
        </div>
      </td>
    </tr>
  )
}

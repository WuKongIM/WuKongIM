import type { ManagerBackupStatusResponse } from "@/lib/manager-api.types"

const terminalBackupStatuses = new Set(["failed", "cancelled", "canceled", "succeeded", "completed"])

function activeBackupKey(status: ManagerBackupStatusResponse | null) {
  const active = status?.active
  if (!active || terminalBackupStatuses.has(active.status.toLowerCase())) {
    return ""
  }
  return `${active.id}:${active.epoch}`
}

function activeVerificationKey(status: ManagerBackupStatusResponse | null) {
  const verification = status?.verification
  if (!verification || (verification.status !== "pending" && verification.status !== "running")) {
    return ""
  }
  return verification.id
}

export function shouldRefreshRestorePointList(
  previous: ManagerBackupStatusResponse | null,
  current: ManagerBackupStatusResponse,
) {
  if (!previous) {
    return false
  }
  const previousBackup = activeBackupKey(previous)
  const previousVerification = activeVerificationKey(previous)
  return (
    (previousBackup !== "" && previousBackup !== activeBackupKey(current))
    || (previousVerification !== "" && previousVerification !== activeVerificationKey(current))
  )
}

export function isBackupOperationActive(status: ManagerBackupStatusResponse | null) {
  return activeBackupKey(status) !== "" || activeVerificationKey(status) !== ""
}

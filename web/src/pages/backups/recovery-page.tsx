import { useEffect, useMemo, useState } from "react"
import { ArrowLeftIcon, CopyIcon, DownloadIcon } from "lucide-react"
import { useIntl, type IntlShape } from "react-intl"
import { useNavigate, useParams } from "react-router-dom"

import { useAuthStore } from "@/auth/auth-store"
import { hasManagerPermission } from "@/auth/permissions"
import { ResourceState } from "@/components/manager/resource-state"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"
import { Button } from "@/components/ui/button"
import {
  getBackupCheckpoint,
  getBackupCheckpoints,
  ManagerApiError,
} from "@/lib/manager-api"
import type { ManagerBackupCheckpointDetail } from "@/lib/manager-api.types"

type TokenStrategy = "preserve" | "invalidate"

type RecoveryCommand = {
  id: string
  title: string
  description: string
  command: string
}

function shellQuote(value: string) {
  return `'${value.replaceAll("'", "'\"'\"'")}'`
}

function normalizedTargetURL(value: string) {
  try {
    const parsed = new URL(value.trim())
    if (parsed.username || parsed.password) return null
    if (parsed.search || parsed.hash) return null
    const localHTTP = parsed.protocol === "http:" &&
      ["localhost", "127.0.0.1", "::1", "[::1]"].includes(parsed.hostname)
    if (parsed.protocol !== "https:" && !localHTTP) return null
    return parsed.toString().replace(/\/$/, "")
  } catch {
    return null
  }
}

function recoveryCommands(input: {
  catalogHeadToken: string
  checkpointID: string
  invalidateTokens: boolean
  targetURL: string
}, intl: IntlShape): RecoveryCommand[] {
  const checkpoint = shellQuote(input.checkpointID)
  const catalogHead = shellQuote(input.catalogHeadToken)
  const target = shellQuote(input.targetURL)
  const invalidation = input.invalidateTokens ? " --invalidate-tokens" : ""
  return [
    {
      id: "plan",
      title: intl.formatMessage({ id: "backups.recovery.step.plan" }),
      description: intl.formatMessage({ id: "backups.recovery.step.planDescription" }),
      command: `wkcli backup restore plan --checkpoint ${checkpoint} --catalog-head ${catalogHead}${invalidation} --server ${target} --token "$WK_MANAGER_TOKEN"`,
    },
    {
      id: "start",
      title: intl.formatMessage({ id: "backups.recovery.step.start" }),
      description: intl.formatMessage({ id: "backups.recovery.step.startDescription" }),
      command: `wkcli backup restore start "$RESTORE_PLAN_ID" --server ${target} --token "$WK_MANAGER_TOKEN"`,
    },
    {
      id: "verify",
      title: intl.formatMessage({ id: "backups.recovery.step.verify" }),
      description: intl.formatMessage({ id: "backups.recovery.step.verifyDescription" }),
      command: `wkcli backup restore verify "$RESTORE_PLAN_ID" --server ${target} --token "$WK_MANAGER_TOKEN"`,
    },
    {
      id: "fence",
      title: intl.formatMessage({ id: "backups.recovery.step.fence" }),
      description: intl.formatMessage({ id: "backups.recovery.step.fenceDescription" }),
      command: `wkcli backup fence-source --restore-plan "$RESTORE_PLAN_ID" --checkpoint ${checkpoint} --target-cluster "$TARGET_CLUSTER_ID" --target-generation "$TARGET_GENERATION" --server "$SOURCE_MANAGER_URL" --token "$WK_SOURCE_MANAGER_TOKEN" --json > source-fence-receipt.json`,
    },
    {
      id: "activate",
      title: intl.formatMessage({ id: "backups.recovery.step.activate" }),
      description: intl.formatMessage({ id: "backups.recovery.step.activateDescription" }),
      command: `wkcli backup restore activate "$RESTORE_PLAN_ID" --source-fence-receipt ./source-fence-receipt.json --server ${target} --token "$WK_RESTORE_ACTIVATION_TOKEN"`,
    },
  ]
}

export function BackupRecoveryPage() {
  const intl = useIntl()
  const navigate = useNavigate()
  const { checkpointId = "" } = useParams()
  const permissions = useAuthStore((state) => state.permissions)
  const canRead = hasManagerPermission(permissions, "cluster.backup", "r")
  const [checkpoint, setCheckpoint] = useState<ManagerBackupCheckpointDetail | null>(null)
  const [catalogHeadToken, setCatalogHeadToken] = useState("")
  const [loading, setLoading] = useState(canRead)
  const [error, setError] = useState<Error | null>(null)
  const [targetURL, setTargetURL] = useState("")
  const [tokenStrategy, setTokenStrategy] = useState<TokenStrategy>("preserve")
  const [copied, setCopied] = useState("")
  const [copyError, setCopyError] = useState("")
  const normalizedTarget = normalizedTargetURL(targetURL)

  useEffect(() => {
    if (!canRead || !checkpointId) return
    let cancelled = false
    void Promise.all([
      getBackupCheckpoint(checkpointId),
      getBackupCheckpoints({ id: checkpointId, limit: 1 }),
    ]).then(([detail, page]) => {
      if (cancelled) return
      if (!page.catalog_head_token) {
        throw new Error("backup catalog head token is unavailable")
      }
      setCheckpoint(detail)
      setCatalogHeadToken(page.catalog_head_token)
      setError(null)
    }).catch((requestError) => {
      if (!cancelled) {
        setError(requestError instanceof Error ? requestError : new Error("restore point request failed"))
      }
    }).finally(() => {
      if (!cancelled) setLoading(false)
    })
    return () => {
      cancelled = true
    }
  }, [canRead, checkpointId])

  const commands = useMemo(() => {
    if (!checkpoint || !catalogHeadToken || !normalizedTarget) return []
    return recoveryCommands({
      catalogHeadToken,
      checkpointID: checkpoint.id,
      invalidateTokens: tokenStrategy === "invalidate",
      targetURL: normalizedTarget,
    }, intl)
  }, [catalogHeadToken, checkpoint, intl, normalizedTarget, tokenStrategy])

  const copy = async (id: string, value: string) => {
    setCopyError("")
    try {
      await navigator.clipboard.writeText(value)
      setCopied(id)
    } catch {
      setCopyError(intl.formatMessage({ id: "backups.recovery.copyFailed" }))
    }
  }

  const exportMarkdown = () => {
    if (!checkpoint || commands.length === 0) return
    const content = [
      `# ${intl.formatMessage({ id: "backups.recovery.title" })}`,
      "",
      `${intl.formatMessage({ id: "backups.recovery.restorePointLabel" })}: \`${checkpoint.id}\``,
      "",
      ...commands.flatMap((step, index) => [
        `## ${index + 1}. ${step.title}`,
        "",
        step.description,
        "",
        "```sh",
        step.command,
        "```",
        "",
      ]),
    ].join("\n")
    const objectURL = URL.createObjectURL(new Blob([content], { type: "text/markdown" }))
    const anchor = document.createElement("a")
    anchor.href = objectURL
    anchor.download = `wukongim-recovery-${checkpoint.id}.md`
    anchor.click()
    URL.revokeObjectURL(objectURL)
  }

  if (!canRead) {
    return (
      <PageContainer>
        <PageHeader
          description={intl.formatMessage({ id: "backups.recovery.description" })}
          title={intl.formatMessage({ id: "backups.recovery.title" })}
        />
        <ResourceState
          kind="forbidden"
          title={intl.formatMessage({ id: "backups.recovery.forbidden" })}
        />
      </PageContainer>
    )
  }

  return (
    <PageContainer>
      <PageHeader
        actions={
          <Button onClick={() => navigate("/cluster/backups?tab=checkpoints")} size="sm" variant="outline">
            <ArrowLeftIcon aria-hidden="true" />
            {intl.formatMessage({ id: "backups.recovery.back" })}
          </Button>
        }
        description={intl.formatMessage({ id: "backups.recovery.description" })}
        title={intl.formatMessage({ id: "backups.recovery.title" })}
      />

      {loading ? (
        <ResourceState kind="loading" title={intl.formatMessage({ id: "backups.recovery.title" })} />
      ) : error ? (
        <ResourceState
          kind={error instanceof ManagerApiError && error.status === 403 ? "forbidden" : "unavailable"}
          title={intl.formatMessage({ id: "backups.recovery.unavailable" })}
        />
      ) : checkpoint ? (
        <>
          <SectionCard
            description={intl.formatMessage({ id: "backups.recovery.selectedDescription" })}
            title={intl.formatMessage({ id: "backups.recovery.selected" })}
          >
            <dl className="grid gap-3 text-sm sm:grid-cols-2">
              <div>
                <dt className="text-muted-foreground">{intl.formatMessage({ id: "backups.detail.id" })}</dt>
                <dd className="mt-1 break-all font-mono">{checkpoint.id}</dd>
              </div>
              <div>
                <dt className="text-muted-foreground">{intl.formatMessage({ id: "backups.checkpoints.effective" })}</dt>
                <dd className="mt-1">
                  <time
                    dateTime={new Date(checkpoint.effective_at_unix_millis).toISOString()}
                    title={`${new Date(checkpoint.effective_at_unix_millis).toISOString()} (UTC)`}
                  >
                    {new Date(checkpoint.effective_at_unix_millis).toLocaleString()}
                  </time>
                </dd>
              </div>
            </dl>
          </SectionCard>

          <SectionCard
            description={intl.formatMessage({ id: "backups.recovery.targetDescription" })}
            title={intl.formatMessage({ id: "backups.recovery.target" })}
          >
            <label className="grid max-w-2xl gap-1 text-sm">
              <span className="text-muted-foreground">
                {intl.formatMessage({ id: "backups.recovery.targetURL" })}
              </span>
              <input
                aria-label={intl.formatMessage({ id: "backups.recovery.targetURL" })}
                autoComplete="off"
                className="h-10 rounded-md border border-border bg-background px-3"
                onChange={(event) => setTargetURL(event.target.value)}
                placeholder="https://restore-manager.example.com"
                spellCheck={false}
                value={targetURL}
              />
            </label>
            {targetURL && !normalizedTarget ? (
              <p className="mt-2 text-sm text-destructive">
                {intl.formatMessage({ id: "backups.recovery.targetInvalid" })}
              </p>
            ) : null}
          </SectionCard>

          <SectionCard
            description={intl.formatMessage({ id: "backups.recovery.tokensDescription" })}
            title={intl.formatMessage({ id: "backups.recovery.tokens" })}
          >
            <div className="grid gap-2">
              <label className="flex items-start gap-2 text-sm">
                <input
                  checked={tokenStrategy === "preserve"}
                  name="token-strategy"
                  onChange={() => setTokenStrategy("preserve")}
                  type="radio"
                />
                <span>{intl.formatMessage({ id: "backups.recovery.tokensPreserve" })}</span>
              </label>
              <label className="flex items-start gap-2 text-sm">
                <input
                  checked={tokenStrategy === "invalidate"}
                  name="token-strategy"
                  onChange={() => setTokenStrategy("invalidate")}
                  type="radio"
                />
                <span>{intl.formatMessage({ id: "backups.recovery.tokensInvalidate" })}</span>
              </label>
            </div>
          </SectionCard>

          <section className="rounded-2xl border border-warning/30 bg-warning/10 p-4 text-sm">
            <p className="font-medium text-foreground">
              {intl.formatMessage({ id: "backups.recovery.securityTitle" })}
            </p>
            <p className="mt-1 text-muted-foreground">
              {intl.formatMessage({ id: "backups.recovery.securityDescription" })}
            </p>
          </section>

          {commands.length > 0 ? (
            <SectionCard
              action={
                <div className="flex flex-wrap gap-2">
                  <Button
                    onClick={() => void copy("all", commands.map((step) => step.command).join("\n\n"))}
                    size="sm"
                  >
                    <CopyIcon aria-hidden="true" />
                    {copied === "all"
                      ? intl.formatMessage({ id: "backups.recovery.copied" })
                      : intl.formatMessage({ id: "backups.recovery.copyAll" })}
                  </Button>
                  <details className="relative">
                    <summary
                      className="inline-flex h-8 cursor-pointer list-none items-center rounded-full border border-border bg-background px-3 text-[0.8rem] font-medium [&::-webkit-details-marker]:hidden"
                      role="button"
                    >
                      {intl.formatMessage({ id: "backups.recovery.more" })}
                    </summary>
                    <div className="absolute right-0 z-20 mt-1 min-w-52 rounded-lg border border-border bg-popover p-1 shadow-lg">
                      <Button className="w-full justify-start" onClick={exportMarkdown} size="sm" variant="ghost">
                        <DownloadIcon aria-hidden="true" />
                        {intl.formatMessage({ id: "backups.recovery.export" })}
                      </Button>
                    </div>
                  </details>
                </div>
              }
              description={intl.formatMessage({ id: "backups.recovery.commandsDescription" })}
              title={intl.formatMessage({ id: "backups.recovery.commands" })}
            >
              <ol className="space-y-4">
                {commands.map((step, index) => (
                  <li className="rounded-xl border border-border p-4" key={step.id}>
                    <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
                      <div>
                        <p className="font-medium">{index + 1}. {step.title}</p>
                        <p className="mt-1 text-sm text-muted-foreground">{step.description}</p>
                      </div>
                      <Button
                        aria-label={intl.formatMessage(
                          { id: "backups.recovery.copyStep" },
                          { step: index + 1 },
                        )}
                        onClick={() => void copy(step.id, step.command)}
                        size="sm"
                        variant="outline"
                      >
                        <CopyIcon aria-hidden="true" />
                        {copied === step.id
                          ? intl.formatMessage({ id: "backups.recovery.copied" })
                          : intl.formatMessage({ id: "backups.recovery.copy" })}
                      </Button>
                    </div>
                    <pre className="mt-3 overflow-x-auto rounded-lg bg-muted p-3 text-xs leading-5">
                      <code>{step.command}</code>
                    </pre>
                  </li>
                ))}
              </ol>
              {copyError ? <p className="mt-3 text-sm text-destructive">{copyError}</p> : null}
            </SectionCard>
          ) : null}
        </>
      ) : null}
    </PageContainer>
  )
}

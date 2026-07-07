import type { FormEvent } from "react"
import { useCallback, useEffect, useState } from "react"
import { useIntl } from "react-intl"

import { ActionFormDialog } from "@/components/manager/action-form-dialog"
import { ConfirmDialog } from "@/components/manager/confirm-dialog"
import { ResourceState } from "@/components/manager/resource-state"
import { Button } from "@/components/ui/button"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"
import {
  addSystemUsers,
  getSystemUsers,
  ManagerApiError,
  removeSystemUsers,
} from "@/lib/manager-api"
import type { ManagerSystemUser } from "@/lib/manager-api.types"

type SystemUsersState = {
  items: ManagerSystemUser[]
  total: number
  loading: boolean
  refreshing: boolean
  error: Error | null
}

function emptySystemUsersState(): SystemUsersState {
  return {
    items: [],
    total: 0,
    loading: true,
    refreshing: false,
    error: null,
  }
}

function mapErrorKind(error: Error | null) {
  if (!(error instanceof ManagerApiError)) {
    return "error" as const
  }
  if (error.status === 403) {
    return "forbidden" as const
  }
  if (error.status === 503) {
    return "unavailable" as const
  }
  return "error" as const
}

function normalizeUIDs(value: string) {
  const seen = new Set<string>()
  const uids: string[] = []
  for (const raw of value.split(/[,\s;]+/)) {
    const uid = raw.trim()
    if (uid && !seen.has(uid)) {
      seen.add(uid)
      uids.push(uid)
    }
  }
  return uids
}

export function SystemUsersPage() {
  const intl = useIntl()
  const [state, setState] = useState<SystemUsersState>(emptySystemUsersState)
  const [addOpen, setAddOpen] = useState(false)
  const [addPending, setAddPending] = useState(false)
  const [addError, setAddError] = useState("")
  const [removeUID, setRemoveUID] = useState<string | null>(null)
  const [removePending, setRemovePending] = useState(false)
  const [removeError, setRemoveError] = useState("")

  const runQuery = useCallback(async (options?: { refreshing?: boolean }) => {
    setState((current) => ({
      ...current,
      loading: options?.refreshing ? current.loading : true,
      refreshing: Boolean(options?.refreshing),
      error: null,
    }))

    try {
      const page = await getSystemUsers()
      setState({
        items: page.items,
        total: page.total,
        loading: false,
        refreshing: false,
        error: null,
      })
    } catch (error) {
      setState({
        items: [],
        total: 0,
        loading: false,
        refreshing: false,
        error: error instanceof Error ? error : new Error("system users request failed"),
      })
    }
  }, [])

  useEffect(() => {
    void runQuery()
  }, [runQuery])

  const submitAdd = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    const uids = normalizeUIDs(String(form.get("uids") ?? ""))
    if (uids.length === 0) {
      setAddError(intl.formatMessage({ id: "systemUsers.form.emptyUIDs" }))
      return
    }

    setAddPending(true)
    setAddError("")
    try {
      await addSystemUsers({ uids })
      setAddOpen(false)
      await runQuery({ refreshing: true })
    } catch (error) {
      setAddError(error instanceof Error ? error.message : "add system users failed")
    } finally {
      setAddPending(false)
    }
  }

  const confirmRemove = async () => {
    if (!removeUID) {
      return
    }

    setRemovePending(true)
    setRemoveError("")
    try {
      await removeSystemUsers({ uids: [removeUID] })
      setRemoveUID(null)
      await runQuery({ refreshing: true })
    } catch (error) {
      setRemoveError(error instanceof Error ? error.message : "remove system user failed")
    } finally {
      setRemovePending(false)
    }
  }

  return (
    <PageContainer>
      <PageHeader
        actions={(
          <div className="flex flex-wrap gap-2">
            <Button
              onClick={() => {
                setAddError("")
                setAddOpen(true)
              }}
              size="sm"
            >
              {intl.formatMessage({ id: "systemUsers.action.add" })}
            </Button>
            <Button
              onClick={() => {
                void runQuery({ refreshing: true })
              }}
              size="sm"
              variant="outline"
            >
              {state.refreshing
                ? intl.formatMessage({ id: "common.refreshing" })
                : intl.formatMessage({ id: "common.refresh" })}
            </Button>
          </div>
        )}
        title={intl.formatMessage({ id: "systemUsers.title" })}
        description={intl.formatMessage({ id: "systemUsers.description" })}
      />

      <SectionCard
        className="overflow-hidden"
        description={intl.formatMessage({ id: "systemUsers.list.description" })}
        title={intl.formatMessage({ id: "systemUsers.list.title" })}
      >
        <div
          className="mb-4 flex flex-wrap items-center gap-3 border-b border-border pb-4 text-sm text-muted-foreground"
          data-testid="system-users-metadata-row"
        >
          <span className="font-mono text-sm font-semibold text-foreground">
            {intl.formatMessage({ id: "systemUsers.totalValue" }, { count: state.total })}
          </span>
          <p>{intl.formatMessage({ id: "systemUsers.cacheOnlyExcluded" })}</p>
        </div>

        {state.loading ? (
          <ResourceState kind="loading" title={intl.formatMessage({ id: "systemUsers.title" })} />
        ) : null}
        {!state.loading && state.error ? (
          <ResourceState
            kind={mapErrorKind(state.error)}
            onRetry={() => {
              void runQuery()
            }}
            title={intl.formatMessage({ id: "systemUsers.title" })}
          />
        ) : null}
        {!state.loading && !state.error ? (
          state.items.length > 0 ? (
            <div className="overflow-x-auto rounded-md border border-border" data-system-users-surface="inventory">
              <table
                aria-label={intl.formatMessage({ id: "systemUsers.list.title" })}
                className="w-full border-collapse text-sm"
              >
                <thead className="bg-muted/40 text-left text-xs uppercase tracking-[0.14em] text-muted-foreground">
                  <tr>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "systemUsers.table.uid" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "systemUsers.table.actions" })}</th>
                  </tr>
                </thead>
                <tbody>
                  {state.items.map((item) => (
                    <tr className="border-t border-border" key={item.uid}>
                      <td className="px-3 py-3 text-sm font-medium text-foreground">{item.uid}</td>
                      <td className="px-3 py-3 text-sm">
                        <Button
                          aria-label={intl.formatMessage({ id: "systemUsers.action.removeOne" }, { uid: item.uid })}
                          onClick={() => {
                            setRemoveError("")
                            setRemoveUID(item.uid)
                          }}
                          size="sm"
                          variant="outline"
                        >
                          {intl.formatMessage({ id: "systemUsers.action.remove" })}
                        </Button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <ResourceState kind="empty" title={intl.formatMessage({ id: "systemUsers.title" })} />
          )
        ) : null}
      </SectionCard>

      <ActionFormDialog
        description={intl.formatMessage({ id: "systemUsers.add.description" })}
        error={addError}
        onOpenChange={setAddOpen}
        onSubmit={(event) => {
          void submitAdd(event)
        }}
        open={addOpen}
        pending={addPending}
        submitLabel={intl.formatMessage({ id: "systemUsers.action.add" })}
        title={intl.formatMessage({ id: "systemUsers.action.add" })}
      >
        <label className="block text-sm font-medium text-foreground" htmlFor="system-users-uids">
          {intl.formatMessage({ id: "systemUsers.form.uids" })}
        </label>
        <textarea
          className="min-h-28 w-full rounded-md border border-border bg-background px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-ring"
          id="system-users-uids"
          name="uids"
          placeholder={intl.formatMessage({ id: "systemUsers.form.uidsPlaceholder" })}
        />
      </ActionFormDialog>

      <ConfirmDialog
        confirmLabel={intl.formatMessage({ id: "systemUsers.action.confirmRemove" })}
        description={removeUID ? intl.formatMessage({ id: "systemUsers.remove.description" }, { uid: removeUID }) : undefined}
        error={removeError}
        onConfirm={() => {
          void confirmRemove()
        }}
        onOpenChange={(open) => {
          if (!open) {
            setRemoveUID(null)
          }
        }}
        open={removeUID !== null}
        pending={removePending}
        title={intl.formatMessage({ id: "systemUsers.action.remove" })}
      />
    </PageContainer>
  )
}

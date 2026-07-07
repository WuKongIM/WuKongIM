import type { FormEvent } from "react"
import { useCallback, useEffect, useState } from "react"
import { useIntl } from "react-intl"

import { ActionFormDialog } from "@/components/manager/action-form-dialog"
import { ConfirmDialog } from "@/components/manager/confirm-dialog"
import { DetailSheet } from "@/components/manager/detail-sheet"
import { ResourceState } from "@/components/manager/resource-state"
import { StatusBadge } from "@/components/manager/status-badge"
import { Button } from "@/components/ui/button"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"
import { getUser, getUsers, kickUser, ManagerApiError, resetUserToken } from "@/lib/manager-api"
import type {
  ManagerUserDetailResponse,
  ManagerUserListItem,
  ManagerUsersResponse,
  ResetUserTokenResponse,
} from "@/lib/manager-api.types"

const usersPageLimit = 50

type UsersState = {
  items: ManagerUserListItem[]
  hasMore: boolean
  nextCursor?: string
  loading: boolean
  refreshing: boolean
  error: Error | null
}

function emptyUsersState(): UsersState {
  return {
    items: [],
    hasMore: false,
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

function onlineFlags(item: ManagerUserListItem) {
  return item.online_device_flags?.length ? item.online_device_flags.join(", ") : "-"
}

function mergeUsers(current: ManagerUserListItem[], page: ManagerUsersResponse, append: boolean) {
  if (!append) {
    return page.items
  }
  const seen = new Set(current.map((item) => item.uid))
  const next = [...current]
  for (const item of page.items) {
    if (!seen.has(item.uid)) {
      next.push(item)
    }
  }
  return next
}

export function UsersPage() {
  const intl = useIntl()
  const [state, setState] = useState<UsersState>(emptyUsersState)
  const [keywordInput, setKeywordInput] = useState("")
  const [activeKeyword, setActiveKeyword] = useState("")
  const [selectedUID, setSelectedUID] = useState<string | null>(null)
  const [detail, setDetail] = useState<ManagerUserDetailResponse | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const [detailError, setDetailError] = useState<Error | null>(null)
  const [kickOpen, setKickOpen] = useState(false)
  const [kickPending, setKickPending] = useState(false)
  const [kickError, setKickError] = useState("")
  const [resetOpen, setResetOpen] = useState(false)
  const [resetPending, setResetPending] = useState(false)
  const [resetError, setResetError] = useState("")
  const [resetResult, setResetResult] = useState<ResetUserTokenResponse | null>(null)

  const runQuery = useCallback(async (options?: { keyword?: string; cursor?: string; append?: boolean; refreshing?: boolean }) => {
    const keyword = options?.keyword?.trim() ?? activeKeyword
    const append = options?.append ?? false
    setState((current) => ({
      ...current,
      loading: append || options?.refreshing ? current.loading : true,
      refreshing: Boolean(options?.refreshing || append),
      error: null,
    }))

    try {
      const page = await getUsers({
        keyword: keyword || undefined,
        limit: usersPageLimit,
        cursor: options?.cursor,
      })
      setState((current) => ({
        items: mergeUsers(current.items, page, append),
        hasMore: page.has_more,
        nextCursor: page.next_cursor,
        loading: false,
        refreshing: false,
        error: null,
      }))
      setActiveKeyword(keyword)
    } catch (error) {
      setState({
        items: [],
        hasMore: false,
        loading: false,
        refreshing: false,
        error: error instanceof Error ? error : new Error("user request failed"),
      })
    }
  }, [activeKeyword])

  const loadDetail = useCallback(async (uid: string) => {
    setDetailLoading(true)
    setDetailError(null)
    try {
      const nextDetail = await getUser(uid)
      setDetail(nextDetail)
    } catch (error) {
      setDetail(null)
      setDetailError(error instanceof Error ? error : new Error("user detail failed"))
    } finally {
      setDetailLoading(false)
    }
  }, [])

  useEffect(() => {
    void runQuery({ keyword: "", refreshing: false })
    // Run once on mount; follow-up queries are user driven.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const submitSearch = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    void runQuery({ keyword: keywordInput })
  }

  const openDetail = async (uid: string) => {
    setSelectedUID(uid)
    setDetail(null)
    setResetResult(null)
    await loadDetail(uid)
  }

  const closeDetail = (open: boolean) => {
    if (open) {
      return
    }
    setSelectedUID(null)
    setDetail(null)
    setDetailError(null)
    setKickOpen(false)
    setResetOpen(false)
    setResetResult(null)
  }

  const confirmKick = async () => {
    if (!selectedUID) {
      return
    }
    setKickPending(true)
    setKickError("")
    try {
      await kickUser(selectedUID, { deviceFlag: "all" })
      setKickOpen(false)
      await runQuery({ keyword: activeKeyword, refreshing: true })
      await loadDetail(selectedUID)
    } catch (error) {
      setKickError(error instanceof Error ? error.message : "kick failed")
    } finally {
      setKickPending(false)
    }
  }

  const submitReset = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!selectedUID) {
      return
    }
    const form = new FormData(event.currentTarget)
    const deviceFlag = String(form.get("device_flag") ?? "app") as "app" | "web" | "pc" | "system"
    const deviceLevel = String(form.get("device_level") ?? "master") as "master" | "slave"
    setResetPending(true)
    setResetError("")
    setResetResult(null)
    try {
      const result = await resetUserToken(selectedUID, { deviceFlag, deviceLevel })
      setResetResult(result)
      await runQuery({ keyword: activeKeyword, refreshing: true })
      await loadDetail(selectedUID)
    } catch (error) {
      setResetError(error instanceof Error ? error.message : "reset failed")
    } finally {
      setResetPending(false)
    }
  }

  const hideDetailActions = kickOpen || resetOpen

  return (
    <PageContainer>
      <PageHeader
        title={intl.formatMessage({ id: "users.title" })}
        description={intl.formatMessage({ id: "users.description" })}
      />

      <SectionCard
        className="overflow-hidden"
        description={intl.formatMessage({ id: "users.list.description" })}
        title={intl.formatMessage({ id: "users.list.title" })}
      >
        <div
          className="mb-4 flex flex-col gap-3 border-b border-border pb-4 lg:flex-row lg:items-end lg:justify-between"
          data-testid="users-filter-toolbar"
        >
          <form className="flex min-w-0 flex-1 flex-col gap-2 sm:flex-row" onSubmit={submitSearch}>
            <input
              className="h-9 rounded-md border border-border bg-background px-3 text-sm outline-none focus:ring-2 focus:ring-ring"
              onChange={(event) => setKeywordInput(event.target.value)}
              placeholder={intl.formatMessage({ id: "users.search.placeholder" })}
              value={keywordInput}
            />
            <Button size="sm" type="submit">
              {intl.formatMessage({ id: "common.search" })}
            </Button>
          </form>
          <Button
            onClick={() => {
              void runQuery({ keyword: activeKeyword, refreshing: true })
            }}
            size="sm"
            variant="outline"
          >
            {state.refreshing
              ? intl.formatMessage({ id: "common.refreshing" })
              : intl.formatMessage({ id: "common.refresh" })}
          </Button>
        </div>

        {state.loading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "users.title" })} /> : null}
        {!state.loading && state.error ? (
          <ResourceState
            kind={mapErrorKind(state.error)}
            onRetry={() => {
              void runQuery({ keyword: activeKeyword })
            }}
            title={intl.formatMessage({ id: "users.title" })}
          />
        ) : null}
        {!state.loading && !state.error ? (
          state.items.length > 0 ? (
            <div className="space-y-3">
              <div className="overflow-x-auto rounded-md border border-border" data-users-surface="inventory">
                <table aria-label={intl.formatMessage({ id: "users.list.title" })} className="w-full border-collapse text-sm">
                  <thead className="bg-muted/40 text-left text-xs uppercase tracking-[0.14em] text-muted-foreground">
                    <tr>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "users.table.uid" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "users.table.online" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "users.table.devices" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "users.table.tokens" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "users.table.routing" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "users.table.actions" })}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {state.items.map((item) => (
                      <tr className="border-t border-border" key={item.uid}>
                        <td className="px-3 py-3 text-sm font-medium text-foreground">{item.uid}</td>
                        <td className="px-3 py-3 text-sm text-foreground">
                          <StatusBadge value={item.online ? "online" : "offline"} />
                        </td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{item.device_count}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{item.token_set_count}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">
                          <div>{intl.formatMessage({ id: "users.routing.slotValue" }, { slot: item.slot_id, hash: item.hash_slot })}</div>
                          <div className="mt-1 text-xs">{onlineFlags(item)}</div>
                        </td>
                        <td className="px-3 py-3 text-sm text-foreground">
                          <Button
                            aria-label={intl.formatMessage({ id: "users.inspectUser" }, { uid: item.uid })}
                            onClick={() => {
                              void openDetail(item.uid)
                            }}
                            size="sm"
                            variant="outline"
                          >
                            {intl.formatMessage({ id: "common.inspect" })}
                          </Button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              {state.hasMore && state.nextCursor ? (
                <Button
                  onClick={() => {
                    void runQuery({ keyword: activeKeyword, cursor: state.nextCursor, append: true })
                  }}
                  size="sm"
                  variant="outline"
                >
                  {intl.formatMessage({ id: "common.loadMore" })}
                </Button>
              ) : null}
            </div>
          ) : (
            <ResourceState kind="empty" title={intl.formatMessage({ id: "users.title" })} />
          )
        ) : null}
      </SectionCard>

      <DetailSheet
        description={detail ? intl.formatMessage({ id: "users.detail.description" }, { uid: detail.uid }) : undefined}
        footer={
          detail && !hideDetailActions ? (
            <div className="flex justify-end gap-2">
              <Button onClick={() => setKickOpen(true)} size="sm" variant="outline">
                {intl.formatMessage({ id: "users.action.kick" })}
              </Button>
              <Button onClick={() => setResetOpen(true)} size="sm">
                {intl.formatMessage({ id: "users.action.resetToken" })}
              </Button>
            </div>
          ) : null
        }
        onOpenChange={closeDetail}
        open={selectedUID !== null}
        title={detail ? detail.uid : intl.formatMessage({ id: "users.detail.title" })}
      >
        {detailLoading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "users.detail.title" })} /> : null}
        {!detailLoading && detailError ? (
          <ResourceState
            kind={mapErrorKind(detailError)}
            onRetry={() => {
              if (selectedUID) {
                void loadDetail(selectedUID)
              }
            }}
            title={intl.formatMessage({ id: "users.detail.title" })}
          />
        ) : null}
        {!detailLoading && !detailError && detail ? (
          <div className="space-y-4">
            <div className="grid gap-3 sm:grid-cols-3">
              <div className="rounded-md border border-border bg-background p-3 text-sm">
                <div className="text-muted-foreground">{intl.formatMessage({ id: "users.detail.slot" })}</div>
                <div className="mt-1 font-medium">{detail.slot_id}</div>
              </div>
              <div className="rounded-md border border-border bg-background p-3 text-sm">
                <div className="text-muted-foreground">{intl.formatMessage({ id: "users.detail.hashSlot" })}</div>
                <div className="mt-1 font-medium">{detail.hash_slot}</div>
              </div>
              <div className="rounded-md border border-border bg-background p-3 text-sm">
                <div className="text-muted-foreground">{intl.formatMessage({ id: "users.detail.online" })}</div>
                <div className="mt-1 font-medium">{detail.online ? "online" : "offline"}</div>
              </div>
            </div>
            <div>
              <h3 className="mb-2 text-sm font-semibold">{intl.formatMessage({ id: "users.detail.devices" })}</h3>
              <div className="space-y-2">
                {detail.devices.map((device) => (
                  <div className="rounded-md border border-border bg-background p-3 text-sm" key={device.device_flag}>
                    <div className="font-medium">{device.device_flag} / {device.device_level}</div>
                    <div className="mt-1 text-muted-foreground">
                      {intl.formatMessage(
                        { id: "users.device.summary" },
                        { token: device.token_set ? "set" : "empty", sessions: device.online_session_count },
                      )}
                    </div>
                  </div>
                ))}
              </div>
            </div>
            <div>
              <h3 className="mb-2 text-sm font-semibold">{intl.formatMessage({ id: "users.detail.connections" })}</h3>
              <div className="space-y-2">
                {detail.connections.map((connection) => (
                  <div className="rounded-md border border-border bg-background p-3 text-sm" key={connection.session_id}>
                    <div className="font-medium">
                      {intl.formatMessage({ id: "users.connection.session" }, { id: connection.session_id })}
                    </div>
                    <div className="mt-1 text-muted-foreground">
                      {connection.device_flag} / {connection.device_level} · {connection.listener} · node {connection.node_id}
                    </div>
                  </div>
                ))}
              </div>
            </div>
          </div>
        ) : null}
      </DetailSheet>

      <ConfirmDialog
        confirmLabel={intl.formatMessage({ id: "users.action.confirmKick" })}
        description={intl.formatMessage({ id: "users.kick.description" })}
        error={kickError}
        onConfirm={() => {
          void confirmKick()
        }}
        onOpenChange={setKickOpen}
        open={kickOpen}
        pending={kickPending}
        title={intl.formatMessage({ id: "users.action.kick" })}
      />

      <ActionFormDialog
        description={intl.formatMessage({ id: "users.token.visibleOnce" })}
        error={resetError}
        onOpenChange={(open) => {
          setResetOpen(open)
          if (!open) {
            setResetResult(null)
          }
        }}
        onSubmit={submitReset}
        open={resetOpen}
        pending={resetPending}
        submitLabel={intl.formatMessage({ id: "users.action.resetToken" })}
        title={intl.formatMessage({ id: "users.action.resetToken" })}
      >
        <label className="block text-sm">
          {intl.formatMessage({ id: "users.form.deviceFlag" })}
          <select className="mt-1 h-9 w-full rounded-md border border-border bg-background px-2" name="device_flag">
            <option value="app">app</option>
            <option value="web">web</option>
            <option value="pc">pc</option>
            <option value="system">system</option>
          </select>
        </label>
        <label className="block text-sm">
          {intl.formatMessage({ id: "users.form.deviceLevel" })}
          <select className="mt-1 h-9 w-full rounded-md border border-border bg-background px-2" name="device_level">
            <option value="master">master</option>
            <option value="slave">slave</option>
          </select>
        </label>
        {resetResult ? (
          <div className="rounded-md border border-border bg-muted/30 p-3 text-sm">
            <div className="text-muted-foreground">{intl.formatMessage({ id: "users.token.visibleOnce" })}</div>
            <code className="mt-2 block break-all text-foreground">{resetResult.token}</code>
          </div>
        ) : null}
      </ActionFormDialog>
    </PageContainer>
  )
}

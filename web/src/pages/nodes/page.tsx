import { useCallback, useEffect, useMemo, useState } from "react"
import { useIntl, type IntlShape } from "react-intl"

import { useAuthStore } from "@/auth/auth-store"
import { ConfirmDialog } from "@/components/manager/confirm-dialog"
import { DetailSheet } from "@/components/manager/detail-sheet"
import { KeyValueList } from "@/components/manager/key-value-list"
import { ResourceState } from "@/components/manager/resource-state"
import { StatusBadge } from "@/components/manager/status-badge"
import { TableToolbar } from "@/components/manager/table-toolbar"
import { Button } from "@/components/ui/button"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"
import {
  ManagerApiError,
  getNode,
  getNodes,
  markNodeDraining,
  resumeNode,
} from "@/lib/manager-api"
import type {
  ManagerNodeDetailResponse,
  ManagerNodesResponse,
} from "@/lib/manager-api.types"

type NodeAction = "drain" | "resume" | null

type NodesState = {
  nodes: ManagerNodesResponse | null
  loading: boolean
  refreshing: boolean
  error: Error | null
}

function formatTimestamp(intl: IntlShape, value: string) {
  return new Intl.DateTimeFormat(intl.locale, {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).format(new Date(value))
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

function hasPermission(
  permissions: { resource: string; actions: string[] }[],
  resource: string,
  action: string,
) {
  return permissions.some((permission) => {
    if (permission.resource !== resource) {
      return false
    }
    return permission.actions.includes(action) || permission.actions.includes("*")
  })
}

export function NodesPage() {
  const intl = useIntl()
  const permissions = useAuthStore((state) => state.permissions)
  const canWriteNodes = useMemo(
    () => hasPermission(permissions, "cluster.node", "w"),
    [permissions],
  )
  const [state, setState] = useState<NodesState>({
    nodes: null,
    loading: true,
    refreshing: false,
    error: null,
  })
  const [selectedNodeId, setSelectedNodeId] = useState<number | null>(null)
  const [detail, setDetail] = useState<ManagerNodeDetailResponse | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const [detailError, setDetailError] = useState<Error | null>(null)
  const [pendingAction, setPendingAction] = useState<NodeAction>(null)
  const [actionError, setActionError] = useState<string>("")
  const [actionPending, setActionPending] = useState(false)

  const loadNodes = useCallback(async (refreshing: boolean) => {
    setState((current) => ({
      ...current,
      loading: refreshing ? current.loading : true,
      refreshing,
      error: null,
    }))

    try {
      const nodes = await getNodes()
      setState({ nodes, loading: false, refreshing: false, error: null })
    } catch (error) {
      setState({
        nodes: null,
        loading: false,
        refreshing: false,
        error: error instanceof Error ? error : new Error("node request failed"),
      })
    }
  }, [])

  const loadNodeDetail = useCallback(async (nodeId: number) => {
    setDetailLoading(true)
    setDetailError(null)

    try {
      const nextDetail = await getNode(nodeId)
      setDetail(nextDetail)
    } catch (error) {
      setDetail(null)
      setDetailError(error instanceof Error ? error : new Error("node detail failed"))
    } finally {
      setDetailLoading(false)
    }
  }, [])

  useEffect(() => {
    void loadNodes(false)
  }, [loadNodes])

  const openDetail = useCallback(
    async (nodeId: number) => {
      setSelectedNodeId(nodeId)
      await loadNodeDetail(nodeId)
    },
    [loadNodeDetail],
  )

  const closeDetail = useCallback((open: boolean) => {
    if (open) {
      return
    }
    setSelectedNodeId(null)
    setDetail(null)
    setDetailError(null)
    setPendingAction(null)
    setActionError("")
  }, [])

  const runAction = useCallback(async () => {
    if (!selectedNodeId || !pendingAction) {
      return
    }

    setActionPending(true)
    setActionError("")

    try {
      if (pendingAction === "drain") {
        await markNodeDraining(selectedNodeId)
      } else {
        await resumeNode(selectedNodeId)
      }
      setPendingAction(null)
      await loadNodes(true)
      await loadNodeDetail(selectedNodeId)
    } catch (error) {
      setActionError(error instanceof Error ? error.message : "node action failed")
    } finally {
      setActionPending(false)
    }
  }, [loadNodeDetail, loadNodes, pendingAction, selectedNodeId])

  return (
    <PageContainer>
      <PageHeader
        title={intl.formatMessage({ id: "nav.nodes.title" })}
        description={intl.formatMessage({ id: "nav.nodes.description" })}
        actions={
          <>
            <Button
              onClick={() => {
                void loadNodes(true)
              }}
              size="sm"
              variant="outline"
            >
              {state.refreshing
                ? intl.formatMessage({ id: "common.refreshing" })
                : intl.formatMessage({ id: "common.refresh" })}
            </Button>
            <Button disabled size="sm">
              {intl.formatMessage({ id: "common.inspect" })}
            </Button>
          </>
        }
      >
        <div className="flex flex-wrap gap-2 text-xs text-muted-foreground">
          <div className="rounded-md border border-border bg-background px-3 py-2">
            {intl.formatMessage({ id: "nodes.scopeAllNodes" })}
          </div>
          <div className="rounded-md border border-border bg-background px-3 py-2">
            {state.nodes
              ? intl.formatMessage({ id: "nodes.totalValue" }, { total: state.nodes.total })
              : intl.formatMessage({ id: "nodes.totalPending" })}
          </div>
        </div>
      </PageHeader>

      {state.loading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "nav.nodes.title" })} /> : null}
      {!state.loading && state.error ? (
        <ResourceState
          kind={mapErrorKind(state.error)}
          onRetry={() => {
            void loadNodes(false)
          }}
          title={intl.formatMessage({ id: "nav.nodes.title" })}
        />
      ) : null}
      {!state.loading && !state.error && state.nodes ? (
        <SectionCard
          description={intl.formatMessage({ id: "nodes.inventoryDescription" })}
          title={intl.formatMessage({ id: "nodes.inventoryTitle" })}
        >
          <TableToolbar
            description={intl.formatMessage({ id: "nodes.toolbarDescription" })}
            onRefresh={() => {
              void loadNodes(true)
            }}
            refreshing={state.refreshing}
            title={intl.formatMessage({ id: "nodes.toolbarTitle" })}
          />
          {state.nodes.items.length > 0 ? (
            <div className="overflow-x-auto rounded-lg border border-border">
              <table className="w-full border-collapse">
                <thead className="bg-muted/40 text-left text-xs uppercase tracking-[0.14em] text-muted-foreground">
                  <tr>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "nodes.table.node" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "nodes.table.address" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "nodes.table.status" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "nodes.table.controller" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "nodes.table.slots" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "nodes.table.actions" })}</th>
                  </tr>
                </thead>
                <tbody>
                  {state.nodes.items.map((node) => (
                    <tr className="border-t border-border" key={node.node_id}>
                      <td className="px-3 py-3 text-sm font-medium text-foreground">{node.node_id}</td>
                      <td className="px-3 py-3 text-sm text-foreground">{node.addr}</td>
                      <td className="px-3 py-3 text-sm text-foreground">
                        <StatusBadge value={node.status} />
                      </td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">{node.controller.role}</td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">
                        {intl.formatMessage(
                          { id: "nodes.slotSummary" },
                          {
                            total: node.slot_stats.count,
                            leaders: node.slot_stats.leader_count,
                          },
                        )}
                      </td>
                      <td className="px-3 py-3 text-sm text-foreground">
                        <div className="flex items-center gap-2">
                          <Button
                            aria-label={intl.formatMessage(
                              { id: "nodes.inspectNode" },
                              { id: node.node_id },
                            )}
                            onClick={() => {
                              void openDetail(node.node_id)
                            }}
                            size="sm"
                            variant="outline"
                          >
                            {intl.formatMessage({ id: "common.inspect" })}
                          </Button>
                          <Button
                            disabled={!canWriteNodes}
                            onClick={() => {
                              setSelectedNodeId(node.node_id)
                              setPendingAction(node.status === "draining" ? "resume" : "drain")
                              setActionError("")
                            }}
                            size="sm"
                            variant="outline"
                          >
                            {node.status === "draining"
                              ? intl.formatMessage({ id: "nodes.resume" })
                              : intl.formatMessage({ id: "nodes.drain" })}
                          </Button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <ResourceState kind="empty" title={intl.formatMessage({ id: "nodes.inventoryTitle" })} />
          )}
        </SectionCard>
      ) : null}

      <DetailSheet
        description={
          detail
            ? intl.formatMessage({ id: "nodes.detailDescriptionValue" }, { value: detail.addr })
            : intl.formatMessage({ id: "nodes.detailDescriptionFallback" })
        }
        footer={
          detail ? (
            <div className="flex items-center justify-end gap-2">
              <Button
                disabled={!canWriteNodes}
                onClick={() => {
                  setPendingAction(detail.status === "draining" ? "resume" : "drain")
                  setActionError("")
                }}
                size="sm"
              >
                {detail.status === "draining"
                  ? intl.formatMessage({ id: "nodes.resumeNode" })
                  : intl.formatMessage({ id: "nodes.drainNode" })}
              </Button>
            </div>
          ) : null
        }
        onOpenChange={closeDetail}
        open={selectedNodeId !== null}
        title={
          detail
            ? intl.formatMessage({ id: "nodes.detailTitleValue" }, { id: detail.node_id })
            : intl.formatMessage({ id: "nodes.detailTitleFallback" })
        }
      >
        {detailLoading ? (
          <ResourceState kind="loading" title={intl.formatMessage({ id: "nodes.detailTitleFallback" })} />
        ) : null}
        {!detailLoading && detailError ? (
          <ResourceState
            kind={mapErrorKind(detailError)}
            onRetry={() => {
              if (selectedNodeId) {
                void loadNodeDetail(selectedNodeId)
              }
            }}
            title={intl.formatMessage({ id: "nodes.detailTitleFallback" })}
          />
        ) : null}
        {!detailLoading && !detailError && detail ? (
          <div className="space-y-4">
            <KeyValueList
              items={[
                { label: intl.formatMessage({ id: "nodes.detail.address" }), value: detail.addr },
                {
                  label: intl.formatMessage({ id: "nodes.detail.status" }),
                  value: <StatusBadge value={detail.status} />,
                },
                {
                  label: intl.formatMessage({ id: "nodes.detail.controllerRole" }),
                  value: detail.controller.role,
                },
                {
                  label: intl.formatMessage({ id: "nodes.detail.lastHeartbeat" }),
                  value: formatTimestamp(intl, detail.last_heartbeat_at),
                },
                {
                  label: intl.formatMessage({ id: "nodes.detail.capacityWeight" }),
                  value: detail.capacity_weight,
                },
                {
                  label: intl.formatMessage({ id: "nodes.detail.hostedIds" }),
                  value: detail.slots.hosted_ids.join(", ") || "-",
                },
                {
                  label: intl.formatMessage({ id: "nodes.detail.leaderIds" }),
                  value: detail.slots.leader_ids.join(", ") || "-",
                },
              ]}
            />
          </div>
        ) : null}
      </DetailSheet>

      <ConfirmDialog
        confirmLabel={intl.formatMessage({ id: "common.confirm" })}
        description={
          pendingAction === "drain"
            ? intl.formatMessage({ id: "nodes.confirmDrainDescription" }, { id: selectedNodeId })
            : intl.formatMessage({ id: "nodes.confirmResumeDescription" }, { id: selectedNodeId })
        }
        error={actionError}
        onConfirm={() => {
          void runAction()
        }}
        onOpenChange={(open) => {
          if (!open) {
            setPendingAction(null)
            setActionError("")
          }
        }}
        open={pendingAction !== null}
        pending={actionPending}
        title={
          pendingAction === "resume"
            ? intl.formatMessage({ id: "nodes.resumeNode" })
            : intl.formatMessage({ id: "nodes.drainNode" })
        }
      />
    </PageContainer>
  )
}

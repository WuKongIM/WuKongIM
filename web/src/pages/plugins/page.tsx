import { useCallback, useEffect, useMemo, useState } from "react"
import { useIntl, type IntlShape } from "react-intl"

import { DetailSheet } from "@/components/manager/detail-sheet"
import { KeyValueList } from "@/components/manager/key-value-list"
import { NodeFilter, defaultNodeId, hasNode } from "@/components/manager/node-filter"
import { ResourceState } from "@/components/manager/resource-state"
import { StatusBadge } from "@/components/manager/status-badge"
import { Button } from "@/components/ui/button"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"
import { getNodePlugin, getNodePlugins, getNodes, ManagerApiError } from "@/lib/manager-api"
import type { ManagerNodePluginsResponse, ManagerNodesResponse, ManagerPlugin } from "@/lib/manager-api.types"

type PluginInventoryState = {
  page: ManagerNodePluginsResponse | null
  loading: boolean
  refreshing: boolean
  error: Error | null
}

type PluginDetailState = {
  pluginNo: string | null
  detail: ManagerPlugin | null
  loading: boolean
  error: Error | null
}

function mapErrorKind(error: Error | null) {
  if (!(error instanceof ManagerApiError)) {
    return "error" as const
  }
  if (error.status === 403) {
    return "forbidden" as const
  }
  if (error.status === 501 || error.status === 503) {
    return "unavailable" as const
  }
  return "error" as const
}

function formatTimestamp(intl: IntlShape, value?: string | null) {
  if (!value) {
    return intl.formatMessage({ id: "plugins.none" })
  }
  return new Intl.DateTimeFormat(intl.locale, {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).format(new Date(value))
}

function formatMethods(plugin: ManagerPlugin, intl: IntlShape) {
  return plugin.methods.length > 0 ? plugin.methods.join(", ") : intl.formatMessage({ id: "plugins.none" })
}

function formatValue(value: unknown, intl: IntlShape) {
  if (value === null || value === undefined || value === "") {
    return intl.formatMessage({ id: "plugins.none" })
  }
  if (typeof value === "string" || typeof value === "number" || typeof value === "boolean") {
    return String(value)
  }
  return JSON.stringify(value)
}

function pluginSummary(page: ManagerNodePluginsResponse | null) {
  const items = page?.items ?? []
  return {
    total: page?.total ?? items.length,
    running: items.filter((item) => item.status === "running").length,
    failed: items.filter((item) => item.status === "failed").length,
    enabled: items.filter((item) => item.enabled).length,
  }
}

export function PluginsPage() {
  const intl = useIntl()
  const [nodes, setNodes] = useState<ManagerNodesResponse | null>(null)
  const [selectedNodeId, setSelectedNodeId] = useState<number | null>(null)
  const [state, setState] = useState<PluginInventoryState>({
    page: null,
    loading: true,
    refreshing: false,
    error: null,
  })
  const [detailState, setDetailState] = useState<PluginDetailState>({
    pluginNo: null,
    detail: null,
    loading: false,
    error: null,
  })

  const summary = useMemo(() => pluginSummary(state.page), [state.page])

  const loadNodes = useCallback(async () => {
    try {
      const nextNodes = await getNodes()
      setNodes(nextNodes)
      setSelectedNodeId((current) => {
        if (current !== null && hasNode(nextNodes, current)) {
          return current
        }
        return defaultNodeId(nextNodes)
      })
      if (nextNodes.items.length === 0) {
        setState({ page: null, loading: false, refreshing: false, error: null })
      }
    } catch (error) {
      setNodes(null)
      setSelectedNodeId(null)
      setState({
        page: null,
        loading: false,
        refreshing: false,
        error: error instanceof Error ? error : new Error("node request failed"),
      })
    }
  }, [])

  const loadPlugins = useCallback(async (nodeId: number | null, refreshing = false) => {
    if (!nodeId) {
      setState({ page: null, loading: false, refreshing: false, error: null })
      return
    }

    setState((current) => ({
      ...current,
      loading: refreshing ? current.loading : true,
      refreshing,
      error: null,
    }))

    try {
      const page = await getNodePlugins(nodeId)
      setState({ page, loading: false, refreshing: false, error: null })
    } catch (error) {
      setState({
        page: null,
        loading: false,
        refreshing: false,
        error: error instanceof Error ? error : new Error("plugin request failed"),
      })
    }
  }, [])

  const openDetail = useCallback(async (pluginNo: string) => {
    if (!selectedNodeId) {
      return
    }
    setDetailState({ pluginNo, detail: null, loading: true, error: null })
    try {
      const detail = await getNodePlugin(selectedNodeId, pluginNo)
      setDetailState({ pluginNo, detail, loading: false, error: null })
    } catch (error) {
      setDetailState({
        pluginNo,
        detail: null,
        loading: false,
        error: error instanceof Error ? error : new Error("plugin detail failed"),
      })
    }
  }, [selectedNodeId])

  const closeDetail = useCallback((open: boolean) => {
    if (open) {
      return
    }
    setDetailState({ pluginNo: null, detail: null, loading: false, error: null })
  }, [])

  useEffect(() => {
    void loadNodes()
  }, [loadNodes])

  useEffect(() => {
    if (selectedNodeId !== null) {
      void loadPlugins(selectedNodeId)
    }
  }, [loadPlugins, selectedNodeId])

  return (
    <PageContainer>
      <PageHeader
        actions={(
          <div className="flex flex-wrap gap-2">
            <NodeFilter nodes={nodes} onNodeChange={setSelectedNodeId} selectedNodeId={selectedNodeId} />
            <Button
              onClick={() => {
                void loadPlugins(selectedNodeId, true)
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
        description={intl.formatMessage({ id: "plugins.description" })}
        eyebrow={intl.formatMessage({ id: "nav.path.cluster.plugins" })}
        title={intl.formatMessage({ id: "plugins.title" })}
      />

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <SummaryPill label={intl.formatMessage({ id: "plugins.totalValue" }, { count: summary.total })} />
        <SummaryPill label={intl.formatMessage({ id: "plugins.runningValue" }, { count: summary.running })} />
        <SummaryPill label={intl.formatMessage({ id: "plugins.failedValue" }, { count: summary.failed })} />
        <SummaryPill label={intl.formatMessage({ id: "plugins.enabledValue" }, { count: summary.enabled })} />
      </div>

      <SectionCard
        description={intl.formatMessage({ id: "plugins.inventory.description" })}
        title={intl.formatMessage({ id: "plugins.inventory.title" })}
      >
        {state.loading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "plugins.title" })} /> : null}
        {!state.loading && state.error ? (
          <ResourceState
            kind={mapErrorKind(state.error)}
            onRetry={() => {
              void loadPlugins(selectedNodeId)
            }}
            title={intl.formatMessage({ id: "plugins.title" })}
          />
        ) : null}
        {!state.loading && !state.error ? (
          state.page && state.page.items.length > 0 ? (
            <div className="overflow-x-auto rounded-lg border border-border">
              <table className="w-full border-collapse">
                <thead className="bg-muted/40 text-left text-xs uppercase tracking-[0.14em] text-muted-foreground">
                  <tr>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "plugins.table.plugin" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "plugins.table.status" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "plugins.table.enabled" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "plugins.table.methods" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "plugins.table.priority" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "plugins.table.pid" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "plugins.table.lastSeen" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "plugins.table.lastError" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "plugins.table.actions" })}</th>
                  </tr>
                </thead>
                <tbody>
                  {state.page.items.map((plugin) => (
                    <tr className="border-t border-border" key={plugin.plugin_no}>
                      <td className="px-3 py-3 text-sm">
                        <div className="font-medium text-foreground">{plugin.plugin_no}</div>
                        <div className="text-xs text-muted-foreground">{plugin.name} · {plugin.version}</div>
                      </td>
                      <td className="px-3 py-3 text-sm"><StatusBadge value={plugin.status} /></td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">
                        {intl.formatMessage({ id: plugin.enabled ? "plugins.enabled.yes" : "plugins.enabled.no" })}
                      </td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">{formatMethods(plugin, intl)}</td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">{plugin.priority}</td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">{plugin.pid || intl.formatMessage({ id: "plugins.none" })}</td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">{formatTimestamp(intl, plugin.last_seen_at)}</td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">{plugin.last_error || intl.formatMessage({ id: "plugins.none" })}</td>
                      <td className="px-3 py-3 text-sm">
                        <Button
                          aria-label={intl.formatMessage({ id: "plugins.action.viewDetails" }, { pluginNo: plugin.plugin_no })}
                          onClick={() => {
                            void openDetail(plugin.plugin_no)
                          }}
                          size="sm"
                          variant="outline"
                        >
                          {intl.formatMessage({ id: "plugins.action.details" })}
                        </Button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <ResourceState kind="empty" title={intl.formatMessage({ id: "plugins.title" })} />
          )
        ) : null}
      </SectionCard>

      <PluginDetailSheet
        detailState={detailState}
        intl={intl}
        nodeId={selectedNodeId}
        onOpenChange={closeDetail}
      />
    </PageContainer>
  )
}

function SummaryPill({ label }: { label: string }) {
  return (
    <div className="rounded-xl border border-border bg-card px-4 py-3 text-sm font-medium text-foreground">
      {label}
    </div>
  )
}

function PluginDetailSheet({
  detailState,
  intl,
  nodeId,
  onOpenChange,
}: {
  detailState: PluginDetailState
  intl: IntlShape
  nodeId: number | null
  onOpenChange: (open: boolean) => void
}) {
  const open = detailState.pluginNo !== null
  const detail = detailState.detail
  return (
    <DetailSheet
      description={detailState.pluginNo && nodeId
        ? intl.formatMessage({ id: "plugins.detail.description" }, { pluginNo: detailState.pluginNo, nodeId })
        : undefined}
      onOpenChange={onOpenChange}
      open={open}
      title={intl.formatMessage({ id: "plugins.detail.title" })}
    >
      {detailState.loading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "plugins.detail.title" })} /> : null}
      {!detailState.loading && detailState.error ? (
        <ResourceState kind={mapErrorKind(detailState.error)} title={intl.formatMessage({ id: "plugins.detail.title" })} />
      ) : null}
      {!detailState.loading && !detailState.error && detail ? (
        <div className="space-y-4">
          <SectionCard title={intl.formatMessage({ id: "plugins.detail.runtime" })}>
            <KeyValueList
              items={[
                { label: intl.formatMessage({ id: "plugins.detail.pluginNo" }), value: detail.plugin_no },
                { label: intl.formatMessage({ id: "plugins.detail.name" }), value: detail.name },
                { label: intl.formatMessage({ id: "plugins.detail.version" }), value: detail.version },
                { label: intl.formatMessage({ id: "plugins.detail.methods" }), value: formatMethods(detail, intl) },
                { label: intl.formatMessage({ id: "plugins.detail.priority" }), value: detail.priority },
                { label: intl.formatMessage({ id: "plugins.detail.pid" }), value: detail.pid || intl.formatMessage({ id: "plugins.none" }) },
                { label: intl.formatMessage({ id: "plugins.detail.lastSeen" }), value: formatTimestamp(intl, detail.last_seen_at) },
                { label: intl.formatMessage({ id: "plugins.detail.lastError" }), value: detail.last_error || intl.formatMessage({ id: "plugins.none" }) },
                { label: intl.formatMessage({ id: "plugins.detail.createdAt" }), value: formatTimestamp(intl, detail.created_at) },
                { label: intl.formatMessage({ id: "plugins.detail.updatedAt" }), value: formatTimestamp(intl, detail.updated_at) },
              ]}
            />
          </SectionCard>
          <SectionCard title={intl.formatMessage({ id: "plugins.detail.config" })}>
            <KeyValueList
              items={Object.entries(detail.config ?? {}).map(([key, value]) => ({
                label: key,
                value: formatValue(value, intl),
              }))}
            />
          </SectionCard>
          <SectionCard title={intl.formatMessage({ id: "plugins.detail.template" })}>
            <KeyValueList
              items={(detail.config_template?.fields ?? []).map((field) => ({
                label: field.label || field.name || intl.formatMessage({ id: "plugins.none" }),
                value: field.type || intl.formatMessage({ id: "plugins.none" }),
              }))}
            />
          </SectionCard>
        </div>
      ) : null}
    </DetailSheet>
  )
}

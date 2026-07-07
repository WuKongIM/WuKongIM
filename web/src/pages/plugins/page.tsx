import type { FormEvent } from "react"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useIntl, type IntlShape } from "react-intl"

import { ActionFormDialog } from "@/components/manager/action-form-dialog"
import { ConfirmDialog } from "@/components/manager/confirm-dialog"
import { DetailSheet } from "@/components/manager/detail-sheet"
import { KeyValueList } from "@/components/manager/key-value-list"
import { NodeFilter, defaultNodeId, hasNode } from "@/components/manager/node-filter"
import { ResourceState } from "@/components/manager/resource-state"
import { StatusBadge } from "@/components/manager/status-badge"
import { Button } from "@/components/ui/button"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"
import {
  createPluginBinding,
  deleteNodePlugin,
  deletePluginBinding,
  getPluginBindings,
  getNodePlugin,
  getNodePlugins,
  getNodes,
  ManagerApiError,
  restartNodePlugin,
  updateNodePluginConfig,
} from "@/lib/manager-api"
import type {
  ManagerNodePluginsResponse,
  ManagerNodesResponse,
  ManagerPlugin,
  ManagerPluginBinding,
  ManagerPluginBindingsResponse,
  PluginBindingListParams,
} from "@/lib/manager-api.types"

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

type BindingSelector = "uid" | "plugin"

type PluginInventoryFilters = {
  keyword: string
  status: string
  method: string
}

type PluginBindingState = {
  page: ManagerPluginBindingsResponse | null
  loading: boolean
  error: Error | null
  validationError: string
}

const allFilterValue = "__all__"

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

function formatConfig(config?: Record<string, unknown>) {
  return JSON.stringify(config ?? {}, null, 2)
}

type ParseConfigResult = { ok: true; value: Record<string, unknown> } | { ok: false; error: string }

function parseConfigObject(raw: string, intl: IntlShape): ParseConfigResult {
  let value: unknown
  try {
    value = JSON.parse(raw)
  } catch {
    return { ok: false, error: intl.formatMessage({ id: "plugins.config.invalidJSON" }) }
  }
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return { ok: false, error: intl.formatMessage({ id: "plugins.config.notObject" }) }
  }
  return { ok: true, value: value as Record<string, unknown> }
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

function pluginFilterOptions(page: ManagerNodePluginsResponse | null) {
  const statuses = new Set<string>()
  const methods = new Set<string>()
  for (const plugin of page?.items ?? []) {
    if (plugin.status) {
      statuses.add(plugin.status)
    }
    for (const method of plugin.methods) {
      methods.add(method)
    }
  }
  return {
    statuses: Array.from(statuses).sort(),
    methods: Array.from(methods).sort(),
  }
}

function pluginMatchesFilters(plugin: ManagerPlugin, filters: PluginInventoryFilters) {
  const keyword = filters.keyword.trim().toLowerCase()
  const keywordMatches = !keyword || [
    plugin.plugin_no,
    plugin.name,
    plugin.version,
    plugin.last_error,
    ...plugin.methods,
  ].some((value) => value.toLowerCase().includes(keyword))

  const statusMatches = filters.status === allFilterValue || plugin.status === filters.status
  const methodMatches = filters.method === allFilterValue || plugin.methods.includes(filters.method)
  return keywordMatches && statusMatches && methodMatches
}

function bindingSearchParams(selector: BindingSelector, rawQuery: string): PluginBindingListParams | null {
  const query = rawQuery.trim()
  if (!query) {
    return null
  }
  if (selector === "plugin") {
    return { pluginNo: query, limit: 50 }
  }
  return { uid: query }
}

function formatBindingWarnings(binding: ManagerPluginBinding, intl: IntlShape) {
  return binding.warnings.length > 0
    ? binding.warnings.map((warning) => warning.message || warning.code).join(", ")
    : intl.formatMessage({ id: "plugins.none" })
}

function appendBindingPage(
  current: ManagerPluginBindingsResponse | null,
  next: ManagerPluginBindingsResponse,
): ManagerPluginBindingsResponse {
  if (!current) {
    return next
  }
  return {
    ...next,
    items: [...current.items, ...next.items],
    warnings: [...(current.warnings ?? []), ...(next.warnings ?? [])],
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
  const [configPlugin, setConfigPlugin] = useState<ManagerPlugin | null>(null)
  const [configText, setConfigText] = useState("{}")
  const [configPending, setConfigPending] = useState(false)
  const [configError, setConfigError] = useState("")
  const [restartPlugin, setRestartPlugin] = useState<ManagerPlugin | null>(null)
  const [restartPending, setRestartPending] = useState(false)
  const [restartError, setRestartError] = useState("")
  const [uninstallPlugin, setUninstallPlugin] = useState<ManagerPlugin | null>(null)
  const [uninstallPending, setUninstallPending] = useState(false)
  const [uninstallError, setUninstallError] = useState("")
  const [bindingSelector, setBindingSelector] = useState<BindingSelector>("uid")
  const [bindingQuery, setBindingQuery] = useState("")
  const [bindingState, setBindingState] = useState<PluginBindingState>({
    page: null,
    loading: false,
    error: null,
    validationError: "",
  })
  const [lastBindingParams, setLastBindingParams] = useState<PluginBindingListParams | null>(null)
  const [addBindingOpen, setAddBindingOpen] = useState(false)
  const [addBindingUid, setAddBindingUid] = useState("")
  const [addBindingPluginNo, setAddBindingPluginNo] = useState("")
  const [addBindingPending, setAddBindingPending] = useState(false)
  const [addBindingError, setAddBindingError] = useState("")
  const [deleteBindingTarget, setDeleteBindingTarget] = useState<ManagerPluginBinding | null>(null)
  const [deleteBindingPending, setDeleteBindingPending] = useState(false)
  const [deleteBindingError, setDeleteBindingError] = useState("")
  const pluginLoadSeq = useRef(0)

  const summary = useMemo(() => pluginSummary(state.page), [state.page])
  const [filters, setFilters] = useState<PluginInventoryFilters>({
    keyword: "",
    status: allFilterValue,
    method: allFilterValue,
  })
  const filterOptions = useMemo(() => pluginFilterOptions(state.page), [state.page])
  const filteredPlugins = useMemo(
    () => (state.page?.items ?? []).filter((plugin) => pluginMatchesFilters(plugin, filters)),
    [filters, state.page],
  )

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
    const loadSeq = pluginLoadSeq.current + 1
    pluginLoadSeq.current = loadSeq
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
      if (pluginLoadSeq.current !== loadSeq) {
        return
      }
      setState({ page, loading: false, refreshing: false, error: null })
    } catch (error) {
      if (pluginLoadSeq.current !== loadSeq) {
        return
      }
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

  const openConfig = useCallback((plugin: ManagerPlugin) => {
    setConfigPlugin(plugin)
    setConfigText(formatConfig(plugin.config))
    setConfigError("")
  }, [])

  const submitConfig = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!configPlugin || !selectedNodeId) {
      return
    }
    const parsed = parseConfigObject(configText, intl)
    if (!parsed.ok) {
      setConfigError(parsed.error)
      return
    }
    setConfigPending(true)
    setConfigError("")
    try {
      await updateNodePluginConfig(selectedNodeId, configPlugin.plugin_no, parsed.value)
      setConfigPlugin(null)
      await loadPlugins(selectedNodeId, true)
    } catch (error) {
      setConfigError(error instanceof Error ? error.message : "update plugin config failed")
    } finally {
      setConfigPending(false)
    }
  }

  const confirmRestart = async () => {
    if (!restartPlugin || !selectedNodeId) {
      return
    }
    setRestartPending(true)
    setRestartError("")
    try {
      await restartNodePlugin(selectedNodeId, restartPlugin.plugin_no)
      setRestartPlugin(null)
      await loadPlugins(selectedNodeId, true)
    } catch (error) {
      setRestartError(error instanceof Error ? error.message : "restart plugin failed")
    } finally {
      setRestartPending(false)
    }
  }

  const confirmUninstall = async () => {
    if (!uninstallPlugin || !selectedNodeId) {
      return
    }
    setUninstallPending(true)
    setUninstallError("")
    try {
      await deleteNodePlugin(selectedNodeId, uninstallPlugin.plugin_no)
      setUninstallPlugin(null)
      await loadPlugins(selectedNodeId, true)
    } catch (error) {
      setUninstallError(error instanceof Error ? error.message : "uninstall plugin failed")
    } finally {
      setUninstallPending(false)
    }
  }

  const loadBindings = useCallback(async (params: PluginBindingListParams, append = false) => {
    setBindingState((current) => ({
      ...current,
      loading: true,
      error: null,
      validationError: "",
    }))
    try {
      const page = await getPluginBindings(params)
      setBindingState((current) => ({
        page: append ? appendBindingPage(current.page, page) : page,
        loading: false,
        error: null,
        validationError: "",
      }))
    } catch (error) {
      setBindingState({
        page: null,
        loading: false,
        error: error instanceof Error ? error : new Error("plugin binding request failed"),
        validationError: "",
      })
    }
  }, [])

  const submitBindingSearch = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const params = bindingSearchParams(bindingSelector, bindingQuery)
    if (!params) {
      setBindingState((current) => ({
        ...current,
        loading: false,
        error: null,
        validationError: intl.formatMessage({ id: "plugins.bindings.emptyQuery" }),
      }))
      return
    }
    setLastBindingParams(params)
    await loadBindings(params)
  }

  const refreshCurrentBindings = useCallback(async () => {
    if (!lastBindingParams) {
      return
    }
    await loadBindings(lastBindingParams)
  }, [lastBindingParams, loadBindings])

  const loadMoreBindings = async () => {
    const cursor = bindingState.page?.next_cursor
    if (!lastBindingParams || !cursor) {
      return
    }
    await loadBindings({ ...lastBindingParams, cursor }, true)
  }

  const submitAddBinding = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const uid = addBindingUid.trim()
    const pluginNo = addBindingPluginNo.trim()
    if (!uid || !pluginNo) {
      setAddBindingError(intl.formatMessage({ id: "plugins.bindings.addRequired" }))
      return
    }
    setAddBindingPending(true)
    setAddBindingError("")
    try {
      await createPluginBinding({ uid, pluginNo })
      setAddBindingOpen(false)
      setAddBindingUid("")
      setAddBindingPluginNo("")
      await refreshCurrentBindings()
    } catch (error) {
      setAddBindingError(error instanceof Error ? error.message : "create plugin binding failed")
    } finally {
      setAddBindingPending(false)
    }
  }

  const confirmDeleteBinding = async () => {
    if (!deleteBindingTarget) {
      return
    }
    setDeleteBindingPending(true)
    setDeleteBindingError("")
    try {
      await deletePluginBinding({ uid: deleteBindingTarget.uid, pluginNo: deleteBindingTarget.plugin_no })
      setDeleteBindingTarget(null)
      await refreshCurrentBindings()
    } catch (error) {
      setDeleteBindingError(error instanceof Error ? error.message : "delete plugin binding failed")
    } finally {
      setDeleteBindingPending(false)
    }
  }

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

      <div
        className="grid gap-0 border-y border-border sm:grid-cols-2 xl:grid-cols-4"
        data-testid="plugins-summary-strip"
      >
        <SummaryPill label={intl.formatMessage({ id: "plugins.totalValue" }, { count: summary.total })} />
        <SummaryPill label={intl.formatMessage({ id: "plugins.runningValue" }, { count: summary.running })} />
        <SummaryPill label={intl.formatMessage({ id: "plugins.failedValue" }, { count: summary.failed })} />
        <SummaryPill label={intl.formatMessage({ id: "plugins.enabledValue" }, { count: summary.enabled })} />
      </div>

      <SectionCard
        description={intl.formatMessage({ id: "plugins.inventory.description" })}
        title={intl.formatMessage({ id: "plugins.inventory.title" })}
      >
        <PluginInventoryFiltersBar
          filters={filters}
          intl={intl}
          onChange={setFilters}
          options={filterOptions}
        />
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
            filteredPlugins.length > 0 ? (
              <div className="overflow-x-auto border border-border">
                <table
                  aria-label={intl.formatMessage({ id: "plugins.inventory.title" })}
                  className="w-full border-collapse"
                >
                  <thead className="border-b border-border bg-background text-left text-xs uppercase tracking-[0.14em] text-muted-foreground">
                    <tr>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "plugins.table.plugin" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "plugins.table.status" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "plugins.table.capabilities" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "plugins.table.lastSignal" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "plugins.table.actions" })}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {filteredPlugins.map((plugin) => (
                      <tr className="border-t border-border align-top hover:bg-muted/45" key={plugin.plugin_no}>
                        <td className="px-3 py-3 text-sm">
                          <div className="font-medium text-foreground">{plugin.plugin_no}</div>
                          <div className="text-xs text-muted-foreground">{plugin.name} · {plugin.version}</div>
                        </td>
                        <td className="px-3 py-3 text-sm">
                          <div className="flex flex-col gap-1">
                            <StatusBadge value={plugin.status} />
                            <span className="text-xs text-muted-foreground">
                              {intl.formatMessage({ id: plugin.enabled ? "plugins.enabled.yes" : "plugins.enabled.no" })}
                            </span>
                          </div>
                        </td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{formatMethods(plugin, intl)}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">
                          <div>{formatTimestamp(intl, plugin.last_seen_at)}</div>
                          {plugin.last_error ? (
                            <div className="mt-1 max-w-[280px] truncate text-xs text-destructive">{plugin.last_error}</div>
                          ) : null}
                        </td>
                        <td className="px-3 py-3 text-sm">
                          <div className="flex flex-wrap gap-2">
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
                            <Button
                              aria-label={intl.formatMessage({ id: "plugins.action.configurePlugin" }, { pluginNo: plugin.plugin_no })}
                              onClick={() => openConfig(plugin)}
                              size="sm"
                              variant="outline"
                            >
                              {intl.formatMessage({ id: "plugins.action.configure" })}
                            </Button>
                            <Button
                              aria-label={intl.formatMessage({ id: "plugins.action.restartPlugin" }, { pluginNo: plugin.plugin_no })}
                              onClick={() => {
                                setRestartError("")
                                setRestartPlugin(plugin)
                              }}
                              size="sm"
                              variant="outline"
                            >
                              {intl.formatMessage({ id: "plugins.action.restart" })}
                            </Button>
                            <Button
                              aria-label={intl.formatMessage({ id: "plugins.action.uninstallPlugin" }, { pluginNo: plugin.plugin_no })}
                              onClick={() => {
                                setUninstallError("")
                                setUninstallPlugin(plugin)
                              }}
                              size="sm"
                              variant="destructive"
                            >
                              {intl.formatMessage({ id: "plugins.action.uninstall" })}
                            </Button>
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <ResourceState kind="empty" title={intl.formatMessage({ id: "plugins.filteredEmpty" })} />
            )
          ) : (
            <ResourceState kind="empty" title={intl.formatMessage({ id: "plugins.title" })} />
          )
        ) : null}
      </SectionCard>

      <SectionCard
        description={intl.formatMessage({ id: "plugins.bindings.description" })}
        title={intl.formatMessage({ id: "plugins.bindings.title" })}
      >
        <form
          aria-label={intl.formatMessage({ id: "plugins.bindings.title" })}
          className="flex flex-col gap-3 lg:flex-row lg:items-end"
          onSubmit={(event) => {
            void submitBindingSearch(event)
          }}
        >
          <label className="flex flex-col gap-1 text-xs font-medium text-muted-foreground" htmlFor="plugin-binding-selector">
            {intl.formatMessage({ id: "plugins.bindings.selector" })}
            <select
              className="h-8 rounded-md border border-border bg-background px-2 text-sm text-foreground outline-none focus:border-ring focus:ring-2 focus:ring-ring/30"
              id="plugin-binding-selector"
              onChange={(event) => setBindingSelector(event.target.value as BindingSelector)}
              value={bindingSelector}
            >
              <option value="uid">{intl.formatMessage({ id: "plugins.bindings.selector.uid" })}</option>
              <option value="plugin">{intl.formatMessage({ id: "plugins.bindings.selector.plugin" })}</option>
            </select>
          </label>
          <label className="flex min-w-0 flex-1 flex-col gap-1 text-xs font-medium text-muted-foreground" htmlFor="plugin-binding-query">
            {intl.formatMessage({ id: "plugins.bindings.query" })}
            <input
              className="h-8 rounded-md border border-border bg-background px-2 text-sm text-foreground outline-none focus:border-ring focus:ring-2 focus:ring-ring/30"
              id="plugin-binding-query"
              onChange={(event) => setBindingQuery(event.target.value)}
              value={bindingQuery}
            />
          </label>
          <div className="flex flex-wrap gap-2">
            <Button disabled={bindingState.loading} size="sm" type="submit">
              {intl.formatMessage({ id: "plugins.bindings.search" })}
            </Button>
            <Button
              onClick={() => {
                setAddBindingError("")
                setAddBindingOpen(true)
              }}
              size="sm"
              type="button"
              variant="outline"
            >
              {intl.formatMessage({ id: "plugins.bindings.add" })}
            </Button>
          </div>
        </form>
        {bindingState.validationError ? <p className="text-sm text-destructive">{bindingState.validationError}</p> : null}
        {bindingState.loading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "plugins.bindings.title" })} /> : null}
        {!bindingState.loading && bindingState.error ? (
          <ResourceState
            kind={mapErrorKind(bindingState.error)}
            onRetry={() => {
              void refreshCurrentBindings()
            }}
            title={intl.formatMessage({ id: "plugins.bindings.title" })}
          />
        ) : null}
        {!bindingState.loading && !bindingState.error && bindingState.page ? (
          bindingState.page.items.length > 0 ? (
            <div className="space-y-3">
              <div className="overflow-x-auto border border-border">
                <table className="w-full border-collapse">
                  <thead className="border-b border-border bg-background text-left text-xs uppercase tracking-[0.14em] text-muted-foreground">
                    <tr>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "plugins.bindings.table.uid" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "plugins.bindings.table.pluginNo" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "plugins.bindings.table.plugin" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "plugins.bindings.table.warnings" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "plugins.bindings.table.actions" })}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {bindingState.page.items.map((binding) => (
                      <tr className="border-t border-border align-top hover:bg-muted/45" key={`${binding.uid}:${binding.plugin_no}`}>
                        <td className="px-3 py-3 text-sm font-medium text-foreground">{binding.uid}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{binding.plugin_no}</td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">
                          {binding.plugin ? `${binding.plugin.name} · ${binding.plugin.version}` : intl.formatMessage({ id: "plugins.none" })}
                        </td>
                        <td className="px-3 py-3 text-sm text-muted-foreground">{formatBindingWarnings(binding, intl)}</td>
                        <td className="px-3 py-3 text-sm">
                          <Button
                            aria-label={intl.formatMessage(
                              { id: "plugins.bindings.deleteOne" },
                              { uid: binding.uid, pluginNo: binding.plugin_no },
                            )}
                            onClick={() => {
                              setDeleteBindingError("")
                              setDeleteBindingTarget(binding)
                            }}
                            size="sm"
                            variant="outline"
                          >
                            {intl.formatMessage({ id: "plugins.bindings.delete" })}
                          </Button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              {bindingState.page.has_more && bindingState.page.next_cursor ? (
                <Button
                  disabled={bindingState.loading}
                  onClick={() => {
                    void loadMoreBindings()
                  }}
                  size="sm"
                  variant="outline"
                >
                  {intl.formatMessage({ id: "plugins.bindings.loadMore" })}
                </Button>
              ) : null}
            </div>
          ) : (
            <ResourceState kind="empty" title={intl.formatMessage({ id: "plugins.bindings.title" })} />
          )
        ) : null}
      </SectionCard>

      <PluginDetailSheet
        detailState={detailState}
        intl={intl}
        nodeId={selectedNodeId}
        onOpenChange={closeDetail}
      />

      <ActionFormDialog
        description={intl.formatMessage({ id: "plugins.config.description" })}
        error={configError}
        onOpenChange={(open) => {
          if (!open) {
            setConfigPlugin(null)
          }
        }}
        onSubmit={(event) => {
          void submitConfig(event)
        }}
        open={configPlugin !== null}
        pending={configPending}
        submitLabel={intl.formatMessage({ id: "plugins.config.update" })}
        title={intl.formatMessage({ id: "plugins.config.title" })}
      >
        <label className="block text-sm font-medium text-foreground" htmlFor="plugin-config-json">
          {intl.formatMessage({ id: "plugins.config.jsonLabel" })}
        </label>
        <textarea
          className="min-h-48 w-full rounded-md border border-border bg-background px-3 py-2 font-mono text-sm outline-none focus:ring-2 focus:ring-ring"
          id="plugin-config-json"
          name="config"
          onChange={(event) => setConfigText(event.target.value)}
          value={configText}
        />
      </ActionFormDialog>

      <ConfirmDialog
        confirmLabel={intl.formatMessage({ id: "plugins.restart.confirm" })}
        description={restartPlugin && selectedNodeId
          ? intl.formatMessage({ id: "plugins.restart.description" }, { pluginNo: restartPlugin.plugin_no, nodeId: selectedNodeId })
          : undefined}
        error={restartError}
        onConfirm={() => {
          void confirmRestart()
        }}
        onOpenChange={(open) => {
          if (!open) {
            setRestartPlugin(null)
          }
        }}
        open={restartPlugin !== null}
        pending={restartPending}
        title={intl.formatMessage({ id: "plugins.restart.title" })}
      />

      <ConfirmDialog
        confirmLabel={intl.formatMessage({ id: "plugins.uninstall.confirm" })}
        description={uninstallPlugin && selectedNodeId
          ? intl.formatMessage({ id: "plugins.uninstall.description" }, { pluginNo: uninstallPlugin.plugin_no, nodeId: selectedNodeId })
          : undefined}
        error={uninstallError}
        onConfirm={() => {
          void confirmUninstall()
        }}
        onOpenChange={(open) => {
          if (!open) {
            setUninstallPlugin(null)
          }
        }}
        open={uninstallPlugin !== null}
        pending={uninstallPending}
        title={intl.formatMessage({ id: "plugins.uninstall.title" })}
      />

      <ActionFormDialog
        description={intl.formatMessage({ id: "plugins.bindings.description" })}
        error={addBindingError}
        onOpenChange={(open) => {
          setAddBindingOpen(open)
          if (!open) {
            setAddBindingError("")
          }
        }}
        onSubmit={(event) => {
          void submitAddBinding(event)
        }}
        open={addBindingOpen}
        pending={addBindingPending}
        submitLabel={intl.formatMessage({ id: "plugins.bindings.add" })}
        title={intl.formatMessage({ id: "plugins.bindings.add" })}
      >
        <label className="block text-sm font-medium text-foreground" htmlFor="plugin-binding-uid">
          {intl.formatMessage({ id: "plugins.bindings.table.uid" })}
        </label>
        <input
          className="h-8 w-full rounded-md border border-border bg-background px-2 text-sm text-foreground outline-none focus:border-ring focus:ring-2 focus:ring-ring/30"
          id="plugin-binding-uid"
          onChange={(event) => setAddBindingUid(event.target.value)}
          value={addBindingUid}
        />
        <label className="block text-sm font-medium text-foreground" htmlFor="plugin-binding-plugin-no">
          {intl.formatMessage({ id: "plugins.bindings.table.pluginNo" })}
        </label>
        <input
          className="h-8 w-full rounded-md border border-border bg-background px-2 text-sm text-foreground outline-none focus:border-ring focus:ring-2 focus:ring-ring/30"
          id="plugin-binding-plugin-no"
          onChange={(event) => setAddBindingPluginNo(event.target.value)}
          value={addBindingPluginNo}
        />
      </ActionFormDialog>

      <ConfirmDialog
        confirmLabel={intl.formatMessage({ id: "plugins.bindings.delete" })}
        description={deleteBindingTarget
          ? intl.formatMessage(
            { id: "plugins.bindings.deleteDescription" },
            { uid: deleteBindingTarget.uid, pluginNo: deleteBindingTarget.plugin_no },
          )
          : undefined}
        error={deleteBindingError}
        onConfirm={() => {
          void confirmDeleteBinding()
        }}
        onOpenChange={(open) => {
          if (!open) {
            setDeleteBindingTarget(null)
          }
        }}
        open={deleteBindingTarget !== null}
        pending={deleteBindingPending}
        title={intl.formatMessage({ id: "plugins.bindings.delete" })}
      />
    </PageContainer>
  )
}

function PluginInventoryFiltersBar({
  filters,
  intl,
  onChange,
  options,
}: {
  filters: PluginInventoryFilters
  intl: IntlShape
  onChange: (filters: PluginInventoryFilters) => void
  options: ReturnType<typeof pluginFilterOptions>
}) {
  return (
    <div className="mb-4 grid gap-3 lg:grid-cols-[minmax(220px,1fr)_180px_180px]">
      <label className="flex min-w-0 flex-col gap-1 text-xs font-medium text-muted-foreground" htmlFor="plugin-keyword-filter">
        {intl.formatMessage({ id: "plugins.filters.keyword" })}
        <input
          className="h-8 rounded-md border border-border bg-background px-2 text-sm text-foreground outline-none focus:border-ring focus:ring-2 focus:ring-ring/30"
          id="plugin-keyword-filter"
          onChange={(event) => onChange({ ...filters, keyword: event.target.value })}
          placeholder={intl.formatMessage({ id: "plugins.filters.keywordPlaceholder" })}
          value={filters.keyword}
        />
      </label>
      <label className="flex flex-col gap-1 text-xs font-medium text-muted-foreground" htmlFor="plugin-status-filter">
        {intl.formatMessage({ id: "plugins.filters.status" })}
        <select
          className="h-8 rounded-md border border-border bg-background px-2 text-sm text-foreground outline-none focus:border-ring focus:ring-2 focus:ring-ring/30"
          id="plugin-status-filter"
          onChange={(event) => onChange({ ...filters, status: event.target.value })}
          value={filters.status}
        >
          <option value={allFilterValue}>{intl.formatMessage({ id: "plugins.filters.status.all" })}</option>
          {options.statuses.map((status) => <option key={status} value={status}>{status}</option>)}
        </select>
      </label>
      <label className="flex flex-col gap-1 text-xs font-medium text-muted-foreground" htmlFor="plugin-method-filter">
        {intl.formatMessage({ id: "plugins.filters.method" })}
        <select
          className="h-8 rounded-md border border-border bg-background px-2 text-sm text-foreground outline-none focus:border-ring focus:ring-2 focus:ring-ring/30"
          id="plugin-method-filter"
          onChange={(event) => onChange({ ...filters, method: event.target.value })}
          value={filters.method}
        >
          <option value={allFilterValue}>{intl.formatMessage({ id: "plugins.filters.method.all" })}</option>
          {options.methods.map((method) => <option key={method} value={method}>{method}</option>)}
        </select>
      </label>
    </div>
  )
}

function SummaryPill({ label }: { label: string }) {
  return (
    <div className="border-b border-border px-1 py-3 text-sm text-foreground sm:px-3">
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

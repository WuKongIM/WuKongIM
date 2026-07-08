import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { Copy, RefreshCw, Search } from "lucide-react"
import { useIntl, type IntlShape } from "react-intl"
import { useSearchParams } from "react-router-dom"

import { ResourceState } from "@/components/manager/resource-state"
import { StatusBadge } from "@/components/manager/status-badge"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"
import { Button } from "@/components/ui/button"
import { getNodeConfig, getNodes, ManagerApiError } from "@/lib/manager-api"
import type {
  ManagerNode,
  ManagerNodeConfigGroup,
  ManagerNodeConfigResponse,
  ManagerNodesResponse,
} from "@/lib/manager-api.types"
import { cn } from "@/lib/utils"

const allGroupsId = "all"

type NodeConfigState = {
  nodes: ManagerNodesResponse | null
  config: ManagerNodeConfigResponse | null
  loadingNodes: boolean
  loadingConfig: boolean
  nodeError: Error | null
  configError: Error | null
}

export function NodeConfigPage() {
  const intl = useIntl()
  const [searchParams, setSearchParams] = useSearchParams()
  const [selectedNodeId, setSelectedNodeId] = useState<number | null>(null)
  const [nodeQuery, setNodeQuery] = useState("")
  const [configQuery, setConfigQuery] = useState("")
  const [activeGroup, setActiveGroup] = useState(allGroupsId)
  const [copied, setCopied] = useState(false)
  const [state, setState] = useState<NodeConfigState>({
    nodes: null,
    config: null,
    loadingNodes: true,
    loadingConfig: false,
    nodeError: null,
    configError: null,
  })
  const configRequestIdRef = useRef(0)
  const requestedNodeId = useMemo(() => parseNodeId(searchParams.get("node_id")), [searchParams])

  const loadConfig = useCallback(async (nodeId: number) => {
    const requestId = configRequestIdRef.current + 1
    configRequestIdRef.current = requestId
    setState((current) => ({
      ...current,
      config: null,
      loadingConfig: true,
      configError: null,
    }))
    try {
      const config = await getNodeConfig(nodeId)
      if (configRequestIdRef.current !== requestId) {
        return
      }
      setState((current) => ({
        ...current,
        config,
        loadingConfig: false,
        configError: null,
      }))
    } catch (error) {
      if (configRequestIdRef.current !== requestId) {
        return
      }
      setState((current) => ({
        ...current,
        config: null,
        loadingConfig: false,
        configError: error instanceof Error ? error : new Error("node config request failed"),
      }))
    }
  }, [])

  const loadNodes = useCallback(async () => {
    setState((current) => ({ ...current, loadingNodes: true, nodeError: null }))
    try {
      const nodes = await getNodes()
      setState((current) => ({
        ...current,
        nodes,
        loadingNodes: false,
        nodeError: null,
      }))
    } catch (error) {
      setState((current) => ({
        ...current,
        nodes: null,
        loadingNodes: false,
        nodeError: error instanceof Error ? error : new Error("nodes request failed"),
      }))
    }
  }, [])

  useEffect(() => {
    void loadNodes()
  }, [loadNodes])

  useEffect(() => {
    const nodes = state.nodes
    if (!nodes) {
      return
    }
    setSelectedNodeId((currentNodeId) => chooseSelectedNodeId(
      nodes,
      requestedNodeId,
      currentNodeId,
    ))
  }, [requestedNodeId, state.nodes])

  useEffect(() => {
    if (!selectedNodeId) {
      setState((current) => ({ ...current, config: null, loadingConfig: false, configError: null }))
      return
    }
    void loadConfig(selectedNodeId)
  }, [loadConfig, selectedNodeId])

  useEffect(() => {
    if (!selectedNodeId) {
      return
    }
    const nextParams = new URLSearchParams(searchParams)
    if (nextParams.get("node_id") !== String(selectedNodeId)) {
      nextParams.set("node_id", String(selectedNodeId))
      setSearchParams(nextParams, { replace: true })
    }
  }, [searchParams, selectedNodeId, setSearchParams])

  useEffect(() => {
    if (activeGroup === allGroupsId || state.config?.groups.some((group) => group.id === activeGroup)) {
      return
    }
    setActiveGroup(allGroupsId)
  }, [activeGroup, state.config])

  const selectedNode = useMemo(
    () => state.nodes?.items.find((node) => node.node_id === selectedNodeId) ?? null,
    [selectedNodeId, state.nodes],
  )
  const filteredNodes = useMemo(
    () => filterNodes(state.nodes?.items ?? [], nodeQuery),
    [nodeQuery, state.nodes],
  )
  const visibleGroups = useMemo(
    () => filterConfigGroups(state.config, activeGroup, configQuery),
    [activeGroup, configQuery, state.config],
  )
  const visibleItemCount = useMemo(
    () => visibleGroups.reduce((sum, group) => sum + group.items.length, 0),
    [visibleGroups],
  )
  const totalItemCount = useMemo(
    () => (state.config?.groups ?? []).reduce((sum, group) => sum + group.items.length, 0),
    [state.config],
  )
  const groupTabs = state.config?.groups ?? []

  async function refresh() {
    await loadNodes()
    if (selectedNodeId) {
      await loadConfig(selectedNodeId)
    }
  }

  async function copyFilteredResult() {
    if (!state.config) {
      return
    }
    const payload = {
      generated_at: state.config.generated_at,
      node_id: state.config.node_id,
      source: state.config.source,
      requires_restart: state.config.requires_restart,
      groups: visibleGroups,
    }
    await navigator.clipboard?.writeText(JSON.stringify(payload, null, 2))
    setCopied(true)
  }

  function selectNode(nodeId: number) {
    setSelectedNodeId(nodeId)
    const nextParams = new URLSearchParams(searchParams)
    nextParams.set("node_id", String(nodeId))
    setSearchParams(nextParams)
  }

  return (
    <PageContainer>
      <PageHeader
        actions={(
          <>
            <Button disabled={state.loadingNodes || state.loadingConfig} onClick={() => void refresh()} size="sm" variant="outline">
              <RefreshCw className="mr-2 size-4" />
              {intl.formatMessage({ id: "common.refresh" })}
            </Button>
            <Button
              disabled={!state.config || visibleItemCount === 0}
              onClick={() => void copyFilteredResult()}
              size="sm"
              variant="outline"
            >
              <Copy className="mr-2 size-4" />
              {intl.formatMessage({ id: "nodeConfig.copyFiltered" })}
            </Button>
            {copied ? (
              <span className="text-xs font-medium text-muted-foreground">
                {intl.formatMessage({ id: "nodeConfig.copied" })}
              </span>
            ) : null}
          </>
        )}
        description={intl.formatMessage({ id: "nodeConfig.description" })}
        title={intl.formatMessage({ id: "nodeConfig.title" })}
      />

      <div className="grid gap-4 xl:grid-cols-[300px_minmax(0,1fr)]">
        <SectionCard
          action={state.nodes ? (
            <span className="font-mono text-xs text-muted-foreground">
              {formatNodeCount(intl, filteredNodes.length, state.nodes.items.length)}
            </span>
          ) : null}
          title={intl.formatMessage({ id: "nodeConfig.nodes.title" })}
        >
          <div className="space-y-3" data-testid="node-config-node-rail">
            <label className="relative block">
              <span className="sr-only">{intl.formatMessage({ id: "nodeConfig.nodes.search" })}</span>
              <Search className="pointer-events-none absolute left-3 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
              <input
                aria-label={intl.formatMessage({ id: "nodeConfig.nodes.search" })}
                className="h-8 w-full rounded-md border border-input bg-background px-3 pl-8 text-sm outline-none transition-colors placeholder:text-muted-foreground focus:border-ring focus:ring-2 focus:ring-ring/30"
                onChange={(event) => setNodeQuery(event.target.value)}
                value={nodeQuery}
              />
            </label>
            {state.loadingNodes ? (
              <ResourceState kind="loading" title={intl.formatMessage({ id: "nodeConfig.nodes.title" })} />
            ) : null}
            {!state.loadingNodes && state.nodeError ? (
              <ResourceState
                kind={mapErrorKind(state.nodeError)}
                onRetry={() => void loadNodes()}
                title={intl.formatMessage({ id: "nodeConfig.nodes.title" })}
              />
            ) : null}
            {!state.loadingNodes && !state.nodeError && state.nodes && state.nodes.items.length === 0 ? (
              <ResourceState kind="empty" title={intl.formatMessage({ id: "nodeConfig.nodes.empty" })} />
            ) : null}
            {!state.loadingNodes && !state.nodeError && state.nodes && filteredNodes.length === 0 ? (
              <ResourceState kind="empty" title={intl.formatMessage({ id: "nodeConfig.nodes.emptyFilter" })} />
            ) : null}
            {!state.loadingNodes && !state.nodeError && filteredNodes.length > 0 ? (
              <div className="space-y-2">
                {filteredNodes.map((node) => {
                  const selected = node.node_id === selectedNodeId
                  return (
                    <button
                      aria-current={selected ? "true" : undefined}
                      className={cn(
                        "w-full rounded-md border border-border bg-background px-3 py-2 text-left transition-colors hover:bg-muted/50",
                        selected && "border-primary/40 bg-primary/8",
                      )}
                      key={node.node_id}
                      onClick={() => selectNode(node.node_id)}
                      type="button"
                    >
                      <div className="flex items-start justify-between gap-2">
                        <div className="min-w-0">
                          <div className="truncate text-sm font-semibold text-foreground">
                            {formatNodeName(intl, node)}
                          </div>
                          <div className="mt-1 flex flex-wrap gap-x-2 gap-y-1 text-xs text-muted-foreground">
                            <span>{node.membership?.role ?? node.controller.role}</span>
                            <span>{node.membership?.join_state ?? node.status}</span>
                            <span>{formatSlotSummary(intl, node)}</span>
                          </div>
                          <div className="mt-1 truncate font-mono text-xs text-muted-foreground">{node.addr}</div>
                        </div>
                        <StatusBadge value={node.health?.status ?? node.status} />
                      </div>
                    </button>
                  )
                })}
              </div>
            ) : null}
          </div>
        </SectionCard>

        <div className="space-y-4">
          <ConfigSummary config={state.config} intl={intl} node={selectedNode} />

          <SectionCard
            action={state.config ? (
              <span className="font-mono text-xs text-muted-foreground">
                {intl.formatMessage({ id: "nodeConfig.config.count" }, { visible: visibleItemCount, total: totalItemCount })}
              </span>
            ) : null}
            title={intl.formatMessage({ id: "nodeConfig.config.title" })}
          >
            <div className="space-y-4">
              <label className="relative block">
                <span className="sr-only">{intl.formatMessage({ id: "nodeConfig.config.search" })}</span>
                <Search className="pointer-events-none absolute left-3 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
                <input
                  aria-label={intl.formatMessage({ id: "nodeConfig.config.search" })}
                  className="h-8 w-full rounded-md border border-input bg-background px-3 pl-8 font-mono text-xs outline-none transition-colors placeholder:text-muted-foreground focus:border-ring focus:ring-2 focus:ring-ring/30"
                  onChange={(event) => setConfigQuery(event.target.value)}
                  value={configQuery}
                />
              </label>

              {state.config ? (
                <div aria-label={intl.formatMessage({ id: "nodeConfig.groups" })} className="flex flex-wrap gap-2 border-b border-border" role="tablist">
                  <GroupTab active={activeGroup === allGroupsId} id={allGroupsId} label={intl.formatMessage({ id: "nodeConfig.group.all" })} onSelect={setActiveGroup} />
                  {groupTabs.map((group) => (
                    <GroupTab
                      active={activeGroup === group.id}
                      id={group.id}
                      key={group.id}
                      label={group.title}
                      onSelect={setActiveGroup}
                    />
                  ))}
                </div>
              ) : null}

              {state.loadingConfig ? (
                <ResourceState kind="loading" title={intl.formatMessage({ id: "nodeConfig.config.status" })} />
              ) : null}
              {!state.loadingConfig && state.configError ? (
                <ResourceState
                  kind={mapErrorKind(state.configError)}
                  onRetry={() => {
                    if (selectedNodeId) {
                      void loadConfig(selectedNodeId)
                    }
                  }}
                  title={intl.formatMessage({ id: "nodeConfig.config.status" })}
                />
              ) : null}
              {!state.loadingConfig && !state.configError && !selectedNodeId ? (
                <ResourceState kind="empty" title={intl.formatMessage({ id: "nodeConfig.config.noNode" })} />
              ) : null}
              {!state.loadingConfig && !state.configError && state.config && totalItemCount === 0 ? (
                <ResourceState kind="empty" title={intl.formatMessage({ id: "nodeConfig.config.empty" })} />
              ) : null}
              {!state.loadingConfig && !state.configError && state.config && totalItemCount > 0 && visibleItemCount === 0 ? (
                <ResourceState kind="empty" title={intl.formatMessage({ id: "nodeConfig.config.emptyFilter" })} />
              ) : null}
              {!state.loadingConfig && !state.configError && visibleItemCount > 0 ? (
                <ConfigGroups groups={visibleGroups} />
              ) : null}
            </div>
          </SectionCard>
        </div>
      </div>
    </PageContainer>
  )
}

function ConfigSummary({
  config,
  intl,
  node,
}: {
  config: ManagerNodeConfigResponse | null
  intl: IntlShape
  node: ManagerNode | null
}) {
  const cells = [
    {
      label: intl.formatMessage({ id: "nodeConfig.summary.currentNode" }),
      value: node ? formatNodeName(intl, node) : "-",
    },
    {
      label: intl.formatMessage({ id: "nodeConfig.summary.source" }),
      value: config?.source ?? "-",
    },
    {
      label: intl.formatMessage({ id: "nodeConfig.summary.restart" }),
      value: config
        ? intl.formatMessage({
          id: config.requires_restart ? "nodes.config.restartRequired" : "nodes.config.restartNotRequired",
        })
        : "-",
    },
    {
      label: intl.formatMessage({ id: "nodeConfig.summary.generatedAt" }),
      value: config ? formatTimestamp(intl, config.generated_at) : "-",
    },
  ]
  return (
    <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
      {cells.map((cell) => (
        <div className="min-h-20 rounded-md border border-border bg-card p-3" key={cell.label}>
          <div className="text-xs font-medium text-muted-foreground">{cell.label}</div>
          <div className="mt-2 break-words text-sm font-semibold text-foreground">{cell.value}</div>
        </div>
      ))}
    </div>
  )
}

function GroupTab({
  active,
  id,
  label,
  onSelect,
}: {
  active: boolean
  id: string
  label: string
  onSelect: (group: string) => void
}) {
  return (
    <button
      aria-selected={active}
      className={cn(
        "border-b-2 px-3 py-2 text-sm font-medium text-muted-foreground transition-colors",
        active ? "border-[var(--status-healthy)] text-foreground" : "border-transparent hover:text-foreground",
      )}
      onClick={() => onSelect(id)}
      role="tab"
      type="button"
    >
      {label}
    </button>
  )
}

function ConfigGroups({ groups }: { groups: ManagerNodeConfigGroup[] }) {
  const intl = useIntl()

  return (
    <div className="space-y-4">
      {groups.map((group) => (
        <section className="space-y-2" key={group.id}>
          <h3 className="text-xs font-semibold uppercase tracking-[0.14em] text-muted-foreground">
            {group.title}
          </h3>
          <div className="max-h-[520px] overflow-auto rounded-md border border-border bg-background">
            <table aria-label={group.title} className="w-full min-w-[680px] border-collapse text-left text-sm">
              <thead className="sticky top-0 z-10 bg-muted/60 text-xs uppercase tracking-[0.12em] text-muted-foreground">
                <tr>
                  <th className="px-3 py-3">{intl.formatMessage({ id: "nodes.config.table.key" })}</th>
                  <th className="px-3 py-3">{intl.formatMessage({ id: "nodes.config.table.label" })}</th>
                  <th className="px-3 py-3">{intl.formatMessage({ id: "nodes.config.table.value" })}</th>
                  <th className="px-3 py-3">{intl.formatMessage({ id: "nodes.config.table.flags" })}</th>
                </tr>
              </thead>
              <tbody>
                {group.items.map((item) => {
                  const flags = [
                    item.sensitive ? intl.formatMessage({ id: "nodes.config.sensitive" }) : null,
                    item.redacted ? intl.formatMessage({ id: "nodes.config.redacted" }) : null,
                  ].filter((flag): flag is string => Boolean(flag))
                  return (
                    <tr className="border-t border-border align-top hover:bg-muted/30" key={item.key}>
                      <td className="max-w-[280px] break-words px-3 py-3 font-mono text-xs text-muted-foreground">
                        {item.key}
                      </td>
                      <td className="px-3 py-3 text-foreground">{item.label}</td>
                      <td className="max-w-[360px] break-words px-3 py-3 font-mono text-xs text-foreground">
                        {item.value || "-"}
                      </td>
                      <td className="px-3 py-3">
                        {flags.length > 0 ? (
                          <div className="flex flex-wrap gap-1">
                            {flags.map((flag) => (
                              <span className="rounded-full border border-border bg-muted px-2 py-0.5 text-xs text-muted-foreground" key={flag}>
                                {flag}
                              </span>
                            ))}
                          </div>
                        ) : (
                          <span className="text-muted-foreground">-</span>
                        )}
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        </section>
      ))}
    </div>
  )
}

function chooseSelectedNodeId(
  nodes: ManagerNodesResponse,
  requestedNodeId: number | null,
  currentNodeId: number | null,
) {
  if (requestedNodeId && nodes.items.some((node) => node.node_id === requestedNodeId)) {
    return requestedNodeId
  }
  if (currentNodeId && nodes.items.some((node) => node.node_id === currentNodeId)) {
    return currentNodeId
  }
  return nodes.items.find((node) => node.is_local)?.node_id ?? nodes.items[0]?.node_id ?? null
}

function parseNodeId(value: string | null) {
  const parsed = Number(value)
  return Number.isInteger(parsed) && parsed > 0 ? parsed : null
}

function filterNodes(nodes: ManagerNode[], query: string) {
  const normalized = query.trim().toLowerCase()
  if (!normalized) {
    return nodes
  }
  return nodes.filter((node) => [
    String(node.node_id),
    node.name ?? "",
    node.addr,
    node.status,
    node.health?.status ?? "",
    node.membership?.role ?? "",
    node.membership?.join_state ?? "",
    node.controller.role,
  ].some((value) => value.toLowerCase().includes(normalized)))
}

function filterConfigGroups(config: ManagerNodeConfigResponse | null, groupId: string, query: string) {
  const normalized = query.trim().toLowerCase()
  return (config?.groups ?? [])
    .filter((group) => groupId === allGroupsId || group.id === groupId)
    .map((group) => ({
      ...group,
      items: group.items.filter((item) => (
        !normalized ||
        item.key.toLowerCase().includes(normalized) ||
        item.label.toLowerCase().includes(normalized) ||
        item.value.toLowerCase().includes(normalized)
      )),
    }))
    .filter((group) => group.items.length > 0)
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

function formatNodeName(intl: IntlShape, node: ManagerNode) {
  const label = node.name || intl.formatMessage({ id: "common.nodeValue" }, { id: node.node_id })
  return node.is_local ? intl.formatMessage({ id: "common.localNodeValue" }, { label }) : label
}

function formatNodeCount(intl: IntlShape, filtered: number, total: number) {
  return filtered === total
    ? intl.formatMessage({ id: "nodeConfig.nodes.count" }, { count: total })
    : intl.formatMessage({ id: "nodeConfig.nodes.filteredCount" }, { filtered, total })
}

function formatSlotSummary(intl: IntlShape, node: ManagerNode) {
  return intl.formatMessage(
    { id: "nodeConfig.nodes.slotSummary" },
    {
      leaders: node.slots?.leader_count ?? node.slot_stats.leader_count,
      slots: node.slots?.replica_count ?? node.slot_stats.count,
    },
  )
}

function formatTimestamp(intl: IntlShape, value: string) {
  if (!value) {
    return "-"
  }
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return value
  }
  return intl.formatDate(date, {
    dateStyle: "medium",
    timeStyle: "medium",
  })
}

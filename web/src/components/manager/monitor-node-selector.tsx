import { useIntl, type IntlShape } from "react-intl"

import type { ManagerNode, ManagerNodesResponse } from "@/lib/manager-api.types"

type MonitorNodeSelectorProps = {
  nodes: ManagerNodesResponse | null
  selectedNodeId: number | null
  labelId: string
  allNodesLabelId: string
  onNodeChange: (nodeId: number | null) => void
}

export function monitorNodeLabel(intl: IntlShape, node: Pick<ManagerNode, "node_id" | "name" | "is_local">) {
  const label = node.name
    ? `${node.name} (${node.node_id})`
    : intl.formatMessage({ id: "common.nodeValue" }, { id: node.node_id })
  return node.is_local ? intl.formatMessage({ id: "common.localNodeValue" }, { label }) : label
}

export function selectedMonitorNodeLabel(intl: IntlShape, nodes: ManagerNodesResponse | null, selectedNodeId: number) {
  const node = nodes?.items.find((item) => item.node_id === selectedNodeId)
  if (node) {
    return monitorNodeLabel(intl, node)
  }
  return intl.formatMessage({ id: "common.nodeValue" }, { id: selectedNodeId })
}

export function MonitorNodeSelector({ nodes, selectedNodeId, labelId, allNodesLabelId, onNodeChange }: MonitorNodeSelectorProps) {
  const intl = useIntl()
  const label = intl.formatMessage({ id: labelId })

  return (
    <label className="flex h-8 items-center gap-2 rounded-lg border border-border bg-background px-2 text-xs font-medium text-muted-foreground">
      <span>{label}</span>
      <select
        aria-label={label}
        className="h-6 min-w-32 bg-transparent text-xs font-medium text-foreground outline-none"
        onChange={(event) => {
          const nextNodeId = Number(event.currentTarget.value)
          onNodeChange(Number.isInteger(nextNodeId) && nextNodeId > 0 ? nextNodeId : null)
        }}
        value={selectedNodeId ?? ""}
      >
        <option value="">{intl.formatMessage({ id: allNodesLabelId })}</option>
        {nodes?.items.map((node) => (
          <option key={node.node_id} value={node.node_id}>
            {monitorNodeLabel(intl, node)}
          </option>
        ))}
      </select>
    </label>
  )
}

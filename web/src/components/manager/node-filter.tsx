import { useIntl, type IntlShape } from "react-intl"

import type { ManagerNode, ManagerNodesResponse } from "@/lib/manager-api.types"

type NodeFilterProps = {
  nodes: ManagerNodesResponse | null
  selectedNodeId: number | null
  onNodeChange: (nodeId: number | null) => void
}

export function defaultNodeId(nodes: ManagerNodesResponse | null) {
  if (!nodes || nodes.items.length === 0) {
    return null
  }
  return nodes.items.find((node) => node.is_local)?.node_id ?? nodes.items[0].node_id
}

export function hasNode(nodes: ManagerNodesResponse | null, nodeId: number) {
  return Boolean(nodes?.items.some((node) => node.node_id === nodeId))
}

function formatNodeOption(intl: IntlShape, node: ManagerNode) {
  const label = node.name
    ? `${node.name} (${node.node_id})`
    : intl.formatMessage({ id: "common.nodeValue" }, { id: node.node_id })
  return node.is_local ? intl.formatMessage({ id: "common.localNodeValue" }, { label }) : label
}

export function NodeFilter({ nodes, selectedNodeId, onNodeChange }: NodeFilterProps) {
  const intl = useIntl()
  const label = intl.formatMessage({ id: "common.nodeFilter" })

  return (
    <label className="flex items-center gap-2 text-xs font-medium text-muted-foreground">
      <span>{label}</span>
      <select
        aria-label={label}
        className="h-7 rounded-md border border-border bg-background px-2 text-sm text-foreground outline-none focus:border-ring focus:ring-2 focus:ring-ring/30"
        disabled={!nodes || nodes.items.length === 0}
        onChange={(event) => {
          const nextNodeId = Number(event.target.value)
          onNodeChange(Number.isInteger(nextNodeId) && nextNodeId > 0 ? nextNodeId : null)
        }}
        value={selectedNodeId ?? ""}
      >
        {nodes?.items.map((node) => (
          <option key={node.node_id} value={node.node_id}>
            {formatNodeOption(intl, node)}
          </option>
        ))}
      </select>
    </label>
  )
}

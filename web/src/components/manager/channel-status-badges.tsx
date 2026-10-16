import { useIntl } from "react-intl"

import { StatusBadge } from "@/components/manager/status-badge"
import type { ManagerChannelRuntimeMeta } from "@/lib/manager-api.types"

// The durable lifecycle remains available after an idle runtime is unloaded.
export function ChannelLifecycleBadge({ status }: { status: string }) {
  const intl = useIntl()
  return <StatusBadge value={status} label={status === "active" ? intl.formatMessage({ id: "channels.lifecycle.available" }) : undefined} />
}

// Missing observations from older or unavailable nodes never imply inactivity.
export function ChannelRuntimeStatusBadge({ channel }: { channel: ManagerChannelRuntimeMeta }) {
  const intl = useIntl()
  const status = channel.runtime_status === "active" || channel.runtime_status === "inactive" ? channel.runtime_status : "unknown"
  const nodeId = channel.runtime_node_id ?? channel.leader
  const title = nodeId > 0
    ? intl.formatMessage({ id: "channels.runtime.leaderNode" }, { node: nodeId })
    : intl.formatMessage({ id: "channels.runtime.leaderUnknown" })

  return (
    <span title={title}>
      <StatusBadge value={status} label={intl.formatMessage({ id: `channels.runtime.${status}` })} />
    </span>
  )
}

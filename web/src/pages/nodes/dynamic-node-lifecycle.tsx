import { useCallback, useEffect, useMemo, useRef, useState } from "react"

import { useAuthStore } from "@/auth/auth-store"
import { DetailSheet } from "@/components/manager/detail-sheet"
import { StatusBadge } from "@/components/manager/status-badge"
import { Button } from "@/components/ui/button"
import {
  activateNode,
  joinNode,
} from "@/lib/manager-api"
import type {
  ManagerActivateNodeResponse,
  ManagerJoinNodeResponse,
  ManagerNode,
} from "@/lib/manager-api.types"

export type DynamicNodeLifecycleMode = "join" | "node"

export type DynamicNodeLifecycleSheetProps = {
  open: boolean
  mode: DynamicNodeLifecycleMode
  node: ManagerNode | null
  onOpenChange: (open: boolean) => void
  onCompleted: () => void
}

function hasPermission(permissions: { resource: string; actions: string[] }[], resource: string, action: string) {
  return permissions.some((permission) => {
    if (permission.resource !== resource && permission.resource !== "*") {
      return false
    }
    return permission.actions.includes(action) || permission.actions.includes("*")
  })
}

function nodeJoinState(node: ManagerNode | null) {
  return node?.membership?.join_state ?? "unknown"
}

function parsePositiveFiniteNumber(value: string) {
  const parsed = Number(value)
  return Number.isFinite(parsed) && parsed > 0 ? parsed : null
}

export function DynamicNodeLifecycleSheet({
  open,
  mode,
  node,
  onOpenChange,
  onCompleted,
}: DynamicNodeLifecycleSheetProps) {
  const permissions = useAuthStore((state) => state.permissions)
  const canWriteNodes = useMemo(() => hasPermission(permissions, "cluster.node", "w"), [permissions])
  const [nodeId, setNodeId] = useState("")
  const [name, setName] = useState("")
  const [addr, setAddr] = useState("")
  const [capacityWeight, setCapacityWeight] = useState("1")
  const [pending, setPending] = useState(false)
  const [error, setError] = useState("")
  const [joinResult, setJoinResult] = useState<ManagerJoinNodeResponse | null>(null)
  const [activateResult, setActivateResult] = useState<ManagerActivateNodeResponse | null>(null)
  const lifecycleIdentity = `${mode}:${node?.node_id ?? "join"}`
  const previousLifecycleIdentity = useRef(lifecycleIdentity)
  const wasOpen = useRef(open)
  const openRef = useRef(open)
  const operationGeneration = useRef(0)

  useEffect(() => {
    openRef.current = open
  }, [open])

  const beginOperation = useCallback(() => {
    operationGeneration.current += 1
    return operationGeneration.current
  }, [])

  const isCurrentOperation = useCallback((generation: number) => {
    return openRef.current && operationGeneration.current === generation
  }, [])

  const resetTransientState = useCallback(() => {
    operationGeneration.current += 1
    setNodeId("")
    setName("")
    setAddr("")
    setCapacityWeight("1")
    setPending(false)
    setError("")
    setJoinResult(null)
    setActivateResult(null)
  }, [])

  useEffect(() => {
    if (previousLifecycleIdentity.current !== lifecycleIdentity) {
      previousLifecycleIdentity.current = lifecycleIdentity
      resetTransientState()
    }
  }, [lifecycleIdentity, resetTransientState])

  useEffect(() => {
    if (wasOpen.current && !open) {
      resetTransientState()
    }
    wasOpen.current = open
  }, [open, resetTransientState])

  const submitJoin = useCallback(async () => {
    if (!canWriteNodes) {
      return
    }
    const parsedNodeId = parsePositiveFiniteNumber(nodeId)
    if (parsedNodeId === null) {
      setError("Node ID must be a positive number.")
      return
    }
    const trimmedAddr = addr.trim()
    if (!trimmedAddr) {
      setError("Address is required.")
      return
    }
    const parsedCapacityWeight = parsePositiveFiniteNumber(capacityWeight)
    if (parsedCapacityWeight === null) {
      setError("Capacity weight must be a positive number.")
      return
    }

    setPending(true)
    setError("")
    const generation = beginOperation()

    try {
      const result = await joinNode({
        nodeId: parsedNodeId,
        addr: trimmedAddr,
        name: name.trim(),
        capacityWeight: parsedCapacityWeight,
      })
      if (!isCurrentOperation(generation)) {
        return
      }
      setJoinResult(result)
      onCompleted()
    } catch (err) {
      if (!isCurrentOperation(generation)) {
        return
      }
      setError(err instanceof Error ? err.message : "node join failed")
    } finally {
      if (isCurrentOperation(generation)) {
        setPending(false)
      }
    }
  }, [addr, beginOperation, canWriteNodes, capacityWeight, isCurrentOperation, name, nodeId, onCompleted])

  const submitActivate = useCallback(async () => {
    if (!node || !canWriteNodes) {
      return
    }
    setPending(true)
    setError("")
    const generation = beginOperation()

    try {
      const result = await activateNode(node.node_id)
      if (!isCurrentOperation(generation)) {
        return
      }
      setActivateResult(result)
      onCompleted()
    } catch (err) {
      if (!isCurrentOperation(generation)) {
        return
      }
      setError(err instanceof Error ? err.message : "node activation failed")
    } finally {
      if (isCurrentOperation(generation)) {
        setPending(false)
      }
    }
  }, [beginOperation, canWriteNodes, isCurrentOperation, node, onCompleted])

  const title = mode === "join" ? "Add node" : "Node lifecycle"

  return (
    <DetailSheet onOpenChange={onOpenChange} open={open} title={title}>
      <div className="space-y-4">
        {!canWriteNodes ? (
          <div className="rounded-lg border border-border bg-muted/30 px-4 py-3 text-sm text-muted-foreground">
            Requires cluster.node write permission.
          </div>
        ) : null}
        {error ? <p className="text-sm text-destructive" role="alert">{error}</p> : null}

        {mode === "join" ? (
          <div className="grid gap-3">
            <label className="text-sm font-medium text-foreground">
              Node ID
              <input
                className="mt-1 h-9 w-full rounded-md border border-border bg-background px-3 text-sm"
                onChange={(event) => setNodeId(event.target.value)}
                type="number"
                value={nodeId}
              />
            </label>
            <label className="text-sm font-medium text-foreground">
              Address
              <input
                className="mt-1 h-9 w-full rounded-md border border-border bg-background px-3 text-sm"
                onChange={(event) => setAddr(event.target.value)}
                value={addr}
              />
            </label>
            <label className="text-sm font-medium text-foreground">
              Name
              <input
                className="mt-1 h-9 w-full rounded-md border border-border bg-background px-3 text-sm"
                onChange={(event) => setName(event.target.value)}
                value={name}
              />
            </label>
            <label className="text-sm font-medium text-foreground">
              Capacity weight
              <input
                className="mt-1 h-9 w-full rounded-md border border-border bg-background px-3 text-sm"
                onChange={(event) => setCapacityWeight(event.target.value)}
                type="number"
                value={capacityWeight}
              />
            </label>
            <Button disabled={pending || !canWriteNodes} onClick={() => void submitJoin()}>
              Join node
            </Button>
            {joinResult ? (
              <div className="rounded-lg border border-border bg-muted/30 p-3 text-sm text-muted-foreground">
                <div>Node {joinResult.node_id}</div>
                <div>Join state: {joinResult.join_state}</div>
                <div>Revision: {joinResult.revision}</div>
              </div>
            ) : null}
          </div>
        ) : null}

        {mode === "node" && node ? (
          <div className="space-y-4">
            <div className="flex flex-wrap items-center gap-2 text-sm text-muted-foreground">
              <StatusBadge value={nodeJoinState(node)} />
              <span>Node {node.node_id}</span>
              <span>{node.addr}</span>
            </div>
            {nodeJoinState(node) === "joining" ? (
              <Button disabled={pending || !canWriteNodes} onClick={() => void submitActivate()}>
                Activate node
              </Button>
            ) : null}
            {activateResult ? (
              <div className="rounded-lg border border-border bg-muted/30 p-3 text-sm text-muted-foreground">
                <div>Join state: {activateResult.join_state}</div>
                <div>Revision: {activateResult.revision}</div>
              </div>
            ) : null}
            <Button size="sm" type="button" variant="outline">
              Diagnostics
            </Button>
          </div>
        ) : null}
      </div>
    </DetailSheet>
  )
}

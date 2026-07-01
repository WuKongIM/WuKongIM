import { useCallback, useEffect, useMemo, useRef, useState } from "react"

import { useAuthStore } from "@/auth/auth-store"
import { DetailSheet } from "@/components/manager/detail-sheet"
import { StatusBadge } from "@/components/manager/status-badge"
import { Button } from "@/components/ui/button"
import {
  activateNode,
  advanceNodeOnboarding,
  getNodeOnboardingStatus,
  joinNode,
  planNodeOnboarding,
  startNodeOnboarding,
} from "@/lib/manager-api"
import type {
  ManagerActivateNodeResponse,
  ManagerJoinNodeResponse,
  ManagerNode,
  ManagerNodeOnboardingPlanResponse,
  ManagerNodeOnboardingStartResponse,
  ManagerNodeOnboardingStatusResponse,
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

function parsePositiveSafeInteger(value: string, defaultValue: number) {
  const trimmed = value.trim()
  if (!trimmed) {
    return defaultValue
  }
  const parsed = Number(trimmed)
  return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : null
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
  const [maxSlotMoves, setMaxSlotMoves] = useState("1")
  const [onboardingPlan, setOnboardingPlan] = useState<ManagerNodeOnboardingPlanResponse | null>(null)
  const [onboardingStart, setOnboardingStart] = useState<ManagerNodeOnboardingStartResponse | null>(null)
  const [onboardingStatus, setOnboardingStatus] = useState<ManagerNodeOnboardingStatusResponse | null>(null)
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
    setMaxSlotMoves("1")
    setOnboardingPlan(null)
    setOnboardingStart(null)
    setOnboardingStatus(null)
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

  const boundedMovesInput = useCallback(() => {
    const parsedMaxSlotMoves = parsePositiveSafeInteger(maxSlotMoves, 1)
    if (parsedMaxSlotMoves === null) {
      setError("Max slot moves must be a positive safe integer.")
      return null
    }
    return { maxSlotMoves: parsedMaxSlotMoves }
  }, [maxSlotMoves])

  const runOnboardingPlan = useCallback(async () => {
    if (!node || !canWriteNodes) {
      return
    }
    const input = boundedMovesInput()
    if (!input) {
      return
    }
    setPending(true)
    setError("")
    const generation = beginOperation()

    try {
      const result = await planNodeOnboarding(node.node_id, input)
      if (!isCurrentOperation(generation)) {
        return
      }
      setOnboardingPlan(result)
      setOnboardingStart(null)
      setOnboardingStatus(null)
    } catch (err) {
      if (!isCurrentOperation(generation)) {
        return
      }
      setError(err instanceof Error ? err.message : "node onboarding plan failed")
    } finally {
      if (isCurrentOperation(generation)) {
        setPending(false)
      }
    }
  }, [beginOperation, boundedMovesInput, canWriteNodes, isCurrentOperation, node])

  const runOnboardingStart = useCallback(async () => {
    if (!node || !canWriteNodes) {
      return
    }
    const input = boundedMovesInput()
    if (!input) {
      return
    }
    setPending(true)
    setError("")
    const generation = beginOperation()

    try {
      const result = await startNodeOnboarding(node.node_id, input)
      if (!isCurrentOperation(generation)) {
        return
      }
      setOnboardingStart(result)
      onCompleted()
    } catch (err) {
      if (!isCurrentOperation(generation)) {
        return
      }
      setError(err instanceof Error ? err.message : "node onboarding start failed")
    } finally {
      if (isCurrentOperation(generation)) {
        setPending(false)
      }
    }
  }, [beginOperation, boundedMovesInput, canWriteNodes, isCurrentOperation, node, onCompleted])

  const refreshOnboardingStatus = useCallback(async () => {
    if (!node || !canWriteNodes) {
      return
    }
    setPending(true)
    setError("")
    const generation = beginOperation()

    try {
      const result = await getNodeOnboardingStatus(node.node_id)
      if (!isCurrentOperation(generation)) {
        return
      }
      setOnboardingStatus(result)
    } catch (err) {
      if (!isCurrentOperation(generation)) {
        return
      }
      setError(err instanceof Error ? err.message : "node onboarding status refresh failed")
    } finally {
      if (isCurrentOperation(generation)) {
        setPending(false)
      }
    }
  }, [beginOperation, canWriteNodes, isCurrentOperation, node])

  const runOnboardingAdvance = useCallback(async () => {
    if (!node || !canWriteNodes) {
      return
    }
    const input = boundedMovesInput()
    if (!input) {
      return
    }
    setPending(true)
    setError("")
    const generation = beginOperation()

    try {
      const result = await advanceNodeOnboarding(node.node_id, input)
      if (!isCurrentOperation(generation)) {
        return
      }
      setOnboardingStart(result)
      onCompleted()
    } catch (err) {
      if (!isCurrentOperation(generation)) {
        return
      }
      setError(err instanceof Error ? err.message : "node onboarding advance failed")
    } finally {
      if (isCurrentOperation(generation)) {
        setPending(false)
      }
    }
  }, [beginOperation, boundedMovesInput, canWriteNodes, isCurrentOperation, node, onCompleted])

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
            {nodeJoinState(node) === "active" && node.membership?.schedulable ? (
              <div className="space-y-3 rounded-lg border border-border bg-muted/20 p-3">
                <div className="flex flex-col gap-3 sm:flex-row sm:items-end">
                  <label className="text-sm font-medium text-foreground">
                    Max slot moves
                    <input
                      className="mt-1 h-9 w-32 rounded-md border border-border bg-background px-3 text-sm"
                      onChange={(event) => setMaxSlotMoves(event.target.value)}
                      value={maxSlotMoves}
                    />
                  </label>
                  <Button
                    disabled={pending || !canWriteNodes}
                    onClick={() => void runOnboardingPlan()}
                    size="sm"
                    type="button"
                    variant="outline"
                  >
                    Plan slot onboarding
                  </Button>
                  <Button
                    disabled={pending || !canWriteNodes || !onboardingPlan}
                    onClick={() => void runOnboardingStart()}
                    size="sm"
                    type="button"
                  >
                    Start onboarding
                  </Button>
                  <Button
                    disabled={pending || !canWriteNodes}
                    onClick={() => void refreshOnboardingStatus()}
                    size="sm"
                    type="button"
                    variant="outline"
                  >
                    Refresh onboarding status
                  </Button>
                  <Button
                    disabled={pending || !canWriteNodes}
                    onClick={() => void runOnboardingAdvance()}
                    size="sm"
                    type="button"
                    variant="outline"
                  >
                    Advance onboarding
                  </Button>
                </div>
                {onboardingPlan ? (
                  <div className="space-y-2 text-sm">
                    <div>State revision: {onboardingPlan.state_revision}</div>
                    {onboardingPlan.candidates.map((candidate) => (
                      <div className="rounded-md border border-border bg-background px-3 py-2" key={candidate.slot_id}>
                        <div className="font-medium text-foreground">Slot {candidate.slot_id}</div>
                        <div className="text-muted-foreground">
                          {candidate.source_node_id} -&gt; {candidate.target_node_id}
                        </div>
                        <div className="text-muted-foreground">
                          Target peers: {candidate.target_peers.join(", ")}
                        </div>
                      </div>
                    ))}
                    {onboardingPlan.skipped.map((skip) => (
                      <div className="text-muted-foreground" key={`${skip.slot_id}-${skip.reason}`}>
                        {skip.message || skip.reason}
                      </div>
                    ))}
                  </div>
                ) : null}
                {onboardingStart ? (
                  <div className="space-y-1 text-sm text-muted-foreground">
                    <div>Created tasks: {onboardingStart.created}</div>
                    {onboardingStart.skipped.map((skip) => (
                      <div key={`${skip.slot_id}-${skip.reason}`}>{skip.message || skip.reason}</div>
                    ))}
                  </div>
                ) : null}
                {onboardingStatus ? (
                  <div className="text-sm text-muted-foreground">
                    Active tasks: {onboardingStatus.summary.total_active}
                  </div>
                ) : null}
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

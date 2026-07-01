import { useCallback, useEffect, useMemo, useRef, useState } from "react"

import { useAuthStore } from "@/auth/auth-store"
import { DetailSheet } from "@/components/manager/detail-sheet"
import { StatusBadge } from "@/components/manager/status-badge"
import { Button } from "@/components/ui/button"
import {
  activateNode,
  advanceNodeScaleIn,
  advanceNodeOnboarding,
  getNodeOnboardingStatus,
  getNodeScaleInStatus,
  joinNode,
  planNodeOnboarding,
  planNodeScaleIn,
  removeNodeAfterScaleIn,
  setNodeScaleInDrain,
  startNodeScaleIn,
  startNodeOnboarding,
} from "@/lib/manager-api"
import type {
  ManagerActivateNodeResponse,
  ManagerJoinNodeResponse,
  ManagerNode,
  ManagerNodeOnboardingPlanResponse,
  ManagerNodeOnboardingStartResponse,
  ManagerNodeOnboardingStatusResponse,
  ManagerNodeScaleInAdvanceResponse,
  ManagerNodeScaleInDrainResponse,
  ManagerNodeScaleInPlanResponse,
  ManagerNodeScaleInRemoveResponse,
  ManagerNodeScaleInStartResponse,
  ManagerNodeScaleInStatusResponse,
} from "@/lib/manager-api.types"

export type DynamicNodeLifecycleMode = "join" | "node"

export type DynamicNodeLifecycleSheetProps = {
  open: boolean
  mode: DynamicNodeLifecycleMode
  node: ManagerNode | null
  onOpenChange: (open: boolean) => void
  onCompleted: () => void | Promise<void>
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

function yesNo(value: boolean) {
  return value ? "yes" : "no"
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
  const [scaleInPlan, setScaleInPlan] = useState<ManagerNodeScaleInPlanResponse | null>(null)
  const [scaleInStart, setScaleInStart] = useState<ManagerNodeScaleInStartResponse | null>(null)
  const [scaleInDrain, setScaleInDrain] = useState<ManagerNodeScaleInDrainResponse | null>(null)
  const [scaleInStatus, setScaleInStatus] = useState<ManagerNodeScaleInStatusResponse | null>(null)
  const [scaleInAdvance, setScaleInAdvance] = useState<ManagerNodeScaleInAdvanceResponse | null>(null)
  const [scaleInRemove, setScaleInRemove] = useState<ManagerNodeScaleInRemoveResponse | null>(null)
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
    setScaleInPlan(null)
    setScaleInStart(null)
    setScaleInDrain(null)
    setScaleInStatus(null)
    setScaleInAdvance(null)
    setScaleInRemove(null)
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
      await onCompleted()
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
      await onCompleted()
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

  const currentJoinState = nodeJoinState(node)
  const showScaleInActions = Boolean(node && node.membership?.role === "data" && currentJoinState !== "removed")
  const canUseScaleInActions = Boolean(
    node
      && canWriteNodes
      && showScaleInActions
      && (currentJoinState === "active" || currentJoinState === "leaving")
      && node.actions?.can_scale_in === true,
  )

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
      await onCompleted()
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
      await onCompleted()
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

  const runScaleInPlan = useCallback(async () => {
    if (!node || !canUseScaleInActions) {
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
      const result = await planNodeScaleIn(node.node_id, input)
      if (!isCurrentOperation(generation)) {
        return
      }
      setScaleInPlan(result)
      setScaleInStart(null)
      setScaleInDrain(null)
      setScaleInStatus(null)
      setScaleInAdvance(null)
      setScaleInRemove(null)
    } catch (err) {
      if (!isCurrentOperation(generation)) {
        return
      }
      setError(err instanceof Error ? err.message : "node scale-in plan failed")
    } finally {
      if (isCurrentOperation(generation)) {
        setPending(false)
      }
    }
  }, [beginOperation, boundedMovesInput, canUseScaleInActions, isCurrentOperation, node])

  const runScaleInStart = useCallback(async () => {
    if (!node || !canUseScaleInActions) {
      return
    }
    setScaleInStatus(null)
    setPending(true)
    setError("")
    const generation = beginOperation()

    try {
      const result = await startNodeScaleIn(node.node_id)
      if (!isCurrentOperation(generation)) {
        return
      }
      setScaleInStart(result)
      setScaleInStatus(null)
      await onCompleted()
    } catch (err) {
      if (!isCurrentOperation(generation)) {
        return
      }
      setError(err instanceof Error ? err.message : "node scale-in start failed")
    } finally {
      if (isCurrentOperation(generation)) {
        setPending(false)
      }
    }
  }, [beginOperation, canUseScaleInActions, isCurrentOperation, node, onCompleted])

  const enableDrainMode = useCallback(async () => {
    if (!node || !canUseScaleInActions) {
      return
    }
    setScaleInStatus(null)
    setPending(true)
    setError("")
    const generation = beginOperation()

    try {
      const result = await setNodeScaleInDrain(node.node_id, { draining: true })
      if (!isCurrentOperation(generation)) {
        return
      }
      setScaleInDrain(result)
      setScaleInStatus(null)
      await onCompleted()
    } catch (err) {
      if (!isCurrentOperation(generation)) {
        return
      }
      setError(err instanceof Error ? err.message : "node scale-in drain failed")
    } finally {
      if (isCurrentOperation(generation)) {
        setPending(false)
      }
    }
  }, [beginOperation, canUseScaleInActions, isCurrentOperation, node, onCompleted])

  const refreshScaleInStatus = useCallback(async () => {
    if (!node || !canUseScaleInActions) {
      return
    }
    setScaleInStatus(null)
    setPending(true)
    setError("")
    const generation = beginOperation()

    try {
      const result = await getNodeScaleInStatus(node.node_id)
      if (!isCurrentOperation(generation)) {
        return
      }
      setScaleInStatus(result)
    } catch (err) {
      if (!isCurrentOperation(generation)) {
        return
      }
      setError(err instanceof Error ? err.message : "node scale-in status refresh failed")
    } finally {
      if (isCurrentOperation(generation)) {
        setPending(false)
      }
    }
  }, [beginOperation, canUseScaleInActions, isCurrentOperation, node])

  const canRemoveScaleInNode = Boolean(
    canUseScaleInActions && node && scaleInStatus?.node_id === node.node_id && scaleInStatus.safe_to_remove === true,
  )

  const runScaleInAdvance = useCallback(async () => {
    if (!node || !canUseScaleInActions) {
      return
    }
    const input = boundedMovesInput()
    if (!input) {
      return
    }
    setScaleInStatus(null)
    setPending(true)
    setError("")
    const generation = beginOperation()

    try {
      const result = await advanceNodeScaleIn(node.node_id, input)
      if (!isCurrentOperation(generation)) {
        return
      }
      setScaleInAdvance(result)
      setScaleInStatus(null)
      await onCompleted()
    } catch (err) {
      if (!isCurrentOperation(generation)) {
        return
      }
      setError(err instanceof Error ? err.message : "node scale-in advance failed")
    } finally {
      if (isCurrentOperation(generation)) {
        setPending(false)
      }
    }
  }, [beginOperation, boundedMovesInput, canUseScaleInActions, isCurrentOperation, node, onCompleted])

  const runScaleInRemove = useCallback(async () => {
    if (!node || !canRemoveScaleInNode) {
      return
    }
    setScaleInStatus(null)
    setPending(true)
    setError("")
    const generation = beginOperation()

    try {
      const result = await removeNodeAfterScaleIn(node.node_id)
      if (!isCurrentOperation(generation)) {
        return
      }
      setScaleInRemove(result)
      setScaleInStatus(null)
      await onCompleted()
    } catch (err) {
      if (!isCurrentOperation(generation)) {
        return
      }
      setError(err instanceof Error ? err.message : "node scale-in remove failed")
    } finally {
      if (isCurrentOperation(generation)) {
        setPending(false)
      }
    }
  }, [beginOperation, canRemoveScaleInNode, isCurrentOperation, node, onCompleted])

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
            {showScaleInActions ? (
              <div className="space-y-3 rounded-lg border border-border bg-muted/20 p-3">
                <div className="flex flex-col gap-3 sm:flex-row sm:items-end">
                  <label className="text-sm font-medium text-foreground">
                    Scale-in max slot moves
                    <input
                      className="mt-1 h-9 w-32 rounded-md border border-border bg-background px-3 text-sm"
                      onChange={(event) => setMaxSlotMoves(event.target.value)}
                      value={maxSlotMoves}
                    />
                  </label>
                  <Button
                    disabled={pending || !canUseScaleInActions}
                    onClick={() => void runScaleInPlan()}
                    size="sm"
                    type="button"
                    variant="outline"
                  >
                    Plan scale-in
                  </Button>
                  <Button
                    disabled={pending || !canUseScaleInActions}
                    onClick={() => void runScaleInStart()}
                    size="sm"
                    type="button"
                    variant="outline"
                  >
                    Mark leaving
                  </Button>
                  <Button
                    disabled={pending || !canUseScaleInActions}
                    onClick={() => void enableDrainMode()}
                    size="sm"
                    type="button"
                    variant="outline"
                  >
                    Enable drain mode
                  </Button>
                  <Button
                    disabled={pending || !canUseScaleInActions}
                    onClick={() => void refreshScaleInStatus()}
                    size="sm"
                    type="button"
                    variant="outline"
                  >
                    Refresh scale-in status
                  </Button>
                  <Button
                    disabled={pending || !canUseScaleInActions}
                    onClick={() => void runScaleInAdvance()}
                    size="sm"
                    type="button"
                    variant="outline"
                  >
                    Advance scale-in
                  </Button>
                  <Button
                    disabled={pending || !canRemoveScaleInNode}
                    onClick={() => void runScaleInRemove()}
                    size="sm"
                    type="button"
                    variant="outline"
                  >
                    Remove node
                  </Button>
                </div>
                {scaleInPlan ? (
                  <div className="space-y-2 text-sm">
                    <div>State revision: {scaleInPlan.state_revision}</div>
                    <div>Blocked by status: {yesNo(scaleInPlan.blocked_by_status)}</div>
                    {scaleInPlan.candidates.map((candidate) => (
                      <div className="rounded-md border border-border bg-background px-3 py-2" key={candidate.slot_id}>
                        <div className="font-medium text-foreground">Slot {candidate.slot_id}</div>
                        <div className="text-muted-foreground">
                          {candidate.source_node_id} -&gt; {candidate.target_node_id}
                        </div>
                        <div className="text-muted-foreground">
                          Desired peers: {candidate.desired_peers.join(", ")}
                        </div>
                        <div className="text-muted-foreground">
                          Target peers: {candidate.target_peers.join(", ")}
                        </div>
                      </div>
                    ))}
                  </div>
                ) : null}
                {scaleInStart ? (
                  <div className="space-y-1 text-sm text-muted-foreground">
                    <div>Join state: {scaleInStart.join_state}</div>
                    <div>Revision: {scaleInStart.revision}</div>
                  </div>
                ) : null}
                {scaleInDrain ? (
                  <div className="space-y-1 text-sm text-muted-foreground">
                    <div>Draining: {yesNo(scaleInDrain.draining)}</div>
                    <div>Accepting new sessions: {yesNo(scaleInDrain.accepting_new_sessions)}</div>
                    <div>Gateway sessions: {scaleInDrain.gateway_sessions}</div>
                    <div>Active online: {scaleInDrain.active_online}</div>
                    <div>Closing online: {scaleInDrain.closing_online}</div>
                    <div>Pending activations: {scaleInDrain.pending_activations}</div>
                  </div>
                ) : null}
                {scaleInStatus ? (
                  <div className="space-y-2 text-sm text-muted-foreground">
                    <div>Safe to proceed: {yesNo(scaleInStatus.safe_to_proceed)}</div>
                    <div>Safe to remove: {yesNo(scaleInStatus.safe_to_remove)}</div>
                    <div>Join state: {scaleInStatus.join_state}</div>
                    <div>
                      Slots: replicas {scaleInStatus.slot_replica_count} / leaders {scaleInStatus.slot_leader_count}
                    </div>
                    <div>
                      Tasks: active {scaleInStatus.active_task_count} / failed {scaleInStatus.failed_task_count}
                    </div>
                    <div>
                      Channels: leaders {scaleInStatus.channel_leader_count} / replicas {scaleInStatus.channel_replica_count}
                    </div>
                    <div>Gateway draining: {yesNo(scaleInStatus.gateway_draining)}</div>
                    <div>Accepting new sessions: {yesNo(scaleInStatus.accepting_new_sessions)}</div>
                    {scaleInStatus.blocked_reasons.length > 0 ? (
                      <div>
                        Blockers: {scaleInStatus.blocked_reasons.join(", ")}
                      </div>
                    ) : (
                      <div>Blockers: none</div>
                    )}
                  </div>
                ) : null}
                {scaleInAdvance ? (
                  <div className="space-y-1 text-sm text-muted-foreground">
                    <div>Created tasks: {scaleInAdvance.created} / skipped: {scaleInAdvance.skipped}</div>
                    {scaleInAdvance.candidates.map((candidate) => (
                      <div key={candidate.slot_id}>Slot {candidate.slot_id}: {candidate.target_peers.join(", ")}</div>
                    ))}
                  </div>
                ) : null}
                {scaleInRemove ? (
                  <div className="space-y-1 text-sm text-muted-foreground">
                    <div>Join state: {scaleInRemove.join_state}</div>
                    <div>Removed revision: {scaleInRemove.revision}</div>
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

import { useCallback, useEffect, useMemo, useState } from "react"
import { Copy, RefreshCw, ShieldCheck } from "lucide-react"
import { useIntl } from "react-intl"

import { useAuthStore } from "@/auth/auth-store"
import { ResourceState } from "@/components/manager/resource-state"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"
import { Button } from "@/components/ui/button"
import {
  createMCPToken,
  getMCPAudits,
  getMCPStatus,
  ManagerApiError,
  revokeMCPToken,
  setMCPOwner,
  startMCP,
  stopMCP,
} from "@/lib/manager-api"
import type {
  ManagerMCPAudit,
  ManagerMCPStatusResponse,
} from "@/lib/manager-api.types"

type PageState = {
  status: ManagerMCPStatusResponse | null
  audits: ManagerMCPAudit[]
  loading: boolean
  error: Error | null
}

function mutationKey(operation: string) {
  const suffix = globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random()}`
  return `manager-mcp-${operation}-${suffix}`
}

function hasPermission(
  permissions: { resource: string; actions: string[] }[],
  resource: string,
  action: string,
) {
  return permissions.some((permission) =>
    (permission.resource === resource || permission.resource === "*") &&
    (permission.actions.includes(action) || permission.actions.includes("*")))
}

function errorKind(error: Error | null) {
  if (!(error instanceof ManagerApiError)) return "error" as const
  if (error.status === 403) return "forbidden" as const
  if (error.status === 503) return "unavailable" as const
  return "error" as const
}

function clientConfig(token: string) {
  const endpoint = `${window.location.origin}/mcp`
  return JSON.stringify({
    mcpServers: {
      "wukongim-ops": {
        type: "http",
        url: endpoint,
        headers: { Authorization: `Bearer ${token || "<MCP_TOKEN>"}` },
      },
    },
  }, null, 2)
}

function auditTarget(audit: ManagerMCPAudit) {
  const target = []
  if (audit.recorder_node_id) target.push(`recorded_by=${audit.recorder_node_id}`)
  if (audit.phase) target.push(`phase=${audit.phase}`)
  if (audit.ingress_node_id) target.push(`ingress=${audit.ingress_node_id}`)
  if (audit.owner_node_id) target.push(`owner=${audit.owner_node_id}`)
  if (audit.node_id) target.push(`node=${audit.node_id}`)
  if (audit.slot_id) target.push(`slot=${audit.slot_id}`)
  if (audit.channel_type) target.push(`channel_type=${audit.channel_type}`)
  if (audit.pprof_kind) {
    target.push(`pprof=${audit.pprof_kind}${audit.pprof_seconds ? `/${audit.pprof_seconds}s` : ""}`)
  }
  return target.join(" · ") || "-"
}

async function copyText(value: string) {
  await navigator.clipboard?.writeText(value)
}

export function MCPSettingsPage() {
  const intl = useIntl()
  const permissions = useAuthStore((state) => state.permissions)
  const canWrite = useMemo(() => hasPermission(permissions, "cluster.mcp", "w"), [permissions])
  const [state, setState] = useState<PageState>({
    status: null, audits: [], loading: true, error: null,
  })
  const [ownerNodeID, setOwnerNodeID] = useState(0)
  const [oneTimeToken, setOneTimeToken] = useState("")
  const [mutationError, setMutationError] = useState("")
  const [busy, setBusy] = useState("")

  const load = useCallback(async () => {
    setState((current) => ({ ...current, loading: true, error: null }))
    try {
      const [status, audits] = await Promise.all([
        getMCPStatus(), getMCPAudits(200),
      ])
      setOwnerNodeID(status.owner_node_id)
      setState({ status, audits: audits.items, loading: false, error: null })
    } catch (error) {
      setState((current) => ({
        ...current, loading: false,
        error: error instanceof Error ? error : new Error("MCP request failed"),
      }))
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const mutate = useCallback(async (operation: string, action: () => Promise<void>) => {
    setBusy(operation)
    setMutationError("")
    try {
      await action()
      await load()
    } catch (error) {
      setMutationError(error instanceof Error ? error.message : "MCP mutation failed")
    } finally {
      setBusy("")
    }
  }, [load])

  const status = state.status

  return (
    <PageContainer>
      <PageHeader
        title={intl.formatMessage({ id: "mcp.title" })}
        description={intl.formatMessage({ id: "mcp.description" })}
      />

      {window.location.protocol === "http:" ? (
        <div className="rounded-md border border-amber-500/40 bg-amber-500/10 px-4 py-3 text-sm text-foreground">
          <div className="font-semibold">{intl.formatMessage({ id: "mcp.http.title" })}</div>
          <div className="mt-1 text-muted-foreground">{intl.formatMessage({ id: "mcp.http.description" })}</div>
        </div>
      ) : null}

      {state.loading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "mcp.title" })} /> : null}
      {!state.loading && state.error ? (
        <ResourceState
          kind={errorKind(state.error)}
          onRetry={() => { void load() }}
          title={intl.formatMessage({ id: "mcp.title" })}
        />
      ) : null}

      {!state.loading && !state.error && status ? (
        <>
          <SectionCard
            title={intl.formatMessage({ id: "mcp.status.title" })}
            description={intl.formatMessage({ id: "mcp.status.description" })}
          >
            <div className="grid overflow-hidden rounded-md border border-border md:grid-cols-4">
              {[
                [intl.formatMessage({ id: "mcp.status.desired" }), status.enabled ? intl.formatMessage({ id: "mcp.enabled" }) : intl.formatMessage({ id: "mcp.disabled" })],
                [intl.formatMessage({ id: "mcp.status.observed" }), intl.formatMessage({ id: `mcp.observed.${status.observed_status}` })],
                [intl.formatMessage({ id: "mcp.status.owner" }), status.owner_node_id ? intl.formatMessage({ id: "common.nodeValue" }, { id: status.owner_node_id }) : "-"],
                [intl.formatMessage({ id: "mcp.status.revision" }), String(status.revision)],
              ].map(([label, value]) => (
                <div className="border-b border-border px-3 py-3 text-sm md:border-r md:border-b-0 md:last:border-r-0" key={label}>
                  <div className="text-muted-foreground">{label}</div>
                  <div className="mt-1 font-semibold text-foreground">{value}</div>
                </div>
              ))}
            </div>
            <div className="mt-4 flex flex-wrap gap-2">
              <Button
                disabled={!canWrite || busy !== "" || status.owner_node_id === 0 || status.credentials.length === 0}
                onClick={() => {
                  void mutate(status.enabled ? "stop" : "start", async () => {
                    const input = { expectedRevision: status.revision, idempotencyKey: mutationKey(status.enabled ? "stop" : "start") }
                    if (status.enabled) await stopMCP(input)
                    else await startMCP(input)
                  })
                }}
                variant={status.enabled ? "destructive" : "default"}
              >
                {status.enabled ? intl.formatMessage({ id: "mcp.stop" }) : intl.formatMessage({ id: "mcp.start" })}
              </Button>
              <Button disabled={state.loading || busy !== ""} onClick={() => { void load() }} variant="outline">
                <RefreshCw /> {intl.formatMessage({ id: "common.refresh" })}
              </Button>
            </div>
          </SectionCard>

          <SectionCard
            title={intl.formatMessage({ id: "mcp.owner.title" })}
            description={intl.formatMessage({ id: "mcp.owner.description" })}
          >
            <div className="flex flex-col gap-3 sm:flex-row sm:items-end">
              <label className="flex min-w-64 flex-col gap-1 text-sm font-medium text-foreground">
                {intl.formatMessage({ id: "mcp.owner.label" })}
                <select
                  className="h-9 rounded-md border border-input bg-background px-3 text-sm"
                  disabled={!canWrite || status.enabled || busy !== ""}
                  onChange={(event) => setOwnerNodeID(Number(event.target.value))}
                  value={ownerNodeID}
                >
                  <option value={0}>-</option>
                  {status.owner_candidates.map((node) => (
                    <option key={node.node_id} value={node.node_id}>
                      {`node-${node.node_id}`} · {node.status}
                    </option>
                  ))}
                </select>
              </label>
              <Button
                aria-label={intl.formatMessage({ id: "mcp.owner.save" })}
                disabled={!canWrite || status.enabled || ownerNodeID === 0 || ownerNodeID === status.owner_node_id || busy !== ""}
                onClick={() => {
                  void mutate("owner", async () => {
                    await setMCPOwner({
                      ownerNodeId: ownerNodeID, expectedRevision: status.revision,
                      idempotencyKey: mutationKey("owner"),
                    })
                  })
                }}
              >
                {intl.formatMessage({ id: "mcp.owner.save" })}
              </Button>
            </div>
            {status.enabled ? <p className="mt-3 text-sm text-muted-foreground">{intl.formatMessage({ id: "mcp.owner.stopFirst" })}</p> : null}
          </SectionCard>

          <SectionCard
            title={intl.formatMessage({ id: "mcp.tokens.title" })}
            description={intl.formatMessage({ id: "mcp.tokens.description" })}
          >
            <div className="mb-3 flex flex-wrap items-center gap-2">
              <Button
                disabled={!canWrite || busy !== "" || status.credentials.length >= 2}
                onClick={() => {
                  void mutate("token", async () => {
                    const response = await createMCPToken({
                      expectedRevision: status.revision, idempotencyKey: mutationKey("token"),
                    })
                    setOneTimeToken(response.token)
                  })
                }}
              >
                {intl.formatMessage({ id: "mcp.tokens.generate" })}
              </Button>
              <span className="text-sm text-muted-foreground">
                {intl.formatMessage({ id: "mcp.tokens.count" }, { count: status.credentials.length })}
              </span>
            </div>

            {oneTimeToken ? (
              <div className="mb-4 rounded-md border border-amber-500/40 bg-amber-500/10 p-3">
                <div className="flex items-center gap-2 font-semibold"><ShieldCheck className="size-4" />{intl.formatMessage({ id: "mcp.tokens.once" })}</div>
                <code className="mt-2 block overflow-x-auto rounded bg-background p-3 text-xs">{oneTimeToken}</code>
                <div className="mt-3 flex flex-wrap gap-2">
                  <Button onClick={() => { void copyText(oneTimeToken) }} size="sm" variant="outline">
                    <Copy /> {intl.formatMessage({ id: "mcp.tokens.copy" })}
                  </Button>
                  <Button onClick={() => setOneTimeToken("")} size="sm" variant="ghost">
                    {intl.formatMessage({ id: "common.close" })}
                  </Button>
                </div>
              </div>
            ) : null}

            <div className="overflow-x-auto rounded-md border border-border">
              <table className="w-full text-sm">
                <thead className="bg-muted/40 text-left text-xs uppercase tracking-[0.12em] text-muted-foreground">
                  <tr><th className="px-3 py-2">ID</th><th className="px-3 py-2">{intl.formatMessage({ id: "mcp.tokens.created" })}</th><th className="px-3 py-2">{intl.formatMessage({ id: "mcp.tokens.action" })}</th></tr>
                </thead>
                <tbody>
                  {status.credentials.map((credential) => (
                    <tr className="border-t border-border" key={credential.id}>
                      <td className="px-3 py-2 font-mono text-xs">{credential.id}</td>
                      <td className="px-3 py-2">{new Date(credential.created_at_unix_ms).toLocaleString()}</td>
                      <td className="px-3 py-2">
                        <Button
                          disabled={!canWrite || busy !== "" || (status.enabled && status.credentials.length === 1)}
                          onClick={() => {
                            void mutate("revoke", async () => {
                              await revokeMCPToken({
                                credentialId: credential.id, expectedRevision: status.revision,
                                idempotencyKey: mutationKey("revoke"),
                              })
                            })
                          }}
                          size="sm"
                          variant="destructive"
                        >
                          {intl.formatMessage({ id: "mcp.tokens.revoke" })}
                        </Button>
                      </td>
                    </tr>
                  ))}
                  {status.credentials.length === 0 ? (
                    <tr><td className="px-3 py-6 text-center text-muted-foreground" colSpan={3}>{intl.formatMessage({ id: "mcp.tokens.empty" })}</td></tr>
                  ) : null}
                </tbody>
              </table>
            </div>
          </SectionCard>

          <SectionCard
            title={intl.formatMessage({ id: "mcp.client.title" })}
            description={intl.formatMessage({ id: "mcp.client.description" })}
          >
            <pre className="overflow-x-auto rounded-md border border-border bg-muted/30 p-3 text-xs">{clientConfig(oneTimeToken)}</pre>
            <Button className="mt-3" onClick={() => { void copyText(clientConfig(oneTimeToken)) }} size="sm" variant="outline">
              <Copy /> {intl.formatMessage({ id: "mcp.client.copy" })}
            </Button>
          </SectionCard>

          <SectionCard
            title={intl.formatMessage({ id: "mcp.audits.title" })}
            description={intl.formatMessage({ id: "mcp.audits.description" })}
          >
            <div className="overflow-x-auto rounded-md border border-border">
              <table className="w-full text-sm">
                <thead className="bg-muted/40 text-left text-xs uppercase tracking-[0.12em] text-muted-foreground">
                  <tr>
                    <th className="px-3 py-2">{intl.formatMessage({ id: "mcp.audits.request" })}</th>
                    <th className="px-3 py-2">{intl.formatMessage({ id: "mcp.audits.tool" })}</th>
                    <th className="px-3 py-2">{intl.formatMessage({ id: "mcp.audits.target" })}</th>
                    <th className="px-3 py-2">{intl.formatMessage({ id: "mcp.audits.result" })}</th>
                    <th className="px-3 py-2">{intl.formatMessage({ id: "mcp.audits.duration" })}</th>
                  </tr>
                </thead>
                <tbody>
                  {state.audits.map((audit) => (
                    <tr className="border-t border-border" key={`${audit.request_id}-${audit.started_at}`}>
                      <td className="px-3 py-2 font-mono text-xs">{audit.request_id}</td>
                      <td className="px-3 py-2">{audit.tool || "-"}</td>
                      <td className="px-3 py-2 font-mono text-xs">{auditTarget(audit)}</td>
                      <td className="px-3 py-2">{audit.result}</td>
                      <td className="px-3 py-2">{audit.duration_ms} ms{audit.cache_hit ? " · cache" : ""}</td>
                    </tr>
                  ))}
                  {state.audits.length === 0 ? (
                    <tr><td className="px-3 py-6 text-center text-muted-foreground" colSpan={5}>{intl.formatMessage({ id: "mcp.audits.empty" })}</td></tr>
                  ) : null}
                </tbody>
              </table>
            </div>
          </SectionCard>

          {mutationError ? <div className="rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">{mutationError}</div> : null}
        </>
      ) : null}
    </PageContainer>
  )
}

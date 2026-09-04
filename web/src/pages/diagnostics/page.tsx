import { useCallback, useEffect, useMemo, useState } from "react"
import { useIntl, type IntlShape } from "react-intl"
import { Link } from "react-router-dom"

import { ResourceState } from "@/components/manager/resource-state"
import { Button } from "@/components/ui/button"
import { PageContainer } from "@/components/shell/page-container"
import { SectionCard } from "@/components/shell/section-card"
import {
  ManagerApiError,
  createDiagnosticsTrackingRule,
  deleteDiagnosticsTrackingRule,
  getDiagnosticsEvents,
  getDiagnosticsMessage,
  getDiagnosticsTrace,
  listDiagnosticsTrackingRules,
} from "@/lib/manager-api"
import type { CreateDiagnosticsTrackingRuleInput, ManagerDiagnosticsEvent, ManagerDiagnosticsResponse, ManagerDiagnosticsTrackingRule } from "@/lib/manager-api.types"

type DiagnosticsQueryMode = "trace" | "client_msg_no" | "channel_seq" | "recent_errors"

type DiagnosticsResultFilter = "" | "error" | "timeout" | "partial" | "dropped" | "canceled" | "skipped"

type DiagnosticsQueryForm = {
  mode: DiagnosticsQueryMode
  traceId: string
  clientMsgNo: string
  channelKey: string
  messageSeq: string
  stage: string
  result: DiagnosticsResultFilter
  nodeId: string
  limit: string
}

type TrackingForm = {
  target: "sender_uid" | "channel"
  uid: string
  channelId: string
  channelType: string
  ttlSeconds: string
  customTTLSeconds: string
  sampleRate: string
}

type DiagnosticsState = {
  response: ManagerDiagnosticsResponse | null
  loading: boolean
  refreshing: boolean
  error: Error | null
  queried: boolean
}

type CommonParams = { nodeId?: number; limit: number }

const defaultTrackingForm: TrackingForm = {
  target: "sender_uid",
  uid: "",
  channelId: "",
  channelType: "",
  ttlSeconds: "3600",
  customTTLSeconds: "",
  sampleRate: "1",
}

const defaultForm: DiagnosticsQueryForm = {
  mode: "trace",
  traceId: "",
  clientMsgNo: "",
  channelKey: "",
  messageSeq: "",
  stage: "",
  result: "error",
  nodeId: "",
  limit: "100",
}

const resultOptions: DiagnosticsResultFilter[] = ["", "error", "timeout", "partial", "dropped", "canceled", "skipped"]
const failureResults = new Set(["error", "timeout", "partial", "dropped", "canceled", "skipped"])
const diagnosticsTrackingNotConfiguredMarker = "diagnostics tracking not configured"

function positiveInteger(value: string) {
  if (!/^\d+$/.test(value.trim())) return null
  const parsed = Number(value.trim())
  return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : null
}

function validateForm(form: DiagnosticsQueryForm, intl: IntlShape) {
  const errors: string[] = []
  const params: CommonParams = { limit: 100 }

  if (form.mode === "trace" && !form.traceId.trim()) {
    errors.push(intl.formatMessage({ id: "diagnostics.validation.traceId" }))
  }
  if (form.mode === "client_msg_no" && !form.clientMsgNo.trim()) {
    errors.push(intl.formatMessage({ id: "diagnostics.validation.clientMsgNo" }))
  }
  if (form.mode === "channel_seq") {
    if (!form.channelKey.trim()) {
      errors.push(intl.formatMessage({ id: "diagnostics.validation.channelKey" }))
    }
    if (positiveInteger(form.messageSeq) === null) {
      errors.push(intl.formatMessage({ id: "diagnostics.validation.messageSeq" }))
    }
  }

  if (form.nodeId.trim()) {
    const nodeId = positiveInteger(form.nodeId)
    if (nodeId === null) {
      errors.push(intl.formatMessage({ id: "diagnostics.validation.nodeId" }))
    } else {
      params.nodeId = nodeId
    }
  }

  if (form.limit.trim()) {
    const limit = positiveInteger(form.limit)
    if (limit === null || limit < 1 || limit > 500) {
      errors.push(intl.formatMessage({ id: "diagnostics.validation.limit" }))
    } else {
      params.limit = limit
    }
  }

  return { errors, params }
}

function validateTrackingForm(form: TrackingForm, intl: IntlShape): { errors: string[]; input: CreateDiagnosticsTrackingRuleInput | null } {
  const errors: string[] = []
  const ttlSeconds = form.ttlSeconds === "custom" ? positiveInteger(form.customTTLSeconds) : positiveInteger(form.ttlSeconds)
  const sampleRate = Number(form.sampleRate.trim() || "1")

  if (ttlSeconds === null) {
    errors.push(intl.formatMessage({ id: "diagnostics.validation.ttl" }))
  }
  if (!Number.isFinite(sampleRate) || sampleRate < 0 || sampleRate > 1) {
    errors.push(intl.formatMessage({ id: "diagnostics.validation.sampleRate" }))
  }

  if (form.target === "sender_uid") {
    const uid = form.uid.trim()
    if (!uid) {
      errors.push(intl.formatMessage({ id: "diagnostics.validation.senderUid" }))
    }
    return {
      errors,
      input: errors.length === 0 && ttlSeconds !== null ? { target: "sender_uid", uid, ttlSeconds, sampleRate } : null,
    }
  }

  const channelId = form.channelId.trim()
  const channelType = positiveInteger(form.channelType)
  if (!channelId) {
    errors.push(intl.formatMessage({ id: "diagnostics.validation.channelId" }))
  }
  if (channelType === null) {
    errors.push(intl.formatMessage({ id: "diagnostics.validation.channelType" }))
  }
  return {
    errors,
    input: errors.length === 0 && ttlSeconds !== null && channelType !== null
      ? { target: "channel", channelId, channelType, ttlSeconds, sampleRate }
      : null,
  }
}

function timestamp(intl: IntlShape, value: string) {
  const date = new Date(value)
  if (!value || Number.isNaN(date.getTime())) return "-"
  return new Intl.DateTimeFormat(intl.locale, {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).format(date)
}

function mapErrorKind(error: Error | null) {
  if (!(error instanceof ManagerApiError)) return "error" as const
  if (error.status === 403) return "forbidden" as const
  if (error.status === 503) return "unavailable" as const
  return "error" as const
}

function statusTone(status: string) {
  if (status === "ok") return "border-emerald-500/30 bg-emerald-500/10 text-emerald-700"
  if (status === "partial" || status === "timeout") return "border-amber-500/30 bg-amber-500/10 text-amber-700"
  if (status === "not_found") return "border-slate-400/30 bg-slate-500/10 text-slate-700"
  return "border-destructive/30 bg-destructive/10 text-destructive"
}

function diagnosticsStatusLabel(intl: IntlShape, value: string) {
  const known = new Set(["ok", "error", "timeout", "partial", "not_found", "unavailable", "skipped", "dropped", "canceled"])
  if (!known.has(value)) return value
  const suffix = value === "not_found" ? "notFound" : value
  return intl.formatMessage({ id: `diagnostics.status.${suffix}` })
}

function StatusPill({ intl, value }: { intl: IntlShape; value: string }) {
  return <span className={`inline-flex whitespace-nowrap rounded-full border px-2 py-0.5 text-xs font-semibold ${statusTone(value)}`}>{diagnosticsStatusLabel(intl, value)}</span>
}

function trackingRuleLabel(rule: ManagerDiagnosticsTrackingRule, intl: IntlShape) {
  if (rule.target === "sender_uid") {
    return `${intl.formatMessage({ id: "diagnostics.tracking.target.senderUid" })}: ${rule.uid ?? "-"}`
  }
  return `${intl.formatMessage({ id: "diagnostics.tracking.target.channel" })}: ${rule.channel_key ?? rule.channel_id ?? "-"}`
}

function upsertTrackingRule(rules: ManagerDiagnosticsTrackingRule[], next: ManagerDiagnosticsTrackingRule) {
  const existing = rules.filter((rule) => rule.rule_id !== next.rule_id)
  return [next, ...existing]
}

function trackingResponseNotice(intl: IntlShape, status: string, notes: string[], nodes: { node_id: number; status: string; notes: string[] }[]) {
  const nodeNotes = nodes.flatMap((node) => node.notes.map((note) => `${intl.formatMessage({ id: "diagnostics.nodes.node" }, { id: node.node_id })}: ${note}`))
  const details = [...notes, ...nodeNotes]
  const statusLabel = diagnosticsStatusLabel(intl, status)
  if (details.length > 0) {
    return `${statusLabel}: ${details.join("; ")}`
  }
  return status === "partial" || status === "error" ? statusLabel : ""
}

function diagnosticsTrackingSetupNodes(notes: string[], nodes: { node_id: number; notes: string[] }[]) {
  const isNotConfigured = (note: string) => note.toLowerCase().includes(diagnosticsTrackingNotConfiguredMarker)
  const setupRequired = notes.some(isNotConfigured) || nodes.some((node) => node.notes.some(isNotConfigured))
  if (!setupRequired) return null

  return nodes
    .filter((node) => node.notes.some(isNotConfigured))
    .map((node) => node.node_id)
    .sort((left, right) => left - right)
}

function HeaderBadge({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-lg border border-border bg-background px-3 py-2">
      <div className="text-[11px] uppercase tracking-[0.16em] text-muted-foreground">{label}</div>
      <div className="mt-1 text-sm font-semibold text-foreground">{value}</div>
    </div>
  )
}

function SummaryCard({ label, value, detail }: { label: string; value: string; detail?: string }) {
  return (
    <div className="rounded-xl border border-border bg-muted/25 p-4">
      <div className="text-xs uppercase tracking-[0.16em] text-muted-foreground">{label}</div>
      <div className="mt-2 text-xl font-semibold text-foreground">{value}</div>
      {detail ? <div className="mt-1 text-xs text-muted-foreground">{detail}</div> : null}
    </div>
  )
}

function timelineContext(event: ManagerDiagnosticsEvent, intl: IntlShape) {
  const parts: string[] = []
  if (event.node_id) parts.push(intl.formatMessage({ id: "diagnostics.context.node" }, { id: event.node_id }))
  if (event.peer_node_id) parts.push(intl.formatMessage({ id: "diagnostics.context.peer" }, { id: event.peer_node_id }))
  if (event.slot_id) parts.push(intl.formatMessage({ id: "diagnostics.context.slot" }, { id: event.slot_id }))
  if (event.message_seq) parts.push(intl.formatMessage({ id: "diagnostics.context.seq" }, { id: event.message_seq }))
  return parts.length > 0 ? parts.join(" · ") : intl.formatMessage({ id: "diagnostics.context.cluster" })
}

function timelineDetails(event: ManagerDiagnosticsEvent, intl: IntlShape) {
  const details: string[] = []
  if (event.channel_key) details.push(event.channel_key)
  if (event.service) details.push(event.service)
  if (event.attempt) details.push(intl.formatMessage({ id: "diagnostics.context.attempt" }, { value: event.attempt }))
  if (event.queue_depth) details.push(intl.formatMessage({ id: "diagnostics.context.queue" }, { value: event.queue_depth }))
  return details
}

function parseChannelKey(channelKey: string | undefined) {
  if (!channelKey) return null
  const match = /^(\d+):([^\s/?#]+)$/.exec(channelKey)
  if (!match) return null
  return { channelType: match[1], channelId: match[2] }
}

function RelatedLinks({ response }: { response: ManagerDiagnosticsResponse }) {
  const intl = useIntl()
  const links = new Map<string, { href: string; label: string }>()

  for (const event of response.events) {
    if (event.slot_id && event.node_id) {
      links.set(`slot-${event.slot_id}-${event.node_id}`, {
        href: `/cluster/slots?tab=logs&slot_id=${event.slot_id}&node_id=${event.node_id}`,
        label: intl.formatMessage({ id: "diagnostics.related.slotLogs" }, { slot: event.slot_id, node: event.node_id }),
      })
    }
    if (event.node_id) {
      links.set(`conn-${event.node_id}`, {
        href: `/business/connections?node_id=${event.node_id}`,
        label: intl.formatMessage({ id: "diagnostics.related.connections" }, { node: event.node_id }),
      })
      links.set("nodes", { href: "/cluster/nodes", label: intl.formatMessage({ id: "diagnostics.related.nodes" }) })
    }
    const parsed = parseChannelKey(event.channel_key)
    if (parsed) {
      links.set(`channel-${event.channel_key}`, {
        href: `/business/messages?channel_id=${encodeURIComponent(parsed.channelId)}&channel_type=${parsed.channelType}`,
        label: intl.formatMessage({ id: "diagnostics.related.messages" }, { channel: event.channel_key }),
      })
    }
  }

  const parsedSummary = parseChannelKey(response.summary.channel_key)
  if (parsedSummary && response.summary.channel_key) {
    links.set(`summary-channel-${response.summary.channel_key}`, {
      href: `/messages?channel_id=${encodeURIComponent(parsedSummary.channelId)}&channel_type=${parsedSummary.channelType}`,
      label: intl.formatMessage({ id: "diagnostics.related.messages" }, { channel: response.summary.channel_key }),
    })
  }

  return (
    <SectionCard title={intl.formatMessage({ id: "diagnostics.related.title" })} description={intl.formatMessage({ id: "diagnostics.related.description" })}>
      <div className="flex flex-wrap gap-2">
        {Array.from(links.values()).map((link) => (
          <Link key={link.href} className="rounded-md border border-border px-3 py-2 text-sm font-medium text-foreground hover:bg-muted" to={link.href}>
            {link.label}
          </Link>
        ))}
        {response.summary.channel_key && !parseChannelKey(response.summary.channel_key) ? (
          <span className="rounded-md border border-dashed border-border px-3 py-2 text-sm text-muted-foreground">
            {intl.formatMessage({ id: "diagnostics.related.channelKey" }, { channel: response.summary.channel_key })}
          </span>
        ) : null}
        {links.size === 0 && !response.summary.channel_key ? <span className="text-sm text-muted-foreground">{intl.formatMessage({ id: "diagnostics.related.empty" })}</span> : null}
      </div>
    </SectionCard>
  )
}

export function DiagnosticsTracePanel() {
  const intl = useIntl()
  const [form, setForm] = useState<DiagnosticsQueryForm>(defaultForm)
  const [trackingForm, setTrackingForm] = useState<TrackingForm>(defaultTrackingForm)
  const [trackingRules, setTrackingRules] = useState<ManagerDiagnosticsTrackingRule[]>([])
  const [trackingErrors, setTrackingErrors] = useState<string[]>([])
  const [trackingNotice, setTrackingNotice] = useState("")
  const [trackingSetupNodeIds, setTrackingSetupNodeIds] = useState<number[] | null>(null)
  const [trackingLoading, setTrackingLoading] = useState(false)
  const [validationErrors, setValidationErrors] = useState<string[]>([])
  const [exported, setExported] = useState(false)
  const [state, setState] = useState<DiagnosticsState>({
    response: null,
    loading: false,
    refreshing: false,
    error: null,
    queried: false,
  })

  const events = useMemo(() => [...(state.response?.events ?? [])].sort((a, b) => a.at.localeCompare(b.at)), [state.response])

  const refreshTrackingRules = useCallback(async () => {
    setTrackingLoading(true)
    try {
      const response = await listDiagnosticsTrackingRules()
      const setupNodeIds = diagnosticsTrackingSetupNodes(response.notes, response.nodes)
      setTrackingRules(response.rules)
      setTrackingSetupNodeIds(setupNodeIds)
      setTrackingNotice(setupNodeIds === null ? trackingResponseNotice(intl, response.status, response.notes, response.nodes) : "")
      setTrackingErrors([])
    } catch (error) {
      const message = error instanceof Error ? error.message : intl.formatMessage({ id: "diagnostics.error.loadTracking" })
      const setupRequired = message.toLowerCase().includes(diagnosticsTrackingNotConfiguredMarker)
      setTrackingSetupNodeIds(setupRequired ? [] : null)
      setTrackingErrors(setupRequired ? [] : [message])
    } finally {
      setTrackingLoading(false)
    }
  }, [intl])

  useEffect(() => {
    void refreshTrackingRules()
  }, [refreshTrackingRules])

  const submit = useCallback(async () => {
    const { errors, params } = validateForm(form, intl)
    setValidationErrors(errors)
    setExported(false)
    if (errors.length > 0) return

    setState((current) => ({ ...current, loading: !current.response, refreshing: Boolean(current.response), error: null, queried: true }))
    try {
      let response: ManagerDiagnosticsResponse
      if (form.mode === "trace") {
        response = await getDiagnosticsTrace(form.traceId.trim(), params)
      } else if (form.mode === "client_msg_no") {
        response = await getDiagnosticsMessage({ clientMsgNo: form.clientMsgNo.trim(), ...params })
      } else if (form.mode === "channel_seq") {
        response = await getDiagnosticsMessage({ channelKey: form.channelKey.trim(), messageSeq: positiveInteger(form.messageSeq) ?? 0, ...params })
      } else {
        response = await getDiagnosticsEvents({
          stage: form.stage.trim() || undefined,
          result: form.result || undefined,
          ...params,
        })
      }
      setState({ response, loading: false, refreshing: false, error: null, queried: true })
    } catch (error) {
      setState({ response: null, loading: false, refreshing: false, error: error instanceof Error ? error : new Error(intl.formatMessage({ id: "diagnostics.error.query" })), queried: true })
    }
  }, [form, intl])

  const exportJSON = useCallback(async () => {
    if (!state.response) return
    const payload = JSON.stringify(state.response, null, 2)
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(payload)
      setExported(true)
    }
  }, [state.response])

  const createTrackingRule = useCallback(async () => {
    const { errors, input } = validateTrackingForm(trackingForm, intl)
    setTrackingErrors(errors)
    if (!input) return

    setTrackingLoading(true)
    try {
      const response = await createDiagnosticsTrackingRule(input)
      setTrackingRules((current) => upsertTrackingRule(current, response.rule))
      setTrackingNotice(trackingResponseNotice(intl, response.status, response.notes, response.nodes))
      setTrackingErrors([])
    } catch (error) {
      setTrackingErrors([error instanceof Error ? error.message : intl.formatMessage({ id: "diagnostics.error.createTracking" })])
    } finally {
      setTrackingLoading(false)
    }
  }, [intl, trackingForm])

  const queryTrackingRule = useCallback(async (rule: ManagerDiagnosticsTrackingRule) => {
    const limit = positiveInteger(form.limit) ?? 100
    setState((current) => ({ ...current, loading: !current.response, refreshing: Boolean(current.response), error: null, queried: true }))
    try {
      const response = rule.target === "sender_uid" && rule.uid
        ? await getDiagnosticsEvents({ uid: rule.uid, limit })
        : await getDiagnosticsEvents({ channelKey: rule.channel_key, limit })
      setState({ response, loading: false, refreshing: false, error: null, queried: true })
    } catch (error) {
      setState({ response: null, loading: false, refreshing: false, error: error instanceof Error ? error : new Error(intl.formatMessage({ id: "diagnostics.error.query" })), queried: true })
    }
  }, [form.limit, intl])

  const stopTrackingRule = useCallback(async (ruleId: string) => {
    setTrackingLoading(true)
    try {
      const response = await deleteDiagnosticsTrackingRule(ruleId)
      setTrackingNotice(trackingResponseNotice(intl, response.status, response.notes, response.nodes))
      setTrackingErrors([])
      await refreshTrackingRules()
    } catch (error) {
      setTrackingErrors([error instanceof Error ? error.message : intl.formatMessage({ id: "diagnostics.error.deleteTracking" })])
    } finally {
      setTrackingLoading(false)
    }
  }, [intl, refreshTrackingRules])

  const response = state.response
  const partialNodes = response?.nodes.filter((node) => node.status === "unavailable" || node.status === "skipped") ?? []
  const errorKind = mapErrorKind(state.error)

  return (
    <>
      <section className="rounded-lg border border-border bg-card p-5">
        <div className="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
          <div className="space-y-2">
            <h2 className="text-xl font-semibold tracking-tight text-foreground">
              {intl.formatMessage({ id: "diagnostics.title" })}
            </h2>
            <p className="max-w-3xl text-sm leading-6 text-muted-foreground">
              {intl.formatMessage({ id: "diagnostics.description" })}
            </p>
          </div>
          {response ? <Button onClick={exportJSON} size="sm" variant="outline">{intl.formatMessage({ id: "diagnostics.export" })}</Button> : null}
        </div>
        <div className="mt-4 grid gap-3 sm:grid-cols-3">
          <HeaderBadge label={intl.formatMessage({ id: "diagnostics.header.scope" })} value={response?.scope === "cluster" || !response ? intl.formatMessage({ id: "diagnostics.scope.cluster" }) : response.scope} />
          <HeaderBadge label={intl.formatMessage({ id: "diagnostics.header.generated" })} value={response ? timestamp(intl, response.generated_at) : intl.formatMessage({ id: "diagnostics.pending" })} />
          <HeaderBadge label={intl.formatMessage({ id: "diagnostics.header.events" })} value={intl.formatMessage({ id: "diagnostics.eventCount" }, { count: response?.summary.event_count ?? 0 })} />
        </div>
      </section>

      <SectionCard title={intl.formatMessage({ id: "diagnostics.tracking.title" })} description={intl.formatMessage({ id: "diagnostics.tracking.description" })}>
        <div className="rounded-lg border border-amber-500/30 bg-amber-500/10 p-3 text-sm text-amber-800">
          {intl.formatMessage({ id: "diagnostics.tracking.warning" })}
        </div>
        {trackingSetupNodeIds !== null ? (
          <div className="mt-3 rounded-lg border border-amber-500/30 bg-amber-500/10 p-4 text-sm text-amber-900" role="status">
            <div className="font-semibold">{intl.formatMessage({ id: "diagnostics.tracking.setupRequired.title" })}</div>
            <p className="mt-1 leading-6">{intl.formatMessage({ id: "diagnostics.tracking.setupRequired.description" })}</p>
            {trackingSetupNodeIds.length > 0 ? (
              <p className="mt-1">{intl.formatMessage({ id: "diagnostics.tracking.setupRequired.nodes" }, { nodes: trackingSetupNodeIds.join(", ") })}</p>
            ) : null}
            <div className="mt-3 grid gap-3 lg:grid-cols-2">
              <div>
                <div className="text-xs font-semibold uppercase tracking-wide">{intl.formatMessage({ id: "diagnostics.tracking.setupRequired.toml" })}</div>
                <pre className="mt-1 overflow-x-auto rounded-md bg-background/80 p-3 font-mono text-xs text-foreground">{"[diagnostics]\nenable = true"}</pre>
              </div>
              <div>
                <div className="text-xs font-semibold uppercase tracking-wide">{intl.formatMessage({ id: "diagnostics.tracking.setupRequired.environment" })}</div>
                <pre className="mt-1 overflow-x-auto rounded-md bg-background/80 p-3 font-mono text-xs text-foreground">WK_DIAGNOSTICS_ENABLE=true</pre>
              </div>
            </div>
          </div>
        ) : null}
        <div className="mt-4 grid gap-3 md:grid-cols-5">
          <label className="flex flex-col gap-1 text-sm font-medium text-foreground">
            {intl.formatMessage({ id: "diagnostics.tracking.target" })}
            <select
              className="rounded-md border border-input bg-background px-3 py-2 text-sm"
              value={trackingForm.target}
              onChange={(event) => setTrackingForm((current) => ({ ...current, target: event.target.value as TrackingForm["target"] }))}
            >
              <option value="sender_uid">{intl.formatMessage({ id: "diagnostics.tracking.target.senderUid" })}</option>
              <option value="channel">{intl.formatMessage({ id: "diagnostics.tracking.target.channel" })}</option>
            </select>
          </label>
          {trackingForm.target === "sender_uid" ? (
            <TextInput label={intl.formatMessage({ id: "diagnostics.tracking.senderUid" })} value={trackingForm.uid} onChange={(uid) => setTrackingForm((current) => ({ ...current, uid }))} />
          ) : (
            <>
              <TextInput label={intl.formatMessage({ id: "diagnostics.tracking.channelId" })} value={trackingForm.channelId} onChange={(channelId) => setTrackingForm((current) => ({ ...current, channelId }))} />
              <TextInput label={intl.formatMessage({ id: "diagnostics.tracking.channelType" })} value={trackingForm.channelType} onChange={(channelType) => setTrackingForm((current) => ({ ...current, channelType }))} />
            </>
          )}
          <label className="flex flex-col gap-1 text-sm font-medium text-foreground">
            {intl.formatMessage({ id: "diagnostics.tracking.ttl" })}
            <select
              className="rounded-md border border-input bg-background px-3 py-2 text-sm"
              value={trackingForm.ttlSeconds}
              onChange={(event) => setTrackingForm((current) => ({ ...current, ttlSeconds: event.target.value }))}
            >
              <option value="600">{intl.formatMessage({ id: "diagnostics.tracking.ttl.10m" })}</option>
              <option value="3600">{intl.formatMessage({ id: "diagnostics.tracking.ttl.1h" })}</option>
              <option value="21600">{intl.formatMessage({ id: "diagnostics.tracking.ttl.6h" })}</option>
              <option value="custom">{intl.formatMessage({ id: "diagnostics.tracking.ttl.custom" })}</option>
            </select>
          </label>
          {trackingForm.ttlSeconds === "custom" ? (
            <TextInput label={intl.formatMessage({ id: "diagnostics.tracking.customTTL" })} value={trackingForm.customTTLSeconds} onChange={(customTTLSeconds) => setTrackingForm((current) => ({ ...current, customTTLSeconds }))} />
          ) : null}
          <TextInput label={intl.formatMessage({ id: "diagnostics.tracking.sampleRate" })} value={trackingForm.sampleRate} onChange={(sampleRate) => setTrackingForm((current) => ({ ...current, sampleRate }))} />
        </div>
        {trackingErrors.length > 0 ? (
          <div className="mt-3 rounded-lg border border-destructive/30 bg-destructive/10 p-3 text-sm text-destructive" role="alert">
            {trackingErrors.map((error) => <div key={error}>{error}</div>)}
          </div>
        ) : null}
        {trackingNotice ? <div className="mt-3 text-sm text-muted-foreground">{trackingNotice}</div> : null}
        <Button className="mt-4" disabled={trackingLoading || trackingSetupNodeIds !== null} onClick={() => void createTrackingRule()}>
          {trackingLoading ? intl.formatMessage({ id: "diagnostics.tracking.working" }) : intl.formatMessage({ id: "diagnostics.tracking.start" })}
        </Button>
        <div className="mt-4 grid gap-3">
          {trackingRules.map((rule) => (
            <div key={rule.rule_id} className="rounded-lg border border-border bg-muted/20 p-3">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <div>
                  <div className="font-medium text-foreground">{trackingRuleLabel(rule, intl)}</div>
                  {rule.target === "sender_uid" && rule.uid ? <div className="text-sm text-foreground">{rule.uid}</div> : null}
                  {rule.target === "channel" && rule.channel_key ? <div className="text-sm text-foreground">{rule.channel_key}</div> : null}
                  <div className="mt-1 text-xs text-muted-foreground">
                    {intl.formatMessage(
                      { id: "diagnostics.tracking.ruleMeta" },
                      { id: rule.rule_id, sample: rule.sample_rate, expires: rule.expires_at ? timestamp(intl, rule.expires_at) : "-" },
                    )}
                  </div>
                </div>
                <div className="flex gap-2">
                  <Button size="sm" variant="outline" onClick={() => void queryTrackingRule(rule)}>
                    {intl.formatMessage({ id: "diagnostics.tracking.query" })}
                  </Button>
                  <Button size="sm" variant="outline" onClick={() => void stopTrackingRule(rule.rule_id)}>
                    {intl.formatMessage({ id: "diagnostics.tracking.stop" })}
                  </Button>
                </div>
              </div>
            </div>
          ))}
          {trackingRules.length === 0 && trackingSetupNodeIds === null ? <div className="text-sm text-muted-foreground">{intl.formatMessage({ id: "diagnostics.tracking.empty" })}</div> : null}
        </div>
      </SectionCard>

      <SectionCard title={intl.formatMessage({ id: "diagnostics.query.title" })} description={intl.formatMessage({ id: "diagnostics.query.description" })}>
        <div className="grid gap-3 md:grid-cols-4">
          <label className="flex flex-col gap-1 text-sm font-medium text-foreground">
            {intl.formatMessage({ id: "diagnostics.query.mode" })}
            <select
              className="rounded-md border border-input bg-background px-3 py-2 text-sm"
              value={form.mode}
              onChange={(event) => setForm((current) => ({ ...current, mode: event.target.value as DiagnosticsQueryMode }))}
            >
              <option value="trace">{intl.formatMessage({ id: "diagnostics.query.trace" })}</option>
              <option value="client_msg_no">{intl.formatMessage({ id: "diagnostics.query.clientMsgNo" })}</option>
              <option value="channel_seq">{intl.formatMessage({ id: "diagnostics.query.channelSeq" })}</option>
              <option value="recent_errors">{intl.formatMessage({ id: "diagnostics.query.recentErrors" })}</option>
            </select>
          </label>

          {form.mode === "trace" ? <TextInput label={intl.formatMessage({ id: "diagnostics.query.traceId" })} value={form.traceId} onChange={(traceId) => setForm((current) => ({ ...current, traceId }))} /> : null}
          {form.mode === "client_msg_no" ? <TextInput label={intl.formatMessage({ id: "diagnostics.query.clientMsgNoLabel" })} value={form.clientMsgNo} onChange={(clientMsgNo) => setForm((current) => ({ ...current, clientMsgNo }))} /> : null}
          {form.mode === "channel_seq" ? (
            <>
              <TextInput label={intl.formatMessage({ id: "diagnostics.query.channelKey" })} value={form.channelKey} onChange={(channelKey) => setForm((current) => ({ ...current, channelKey }))} />
              <TextInput label={intl.formatMessage({ id: "diagnostics.query.messageSeq" })} value={form.messageSeq} onChange={(messageSeq) => setForm((current) => ({ ...current, messageSeq }))} />
            </>
          ) : null}
          {form.mode === "recent_errors" ? (
            <>
              <TextInput label={intl.formatMessage({ id: "diagnostics.query.stage" })} value={form.stage} onChange={(stage) => setForm((current) => ({ ...current, stage }))} />
              <label className="flex flex-col gap-1 text-sm font-medium text-foreground">
                {intl.formatMessage({ id: "diagnostics.query.result" })}
                <select
                  className="rounded-md border border-input bg-background px-3 py-2 text-sm"
                  value={form.result}
                  onChange={(event) => setForm((current) => ({ ...current, result: event.target.value as DiagnosticsResultFilter }))}
                >
                  {resultOptions.map((result) => <option key={result || "all"} value={result}>{result ? diagnosticsStatusLabel(intl, result) : intl.formatMessage({ id: "diagnostics.query.allResults" })}</option>)}
                </select>
              </label>
            </>
          ) : null}
          <TextInput label={intl.formatMessage({ id: "diagnostics.query.nodeId" })} value={form.nodeId} onChange={(nodeId) => setForm((current) => ({ ...current, nodeId }))} />
          <TextInput label={intl.formatMessage({ id: "diagnostics.query.limit" })} value={form.limit} onChange={(limit) => setForm((current) => ({ ...current, limit }))} />
        </div>
        {validationErrors.length > 0 ? (
          <div className="mt-3 rounded-lg border border-destructive/30 bg-destructive/10 p-3 text-sm text-destructive" role="alert">
            {validationErrors.map((error) => <div key={error}>{error}</div>)}
          </div>
        ) : null}
        <Button className="mt-4" disabled={state.loading || state.refreshing} onClick={() => void submit()}>
          {state.loading || state.refreshing ? intl.formatMessage({ id: "diagnostics.query.running" }) : intl.formatMessage({ id: "diagnostics.query.run" })}
        </Button>
      </SectionCard>

      {state.loading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "diagnostics.title" })} /> : null}
      {!state.loading && state.error ? (
        <ResourceState
          kind={errorKind}
          title={errorKind === "forbidden" ? intl.formatMessage({ id: "diagnostics.permission.title" }) : intl.formatMessage({ id: "diagnostics.title" })}
          description={errorKind === "forbidden" ? intl.formatMessage({ id: "diagnostics.permission.description" }) : undefined}
          onRetry={() => void submit()}
        />
      ) : null}

      {!state.loading && !state.error && response?.status === "not_found" ? (
        <ResourceState
          kind="empty"
          title={intl.formatMessage({ id: "diagnostics.notFound.title" })}
          description={intl.formatMessage({ id: "diagnostics.notFound.description" })}
        />
      ) : null}

      {response ? (
        <>
          {response.status === "partial" ? (
            <div className="rounded-xl border border-amber-500/30 bg-amber-500/10 p-4 text-sm text-amber-800" role="status">
              <div className="font-semibold">{intl.formatMessage({ id: "diagnostics.partial.title" })}</div>
              <div className="mt-1">{intl.formatMessage({ id: "diagnostics.partial.description" })}</div>
            </div>
          ) : null}

          <section className="grid gap-3 md:grid-cols-4">
            <SummaryCard label={intl.formatMessage({ id: "diagnostics.summary.status" })} value={diagnosticsStatusLabel(intl, response.status)} detail={response.summary.first_failure_error_code} />
            <SummaryCard label={intl.formatMessage({ id: "diagnostics.summary.firstFailure" })} value={response.summary.first_failure_stage ?? "-"} detail={response.summary.first_failure_result ? diagnosticsStatusLabel(intl, response.summary.first_failure_result) : undefined} />
            <SummaryCard label={intl.formatMessage({ id: "diagnostics.summary.slowestStage" })} value={response.summary.slowest_stage ?? "-"} detail={response.summary.slowest_duration_ms ? `${response.summary.slowest_duration_ms} ms` : undefined} />
            <SummaryCard label={intl.formatMessage({ id: "diagnostics.summary.involvedNodes" })} value={response.summary.involved_nodes.length ? response.summary.involved_nodes.join(", ") : "-"} detail={intl.formatMessage({ id: "diagnostics.eventCount" }, { count: response.summary.event_count })} />
          </section>

          <SectionCard title={intl.formatMessage({ id: "diagnostics.timeline.title" })} description={intl.formatMessage({ id: "diagnostics.timeline.description" })}>
            {events.length > 0 ? (
              <ol aria-label={intl.formatMessage({ id: "diagnostics.timeline.aria" })} className="overflow-hidden rounded-lg border border-border bg-background">
                {events.map((event, index) => (
                  <TimelineEventItem
                    key={`${event.at}-${event.stage}-${index}`}
                    event={event}
                    intl={intl}
                    isLast={index === events.length - 1}
                  />
                ))}
              </ol>
            ) : <div className="text-sm text-muted-foreground">{intl.formatMessage({ id: "diagnostics.timeline.empty" })}</div>}
          </SectionCard>

          <SectionCard title={intl.formatMessage({ id: "diagnostics.nodes.title" })} description={intl.formatMessage({ id: "diagnostics.nodes.description" })}>
            <div className="grid gap-3 md:grid-cols-2">
              {response.nodes.map((node) => (
                <div key={node.node_id} className="rounded-lg border border-border bg-muted/20 p-3">
                  <div className="flex items-center justify-between gap-2">
                    <span className="font-medium text-foreground">{intl.formatMessage({ id: "diagnostics.nodes.node" }, { id: node.node_id })}</span>
                    <StatusPill intl={intl} value={node.status} />
                  </div>
                  <div className="mt-2 text-sm text-muted-foreground">{intl.formatMessage({ id: "diagnostics.eventCount" }, { count: node.event_count })} · {node.duration_ms} ms</div>
                  {node.notes.map((note) => <div key={note} className="mt-1 text-sm text-amber-700">{note}</div>)}
                </div>
              ))}
              {response.nodes.length === 0 ? <div className="text-sm text-muted-foreground">{intl.formatMessage({ id: "diagnostics.nodes.empty" })}</div> : null}
            </div>
            {partialNodes.length > 0 ? <div className="mt-3 text-sm text-amber-700">{intl.formatMessage({ id: "diagnostics.nodes.partial" }, { nodes: partialNodes.map((node) => node.node_id).join(", ") })}</div> : null}
            {response.notes.map((note) => <div key={note} className="mt-2 text-sm text-muted-foreground">{note}</div>)}
          </SectionCard>

          <SectionCard title={intl.formatMessage({ id: "diagnostics.events.title" })} description={intl.formatMessage({ id: "diagnostics.events.description" })}>
            <div className="overflow-x-auto rounded-lg border border-border">
              <table aria-label={intl.formatMessage({ id: "diagnostics.events.title" })} className="w-full min-w-[72rem] border-collapse text-sm">
                <thead className="bg-muted/40 text-left text-xs uppercase tracking-[0.14em] text-muted-foreground">
                  <tr>
                    <th className="whitespace-nowrap px-3 py-3">{intl.formatMessage({ id: "diagnostics.events.stage" })}</th>
                    <th className="whitespace-nowrap px-3 py-3">{intl.formatMessage({ id: "diagnostics.events.result" })}</th>
                    <th className="whitespace-nowrap px-3 py-3">{intl.formatMessage({ id: "diagnostics.events.duration" })}</th>
                    <th className="whitespace-nowrap px-3 py-3">{intl.formatMessage({ id: "diagnostics.events.node" })}</th>
                    <th className="whitespace-nowrap px-3 py-3">{intl.formatMessage({ id: "diagnostics.events.peer" })}</th>
                    <th className="whitespace-nowrap px-3 py-3">{intl.formatMessage({ id: "diagnostics.events.slot" })}</th>
                    <th className="whitespace-nowrap px-3 py-3">{intl.formatMessage({ id: "diagnostics.events.channelKey" })}</th>
                    <th className="whitespace-nowrap px-3 py-3">{intl.formatMessage({ id: "diagnostics.events.messageSeq" })}</th>
                    <th className="whitespace-nowrap px-3 py-3">{intl.formatMessage({ id: "diagnostics.events.errorCode" })}</th>
                    <th className="whitespace-nowrap px-3 py-3">{intl.formatMessage({ id: "diagnostics.events.service" })}</th>
                    <th className="whitespace-nowrap px-3 py-3">{intl.formatMessage({ id: "diagnostics.events.attempt" })}</th>
                    <th className="whitespace-nowrap px-3 py-3">{intl.formatMessage({ id: "diagnostics.events.queueDepth" })}</th>
                  </tr>
                </thead>
                <tbody>
                  {events.map((event, index) => <EventRow key={`${event.at}-${event.stage}-row-${index}`} event={event} intl={intl} />)}
                  {events.length === 0 ? <tr><td className="px-3 py-4 text-muted-foreground" colSpan={12}>{intl.formatMessage({ id: "diagnostics.events.empty" })}</td></tr> : null}
                </tbody>
              </table>
            </div>
          </SectionCard>

          <RelatedLinks response={response} />
          {exported ? <div className="text-sm text-muted-foreground" role="status">{intl.formatMessage({ id: "diagnostics.exported" })}</div> : null}
        </>
      ) : null}
    </>
  )
}

export function DiagnosticsPage() {
  return (
    <PageContainer>
      <DiagnosticsTracePanel />
    </PageContainer>
  )
}

function TextInput({ label, value, onChange }: { label: string; value: string; onChange: (value: string) => void }) {
  return (
    <label className="flex flex-col gap-1 text-sm font-medium text-foreground">
      {label}
      <input className="rounded-md border border-input bg-background px-3 py-2 text-sm" value={value} onChange={(event) => onChange(event.target.value)} />
    </label>
  )
}

function valueOrDash(value: string | number | undefined) {
  if (value === undefined || value === "") return "-"
  return String(value)
}

function TimelineEventItem({ event, intl, isLast }: { event: ManagerDiagnosticsEvent; intl: IntlShape; isLast: boolean }) {
  const failed = failureResults.has(event.result)
  const details = timelineDetails(event, intl)
  return (
    <li className={`grid grid-cols-[1.25rem_minmax(0,1fr)] border-b border-border/70 last:border-b-0 ${failed ? "bg-destructive/5" : "hover:bg-muted/25"}`}>
      <div className="relative flex justify-center py-3" aria-hidden="true">
        {!isLast ? <span className="absolute bottom-0 top-6 w-px bg-border" /> : null}
        <span className={`relative z-10 mt-1 size-2.5 rounded-full border ${failed ? "border-destructive bg-destructive" : "border-emerald-500 bg-emerald-500"}`} />
      </div>
      <div className="min-w-0 py-2.5 pr-3">
        <div className="grid gap-2 md:grid-cols-[minmax(12rem,1fr)_auto_auto_auto] md:items-center">
          <div className="min-w-0">
            <div className="truncate text-sm font-semibold text-foreground" title={event.stage}>{event.stage}</div>
            <div className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
              <span>{timelineContext(event, intl)}</span>
              {details.map((detail, detailIndex) => <span key={`${detail}-${detailIndex}`} className="max-w-full truncate">{detail}</span>)}
            </div>
          </div>
          <StatusPill intl={intl} value={event.result} />
          <span className="text-xs font-medium tabular-nums text-muted-foreground">{typeof event.duration_ms === "number" ? `${event.duration_ms} ms` : "-"}</span>
          <time className="text-xs tabular-nums text-muted-foreground" dateTime={event.at}>{timestamp(intl, event.at)}</time>
        </div>
        {event.error ? <div className="mt-1.5 rounded-md border border-destructive/20 bg-destructive/10 px-2 py-1 text-xs text-destructive">{event.error}</div> : null}
      </div>
    </li>
  )
}

function EventRow({ event, intl }: { event: ManagerDiagnosticsEvent; intl: IntlShape }) {
  return (
    <tr className="border-t border-border align-top">
      <td className="px-3 py-3 font-medium text-foreground">{event.stage}</td>
      <td className="px-3 py-3"><StatusPill intl={intl} value={event.result} /></td>
      <td className="px-3 py-3 text-muted-foreground">{valueOrDash(event.duration_ms)}</td>
      <td className="px-3 py-3 text-muted-foreground">{valueOrDash(event.node_id)}</td>
      <td className="px-3 py-3 text-muted-foreground">{valueOrDash(event.peer_node_id)}</td>
      <td className="px-3 py-3 text-muted-foreground">{valueOrDash(event.slot_id)}</td>
      <td className="px-3 py-3 text-muted-foreground">{valueOrDash(event.channel_key)}</td>
      <td className="px-3 py-3 text-muted-foreground">{valueOrDash(event.message_seq)}</td>
      <td className="px-3 py-3 text-muted-foreground">{valueOrDash(event.error_code)}</td>
      <td className="px-3 py-3 text-muted-foreground">{valueOrDash(event.service)}</td>
      <td className="px-3 py-3 text-muted-foreground">{valueOrDash(event.attempt)}</td>
      <td className="px-3 py-3 text-muted-foreground">{valueOrDash(event.queue_depth)}</td>
    </tr>
  )
}

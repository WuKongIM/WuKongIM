# Cloud View Flow

`cloudview` is the HTTP/WebSocket entry adapter for one Cloud Simulation Run.
It never joins the cluster, decodes WKProto frames, or holds cloud credentials.

```text
public HTTP/WS :19443
  -> source-IP HTTP token buckets / WebSocket concurrency limits
  -> cached /readyz checks for node-1, node-2, node-3
  -> round-robin selection across healthy nodes
  -> Manager UI and /manager/* -> selected node Manager :5301
  -> Demo and legacy product APIs -> selected node API :5001
  -> WebSocket upgrades -> selected node gateway :5200
  -> /prometheus/* -> simulator-local Prometheus :9090
```

`/route` responses are rewritten to the run's public `ws://SIM_PUBLIC:19443`
origin. Safe reads and WebSocket handshakes retry another healthy node after an
upstream transport failure; Manager writes are never replayed because their
effect may be irreversible.

Successful Demo/API or WebSocket use asks `internal/runtime/cloudviewstate` to
record `interactive=true`. A successful non-login Manager write records
`operator_modified=true`. The automated provisioning doctor carries a
run-scoped secret header and is excluded from these transitions. The runtime
owns durable JSON and node_exporter projections, and `wkcloudview
annotate-report` reads live simulator-local state and adds the final purity
verdict to the wkbench report. Missing or degraded state writes `pure=false`
and fails the systemd post-stop action instead of leaving a pure result.

`GET /cloud-view/status` is a passive control-plane read. It reports the direct
transport peer as `observed_ipv4` from `RemoteAddr`, ignores forwarded-address
headers, and does not change benchmark purity. Local Analysis uses that
same-destination observation to bind its single `/32` ingress rule when a
transparent VPN or policy router gives public IP echo services another egress.
The response is `no-store`, and Analysis adds a per-session cache buster. This
plain-HTTP signal is only a best-effort rebind hint: unavailable, legacy, or
invalid status falls back to the HTTPS public echo address, while the pinned-TLS
MCP health check remains the authority for accepting the Analysis endpoint.

For real interactive traffic, the conservative marker is persisted before the
request is forwarded. If persistence is degraded, Cloud View returns `503` and
does not execute the Demo/API/WS request or an irreversible Manager operation.
An upstream failure after admission can therefore create a conservative false
positive, but never an unmarked successful modification.

`internal/app.NewCloudViewHandler` is the composition root. The standalone
`cmd/wkcloudview` process owns listener lifecycle and the strict JSON config.

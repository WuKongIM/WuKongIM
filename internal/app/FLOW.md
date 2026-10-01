---
scope: package
summary: Composes product and Agent runtimes and owns their dependency-safe lifecycle ordering.
---

# internal/app Flow

## Responsibility

This package is the only composition root under `internal`. It converts
validated configuration into product access adapters, use cases, node-local
runtimes, infrastructure adapters, cluster/gateway services, observability,
and lifecycle ownership. Agent/Analysis/View roots do not start the product cluster.

## Boundaries

Business policy belongs in `internal/usecase`, entry mapping in
`internal/access`, node-local capabilities in `internal/runtime`, and concrete
adapters in `internal/infra` or `pkg`. This package may adapt sibling DTOs and
wire optional capabilities, but must not become a global service object or a
second implementation of those layers.

## Main Flows

```text
validated Config + build identity -> format guard and fresh-directory provenance before logging
  -> construct cluster and shared runtime foundations
  -> construct use cases and infrastructure ports
  -> bind the user use case's durable device-token verifier into Gateway CONNECT authentication
  -> compose PageReader and its committed-record adapter for ordinary sync, exact lookup and plugin reads
  -> adapt the message use case's explicit persisted batch read into the narrow
     conversation legacy-sync message port
  -> when bearer and all real drain ports exist, bind one terminal controller
     to both the already-created gateway handler and API prepare route
  -> wire legacy product plugin HTTP routes into the same plugin usecase
  -> register node RPC and access adapters
  -> expose optional API, Manager, metrics, diagnostics, plugins, and gateway

Start
  -> cluster/control readiness
  -> internal producers and post-commit consumers
  -> API and Manager
  -> Prometheus and Gateway admission

Stop or startup rollback
  -> mark the gateway handler's planned-shutdown warning fence
  -> close entry admission
  -> drain Channel append and accepted post-commit work
  -> stop side-effect, presence, and cluster dependencies in reverse order
```

## Invariants and Failure Semantics

- Every product deployment, including one node, uses cluster semantics. Wiring
  must not introduce a local business bypass.
  Real single-node SEND/SENDACK smoke coverage lives in the integration tier and
  uses the production-default send deadline; native-package preview runs it explicitly.
- Synchronous before-send Webhook admission is wired independently of asynchronous
  Webhook workers and plugin enablement; configuration errors fail startup.
- Gateway feedback uses the existing Online Delivery FeedbackHandler directly;
  disabled delivery leaves that optional port absent.
- Optional features are wired only when all required ports exist; unavailable
  capabilities stay explicit instead of receiving partial implementations.
- Opt-in MQTT composition shares the existing Gateway and device-token verifier,
  without WK device-conflict actions. Exact-owner RPC, connection renewal,
  group/inbox projection, future person-source admission, replay maintenance,
  delivery, ACKs, owner sweeps, offline accounting/closed-source drain/progress, pending-subscription completion and Channel/ended-UID removal are wired before admission. Real process
  Paho/WKProto interop passes in single-node and three-node 256-hash-Slot clusters.
  Stop closes admission, joins MQTT work/Owners while transport callbacks and message/cluster dependencies remain alive,
  then persists proved boot retirement before Gateway shutdown. Incomplete cleanup retains dependencies.
  Exact-owner RPC uses persisted node-local retirement/crashed-boot facts. Restore
  joins CONNECT handoffs, workers and Owners before fresh generation construction;
  atomic publication switches stable Gateway/RPC dispatch only after full startup.
  Restore connection teardown requires exact local isolation without new Session
  mutations against already-fenced replicas; ordinary Stop keeps durable disconnect.
  Connections pin the original handler; shutdown prevents observer admission reopen.
  WillExecutor freezes hook output before first dispatch. A bounded local journal
  is reserved before Started and admitted before publication; exact-node RPC 105
  seals only proved non-dispatch. The existing generation lock and retired-boot
  facts fence older journals; Stop joins Will workers and closes the journal
  before releasing that lock. Admitted/legacy work remains positive-receipt-only. Its managed scanner and four-turn cohort now
  publish detached due work. Consumer recovery also completes disconnected Preparing/Removing intent before a first binding. Concrete group/inbox offline preparation ports are composed before that shared cohort; fresh permission denial routes through exact Session ending. That cohort also builds historical reclamation coverage, invokes exact-owner Session-child cleanup and retires qualified source tombstones; fixed metrics expose completion and indexed rows.
  Safe uncertain Will redispatch and the full partition/failure/workload matrix
  remain unqualified; completed process and group-scale scenarios cover only their
  recorded bounds. See
  [product composition](../../docs/specs/mqtt-product-runtime.md) and the linked
  module contracts; successful online interop is not complete MQTT delivery.
- MQTT delivery quiet hints are bounded to ten seconds. Ordinary durable
  post-commit envelopes and timely replay anchors wake only indexed Channel
  interests in the existing scheduler. Remote/lost hints recover through full
  refresh; no cached permission or local cluster bypass is introduced.
- Product MQTT replay confirmation opts the concurrent-safe cluster Node into
  four joined replica calls; all-replica proof and final fresh placement remain
  required before subscription activation.
- Command-channel suffixes are injected across send, delivery, CMD sync, plugin
  projection and Manager filtering without process-global state.
- The normalized message system UID is injected consistently into user
  privilege checks, Product HTTP compatibility, legacy conversation projection,
  and plugin-origin default sends.
- The legacy conversation-sync adapter only translates sibling DTOs; directory
  scanning, filtering, whole-request failure, and message ordering stay in the
  conversation use case. It transfers the message usecase's already-detached
  base/stream bytes without another copy; the conversation port retains its
  defensive response ownership boundary.
- The benchmark terminal controller is advertised only with a non-empty token,
  the real Gateway SEND drainer, Channel append group, and Online Delivery
  runtime. Partial compositions cannot mint a terminal capability.
- Channel append uses Slot-leader subscribers and owns versioned snapshot reuse.
  Producers start after consumers and drain before them; a drain timeout keeps
  dependencies alive so a later `Stop` can continue the same drain.
  Idempotency wiring requires routed original committed reads so later history
  edits cannot change the content used to prove a send retry.
- Startup failure rolls back completed components in reverse order. Constructor
  failure releases constructor-owned pools, sinks, and audit resources.
- Presence wiring shares one owner boot identity between session activation and
  recovery replies, and installs bounded UID reconstruction before lookups.
- Gateway admission opens only after cluster write routing and required runtime
  readiness. Joining nodes remain fenced until observed membership permits it.
- Backup RPC binds one target-local Controller repository resolver to all four
  export, probe and restore handlers before advertising them.
- Restore maintenance keeps Manager reachable while product traffic is fenced;
  restore-sensitive caches and side-effect runtimes are reactivated before
  Controller clears maintenance.
- Observability is bounded and low-cardinality; MQTT SUB/UNSUB entry closure observations use 34 fixed operation/reason series and supply no isolation or retry proof. Runtime labels must not contain
  UIDs, Channel IDs, client message IDs, addresses, or secret material.
  Transport publishes exact bounded-label counter batches and one-in-32 latency
  samples, separating handler, service-queue and complete client RPC time. Its
  retained-memory gauge includes queued and executing request owners.
  Channel-create coalescer gauges and closed delivery/post-commit terminal
  result partitions must materialize true zero series rather than imply them.
- A Slot replica match index may exceed commit while replication is pending;
  its committed-entry lag is zero, not invalid or an unsigned underflow.
- Issue/Review Agent composition keeps read, verification, signed-state, and
  publication credentials separated and never joins the product cluster.
- Message edits wire Slot storage, online hints and one bounded repair worker. Start/stop and restore maintenance join the worker before its dependencies close; resume restarts it with fresh scan cursors. HTTP content epochs and edit cursors reuse the Controller successful-restore generation. A packed atomic maintenance-transition stamp fences response assembly across successful and failed restore cycles without an extra distributed read.
- Successful edit commits enqueue body-free identities through a nonblocking callback into that same worker. Its bounded volatile queue accelerates authoritative dispatch; stop/restore clears it and durable pending scans remain the recovery source.

## Read First
- [app.go](app.go)
- [FLOW_PRODUCT_RUNTIME.md](FLOW_PRODUCT_RUNTIME.md)
- [backup.go](backup.go)
- [issue_agent.go](issue_agent.go)
- [review_agent.go](review_agent.go)
## Update Triggers

- Dependency ownership or the sole-composition-root boundary changes.
- Product startup, readiness, rollback, drain, or shutdown ordering changes.
- Restore maintenance, side-effect fencing, optional wiring, or Agent authority changes.

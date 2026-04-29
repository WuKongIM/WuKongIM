# Message Delivery Performance Optimization Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce hot-path message delivery overhead while preserving cluster-first durable send semantics and current realtime delivery guarantees.

**Architecture:** Add lightweight observability first, then make delivery runtime lifecycle explicit, add a safe channel metadata cache fast path with bounded invalidation/retry, replace goroutine-per-committed-message routing with sharded bounded workers, and batch remote delivery push per committed message. The implementation stays within existing boundaries: `internal/access/*` adapts protocols/RPC, `internal/usecase/*` coordinates business flow, `internal/runtime/*` owns node-local runtime primitives, and `internal/app` remains the composition root.

**Tech Stack:** Go, Prometheus client metrics in `pkg/metrics`, existing app lifecycle primitives in `internal/app/lifecycle`, existing channel log and delivery runtime packages, `go test` for unit/integration validation.

---

## References

- Spec: `docs/superpowers/specs/2026-04-29-message-delivery-performance-optimization-design.md`
- Architecture docs: `internal/FLOW.md`, `internal/runtime/channelmeta/FLOW.md`, `pkg/channel/FLOW.md`
- Current delivery flow: `internal/app/deliveryrouting.go`, `internal/runtime/delivery/manager.go`, `internal/runtime/delivery/actor.go`
- Current metadata refresh: `internal/usecase/message/retry.go`, `internal/runtime/channelmeta/resolver.go`, `internal/runtime/channelmeta/cache.go`
- Current committed replay: `internal/app/committed_replay.go`

## Implementation Notes

- Before implementation, run `git status --short` and preserve unrelated user changes. Do not reset or revert unrelated files.
- Use TDD for each task: write the failing test first, run it, implement the smallest change, rerun focused tests, then commit.
- Metric names must follow existing conventions: `wukongim_...`, duration histograms end with `_duration_seconds`, counters end with `_total`.
- Public configuration is intentionally not added in this phase. Queue sizes, intervals, and retry expiry defaults remain internal constants unless tests need injection.
- Remote push batching is only within a single committed message / `deliveryruntime.PushCommand`. Do not batch across multiple messages in this phase.
- Add concise English doc comments for new exported structs, interfaces, methods, and major config fields.

## File Structure

### Metrics

- Modify: `pkg/metrics/registry.go` — add `Message *MessageMetrics` and `Delivery *DeliveryMetrics` to the registry.
- Create: `pkg/metrics/message.go` — message send/meta/dispatch/replay metrics.
- Create: `pkg/metrics/delivery.go` — delivery resolve/push/inflight/expiry metrics.
- Modify: `pkg/metrics/registry_test.go` — assert new metric families and snapshots.
- Modify: `internal/app/observability.go` — wire app-level observers to metrics where instrumentation is in `internal/app`.

### Channel Metadata Fast Path

- Modify: `internal/runtime/channelmeta/cache.go` — add per-key invalidation and cache entries that retain both applied `channel.Meta` and source `metadb.ChannelRuntimeMeta`.
- Modify: `internal/runtime/channelmeta/resolver.go` — add `CachedChannelMeta`, `InvalidateChannelMeta`, and meta refresh observer emission.
- Modify: `internal/runtime/channelmeta/interfaces.go` — add `MetaRefreshResult`, `MetaRefreshEvent`, and `MetaRefreshObserver`.
- Modify: `internal/usecase/message/deps.go` — add optional cache invalidation capability for send retry.
- Modify: `internal/usecase/message/retry.go` — use fast path, force refresh after stale/leader/lease errors, retry once.
- Test: `internal/runtime/channelmeta/cache_test.go`, `internal/runtime/channelmeta/resolver_test.go`, `internal/usecase/message/retry_test.go`.

### Delivery Runtime Lifecycle and Expiry

- Modify: `internal/runtime/delivery/types.go` — add `Observer`, `RouteExpiredEvent`, and maintenance snapshot hooks for route expiry and gauge metrics.
- Modify: `internal/runtime/delivery/manager.go` — expose snapshots/counters and route expiry behavior.
- Modify: `internal/runtime/delivery/actor.go` — finish routes after final retry attempt.
- Create: `internal/app/delivery_lifecycle.go` — app-owned retry/idle ticker component.
- Modify: `internal/app/app.go` — add `deliveryRuntimeLifecycle *deliveryRuntimeLifecycle` and `deliveryRuntimeOn atomic.Bool` fields.
- Modify: `internal/app/lifecycle_components.go` — start delivery runtime before gateway/API accept sends and stop it after gateway/API stop accepting new work.
- Modify: `internal/app/lifecycle.go` — add start/stop methods.
- Test: `internal/runtime/delivery/actor_test.go`, `internal/runtime/delivery/manager_test.go`, `internal/app/lifecycle_components_test.go`.

### Committed Dispatch Queue

- Modify: `internal/app/deliveryrouting.go` — replace goroutine-per-event dispatcher with sharded queue implementation.
- Test: `internal/app/deliveryrouting_test.go` — ordering, overflow, conversation fallback, clean stop.

### Remote Push Batching

- Modify: `internal/app/deliveryrouting.go` — introduce a narrow `deliveryPushClient` interface and group remote routes by target node and frame identity within one `PushCommand`.
- Modify: `internal/access/node/client.go` — add `PushBatchItems` to send multiple delivery push items in one RPC.
- Modify: `internal/access/node/delivery_push_rpc.go` — decode/process multi-item request and return aggregate route results.
- Modify: `internal/access/node/delivery_push_rpc_test.go` — cover group batch, person view isolation, partial success.
- Test: `internal/app/deliveryrouting_test.go`, `internal/access/node/delivery_push_rpc_test.go`, `internal/app/multinode_integration_test.go` focused group delivery test when integration validation is requested.

### Docs

- Modify: `internal/FLOW.md` — update delivery flow for bounded committed dispatcher, metadata fast path, lifecycle retry/expiry, and remote push batching.
- Modify: `docs/development/PROJECT_KNOWLEDGE.md` only if implementation reveals a new durable project rule. Keep it short.

---

### Task 1: Add Message and Delivery Metrics Surfaces

**Files:**
- Create: `pkg/metrics/message.go`
- Create: `pkg/metrics/delivery.go`
- Modify: `pkg/metrics/registry.go`
- Modify: `pkg/metrics/registry_test.go`

- [ ] **Step 1: Write failing metrics registry tests**

Add tests to `pkg/metrics/registry_test.go`:

```go
func TestRegistryExposesMessageMetrics(t *testing.T) {
	reg := New(1, "n1")
	reg.Message.ObserveMetaRefresh("cache_hit", 3*time.Millisecond)
	reg.Message.ObserveAppend("local", "ok", 5*time.Millisecond)
	reg.Message.SetCommittedDispatchQueueDepth("0", 7)
	reg.Message.ObserveCommittedDispatchEnqueue("0", "ok")
	reg.Message.ObserveCommittedDispatchEnqueue("0", "overflow")
	reg.Message.ObserveCommittedDispatchOverflow("0")
	reg.Message.SetCommittedReplayLag("person", 3)
	reg.Message.ObserveCommittedReplayPass("ok", 9*time.Millisecond)

	families, err := reg.Gather()
	require.NoError(t, err)
	requireMetricFamily(t, families, "wukongim_message_meta_refresh_total")
	requireMetricFamily(t, families, "wukongim_message_meta_refresh_duration_seconds")
	requireMetricFamily(t, families, "wukongim_message_append_duration_seconds")
	requireMetricFamily(t, families, "wukongim_message_committed_dispatch_queue_depth")
	requireMetricFamily(t, families, "wukongim_message_committed_dispatch_enqueue_total")
	requireMetricFamily(t, families, "wukongim_message_committed_dispatch_overflow_total")
	requireMetricFamily(t, families, "wukongim_message_committed_replay_lag_messages")
	requireMetricFamily(t, families, "wukongim_message_committed_replay_pass_duration_seconds")
}

func TestRegistryExposesDeliveryMetrics(t *testing.T) {
	reg := New(1, "n1")
	reg.Delivery.ObserveResolve("person", "ok", 2*time.Millisecond, 1, 2)
	reg.Delivery.ObservePushRPC("2", "ok", 4*time.Millisecond, 3)
	reg.Delivery.SetActorInflightRoutes(9)
	reg.Delivery.SetAckBindings(5)
	reg.Delivery.ObserveRouteExpired("group")

	families, err := reg.Gather()
	require.NoError(t, err)
	requireMetricFamily(t, families, "wukongim_delivery_resolve_duration_seconds")
	requireMetricFamily(t, families, "wukongim_delivery_resolve_pages_total")
	requireMetricFamily(t, families, "wukongim_delivery_resolve_routes_total")
	requireMetricFamily(t, families, "wukongim_delivery_push_rpc_total")
	requireMetricFamily(t, families, "wukongim_delivery_push_rpc_duration_seconds")
	requireMetricFamily(t, families, "wukongim_delivery_push_rpc_routes_total")
	requireMetricFamily(t, families, "wukongim_delivery_actor_inflight_routes")
	requireMetricFamily(t, families, "wukongim_delivery_ack_bindings")
	requireMetricFamily(t, families, "wukongim_delivery_route_expired_total")
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `GOWORK=off go test ./pkg/metrics -run 'TestRegistryExposes(Message|Delivery)Metrics' -count=1`

Expected: FAIL because `Registry.Message`, `Registry.Delivery`, and metric methods do not exist.

- [ ] **Step 3: Implement metrics types**

Create `pkg/metrics/message.go` with:

```go
package metrics

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type MessageSnapshot struct {
	CommittedDispatchQueueDepth map[string]int64
}

type MessageMetrics struct {
	metaRefreshTotal          *prometheus.CounterVec
	metaRefreshDuration       *prometheus.HistogramVec
	appendTotal               *prometheus.CounterVec
	appendDuration            *prometheus.HistogramVec
	committedDispatchDepth     *prometheus.GaugeVec
	committedDispatchEnqueues  *prometheus.CounterVec
	committedDispatchOverflows *prometheus.CounterVec
	committedReplayLag         *prometheus.GaugeVec
	committedReplayPassDuration *prometheus.HistogramVec
	mu                         sync.Mutex
	queueDepth                 map[string]int64
}

func newMessageMetrics(registry prometheus.Registerer, labels prometheus.Labels) *MessageMetrics {
	m := &MessageMetrics{
		metaRefreshTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_message_meta_refresh_total",
			Help:        "Total number of message send channel metadata refresh decisions.",
			ConstLabels: labels,
		}, []string{"result"}),
		metaRefreshDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_message_meta_refresh_duration_seconds",
			Help:        "Message send channel metadata refresh latency in seconds.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"result"}),
		appendTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_message_append_total",
			Help:        "Total number of message append attempts from the message usecase.",
			ConstLabels: labels,
		}, []string{"path", "result"}),
		appendDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_message_append_duration_seconds",
			Help:        "Message append latency in seconds.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"path", "result"}),
		committedDispatchDepth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_message_committed_dispatch_queue_depth",
			Help:        "Current committed message dispatch queue depth by shard.",
			ConstLabels: labels,
		}, []string{"shard"}),
		committedDispatchEnqueues: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_message_committed_dispatch_enqueue_total",
			Help:        "Total committed message dispatch enqueue attempts.",
			ConstLabels: labels,
		}, []string{"shard", "result"}),
		committedDispatchOverflows: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_message_committed_dispatch_overflow_total",
			Help:        "Total committed message dispatch queue overflows.",
			ConstLabels: labels,
		}, []string{"shard"}),
		committedReplayLag: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_message_committed_replay_lag_messages",
			Help:        "Committed replay lag in messages by channel type.",
			ConstLabels: labels,
		}, []string{"channel_type"}),
		committedReplayPassDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_message_committed_replay_pass_duration_seconds",
			Help:        "Committed replay pass latency in seconds.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"result"}),
		queueDepth: make(map[string]int64),
	}
	registry.MustRegister(m.metaRefreshTotal, m.metaRefreshDuration, m.appendTotal, m.appendDuration, m.committedDispatchDepth, m.committedDispatchEnqueues, m.committedDispatchOverflows, m.committedReplayLag, m.committedReplayPassDuration)
	return m
}

func (m *MessageMetrics) ObserveMetaRefresh(result string, dur time.Duration) {
	if m == nil {
		return
	}
	m.metaRefreshTotal.WithLabelValues(result).Inc()
	m.metaRefreshDuration.WithLabelValues(result).Observe(dur.Seconds())
}

func (m *MessageMetrics) ObserveAppend(path, result string, dur time.Duration) {
	if m == nil {
		return
	}
	m.appendTotal.WithLabelValues(path, result).Inc()
	m.appendDuration.WithLabelValues(path, result).Observe(dur.Seconds())
}

func (m *MessageMetrics) SetCommittedDispatchQueueDepth(shard string, depth int) {
	if m == nil {
		return
	}
	m.committedDispatchDepth.WithLabelValues(shard).Set(float64(depth))
	m.mu.Lock()
	m.queueDepth[shard] = int64(depth)
	m.mu.Unlock()
}

func (m *MessageMetrics) ObserveCommittedDispatchEnqueue(shard, result string) {
	if m == nil {
		return
	}
	m.committedDispatchEnqueues.WithLabelValues(shard, result).Inc()
}

func (m *MessageMetrics) ObserveCommittedDispatchOverflow(shard string) {
	if m == nil {
		return
	}
	m.committedDispatchOverflows.WithLabelValues(shard).Inc()
}

func (m *MessageMetrics) SetCommittedReplayLag(channelType string, lag uint64) {
	if m == nil {
		return
	}
	m.committedReplayLag.WithLabelValues(channelType).Set(float64(lag))
}

func (m *MessageMetrics) ObserveCommittedReplayPass(result string, dur time.Duration) {
	if m == nil {
		return
	}
	m.committedReplayPassDuration.WithLabelValues(result).Observe(dur.Seconds())
}
```

Create `pkg/metrics/delivery.go` with:

```go
package metrics

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type DeliverySnapshot struct {
	ActorInflightRoutes int64
	AckBindings int64
}

type DeliveryMetrics struct {
	resolveDuration *prometheus.HistogramVec
	resolvePagesTotal *prometheus.CounterVec
	resolveRoutesTotal *prometheus.CounterVec
	pushRPCTotal *prometheus.CounterVec
	pushRPCDuration *prometheus.HistogramVec
	pushRPCRoutesTotal *prometheus.CounterVec
	actorInflightRoutes prometheus.Gauge
	ackBindings prometheus.Gauge
	routeExpiredTotal *prometheus.CounterVec
	mu sync.Mutex
	snapshot DeliverySnapshot
}

func newDeliveryMetrics(registry prometheus.Registerer, labels prometheus.Labels) *DeliveryMetrics {
	m := &DeliveryMetrics{
		resolveDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "wukongim_delivery_resolve_duration_seconds", Help: "Delivery route resolve latency in seconds.", ConstLabels: labels, Buckets: gatewayFrameDurationBuckets}, []string{"channel_type", "result"}),
		resolvePagesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "wukongim_delivery_resolve_pages_total", Help: "Total delivery resolver pages processed.", ConstLabels: labels}, []string{"channel_type", "result"}),
		resolveRoutesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "wukongim_delivery_resolve_routes_total", Help: "Total delivery routes resolved.", ConstLabels: labels}, []string{"channel_type", "result"}),
		pushRPCTotal: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "wukongim_delivery_push_rpc_total", Help: "Total remote delivery push RPC calls.", ConstLabels: labels}, []string{"target_node", "result"}),
		pushRPCDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "wukongim_delivery_push_rpc_duration_seconds", Help: "Remote delivery push RPC latency in seconds.", ConstLabels: labels, Buckets: gatewayFrameDurationBuckets}, []string{"target_node", "result"}),
		pushRPCRoutesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "wukongim_delivery_push_rpc_routes_total", Help: "Total remote delivery push RPC routes.", ConstLabels: labels}, []string{"target_node", "result"}),
		actorInflightRoutes: prometheus.NewGauge(prometheus.GaugeOpts{Name: "wukongim_delivery_actor_inflight_routes", Help: "Current delivery actor inflight route count.", ConstLabels: labels}),
		ackBindings: prometheus.NewGauge(prometheus.GaugeOpts{Name: "wukongim_delivery_ack_bindings", Help: "Current delivery ack binding count.", ConstLabels: labels}),
		routeExpiredTotal: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "wukongim_delivery_route_expired_total", Help: "Total realtime delivery routes expired after retry budget.", ConstLabels: labels}, []string{"channel_type"}),
	}
	registry.MustRegister(m.resolveDuration, m.resolvePagesTotal, m.resolveRoutesTotal, m.pushRPCTotal, m.pushRPCDuration, m.pushRPCRoutesTotal, m.actorInflightRoutes, m.ackBindings, m.routeExpiredTotal)
	return m
}

func (m *DeliveryMetrics) ObserveResolve(channelType, result string, dur time.Duration, pages, routes int) {
	if m == nil { return }
	m.resolveDuration.WithLabelValues(channelType, result).Observe(dur.Seconds())
	m.resolvePagesTotal.WithLabelValues(channelType, result).Add(float64(pages))
	m.resolveRoutesTotal.WithLabelValues(channelType, result).Add(float64(routes))
}

func (m *DeliveryMetrics) ObservePushRPC(targetNode, result string, dur time.Duration, routes int) {
	if m == nil { return }
	m.pushRPCTotal.WithLabelValues(targetNode, result).Inc()
	m.pushRPCDuration.WithLabelValues(targetNode, result).Observe(dur.Seconds())
	m.pushRPCRoutesTotal.WithLabelValues(targetNode, result).Add(float64(routes))
}

func (m *DeliveryMetrics) SetActorInflightRoutes(v int) {
	if m == nil { return }
	m.actorInflightRoutes.Set(float64(v))
	m.mu.Lock(); m.snapshot.ActorInflightRoutes = int64(v); m.mu.Unlock()
}

func (m *DeliveryMetrics) SetAckBindings(v int) {
	if m == nil { return }
	m.ackBindings.Set(float64(v))
	m.mu.Lock(); m.snapshot.AckBindings = int64(v); m.mu.Unlock()
}

func (m *DeliveryMetrics) ObserveRouteExpired(channelType string) {
	if m == nil { return }
	m.routeExpiredTotal.WithLabelValues(channelType).Inc()
}
```

- [ ] **Step 4: Wire registry fields**

Update `pkg/metrics/registry.go`:

```go
type Registry struct {
	registry *prometheus.Registry

	Gateway    *GatewayMetrics
	Channel    *ChannelMetrics
	Slot       *SlotMetrics
	Controller *ControllerMetrics
	Transport  *TransportMetrics
	Storage    *StorageMetrics
	Message    *MessageMetrics
	Delivery   *DeliveryMetrics
}
```

In `New`, initialize `Message: newMessageMetrics(registry, labels)` and `Delivery: newDeliveryMetrics(registry, labels)`.

- [ ] **Step 5: Run metrics tests**

Run: `GOWORK=off go test ./pkg/metrics -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/metrics/registry.go pkg/metrics/message.go pkg/metrics/delivery.go pkg/metrics/registry_test.go
git commit -m "feat: add message delivery performance metrics"
```

---

### Task 2: Add Delivery Runtime Lifecycle and Route Expiry

**Files:**
- Modify: `internal/runtime/delivery/types.go`
- Modify: `internal/runtime/delivery/manager.go`
- Modify: `internal/runtime/delivery/actor.go`
- Create: `internal/app/delivery_lifecycle.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/lifecycle_components.go`
- Modify: `internal/app/lifecycle.go`
- Test: `internal/runtime/delivery/actor_test.go`
- Test: `internal/app/build_test.go` and `internal/app/lifecycle_components_test.go`

- [ ] **Step 1: Write failing route expiry test**

Add to `internal/runtime/delivery/actor_test.go`:

```go
func TestActorExpiresAcceptedRouteAfterFinalRetryAttempt(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)}
	route := testRoute("u2", 1, 11, 2)
	pusher := &recordingPusher{responses: []pushResponse{
		{result: PushResult{Accepted: []RouteKey{route}}},
		{result: PushResult{Accepted: []RouteKey{route}}},
	}}
	runtime := NewManager(Config{
		Resolver: &stubResolver{routesByChannel: map[string][]RouteKey{testChannelID: []RouteKey{route}}},
		Push: pusher,
		Clock: clock,
		RetryDelays: []time.Duration{time.Second},
		MaxRetryAttempts: 2,
	})

	require.NoError(t, runtime.Submit(context.Background(), testEnvelope(101, 1)))
	require.True(t, runtime.HasAckBinding(route.SessionID, 101))

	clock.Advance(cappedBackoffWithJitter([]time.Duration{time.Second}, 2))
	require.NoError(t, runtime.ProcessRetryTicks(context.Background()))

	require.False(t, runtime.HasAckBinding(route.SessionID, 101))
	require.Equal(t, 2, pusher.attemptsFor(101))
}
```

- [ ] **Step 2: Write failing route expiry observer test**

Add to `internal/runtime/delivery/actor_test.go`:

```go
func TestActorReportsRouteExpiryObserver(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)}
	observer := &recordingDeliveryObserver{}
	route := testRoute("u2", 1, 11, 2)
	runtime := NewManager(Config{
		Resolver: &stubResolver{routesByChannel: map[string][]RouteKey{testChannelID: []RouteKey{route}}},
		Push: &recordingPusher{responses: []pushResponse{
			{result: PushResult{Accepted: []RouteKey{route}}},
		}},
		Clock: clock,
		RetryDelays: []time.Duration{time.Second},
		MaxRetryAttempts: 1,
		Observer: observer,
	})

	require.NoError(t, runtime.Submit(context.Background(), testEnvelope(101, 1)))
	require.Len(t, observer.expired, 1)
	require.Equal(t, uint64(101), observer.expired[0].MessageID)
	require.Equal(t, route.SessionID, observer.expired[0].Route.SessionID)
}
```

- [ ] **Step 3: Run expiry tests to verify failure**

Run: `GOWORK=off go test ./internal/runtime/delivery -run 'TestActorExpiresAcceptedRouteAfterFinalRetryAttempt|TestActorReportsRouteExpiryObserver' -count=1`

Expected: FAIL because the final accepted route remains bound and no route expiry observer exists.

- [ ] **Step 4: Define delivery observer contract**

In `internal/runtime/delivery/types.go` add:

```go
type RouteExpiredEvent struct {
	ChannelID string
	ChannelType uint8
	MessageID uint64
	MessageSeq uint64
	Route RouteKey
	Attempt int
}

type MaintenanceSnapshot struct {
	InflightRoutes int
	AckBindings int
}

type Observer interface {
	OnRouteExpired(RouteExpiredEvent)
	OnMaintenanceSnapshot(MaintenanceSnapshot)
}
```

Add `Observer Observer` to `Config` and store it on `Manager`. `expireRoute` must call `Observer.OnRouteExpired` after `finishRoute` succeeds. The app metrics adapter will map this event to `DeliveryMetrics.ObserveRouteExpired(channelType)`.

- [ ] **Step 5: Implement final-attempt expiry**

In `internal/runtime/delivery/actor.go`, change `applyPush` so accepted or retryable routes are finished when no next retry can be scheduled after the current attempt and the route has not acked during the push call:

```go
func (a *actor) scheduleRetry(msg *InflightMessage, route RouteKey, attempt int) bool {
	delay, ok := a.shard.nextRetryDelay(attempt + 1)
	if !ok {
		return false
	}
	a.shard.wheel.Schedule(RetryEntry{
		When:        a.shard.manager.clock.Now().Add(delay),
		ChannelID:   a.key.ChannelID,
		ChannelType: a.key.ChannelType,
		MessageID:   msg.MessageID,
		Route:       route,
		Attempt:     attempt + 1,
	})
	return true
}
```

Then in `applyPush`:

```go
if !a.scheduleRetry(msg, route, attempt) {
	if err := a.expireRoute(ctx, msg, route, attempt); err != nil {
		return err
	}
}
```

Add `expireRoute` as a thin wrapper around `finishRoute` plus observer/metric hook. It must remove the ack binding and decrement pending route count.

- [ ] **Step 6: Add manager snapshots for lifecycle metrics**

Add methods to `internal/runtime/delivery/manager.go`:

```go
func (m *Manager) InflightRouteCount() int
func (m *Manager) AckBindingCount() int
```

Implement `AckBindingCount` by adding a `Len()` method to `internal/runtime/delivery/ackindex.go`.

- [ ] **Step 7: Write failing lifecycle test**

Add an app-level lifecycle test that starts a `deliveryRuntimeLifecycle` directly with fake ticker dependencies:

```go
func TestDeliveryRuntimeLifecycleProcessesTicksAndStops(t *testing.T) {
	clock := &manualDeliveryLifecycleClock{}
	runtime := &recordingDeliveryRuntimeMaintenance{}
	lifecycle := newDeliveryRuntimeLifecycle(deliveryRuntimeLifecycleConfig{
		Runtime: runtime,
		TickInterval: time.Millisecond,
		SweepInterval: time.Millisecond,
		After: clock.After,
	})

	require.NoError(t, lifecycle.Start(context.Background()))
	clock.Fire()
	require.Eventually(t, func() bool { return runtime.retryCalls > 0 }, time.Second, time.Millisecond)
	require.NoError(t, lifecycle.Stop())
}
```

- [ ] **Step 8: Implement `internal/app/delivery_lifecycle.go`**

Create a lifecycle owner that:

- Starts one goroutine.
- Calls `ProcessRetryTicks(ctx)` every `deliveryRuntimeRetryTickInterval`.
- Calls `SweepIdle()` every `deliveryRuntimeSweepInterval`.
- Updates delivery metrics gauges after each pass when metrics are available.
- Stops cleanly on context cancel.

Use defaults:

```go
const (
	deliveryRuntimeRetryTickInterval = 200 * time.Millisecond
	deliveryRuntimeSweepInterval     = 30 * time.Second
)
```

- [ ] **Step 9: Wire app lifecycle**

In `internal/app/lifecycle_components.go`:

- Add constant `appLifecycleDeliveryRuntime = "delivery_runtime"`.
- Insert delivery runtime lifecycle after `conversation_projector` and before `committed_replay`; when Task 4 adds `committed_dispatcher`, keep `delivery_runtime` before it. It must start before gateway accepts sends and stop after gateway stops accepting new work.
- Add `hasDeliveryRuntimeLifecycle` helper.

In `internal/app/app.go`, add `deliveryRuntimeLifecycle *deliveryRuntimeLifecycle` and `deliveryRuntimeOn atomic.Bool`.

In `internal/app/build.go`, initialize lifecycle with `app.deliveryRuntime` and `app.metrics.Delivery`.

- [ ] **Step 10: Run focused tests**

Run:

```bash
GOWORK=off go test ./internal/runtime/delivery -run 'TestActorExpiresAcceptedRouteAfterFinalRetryAttempt|TestActorReportsRouteExpiryObserver|TestActorRetryTickRetriesPendingRoutesUntilAcked' -count=1
GOWORK=off go test ./internal/app -run 'TestDeliveryRuntimeLifecycle|TestAppLifecycleUsesDeclaredComponentOrder|TestAppLifecycleStopsPresenceWorkerAfterGateway' -count=1
```

Expected: PASS.

- [ ] **Step 11: Commit**

```bash
git add internal/runtime/delivery internal/app
git commit -m "feat: manage delivery retry lifecycle"
```

---

### Task 3: Add Safe Channel Metadata Fast Path and One Forced Refresh Retry

**Files:**
- Modify: `internal/runtime/channelmeta/cache.go`
- Modify: `internal/runtime/channelmeta/resolver.go`
- Modify: `internal/runtime/channelmeta/interfaces.go`
- Modify: `internal/usecase/message/deps.go`
- Modify: `internal/usecase/message/retry.go`
- Test: `internal/runtime/channelmeta/cache_test.go`
- Test: `internal/runtime/channelmeta/resolver_test.go`
- Test: `internal/usecase/message/retry_test.go`

- [ ] **Step 1: Write failing activation cache invalidation test**

Add to `internal/runtime/channelmeta/cache_test.go`:

```go
func TestActivationCacheInvalidateDropsOnePositiveEntry(t *testing.T) {
	cache := &ActivationCache{}
	key := channel.ChannelKey("channel/1/dTE")
	other := channel.ChannelKey("channel/1/dTI")
	now := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	cache.StorePositive(key, channel.Meta{Key: key, Leader: 1, LeaseUntil: now.Add(time.Minute), Status: channel.StatusActive}, now)
	cache.StorePositive(other, channel.Meta{Key: other, Leader: 1, LeaseUntil: now.Add(time.Minute), Status: channel.StatusActive}, now)

	cache.Invalidate(key)

	_, ok := cache.LoadPositive(key, now)
	require.False(t, ok)
	_, ok = cache.LoadPositive(other, now)
	require.True(t, ok)
}
```

- [ ] **Step 2: Write failing healthy cache test**

Add to `internal/runtime/channelmeta/resolver_test.go`:

```go
func TestRefreshChannelMetaUsesHealthyBusinessCache(t *testing.T) {
	id := channel.ChannelID{ID: "g-fast", Type: 2}
	now := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	source := &resolverSourceFake{get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{id: {
		ChannelID: id.ID, ChannelType: int64(id.Type), ChannelEpoch: 1, LeaderEpoch: 1,
		Leader: 1, Replicas: []uint64{1}, ISR: []uint64{1}, MinISR: 1,
		Status: uint8(channel.StatusActive), LeaseUntilMS: now.Add(time.Minute).UnixMilli(),
	}}}
	syncer := NewSync(SyncOptions{Source: source, Runtime: &resolverRuntimeFake{}, LocalNode: 1, Now: func() time.Time { return now }})

	_, err := syncer.RefreshChannelMeta(context.Background(), id)
	require.NoError(t, err)
	_, err = syncer.RefreshChannelMeta(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, 1, source.getCalls)
}
```

- [ ] **Step 3: Write failing meta refresh observer test**

Add to `internal/runtime/channelmeta/resolver_test.go`:

```go
func TestRefreshChannelMetaReportsCacheHitAndRefreshOutcomes(t *testing.T) {
	id := channel.ChannelID{ID: "g-observe", Type: 2}
	now := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	observer := &recordingMetaRefreshObserver{}
	source := &resolverSourceFake{get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{id: {
		ChannelID: id.ID, ChannelType: int64(id.Type), ChannelEpoch: 1, LeaderEpoch: 1,
		Leader: 1, Replicas: []uint64{1}, ISR: []uint64{1}, MinISR: 1,
		Status: uint8(channel.StatusActive), LeaseUntilMS: now.Add(time.Minute).UnixMilli(),
	}}}
	syncer := NewSync(SyncOptions{
		Source: source,
		Runtime: &resolverRuntimeFake{},
		LocalNode: 1,
		Now: func() time.Time { return now },
		MetaRefreshObserver: observer,
	})

	_, err := syncer.RefreshChannelMeta(context.Background(), id)
	require.NoError(t, err)
	_, err = syncer.RefreshChannelMeta(context.Background(), id)
	require.NoError(t, err)

	require.Equal(t, []MetaRefreshResult{MetaRefreshAuthoritativeRead, MetaRefreshCacheHit}, observer.results())
}
```

- [ ] **Step 4: Run tests to verify failure**

Run:

```bash
GOWORK=off go test ./internal/runtime/channelmeta -run 'TestActivationCacheInvalidateDropsOnePositiveEntry|TestRefreshChannelMetaUsesHealthyBusinessCache|TestRefreshChannelMetaReportsCacheHitAndRefreshOutcomes' -count=1
```

Expected: FAIL because cache invalidation, business-cache fast path, and meta refresh observer results do not exist.

- [ ] **Step 5: Define meta refresh observer contract**

In `internal/runtime/channelmeta/interfaces.go` add:

```go
type MetaRefreshResult string

const (
	MetaRefreshCacheHit MetaRefreshResult = "cache_hit"
	MetaRefreshAuthoritativeRead MetaRefreshResult = "authoritative_read"
	MetaRefreshBootstrap MetaRefreshResult = "bootstrap"
	MetaRefreshRepair MetaRefreshResult = "repair"
	MetaRefreshError MetaRefreshResult = "error"
)

type MetaRefreshEvent struct {
	ChannelID channel.ChannelID
	Key channel.ChannelKey
	Result MetaRefreshResult
	Duration time.Duration
	Err error
}

type MetaRefreshObserver interface {
	OnMetaRefresh(MetaRefreshEvent)
}
```

Update imports in `internal/runtime/channelmeta/interfaces.go` for `time` and `pkg/channel` if they are not already present.

Add `MetaRefreshObserver MetaRefreshObserver` to `SyncOptions` and `metaRefreshObserver MetaRefreshObserver` to `Sync`. `RefreshChannelMeta` / `ActivateByID` must emit exactly one event per business refresh attempt: `cache_hit` on healthy cache hit, `bootstrap` when missing metadata is bootstrapped, `repair` when repairer changes/validates stale metadata, `authoritative_read` for normal authoritative reads, and `error` on failures.

- [ ] **Step 6: Define healthy cache contract**

In `internal/runtime/channelmeta/cache.go`, change the positive cache entry to retain the authoritative source metadata used to create the applied routing meta:

```go
type cachedChannelMeta struct {
	meta             channel.Meta
	authoritative    metadb.ChannelRuntimeMeta
	hasAuthoritative bool
	expiresAt        time.Time
}
```

Add `StoreAuthoritativePositive(key, applied, authoritative, now)` that sets `hasAuthoritative=true`, and keep `StorePositive` as a test/backward-compatible wrapper that stores only `meta` with `hasAuthoritative=false`. `cachedHealthyBusinessMeta(key)` must return a cache hit only when:

- `meta.Status == channel.StatusActive`.
- `meta.Leader != 0`.
- `meta.Epoch != 0` and `meta.LeaderEpoch != 0`.
- `meta.LeaseUntil.After(s.Now().Add(channelMetaBusinessCacheRefreshLeadTime))`.
- Node liveness for `meta.Leader` is absent or not dead/draining.
- `hasAuthoritative == true`; if `repairPolicy != nil`, `repairPolicy(authoritative)` returns `need == false`.

This avoids reconstructing `metadb.ChannelRuntimeMeta` from lossy `channel.Meta` and gives the implementation a concrete repair-policy input.

- [ ] **Step 7: Implement cache helpers**

In `internal/runtime/channelmeta/cache.go` add:

```go
func (c *ActivationCache) Invalidate(key channel.ChannelKey) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.positive != nil {
		delete(c.positive, key)
	}
	if c.negative != nil {
		delete(c.negative, key)
	}
	c.mu.Unlock()
}
```

In `internal/runtime/channelmeta/resolver.go` add:

```go
const channelMetaBusinessCacheRefreshLeadTime = time.Second

func (s *Sync) InvalidateChannelMeta(id channel.ChannelID) {
	if s == nil {
		return
	}
	s.cache.Invalidate(channelhandler.KeyFromChannelID(id))
}
```

Add helper `cachedHealthyBusinessMeta(key)` that implements the healthy cache contract from Step 4 exactly and returns `(channel.Meta, bool)`.

- [ ] **Step 8: Update business refresh flow**

In `RefreshChannelMeta` / `ActivateByID`, before authoritative load for `ActivationSourceBusiness`, check `cachedHealthyBusinessMeta`. On hit, return it and record metrics result `cache_hit` if metrics are wired by Task 1.

Do not use negative cache for business sends; missing metadata still needs bootstrap.

- [ ] **Step 9: Write failing send retry test**

Add to `internal/usecase/message/retry_test.go`:

```go
func TestSendWithEnsuredMetaInvalidatesCacheAndRetriesAfterStaleAppend(t *testing.T) {
	refresher := &recordingInvalidatingRefresher{
		metas: []channel.Meta{
			{Leader: 1, Epoch: 1, LeaderEpoch: 1, ID: channel.ChannelID{ID: "g1", Type: frame.ChannelTypeGroup}},
			{Leader: 1, Epoch: 2, LeaderEpoch: 2, ID: channel.ChannelID{ID: "g1", Type: frame.ChannelTypeGroup}},
		},
	}
	cluster := &flakyAppendCluster{errs: []error{channel.ErrStaleMeta}, result: channel.AppendResult{MessageID: 7, MessageSeq: 1}}

	result, err := sendWithEnsuredMeta(context.Background(), 1, time.Now, wklog.NewNop(), cluster, nil, refresher, channel.AppendRequest{
		ChannelID: channel.ChannelID{ID: "g1", Type: frame.ChannelTypeGroup},
		Message: channel.Message{FromUID: "u1"},
	})

	require.NoError(t, err)
	require.Equal(t, uint64(7), result.MessageID)
	require.Equal(t, 1, refresher.invalidations)
	require.Equal(t, 2, refresher.refreshCalls)
	require.Equal(t, 2, cluster.appendCalls)
}
```

- [ ] **Step 10: Implement one forced refresh retry**

In `internal/usecase/message/deps.go`, add optional interface:

```go
type MetaInvalidator interface {
	InvalidateChannelMeta(id channel.ChannelID)
}
```

In `sendWithEnsuredMeta`, wrap append in a helper:

```go
func appendWithMeta(ctx context.Context, meta channel.Meta, req channel.AppendRequest) (channel.AppendResult, error)
```

Add `isRetryableMetaAppendError(err)` in `internal/usecase/message/retry.go` that checks `channel.ErrStaleMeta`, `channel.ErrNotLeader`, `channel.ErrLeaseExpired`, and `raftcluster.ErrRerouted` from `pkg/cluster`. On those errors, call invalidator when supported, force a new refresh, and retry exactly once.

- [ ] **Step 11: Run focused tests**

Run:

```bash
GOWORK=off go test ./internal/runtime/channelmeta -run 'TestActivationCache|TestRefreshChannelMeta.*Cache|TestRefreshChannelMetaReports|TestChannelMeta.*Repair' -count=1
GOWORK=off go test ./internal/usecase/message -run 'TestSendWithEnsuredMeta|TestSend' -count=1
```

Expected: PASS.

- [ ] **Step 12: Commit**

```bash
git add internal/runtime/channelmeta internal/usecase/message
git commit -m "feat: add channel metadata send fast path"
```

---

### Task 4: Replace Per-Message Committed Goroutines With Bounded Sharded Dispatch

**Files:**
- Modify: `internal/app/deliveryrouting.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/app.go` — add `committedDispatcher *asyncCommittedDispatcher` and `committedDispatcherOn atomic.Bool` fields.
- Test: `internal/app/deliveryrouting_test.go`

- [ ] **Step 1: Write failing ordering test**

Add to `internal/app/deliveryrouting_test.go`:

```go
func TestCommittedDispatchQueuePreservesPerChannelOrder(t *testing.T) {
	delivery := &recordingCommittedSubmitter{}
	dispatcher := newAsyncCommittedDispatcher(asyncCommittedDispatcherConfig{
		LocalNodeID: 1,
		PreferLocal: true,
		Delivery: delivery,
		ShardCount: 1,
		QueueDepth: 8,
	})
	require.NoError(t, dispatcher.Start(context.Background()))
	defer func() { require.NoError(t, dispatcher.Stop()) }()

	for seq := uint64(1); seq <= 3; seq++ {
		require.NoError(t, dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{Message: channel.Message{
			ChannelID: "g1", ChannelType: frame.ChannelTypeGroup, MessageID: seq, MessageSeq: seq,
		}}))
	}

	require.Eventually(t, func() bool { return len(delivery.calls) == 3 }, time.Second, time.Millisecond)
	require.Equal(t, []uint64{1, 2, 3}, delivery.messageSeqs())
}
```

- [ ] **Step 2: Write failing overflow test**

```go
func TestCommittedDispatchQueueOverflowDoesNotFailCommittedSubmit(t *testing.T) {
	conversation := &recordingFlushingConversationSubmitter{}
	dispatcher := newAsyncCommittedDispatcher(asyncCommittedDispatcherConfig{
		LocalNodeID: 1,
		PreferLocal: true,
		Conversation: conversation,
		ShardCount: 1,
		QueueDepth: 1,
		DisableWorkersForTest: true,
	})
	require.NoError(t, dispatcher.Start(context.Background()))
	defer func() { require.NoError(t, dispatcher.Stop()) }()

	require.NoError(t, dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{Message: channel.Message{ChannelID: "g1", ChannelType: 2, MessageSeq: 1}}))
	require.NoError(t, dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{Message: channel.Message{ChannelID: "g1", ChannelType: 2, MessageSeq: 2}}))
	require.Equal(t, 1, conversation.flushCalls)
}
```

- [ ] **Step 3: Run tests to verify failure**

Run: `GOWORK=off go test ./internal/app -run 'TestCommittedDispatchQueue' -count=1`

Expected: FAIL because dispatcher constructor/lifecycle/queue do not exist.

- [ ] **Step 4: Implement dispatcher config and lifecycle**

Refactor `asyncCommittedDispatcher` into a pointer type with constructor:

```go
type asyncCommittedDispatcherConfig struct {
	LocalNodeID uint64
	PreferLocal bool
	Logger wklog.Logger
	ChannelLog interface{ Status(channel.ChannelID) (channel.ChannelRuntimeStatus, error) }
	Delivery committedDeliverySubmitter
	Conversation committedConversationSubmitter
	NodeClient committedNodeSubmitter
	Metrics interface {
		SetCommittedDispatchQueueDepth(shard string, depth int)
		ObserveCommittedDispatchEnqueue(shard, result string)
		ObserveCommittedDispatchOverflow(shard string)
	}
	ShardCount int
	QueueDepth int
	DisableWorkersForTest bool
}
```

Use defaults:

```go
const (
	committedDispatchDefaultQueueDepth = 4096
	committedDispatchMinShards = 4
	committedDispatchMaxShards = 32
)
```

Implement:

```go
func newAsyncCommittedDispatcher(cfg asyncCommittedDispatcherConfig) *asyncCommittedDispatcher
func (d *asyncCommittedDispatcher) Start(ctx context.Context) error
func (d *asyncCommittedDispatcher) Stop() error
func (d *asyncCommittedDispatcher) SubmitCommitted(ctx context.Context, event messageevents.MessageCommitted) error
```

`SubmitCommitted` selects shard by FNV of `ChannelID + ChannelType`, clones the event, non-blocking enqueues, updates enqueue/depth metrics, calls `ObserveCommittedDispatchOverflow` on full queues, and falls back to conversation flush on overflow.

- [ ] **Step 5: Preserve existing route logic**

Keep `routeCommitted`, `submitLocal`, `submitConversation`, and `submitConversationFallback` behavior equivalent to current code. Workers call `routeCommitted` for dequeued events.

- [ ] **Step 6: Wire in build and lifecycle**

In `internal/app/build.go`, construct the dispatcher once and pass it to `committedFanout`. Start it through app lifecycle before gateway/API accepts sends and stop it after gateway/API stops accepting new work. Add `appLifecycleCommittedDispatcher = "committed_dispatcher"` and place it after `conversation_projector` and before `committed_replay`; it must be active before gateway accepts sends.

- [ ] **Step 7: Run focused tests**

Run:

```bash
GOWORK=off go test ./internal/app -run 'TestAsyncCommittedDispatcher|TestCommittedDispatchQueue' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/app/deliveryrouting.go internal/app/build.go internal/app/app.go internal/app/*lifecycle* internal/app/deliveryrouting_test.go
git commit -m "feat: bound committed delivery dispatch"
```

---

### Task 5: Batch Remote Delivery Push Per Committed Message

**Files:**
- Modify: `internal/app/deliveryrouting.go`
- Modify: `internal/access/node/client.go`
- Modify: `internal/access/node/delivery_push_rpc.go`
- Test: `internal/app/deliveryrouting_test.go`
- Test: `internal/access/node/delivery_push_rpc_test.go`

- [ ] **Step 1: Refactor remote push client dependency for testability**

In `internal/app/deliveryrouting.go`, replace the concrete `*accessnode.Client` field on `distributedDeliveryPush` with a narrow interface:

```go
type deliveryPushClient interface {
	PushBatch(ctx context.Context, nodeID uint64, cmd accessnode.DeliveryPushCommand) (accessnode.DeliveryPushResponse, error)
	PushBatchItems(ctx context.Context, nodeID uint64, cmd accessnode.DeliveryPushBatchCommand) (accessnode.DeliveryPushResponse, error)
}
```

If `deliveryPushResponse` is currently unexported, export it as `DeliveryPushResponse` from `internal/access/node` so both the real client and `recordingDeliveryPushClient` test fake can satisfy this interface. Keep the real `*accessnode.Client` assignment in `internal/app/build.go` unchanged because it should satisfy the interface.

- [ ] **Step 2: Write failing app-level batching test**

Add to `internal/app/deliveryrouting_test.go`:

```go
func TestDistributedDeliveryPushBatchesGroupRoutesByNode(t *testing.T) {
	client := &recordingDeliveryPushClient{}
	push := distributedDeliveryPush{localNodeID: 1, client: client, codec: codec.New(), logger: wklog.NewNop()}

	result, err := push.Push(context.Background(), deliveryruntime.PushCommand{
		Envelope: deliveryruntime.CommittedEnvelope{Message: channel.Message{
			ChannelID: "g1", ChannelType: frame.ChannelTypeGroup, MessageID: 10, MessageSeq: 3, FromUID: "sender", Payload: []byte("hello"),
		}},
		Routes: []deliveryruntime.RouteKey{
			{UID: "u2", NodeID: 2, BootID: 22, SessionID: 201},
			{UID: "u3", NodeID: 2, BootID: 22, SessionID: 202},
		},
	})

	require.NoError(t, err)
	require.Len(t, client.batchItemCalls, 1)
	require.Len(t, client.batchItemCalls[0].Items, 1)
	require.Len(t, client.batchItemCalls[0].Items[0].Routes, 2)
	require.Len(t, result.Accepted, 2)
}
```

- [ ] **Step 3: Write failing person view isolation test**

Add to `internal/app/deliveryrouting_test.go`:

```go
func TestDistributedDeliveryPushBatchesPersonRoutesByRecipientView(t *testing.T) {
	client := &recordingDeliveryPushClient{}
	push := distributedDeliveryPush{localNodeID: 1, client: client, codec: codec.New(), logger: wklog.NewNop()}
	channelID := deliveryusecase.EncodePersonChannel("u1", "u2")

	_, err := push.Push(context.Background(), deliveryruntime.PushCommand{
		Envelope: deliveryruntime.CommittedEnvelope{Message: channel.Message{
			ChannelID: channelID, ChannelType: frame.ChannelTypePerson, MessageID: 11, MessageSeq: 4, FromUID: "u1", Payload: []byte("hello"),
		}},
		Routes: []deliveryruntime.RouteKey{
			{UID: "u1", NodeID: 2, BootID: 22, SessionID: 201},
			{UID: "u2", NodeID: 2, BootID: 22, SessionID: 202},
		},
	})

	require.NoError(t, err)
	require.Len(t, client.batchItemCalls, 1)
	require.Len(t, client.batchItemCalls[0].Items, 2)
	frames := decodeBatchItemFrames(t, codec.New(), client.batchItemCalls[0].Items)
	require.Equal(t, "u2", frames["u1"].ChannelID)
	require.Equal(t, "u1", frames["u2"].ChannelID)
}
```

- [ ] **Step 4: Run app batching tests to verify failure**

Run: `GOWORK=off go test ./internal/app -run 'TestDistributedDeliveryPush.*Batch|TestDistributedDeliveryPush.*Person' -count=1`

Expected: FAIL because remote push still sends per UID through `PushBatch`.

- [ ] **Step 5: Define batch item DTOs**

In `internal/access/node/delivery_push_rpc.go` add:

```go
type DeliveryPushItem struct {
	ChannelID   string                     `json:"channel_id"`
	ChannelType uint8                      `json:"channel_type"`
	MessageID   uint64                     `json:"message_id"`
	MessageSeq  uint64                     `json:"message_seq"`
	Routes      []deliveryruntime.RouteKey `json:"routes"`
	Frame       []byte                     `json:"frame"`
}

type DeliveryPushBatchCommand struct {
	OwnerNodeID uint64             `json:"owner_node_id"`
	Items       []DeliveryPushItem `json:"items"`
}
```

Keep the existing `DeliveryPushCommand` for compatibility with current tests and adapt it into a one-item batch internally.

- [ ] **Step 6: Add client method**

In `internal/access/node/client.go` add:

```go
func (c *Client) PushBatchItems(ctx context.Context, nodeID uint64, cmd DeliveryPushBatchCommand) (DeliveryPushResponse, error)
```

It should call the same `deliveryPushRPCServiceID` and decode `DeliveryPushResponse`.

- [ ] **Step 7: Update RPC handler**

In `handleDeliveryPushRPC`, decode either:

- New shape with `items`.
- Old shape with `routes` and `frame`.

Process each item with current fencing checks and append accepted/retryable/dropped routes to aggregate response. Bind `OwnerNodeID` in `deliveryAckIndex` for each accepted remote route exactly as today.

- [ ] **Step 8: Update distributed pusher grouping**

In `distributedDeliveryPush.Push`:

- Keep local route handling unchanged.
- For group channels, use one frame per target node.
- For person channels, group routes by `recipientChannelView(message, route.UID)` because that determines frame identity.
- Send one `PushBatchItems` RPC per target node.
- Preserve current result aggregation and logging.

- [ ] **Step 9: Run focused tests**

Run:

```bash
GOWORK=off go test ./internal/access/node -run 'TestPushBatchRPC|TestDeliveryPush' -count=1
GOWORK=off go test ./internal/app -run 'TestDistributedDeliveryPush|TestThreeNodeAppGroupChannelRealtimeDeliveryUsesStoredSubscribers' -count=1
```

Expected: PASS for default unit tests. If the three-node test is integration-tagged or slow in the current branch, run the focused unit tests and record the integration validation command in the final handoff.

- [ ] **Step 10: Commit**

```bash
git add internal/app/deliveryrouting.go internal/app/deliveryrouting_test.go internal/access/node/client.go internal/access/node/delivery_push_rpc.go internal/access/node/delivery_push_rpc_test.go
git commit -m "feat: batch remote realtime delivery push"
```

---

### Task 6: Wire Metrics Instrumentation and Replay Lag

**Files:**
- Modify: `internal/usecase/message/retry.go`
- Modify: `internal/app/deliveryrouting.go`
- Modify: `internal/app/delivery_lifecycle.go`
- Modify: `internal/app/committed_replay.go`
- Modify: `internal/app/build.go`
- Test: `internal/usecase/message/retry_test.go`
- Test: `internal/app/deliveryrouting_test.go`
- Test: `internal/app/committed_replay_test.go`

- [ ] **Step 1: Write failing instrumentation tests**

Add minimal fake metrics tests rather than scraping Prometheus in app packages:

```go
func TestAsyncCommittedDispatcherRecordsQueueMetrics(t *testing.T) {
	metrics := &recordingMessageMetrics{}
	dispatcher := newAsyncCommittedDispatcher(asyncCommittedDispatcherConfig{
		LocalNodeID: 1, PreferLocal: true, ShardCount: 1, QueueDepth: 1,
		Metrics: metrics, DisableWorkersForTest: true,
	})
	require.NoError(t, dispatcher.Start(context.Background()))
	defer func() { _ = dispatcher.Stop() }()

	require.NoError(t, dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{Message: channel.Message{ChannelID: "g1", ChannelType: 2, MessageSeq: 1}}))
	require.NoError(t, dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{Message: channel.Message{ChannelID: "g1", ChannelType: 2, MessageSeq: 2}}))

	require.Contains(t, metrics.enqueueResults, "ok")
	require.Contains(t, metrics.enqueueResults, "overflow")
	require.NotEmpty(t, metrics.queueDepths)
}
```

Add these focused tests as well:

- `TestSendWithEnsuredMetaRecordsAppendMetrics` in `internal/usecase/message/retry_test.go`: use a fake metrics sink and assert one append observation.
- `TestChannelMetaSyncObserverRecordsMetaRefreshMetrics` in `internal/runtime/channelmeta/resolver_test.go` or `internal/app/channelmeta_test.go`: use the `MetaRefreshObserver` contract from Task 3 and assert `app.metrics.Message.ObserveMetaRefresh` receives cache-hit and authoritative-read outcomes through an app adapter.
- `TestCommittedReplayerRecordsReplayLag` in `internal/app/committed_replay_test.go`: configure cursor below committed seq and assert the fake metrics sink receives the lag value before replay advances the cursor.
- `TestCommittedReplayerRecordsPassDuration` in `internal/app/committed_replay_test.go`: run one replay pass and assert the fake metrics sink receives an `ok` result duration; inject a failing log source and assert `error` result duration.
- `TestDistributedDeliveryPushRecordsPushRouteMetrics` in `internal/app/deliveryrouting_test.go`: use a fake delivery metrics sink and assert both RPC count and route count are recorded for one batched remote push.
- `TestDeliveryRuntimeObserverRecordsRouteExpiryMetric` in `internal/app/delivery_lifecycle_test.go`: expire one route and assert the fake delivery metrics sink receives one route-expired event.

- [ ] **Step 2: Run instrumentation tests to verify failure**

Run:

```bash
GOWORK=off go test ./internal/runtime/channelmeta ./internal/usecase/message ./internal/app -run 'Test.*Metrics|Test.*ReplayLag|Test.*ReplayPass|TestChannelMetaSyncObserver' -count=1
```

Expected: FAIL until instrumentation interfaces are wired.

- [ ] **Step 3: Define narrow observer interfaces**

Avoid importing `pkg/metrics` directly into low-level packages when a narrow interface is enough. Add local interfaces near the code that uses them:

```go
type messageAppendMetrics interface {
	ObserveAppend(path, result string, dur time.Duration)
}

type channelMetaMetrics interface {
	ObserveMetaRefresh(result string, dur time.Duration)
}
```

`internal/app/build.go` passes `app.metrics.Message` to message/replay/committed dispatcher components and `app.metrics.Delivery` to delivery resolver/push/lifecycle components through narrow option interfaces.

- [ ] **Step 4: Instrument send/meta/append**

In `sendWithEnsuredMeta`, record only append duration/results through `messageAppendMetrics`: `local/ok`, `local/error`, `remote/ok`, `remote/error`. Meta refresh outcome metrics are emitted by `channelmeta.Sync` through the `MetaRefreshObserver` from Task 3, and `internal/app` adapts those events to `app.metrics.Message.ObserveMetaRefresh(string(event.Result), event.Duration)`.

- [ ] **Step 5: Instrument delivery resolve and push**

In `localDeliveryResolver.ResolvePage`, measure each page resolution duration and count page/routes.

In `distributedDeliveryPush.Push`, measure per target node batch RPC duration and route count. Use result `ok` when RPC succeeds, `error` when RPC returns error, and `partial` when the response has retryable routes.

In delivery lifecycle, set inflight and ack binding gauges after each retry tick/sweep. In the delivery observer adapter from Task 2, call `DeliveryMetrics.ObserveRouteExpired(channelType)` for each `RouteExpiredEvent`; add a focused test that expires one route and asserts the fake delivery metrics sink received one expiry.

- [ ] **Step 6: Instrument committed replay lag**

In `committedReplayer.replayChannel`, after loading cursor and committed seq, compute `committedSeq - cursor` and record it through `SetCommittedReplayLag(channelType, lag)`. In `RunOnce` / `runOnceAndLog`, record replay pass duration through `ObserveCommittedReplayPass(result, dur)` with `ok`, `error`, or `canceled`.

- [ ] **Step 7: Run focused tests**

Run:

```bash
GOWORK=off go test ./internal/usecase/message ./internal/app -run 'Test.*Metrics|Test.*Replay|TestAsyncCommittedDispatcher|TestDistributedDeliveryPush' -count=1
GOWORK=off go test ./pkg/metrics -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/usecase/message internal/app pkg/metrics
git commit -m "feat: instrument message delivery performance"
```

---

### Task 7: Update Flow Docs and Run Final Verification

**Files:**
- Modify: `internal/FLOW.md`
- Modify: `docs/development/PROJECT_KNOWLEDGE.md` only if a new concise durable rule is discovered

- [ ] **Step 1: Update `internal/FLOW.md`**

Document these changes:

- Message send uses a healthy channel metadata cache fast path with forced refresh retry on stale/leader/lease errors.
- Committed message delivery uses bounded sharded workers instead of goroutine-per-message routing.
- Delivery runtime lifecycle processes retry ticks and idle sweeping.
- Remote push batches routes per target node within one committed message.
- Route retry expiry removes ack binding and falls back to durable catch-up through Channel Log.

- [ ] **Step 2: Check whether project knowledge needs an entry**

Only when implementation adds the durable route-expiry rule, add this short bullet to `docs/development/PROJECT_KNOWLEDGE.md`:

```markdown
- Realtime delivery retry state is node-local and bounded; retry expiry must remove ack bindings and rely on Channel Log catch-up instead of pinning routes indefinitely.
```

If this is already covered by `internal/FLOW.md`, do not modify project knowledge.

- [ ] **Step 3: Run unit test suite for changed areas**

Run:

```bash
GOWORK=off go test ./pkg/metrics ./internal/runtime/delivery ./internal/runtime/channelmeta ./internal/usecase/message ./internal/access/node ./internal/app -count=1
```

Expected: PASS.

- [ ] **Step 4: Run boundary check**

Run: `GOWORK=off go test ./internal -run TestInternalImportBoundaries -count=1`

Expected: PASS.

- [ ] **Step 5: Run broader non-integration test if time allows**

Run: `GOWORK=off go test ./internal/... ./pkg/...`

Expected: PASS. If this is too slow for the current session, record which focused tests passed and which broad tests remain.

- [ ] **Step 6: Optional performance validation**

Run targeted benchmarks or stress tests created during implementation. At minimum capture:

- Hot channel send benchmark before/after metadata cache hit.
- Remote group push RPC count before/after batching.
- Goroutine count under committed dispatch burst.

Add a small benchmark near the implementation if no existing benchmark covers the optimized path, then run it with `-bench`.

- [ ] **Step 7: Commit docs and verification updates**

```bash
git add internal/FLOW.md docs/development/PROJECT_KNOWLEDGE.md
git commit -m "docs: update message delivery performance flow"
```

Skip `docs/development/PROJECT_KNOWLEDGE.md` in the commit if it was not changed.

---

## Final Handoff Checklist

- [ ] All task commits are present and ordered.
- [ ] Focused tests pass.
- [ ] `GOWORK=off go test ./internal -run TestInternalImportBoundaries -count=1` passes.
- [ ] If broad tests or integration tests were skipped due runtime, final response names them explicitly.
- [ ] Metrics names use `wukongim_` prefix and duration histograms use `_duration_seconds`.
- [ ] No new public config was added without updating `wukongim.conf.example` and English config comments.
- [ ] No single-node non-cluster branch was introduced.

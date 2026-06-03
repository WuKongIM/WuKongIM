# ChannelV2 10k Live Channels Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a three-node ChannelV2 deployment keep 10,000 local channel runtimes active with low idle pull traffic, bounded activation, and cached append metadata.

**Architecture:** Keep the existing per-channel single-writer reactor model. Add observability and benchmark evidence first, then wire node-local runtime capacity, clusterv2 append metadata caching, parked follower recovery probes, and low-risk memory tightening behind existing package boundaries.

**Tech Stack:** Go 1.23, `testing`, `testify/require`, Prometheus metrics in `pkg/metrics`, ChannelV2 reactor/service/clusterv2 packages, existing `wkbench`/perf-triage documentation.

---

## File Structure

- Modify `pkg/channelv2/reactor/metrics.go`: add optional observer interfaces for runtime counts, activation rejects, pull classification, parked follower counts, and recovery probes.
- Modify `pkg/channelv2/reactor/reactor.go`: track per-reactor active counts, enforce max channel budget during activation, and lazy-initialize per-channel maps.
- Modify `pkg/channelv2/reactor/group.go`: propagate max-channel and follower-recovery config into reactors and split node budget across reactor partitions.
- Modify `pkg/channelv2/reactor/follower_replication.go`: park caught-up followers and wake them through PullHint or jittered recovery probes.
- Modify `pkg/channelv2/reactor/replication_state.go`: add parked recovery-probe state and helper methods.
- Modify `pkg/channelv2/reactor/effect.go`: update defaults for follower recovery probe interval/jitter and lazy map helpers.
- Modify `pkg/channelv2/service/service.go`: expose new ChannelV2 service config fields.
- Modify `pkg/channelv2/channel.go`: expose new public config fields with English comments.
- Modify `pkg/clusterv2/config.go`, `pkg/clusterv2/channels/service.go`, `pkg/clusterv2/node_defaults.go`: propagate ChannelV2 max-channel and follower-recovery settings and add append metadata cache.
- Create `pkg/clusterv2/channels/meta_cache.go`: concurrency-safe ChannelMeta cache and optional cache observer.
- Modify `pkg/metrics/channelv2.go`, `pkg/metrics/registry_test.go`: add low-cardinality metrics.
- Modify `internalv2/app/observability.go`: map optional reactor/channel observer events into metrics.
- Modify `cmd/wukongimv2/config.go`, `cmd/wukongimv2/config_test.go`, `cmd/wukongimv2/*.conf.example`, `scripts/wukongimv2/*.conf`, and `wukongim.conf.example`: parse and document new config keys.
- Modify `pkg/channelv2/bench_test.go`: add 10k live-channel and sparse-traffic benchmarks.
- Create `docs/development/perf-runs/20260529-channelv2-10k-live-channels/README.md`: evidence checklist and benchmark commands aligned with `docs/development/PERF_TRIAGE.md`.
- Update `pkg/channelv2/FLOW.md` and `pkg/channelv2/reactor/FLOW.md`: document parked follower semantics and capacity bounds.

## Task 1: Observability Contract And Metrics

**Files:**
- Modify: `pkg/channelv2/reactor/metrics.go`
- Modify: `pkg/metrics/channelv2.go`
- Modify: `pkg/metrics/registry_test.go`
- Modify: `internalv2/app/observability.go`
- Modify: `pkg/channelv2/bench_test.go`
- Modify: `pkg/channelv2/reactor/group_test.go`
- Modify: `pkg/channelv2/testkit/cluster_test.go`

- [ ] **Step 1: Extend the metrics registry test**

Add the extra observations inside `TestChannelV2MetricsTrackReactorAndWorkerRuntime` in `pkg/metrics/registry_test.go`:

```go
	reg.ChannelV2.SetChannelRuntimeCount(2, "leader", 17)
	reg.ChannelV2.ObserveChannelActivationRejected("max_channels")
	reg.ChannelV2.SetFollowerParkedCount(2, 11)
	reg.ChannelV2.ObserveFollowerRecoveryProbe("ok")
	reg.ChannelV2.ObservePull("ok", true)
	reg.ChannelV2.ObserveMetaCache("hit")
	reg.ChannelV2.ObserveMetaCache("invalidate")
```

After the existing `rpcPullTotal` assertion, add:

```go
	runtimes := requireMetricFamily(t, families, "wukongim_channelv2_active_runtimes")
	findMetricByLabels(t, runtimes, map[string]string{
		"node_id":    "8",
		"node_name":  "node-8",
		"reactor_id": "2",
		"role":       "leader",
	})

	rejected := requireMetricFamily(t, families, "wukongim_channelv2_activation_rejected_total")
	findMetricByLabels(t, rejected, map[string]string{
		"node_id":   "8",
		"node_name": "node-8",
		"reason":    "max_channels",
	})

	parked := requireMetricFamily(t, families, "wukongim_channelv2_follower_parked")
	findMetricByLabels(t, parked, map[string]string{
		"node_id":    "8",
		"node_name":  "node-8",
		"reactor_id": "2",
	})

	recovery := requireMetricFamily(t, families, "wukongim_channelv2_recovery_probe_total")
	findMetricByLabels(t, recovery, map[string]string{
		"node_id":   "8",
		"node_name": "node-8",
		"result":    "ok",
	})

	pull := requireMetricFamily(t, families, "wukongim_channelv2_pull_total")
	findMetricByLabels(t, pull, map[string]string{
		"node_id":   "8",
		"node_name": "node-8",
		"result":    "ok",
		"empty":     "true",
	})

	metaCache := requireMetricFamily(t, families, "wukongim_channelv2_meta_cache_total")
	findMetricByLabels(t, metaCache, map[string]string{
		"node_id":   "8",
		"node_name": "node-8",
		"result":    "hit",
	})
	findMetricByLabels(t, metaCache, map[string]string{
		"node_id":   "8",
		"node_name": "node-8",
		"result":    "invalidate",
	})
```

- [ ] **Step 2: Run the failing metrics test**

Run:

```bash
go test ./pkg/metrics -run TestChannelV2MetricsTrackReactorAndWorkerRuntime -count=1
```

Expected: FAIL because `ChannelV2Metrics` does not yet define `SetChannelRuntimeCount`, `ObserveChannelActivationRejected`, `SetFollowerParkedCount`, `ObserveFollowerRecoveryProbe`, `ObservePull`, or `ObserveMetaCache`.

- [ ] **Step 3: Add optional observer interfaces**

In `pkg/channelv2/reactor/metrics.go`, keep `Observer` unchanged and add:

```go
// RuntimeObserver receives low-cardinality active-runtime metrics.
type RuntimeObserver interface {
	SetChannelRuntimeCount(reactorID int, role ch.Role, count int)
	ObserveChannelActivationRejected(reason string)
}

// ReplicationObserver receives follower replication scheduling metrics.
type ReplicationObserver interface {
	SetFollowerParkedCount(reactorID int, count int)
	ObserveFollowerRecoveryProbe(result string)
	ObservePull(result string, empty bool)
}
```

Also add helpers:

```go
func (r *Reactor) observeRuntimeCount(role ch.Role, count int) {
	if observer, ok := r.cfg.Observer.(RuntimeObserver); ok {
		observer.SetChannelRuntimeCount(r.cfg.ID, role, count)
	}
}

func (r *Reactor) observeActivationRejected(reason string) {
	if observer, ok := r.cfg.Observer.(RuntimeObserver); ok {
		observer.ObserveChannelActivationRejected(reason)
	}
}

func (r *Reactor) observeFollowerParkedCount(count int) {
	if observer, ok := r.cfg.Observer.(ReplicationObserver); ok {
		observer.SetFollowerParkedCount(r.cfg.ID, count)
	}
}

func (r *Reactor) observeRecoveryProbe(result string) {
	if observer, ok := r.cfg.Observer.(ReplicationObserver); ok {
		observer.ObserveFollowerRecoveryProbe(result)
	}
}

func (r *Reactor) observePull(result string, empty bool) {
	if observer, ok := r.cfg.Observer.(ReplicationObserver); ok {
		observer.ObservePull(result, empty)
	}
}
```

- [ ] **Step 4: Implement new Prometheus metrics**

In `pkg/metrics/channelv2.go`, add fields to `ChannelV2Metrics`:

```go
	activeRuntimes          *prometheus.GaugeVec
	activationRejectedTotal *prometheus.CounterVec
	followerParked          *prometheus.GaugeVec
	recoveryProbeTotal      *prometheus.CounterVec
	pullTotal               *prometheus.CounterVec
	metaCacheTotal          *prometheus.CounterVec
```

Initialize them in `newChannelV2Metrics`:

```go
		activeRuntimes: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelv2_active_runtimes",
			Help:        "Number of active ChannelV2 runtimes per reactor and role.",
			ConstLabels: labels,
		}, []string{"reactor_id", "role"}),
		activationRejectedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_channelv2_activation_rejected_total",
			Help:        "Total ChannelV2 runtime activation rejections by reason.",
			ConstLabels: labels,
		}, []string{"reason"}),
		followerParked: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelv2_follower_parked",
			Help:        "Number of parked ChannelV2 follower runtimes per reactor.",
			ConstLabels: labels,
		}, []string{"reactor_id"}),
		recoveryProbeTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_channelv2_recovery_probe_total",
			Help:        "Total ChannelV2 parked follower recovery probes by result.",
			ConstLabels: labels,
		}, []string{"result"}),
		pullTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_channelv2_pull_total",
			Help:        "Total ChannelV2 follower pull results by result and empty response status.",
			ConstLabels: labels,
		}, []string{"result", "empty"}),
		metaCacheTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_channelv2_meta_cache_total",
			Help:        "Total clusterv2 ChannelV2 metadata cache events by result.",
			ConstLabels: labels,
		}, []string{"result"}),
```

Register the new collectors in the `registry.MustRegister` list, then add methods:

```go
func (m *ChannelV2Metrics) SetChannelRuntimeCount(reactorID int, role string, count int) {
	if m == nil {
		return
	}
	m.activeRuntimes.WithLabelValues(strconv.Itoa(reactorID), role).Set(float64(count))
}

func (m *ChannelV2Metrics) ObserveChannelActivationRejected(reason string) {
	if m == nil {
		return
	}
	m.activationRejectedTotal.WithLabelValues(reason).Inc()
}

func (m *ChannelV2Metrics) SetFollowerParkedCount(reactorID int, count int) {
	if m == nil {
		return
	}
	m.followerParked.WithLabelValues(strconv.Itoa(reactorID)).Set(float64(count))
}

func (m *ChannelV2Metrics) ObserveFollowerRecoveryProbe(result string) {
	if m == nil {
		return
	}
	m.recoveryProbeTotal.WithLabelValues(result).Inc()
}

func (m *ChannelV2Metrics) ObservePull(result string, empty bool) {
	if m == nil {
		return
	}
	m.pullTotal.WithLabelValues(result, strconv.FormatBool(empty)).Inc()
}

func (m *ChannelV2Metrics) ObserveMetaCache(result string) {
	if m == nil {
		return
	}
	m.metaCacheTotal.WithLabelValues(result).Inc()
}
```

- [ ] **Step 5: Bridge app observers to metrics**

In `internalv2/app/observability.go`, add methods to `channelV2MetricsObserver`:

```go
func (o channelV2MetricsObserver) SetChannelRuntimeCount(reactorID int, role ch.Role, count int) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelV2.SetChannelRuntimeCount(reactorID, channelV2RoleLabel(role), count)
}

func (o channelV2MetricsObserver) ObserveChannelActivationRejected(reason string) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelV2.ObserveChannelActivationRejected(reason)
}

func (o channelV2MetricsObserver) SetFollowerParkedCount(reactorID int, count int) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelV2.SetFollowerParkedCount(reactorID, count)
}

func (o channelV2MetricsObserver) ObserveFollowerRecoveryProbe(result string) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelV2.ObserveFollowerRecoveryProbe(result)
}

func (o channelV2MetricsObserver) ObservePull(result string, empty bool) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelV2.ObservePull(result, empty)
}

func (o channelV2MetricsObserver) ObserveChannelMetaCache(result string) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelV2.ObserveMetaCache(result)
}
```

Add corresponding forwarding methods to `multiChannelV2Observer`, guarding optional interfaces:

```go
func (o multiChannelV2Observer) SetChannelRuntimeCount(reactorID int, role ch.Role, count int) {
	for _, observer := range o {
		if typed, ok := observer.(reactor.RuntimeObserver); ok {
			typed.SetChannelRuntimeCount(reactorID, role, count)
		}
	}
}
```

Repeat the same pattern for `ObserveChannelActivationRejected`, `SetFollowerParkedCount`, `ObserveFollowerRecoveryProbe`, and `ObservePull`. Add `ObserveChannelMetaCache(result string)` to `channelV2MetricsObserver` and `multiChannelV2Observer` now, but do not add an interface assertion until Task 5 creates `pkg/clusterv2/channels.MetaCacheObserver`.

Add the role helper:

```go
func channelV2RoleLabel(role ch.Role) string {
	switch role {
	case ch.RoleLeader:
		return "leader"
	case ch.RoleFollower:
		return "follower"
	default:
		return "unknown"
	}
}
```

- [ ] **Step 6: Run metrics tests**

Run:

```bash
go test ./pkg/metrics ./internalv2/app -run 'TestChannelV2Metrics|TestApp' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/channelv2/reactor/metrics.go pkg/metrics/channelv2.go pkg/metrics/registry_test.go internalv2/app/observability.go
git commit -m "feat(channelv2): add 10k runtime observability"
```

## Task 2: Baseline Benchmarks And Evidence Template

**Files:**
- Modify: `pkg/channelv2/bench_test.go`
- Create: `docs/development/perf-runs/20260529-channelv2-10k-live-channels/README.md`

- [ ] **Step 1: Add a 10k benchmark skeleton**

In `pkg/channelv2/bench_test.go`, add:

```go
func BenchmarkThreeNodeTenThousandChannelsIdle(b *testing.B) {
	h := testkit.NewClusterHarness(b, []ch.NodeID{1, 2, 3})
	defer h.Close()
	const channelCount = 10000
	ids := make([]ch.ChannelID, channelCount)
	for i := 0; i < channelCount; i++ {
		meta := benchMetaThreeNode(fmt.Sprintf("live-10k-%05d", i))
		h.ApplyMetaToAll(meta)
		ids[i] = meta.ID
	}
	ctx := context.Background()
	for i, id := range ids {
		_, err := h.Nodes[1].Append(ctx, ch.AppendRequest{
			ChannelID:  id,
			CommitMode: ch.CommitModeQuorum,
			Message: ch.Message{
				MessageID:   uint64(i + 1),
				ChannelID:   id.ID,
				ChannelType: id.Type,
				Payload:     benchPayload,
			},
		})
		if err != nil {
			b.Fatalf("warm channel %d: %v", i, err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := h.Nodes[1].Tick(ctx); err != nil {
			b.Fatal(err)
		}
		if err := h.Nodes[2].Tick(ctx); err != nil {
			b.Fatal(err)
		}
		if err := h.Nodes[3].Tick(ctx); err != nil {
			b.Fatal(err)
		}
	}
}
```

Also add sparse traffic benchmark:

```go
func BenchmarkThreeNodeTenThousandChannelsSparseTraffic(b *testing.B) {
	h := testkit.NewClusterHarness(b, []ch.NodeID{1, 2, 3})
	defer h.Close()
	const channelCount = 10000
	const activeCount = 500
	ids := make([]ch.ChannelID, channelCount)
	for i := 0; i < channelCount; i++ {
		meta := benchMetaThreeNode(fmt.Sprintf("sparse-10k-%05d", i))
		h.ApplyMetaToAll(meta)
		ids[i] = meta.ID
	}
	ctx := context.Background()
	for i := 0; i < activeCount; i++ {
		_, err := h.Nodes[1].Append(ctx, ch.AppendRequest{
			ChannelID:  ids[i],
			CommitMode: ch.CommitModeQuorum,
			Message:    ch.Message{MessageID: uint64(i + 1), ChannelID: ids[i].ID, ChannelType: ids[i].Type, Payload: benchPayload},
		})
		if err != nil {
			b.Fatalf("warm active channel %d: %v", i, err)
		}
	}
	var seq atomic.Uint64
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			n := seq.Add(1)
			id := ids[int(n-1)%activeCount]
			_, err := h.Nodes[1].Append(ctx, ch.AppendRequest{
				ChannelID:  id,
				CommitMode: ch.CommitModeQuorum,
				Message:    ch.Message{MessageID: 1000000 + n, ChannelID: id.ID, ChannelType: id.Type, Payload: benchPayload},
			})
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}
```

- [ ] **Step 2: Run each benchmark once**

Run:

```bash
go test ./pkg/channelv2 -run '^$' -bench 'BenchmarkThreeNodeTenThousandChannels(Idle|SparseTraffic)' -benchtime=1x -count=1
```

Expected before later tasks: benchmarks may be slow and idle pull traffic is not yet fixed. They must compile and produce a baseline.

- [ ] **Step 3: Add perf evidence template**

Create `docs/development/perf-runs/20260529-channelv2-10k-live-channels/README.md`:

```markdown
# ChannelV2 10k Live Channels Perf Run

## Scenario

- workload: three-node ChannelV2, 10,000 group channels, every channel warmed once, sparse steady traffic over 1%-5% active channels
- clean or accumulated: clean stack unless explicitly recorded otherwise
- duration: 15s warmup, 60s measured window, 10s cooldown
- success criteria:
  - sendack_error_rate = 0
  - recv_verify_error_rate = 0 when receive verification is enabled
  - idle-window ChannelV2 pull rate is near zero except recovery probes
  - append p99 and store p99 recorded for comparison

## Required Evidence

- git revision: `git rev-parse HEAD`
- scenario config: copy wkbench scenario YAML into this directory
- target config: copy target YAML into this directory
- worker config: copy workers YAML into this directory
- metrics:
  - `wukongim_channelv2_active_runtimes`
  - `wukongim_channelv2_follower_parked`
  - `wukongim_channelv2_pull_total`
  - `wukongim_channelv2_recovery_probe_total`
  - `wukongim_channelv2_meta_cache_total`
  - `wukongim_channelv2_append_duration_seconds`
  - `wukongim_channelv2_worker_task_duration_seconds`
  - storage commit histograms
- logs:
  - app.log
  - warn.log
  - error.log
- pprof:
  - CPU profile during measured window
  - heap profile after warmup

## Perf-Triage Rule

Follow `docs/development/PERF_TRIAGE.md`: establish smoke health, collect evidence, classify, form one hypothesis, and change exactly one variable per experiment.
```

- [ ] **Step 4: Commit**

```bash
git add pkg/channelv2/bench_test.go docs/development/perf-runs/20260529-channelv2-10k-live-channels/README.md
git commit -m "test(channelv2): add 10k live channel benchmarks"
```

## Task 3: Capacity Limit Wiring

**Files:**
- Modify: `pkg/channelv2/channel.go`
- Modify: `pkg/channelv2/service/service.go`
- Modify: `pkg/channelv2/reactor/group.go`
- Modify: `pkg/channelv2/reactor/reactor.go`
- Modify: `pkg/channelv2/reactor/group_test.go`
- Modify: `pkg/clusterv2/config.go`
- Modify: `pkg/clusterv2/channels/service.go`
- Modify: `pkg/clusterv2/node_defaults.go`

- [ ] **Step 1: Write reactor capacity tests**

In `pkg/channelv2/reactor/group_test.go`, add:

```go
func TestGroupRejectsNewChannelWhenMaxChannelsReached(t *testing.T) {
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory, MaxChannels: 1})
	require.NoError(t, err)
	defer g.Close()

	first := ch.ChannelID{ID: "one", Type: 1}
	firstMeta := ch.Meta{Key: ch.ChannelKeyForID(first), ID: first, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	require.NoError(t, awaitSubmit(g, firstMeta.Key, Event{Kind: EventApplyMeta, Key: firstMeta.Key, Meta: firstMeta}))

	second := ch.ChannelID{ID: "two", Type: 1}
	secondMeta := ch.Meta{Key: ch.ChannelKeyForID(second), ID: second, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	err = awaitSubmit(g, secondMeta.Key, Event{Kind: EventApplyMeta, Key: secondMeta.Key, Meta: secondMeta})
	require.ErrorIs(t, err, ch.ErrTooManyChannels)
}

func TestGroupAllowsExistingChannelMetaUpdateAtCapacity(t *testing.T) {
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory, MaxChannels: 1})
	require.NoError(t, err)
	defer g.Close()

	id := ch.ChannelID{ID: "one", Type: 1}
	meta := ch.Meta{Key: ch.ChannelKeyForID(id), ID: id, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	meta.LeaderEpoch = 2
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
}
```

- [ ] **Step 2: Run failing capacity tests**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'TestGroup(RejectsNewChannelWhenMaxChannelsReached|AllowsExistingChannelMetaUpdateAtCapacity)' -count=1
```

Expected: FAIL because `Config.MaxChannels` does not exist.

- [ ] **Step 3: Add config fields**

In `pkg/channelv2/channel.go`, add to `Config`:

```go
	// MaxChannels bounds loaded ChannelV2 runtimes on this node. Zero keeps the current unlimited behavior.
	MaxChannels int
```

In `pkg/channelv2/service/service.go`, add to `Config`:

```go
	// MaxChannels bounds loaded ChannelV2 runtimes on this node. Zero keeps the current unlimited behavior.
	MaxChannels int
```

Pass it into `reactor.NewGroup`:

```go
		MaxChannels: cfg.MaxChannels,
```

In `pkg/channelv2/reactor/group.go`, add to `Config`:

```go
	// MaxChannels bounds loaded ChannelV2 runtimes on this node. Zero keeps the current unlimited behavior.
	MaxChannels int
```

Add to `ReactorConfig` in `pkg/channelv2/reactor/reactor.go`:

```go
	// MaxChannels bounds loaded runtimes owned by this reactor. Zero is unlimited.
	MaxChannels int
```

- [ ] **Step 4: Split node capacity across reactors**

In `pkg/channelv2/reactor/group.go`, add:

```go
func reactorChannelBudget(total int, reactors int, index int) int {
	if total <= 0 || reactors <= 0 {
		return 0
	}
	base := total / reactors
	rem := total % reactors
	if index < rem {
		return base + 1
	}
	return base
}
```

When constructing reactors, pass:

```go
			MaxChannels: reactorChannelBudget(cfg.MaxChannels, cfg.ReactorCount, i),
```

- [ ] **Step 5: Enforce capacity in ensureChannel**

In `pkg/channelv2/reactor/reactor.go`, before opening the store in `ensureChannel`, add:

```go
	if r.cfg.MaxChannels > 0 && len(r.channels) >= r.cfg.MaxChannels {
		r.observeActivationRejected("max_channels")
		return nil, ch.ErrTooManyChannels
	}
```

After inserting a runtime into `r.channels`, call a helper:

```go
	r.observeRuntimeCounts()
```

Add the helper:

```go
func (r *Reactor) observeRuntimeCounts() {
	if r == nil {
		return
	}
	leaders := 0
	followers := 0
	for _, rc := range r.channels {
		if rc == nil || rc.state == nil {
			continue
		}
		switch rc.state.Role {
		case ch.RoleLeader:
			leaders++
		case ch.RoleFollower:
			followers++
		}
	}
	r.observeRuntimeCount(ch.RoleLeader, leaders)
	r.observeRuntimeCount(ch.RoleFollower, followers)
}
```

Call `r.observeRuntimeCounts()` after successful metadata role changes and after `evictRuntimeChannel`.

- [ ] **Step 6: Propagate clusterv2 config**

In `pkg/clusterv2/config.go`, add to `ChannelConfig`:

```go
	// MaxChannels bounds loaded ChannelV2 runtimes on this node. Zero keeps unlimited behavior.
	MaxChannels int
```

Validate:

```go
	if c.Channel.MaxChannels < 0 {
		return ErrInvalidConfig
	}
```

In `pkg/clusterv2/channels/service.go`, add to `Config`:

```go
	// MaxChannels bounds loaded ChannelV2 runtimes on this node. Zero keeps unlimited behavior.
	MaxChannels int
```

Pass it to `channelservice.New`:

```go
MaxChannels: cfg.MaxChannels,
```

In `pkg/clusterv2/node_defaults.go`, pass:

```go
MaxChannels: n.cfg.Channel.MaxChannels,
```

- [ ] **Step 7: Run capacity tests**

Run:

```bash
go test ./pkg/channelv2/reactor ./pkg/channelv2/service ./pkg/clusterv2 -run 'TestGroup|TestService|TestConfig' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/channelv2/channel.go pkg/channelv2/service/service.go pkg/channelv2/reactor/group.go pkg/channelv2/reactor/reactor.go pkg/channelv2/reactor/group_test.go pkg/clusterv2/config.go pkg/clusterv2/channels/service.go pkg/clusterv2/node_defaults.go
git commit -m "feat(channelv2): bound active channel runtimes"
```

## Task 4: Wukongimv2 Config Keys

**Files:**
- Modify: `cmd/wukongimv2/config.go`
- Modify: `cmd/wukongimv2/config_test.go`
- Modify: `cmd/wukongimv2/wukongimv2.conf.example`
- Modify: `cmd/wukongimv2/wukongimv2-node1.conf.example`
- Modify: `cmd/wukongimv2/wukongimv2-node2.conf.example`
- Modify: `cmd/wukongimv2/wukongimv2-node3.conf.example`
- Modify: `scripts/wukongimv2/wukongimv2.conf`
- Modify: `scripts/wukongimv2/wukongimv2-node1.conf`
- Modify: `scripts/wukongimv2/wukongimv2-node2.conf`
- Modify: `scripts/wukongimv2/wukongimv2-node3.conf`
- Modify: `wukongim.conf.example`

- [ ] **Step 1: Add config parsing tests**

In `cmd/wukongimv2/config_test.go`, update `TestLoadConfigExplicitConfigFile` input with:

```go
		"WK_CLUSTER_MAX_CHANNELS=10000",
		"WK_CLUSTER_CHANNEL_FOLLOWER_RECOVERY_PROBE_INTERVAL=60s",
		"WK_CLUSTER_CHANNEL_FOLLOWER_RECOVERY_PROBE_JITTER=30s",
```

Add assertions after the reactor count assertion:

```go
	if cfg.Cluster.Channel.MaxChannels != 10000 {
		t.Fatalf("Channel.MaxChannels = %d, want 10000", cfg.Cluster.Channel.MaxChannels)
	}
	if cfg.Cluster.Channel.FollowerRecoveryProbeInterval != 60*time.Second {
		t.Fatalf("Channel.FollowerRecoveryProbeInterval = %s, want 60s", cfg.Cluster.Channel.FollowerRecoveryProbeInterval)
	}
	if cfg.Cluster.Channel.FollowerRecoveryProbeJitter != 30*time.Second {
		t.Fatalf("Channel.FollowerRecoveryProbeJitter = %s, want 30s", cfg.Cluster.Channel.FollowerRecoveryProbeJitter)
	}
```

Add a negative validation test:

```go
func TestLoadConfigRejectsNegativeChannelV2Limits(t *testing.T) {
	unsetLoadConfigEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "wukongim.conf")
	writeConf(t, path,
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7001",
		"WK_CLUSTER_MAX_CHANNELS=-1",
	)
	_, err := loadConfig([]string{"-config", path})
	if err == nil || !strings.Contains(err.Error(), "WK_CLUSTER_MAX_CHANNELS") {
		t.Fatalf("loadConfig() error = %v, want WK_CLUSTER_MAX_CHANNELS validation", err)
	}
}
```

- [ ] **Step 2: Run failing config tests**

Run:

```bash
go test ./cmd/wukongimv2 -run 'TestLoadConfig(ExplicitConfigFile|RejectsNegativeChannelV2Limits)' -count=1
```

Expected: FAIL because the new config keys are not supported.

- [ ] **Step 3: Parse config keys**

In `cmd/wukongimv2/config.go`, add to `supportedConfigKeys`:

```go
	"WK_CLUSTER_MAX_CHANNELS",
	"WK_CLUSTER_CHANNEL_FOLLOWER_RECOVERY_PROBE_INTERVAL",
	"WK_CLUSTER_CHANNEL_FOLLOWER_RECOVERY_PROBE_JITTER",
```

After `WK_CLUSTER_CHANNEL_REACTOR_COUNT`, parse:

```go
	if raw := configValue(values, "WK_CLUSTER_MAX_CHANNELS"); raw != "" {
		maxChannels, err := parseInt("WK_CLUSTER_MAX_CHANNELS", raw)
		if err != nil {
			return app.Config{}, err
		}
		if maxChannels < 0 {
			return app.Config{}, fmt.Errorf("parse WK_CLUSTER_MAX_CHANNELS: value must be >= 0")
		}
		cfg.Cluster.Channel.MaxChannels = maxChannels
	}
	if raw := configValue(values, "WK_CLUSTER_CHANNEL_FOLLOWER_RECOVERY_PROBE_INTERVAL"); raw != "" {
		interval, err := parseDuration("WK_CLUSTER_CHANNEL_FOLLOWER_RECOVERY_PROBE_INTERVAL", raw)
		if err != nil {
			return app.Config{}, err
		}
		if interval < 0 {
			return app.Config{}, fmt.Errorf("parse WK_CLUSTER_CHANNEL_FOLLOWER_RECOVERY_PROBE_INTERVAL: value must be >= 0")
		}
		cfg.Cluster.Channel.FollowerRecoveryProbeInterval = interval
	}
	if raw := configValue(values, "WK_CLUSTER_CHANNEL_FOLLOWER_RECOVERY_PROBE_JITTER"); raw != "" {
		jitter, err := parseDuration("WK_CLUSTER_CHANNEL_FOLLOWER_RECOVERY_PROBE_JITTER", raw)
		if err != nil {
			return app.Config{}, err
		}
		if jitter < 0 {
			return app.Config{}, fmt.Errorf("parse WK_CLUSTER_CHANNEL_FOLLOWER_RECOVERY_PROBE_JITTER: value must be >= 0")
		}
		cfg.Cluster.Channel.FollowerRecoveryProbeJitter = jitter
	}
```

- [ ] **Step 4: Document config examples**

In each wukongimv2 example config, after `WK_CLUSTER_CHANNEL_REACTOR_COUNT=0`, add:

```conf
# Maximum loaded ChannelV2 runtimes on this node. 0 keeps unlimited behavior.
WK_CLUSTER_MAX_CHANNELS=0

# Parked follower recovery probe base interval and deterministic jitter.
# Caught-up followers primarily wake through PullHint; probes prevent permanent sleep if hints are lost.
WK_CLUSTER_CHANNEL_FOLLOWER_RECOVERY_PROBE_INTERVAL=60s
WK_CLUSTER_CHANNEL_FOLLOWER_RECOVERY_PROBE_JITTER=30s
```

In root `wukongim.conf.example`, update the existing `WK_CLUSTER_MAX_CHANNELS` comment to mention both legacy channel runtime and ChannelV2, and add the two follower recovery keys near the ChannelV2 reactor key.

- [ ] **Step 5: Run config tests**

Run:

```bash
go test ./cmd/wukongimv2 -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/wukongimv2/config.go cmd/wukongimv2/config_test.go cmd/wukongimv2/*.conf.example scripts/wukongimv2/*.conf wukongim.conf.example
git commit -m "feat(wukongimv2): expose channelv2 live-channel limits"
```

## Task 5: ChannelMeta Append Cache

**Files:**
- Create: `pkg/clusterv2/channels/meta_cache.go`
- Modify: `pkg/clusterv2/channels/service.go`
- Modify: `pkg/clusterv2/channels/channels_test.go`
- Modify: `internalv2/app/observability.go`

- [ ] **Step 1: Add cache behavior tests**

In `pkg/clusterv2/channels/channels_test.go`, add:

```go
func TestServiceUsesAppendMetaCacheAfterFirstResolve(t *testing.T) {
	id := ch.ChannelID{ID: "cached", Type: 1}
	meta := ch.Meta{Key: ch.ChannelKeyForID(id), ID: id, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	source := &countingMetaSource{meta: meta}
	runtime := &fakeRuntime{appendRequireApply: true, appendBatch: ch.AppendBatchResult{Items: []ch.AppendBatchItemResult{{MessageSeq: 1}}}}
	svc, err := NewService(Config{Runtime: runtime, LocalNode: 1, MetaSource: source})
	require.NoError(t, err)

	for i := 0; i < 2; i++ {
		_, err = svc.AppendBatch(context.Background(), ch.AppendBatchRequest{ChannelID: id, Messages: []ch.Message{{Payload: []byte("hello")}}})
		require.NoError(t, err)
	}
	require.Equal(t, 1, source.ensureCalls)
}

func TestServiceInvalidatesAppendMetaCacheAndRetriesOnce(t *testing.T) {
	id := ch.ChannelID{ID: "stale", Type: 1}
	stale := ch.Meta{Key: ch.ChannelKeyForID(id), ID: id, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	fresh := stale
	fresh.LeaderEpoch = 2
	source := &countingMetaSource{metas: []ch.Meta{stale, fresh}}
	runtime := &staleOnceRuntime{result: ch.AppendBatchResult{Items: []ch.AppendBatchItemResult{{MessageSeq: 2}}}}
	svc, err := NewService(Config{Runtime: runtime, LocalNode: 1, MetaSource: source})
	require.NoError(t, err)

	_, err = svc.AppendBatch(context.Background(), ch.AppendBatchRequest{ChannelID: id, Messages: []ch.Message{{Payload: []byte("hello")}}})
	require.NoError(t, err)
	require.Equal(t, 2, source.ensureCalls)
	require.Equal(t, 2, runtime.appendCalls)
}
```

Add helper fakes:

```go
type countingMetaSource struct {
	meta        ch.Meta
	metas       []ch.Meta
	ensureCalls int
}

func (s *countingMetaSource) ResolveChannelMeta(ctx context.Context, id ch.ChannelID) (ch.Meta, error) {
	return s.EnsureChannelMeta(ctx, id)
}

func (s *countingMetaSource) EnsureChannelMeta(ctx context.Context, id ch.ChannelID) (ch.Meta, error) {
	s.ensureCalls++
	if len(s.metas) > 0 {
		index := s.ensureCalls - 1
		if index >= len(s.metas) {
			index = len(s.metas) - 1
		}
		return cloneMeta(s.metas[index]), nil
	}
	return cloneMeta(s.meta), nil
}

type staleOnceRuntime struct {
	result      ch.AppendBatchResult
	appendCalls int
}

func (r *staleOnceRuntime) ApplyMeta(ch.Meta) error { return nil }
func (r *staleOnceRuntime) Append(context.Context, ch.AppendRequest) (ch.AppendResult, error) {
	return ch.AppendResult{}, nil
}
func (r *staleOnceRuntime) AppendBatch(context.Context, ch.AppendBatchRequest) (ch.AppendBatchResult, error) {
	r.appendCalls++
	if r.appendCalls == 1 {
		return ch.AppendBatchResult{}, ch.ErrStaleMeta
	}
	return r.result, nil
}
func (r *staleOnceRuntime) Tick(context.Context) error { return nil }
func (r *staleOnceRuntime) Close() error { return nil }
func (r *staleOnceRuntime) HandlePull(context.Context, channeltransport.PullRequest) (channeltransport.PullResponse, error) {
	return channeltransport.PullResponse{}, nil
}
func (r *staleOnceRuntime) HandleAck(context.Context, channeltransport.AckRequest) error { return nil }
func (r *staleOnceRuntime) HandleNotify(context.Context, channeltransport.NotifyRequest) error { return nil }
func (r *staleOnceRuntime) HandlePullHint(context.Context, channeltransport.PullHintRequest) error { return nil }
```

- [ ] **Step 2: Run failing cache tests**

Run:

```bash
go test ./pkg/clusterv2/channels -run 'TestService(UsesAppendMetaCacheAfterFirstResolve|InvalidatesAppendMetaCacheAndRetriesOnce)' -count=1
```

Expected: FAIL because no cache exists.

- [ ] **Step 3: Implement cache primitive**

Create `pkg/clusterv2/channels/meta_cache.go`:

```go
package channels

import (
	"sync"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
)

// MetaCacheObserver receives low-cardinality metadata cache events.
type MetaCacheObserver interface {
	ObserveChannelMetaCache(result string)
}

type channelMetaCache struct {
	mu    sync.RWMutex
	items map[ch.ChannelID]ch.Meta
}

func (c *channelMetaCache) get(id ch.ChannelID) (ch.Meta, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	meta, ok := c.items[id]
	if !ok || !cacheableAppendMeta(id, meta) {
		return ch.Meta{}, false
	}
	return cloneMeta(meta), true
}

func (c *channelMetaCache) put(id ch.ChannelID, meta ch.Meta) {
	if !cacheableAppendMeta(id, meta) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		c.items = make(map[ch.ChannelID]ch.Meta)
	}
	c.items[id] = cloneMeta(meta)
}

func (c *channelMetaCache) invalidate(id ch.ChannelID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, id)
}

func cacheableAppendMeta(id ch.ChannelID, meta ch.Meta) bool {
	if meta.ID != id || meta.Key != ch.ChannelKeyForID(id) || meta.Leader == 0 {
		return false
	}
	return meta.Status == ch.StatusActive || meta.Status == ch.StatusCreating
}
```

- [ ] **Step 4: Use cache in service append**

In `pkg/clusterv2/channels/service.go`, add fields:

```go
	metaCache channelMetaCache
	observer  any
```

Store `observer: cfg.Observer` in `NewService`.

Change `AppendBatch` into a retrying path:

```go
func (s *Service) AppendBatch(ctx context.Context, req ch.AppendBatchRequest) (ch.AppendBatchResult, error) {
	res, err, usedCache := s.appendBatchOnce(ctx, req)
	if err == nil || !usedCache || !retryableMetaCacheError(err) {
		return res, err
	}
	s.metaCache.invalidate(req.ChannelID)
	s.observeMetaCache("invalidate")
	return s.appendBatchFresh(ctx, req)
}
```

Add helpers:

```go
func (s *Service) appendBatchOnce(ctx context.Context, req ch.AppendBatchRequest) (ch.AppendBatchResult, error, bool) {
	meta, ok, usedCache, err := s.resolveAppendMetaCached(ctx, req.ChannelID, true)
	if err != nil {
		return ch.AppendBatchResult{}, err, usedCache
	}
	res, err := s.appendBatchWithMeta(ctx, req, meta, ok)
	return res, err, usedCache
}

func (s *Service) appendBatchFresh(ctx context.Context, req ch.AppendBatchRequest) (ch.AppendBatchResult, error) {
	meta, ok, _, err := s.resolveAppendMetaCached(ctx, req.ChannelID, false)
	if err != nil {
		return ch.AppendBatchResult{}, err
	}
	return s.appendBatchWithMeta(ctx, req, meta, ok)
}
```

Move the current local/forward logic into:

```go
func (s *Service) appendBatchWithMeta(ctx context.Context, req ch.AppendBatchRequest, meta ch.Meta, ok bool) (ch.AppendBatchResult, error) {
	if ok {
		if meta.Leader != 0 && meta.Leader != s.localNode {
			if s.forward == nil {
				return ch.AppendBatchResult{}, ch.ErrNotLeader
			}
			return s.forward.ForwardAppendBatch(ctx, meta.Leader, req)
		}
		if err := s.runtime.ApplyMeta(meta); err != nil {
			return ch.AppendBatchResult{}, err
		}
	}
	return s.runtime.AppendBatch(ctx, req)
}
```

Add cached resolve:

```go
func (s *Service) resolveAppendMetaCached(ctx context.Context, id ch.ChannelID, allowCache bool) (ch.Meta, bool, bool, error) {
	if s == nil {
		return ch.Meta{}, false, false, nil
	}
	if allowCache {
		if meta, ok := s.metaCache.get(id); ok {
			s.observeMetaCache("hit")
			return meta, true, true, nil
		}
	}
	s.observeMetaCache("miss")
	meta, ok, err := s.resolveAppendMeta(ctx, id)
	if err == nil && ok {
		s.metaCache.put(id, meta)
	}
	return meta, ok, false, err
}

func retryableMetaCacheError(err error) bool {
	return errors.Is(err, ch.ErrStaleMeta) ||
		errors.Is(err, ch.ErrChannelNotFound) ||
		errors.Is(err, ch.ErrNotLeader) ||
		errors.Is(err, ch.ErrNotReady)
}

func (s *Service) observeMetaCache(result string) {
	if observer, ok := s.observer.(MetaCacheObserver); ok {
		observer.ObserveChannelMetaCache(result)
	}
}
```

Import `errors`.

Apply the same pattern to `Append` by having it call `AppendBatch` as before, so single append inherits cache behavior.

- [ ] **Step 5: Run cache tests**

Run:

```bash
go test ./pkg/clusterv2/channels -run 'TestService(UsesAppendMetaCacheAfterFirstResolve|InvalidatesAppendMetaCacheAndRetriesOnce|ForwardsAppendBatchToResolvedLeader|AppliesResolvedMetaBeforeLocalAppendBatch)' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/clusterv2/channels/meta_cache.go pkg/clusterv2/channels/service.go pkg/clusterv2/channels/channels_test.go internalv2/app/observability.go
git commit -m "feat(clusterv2): cache channel append metadata"
```

## Task 6: Parked Follower Recovery State

**Files:**
- Modify: `pkg/channelv2/channel.go`
- Modify: `pkg/channelv2/service/service.go`
- Modify: `pkg/channelv2/reactor/group.go`
- Modify: `pkg/channelv2/reactor/reactor.go`
- Modify: `pkg/channelv2/reactor/replication_state.go`
- Modify: `pkg/channelv2/reactor/effect.go`
- Modify: `pkg/clusterv2/config.go`
- Modify: `pkg/clusterv2/channels/service.go`
- Modify: `pkg/clusterv2/node_defaults.go`
- Modify: `pkg/channelv2/reactor/replication_state_test.go`

- [ ] **Step 1: Add default and jitter helper tests**

In `pkg/channelv2/reactor/replication_state_test.go`, add:

```go
func TestDefaultConfigSetsFollowerRecoveryProbe(t *testing.T) {
	cfg := defaultConfig(Config{LocalNode: 1, Store: store.NewMemoryFactory()})
	require.Equal(t, time.Minute, cfg.FollowerRecoveryProbeInterval)
	require.Equal(t, 30*time.Second, cfg.FollowerRecoveryProbeJitter)
}

func TestFollowerRecoveryProbeDelayIsDeterministicAndBounded(t *testing.T) {
	delay := followerRecoveryProbeDelay(ch.ChannelKey("1:room"), time.Minute, 30*time.Second)
	require.GreaterOrEqual(t, delay, time.Minute)
	require.LessOrEqual(t, delay, 90*time.Second)
	require.Equal(t, delay, followerRecoveryProbeDelay(ch.ChannelKey("1:room"), time.Minute, 30*time.Second))
}
```

- [ ] **Step 2: Run failing recovery config tests**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'Test(DefaultConfigSetsFollowerRecoveryProbe|FollowerRecoveryProbeDelayIsDeterministicAndBounded)' -count=1
```

Expected: FAIL because the config fields and helper do not exist.

- [ ] **Step 3: Add config fields and defaults**

Add to `pkg/channelv2/channel.go` `Config`:

```go
	// FollowerRecoveryProbeInterval is the base delay for parked follower recovery probes. Zero disables probes.
	FollowerRecoveryProbeInterval time.Duration
	// FollowerRecoveryProbeJitter spreads parked follower recovery probes across this bounded window.
	FollowerRecoveryProbeJitter time.Duration
```

Add the same comments and fields to `service.Config`, `reactor.Config`, `reactor.ReactorConfig`, `clusterv2.ChannelConfig`, and `channels.Config`. Pass them through all constructor calls.

In `defaultConfig` and `defaultReactorConfig`, set:

```go
	if cfg.FollowerRecoveryProbeInterval < 0 {
		cfg.FollowerRecoveryProbeInterval = 0
	}
	if cfg.FollowerRecoveryProbeInterval == 0 {
		cfg.FollowerRecoveryProbeInterval = time.Minute
	}
	if cfg.FollowerRecoveryProbeJitter < 0 {
		cfg.FollowerRecoveryProbeJitter = 0
	}
	if cfg.FollowerRecoveryProbeJitter == 0 {
		cfg.FollowerRecoveryProbeJitter = 30 * time.Second
	}
```

In `pkg/clusterv2/config.go`, validate both fields are non-negative.

- [ ] **Step 4: Add replication state fields and helper**

In `pkg/channelv2/reactor/replication_state.go`, add:

```go
	// recoveryProbe records whether nextPullAt is a parked-follower recovery probe.
	recoveryProbe bool
```

Add methods:

```go
func (s *replicationState) parkWithRecovery(key ch.ChannelKey, now time.Time, interval time.Duration, jitter time.Duration) {
	s.dirty = false
	s.parked = true
	s.nextPullAfter = 0
	s.recoveryProbe = false
	s.nextPullAt = time.Time{}
	if interval > 0 {
		s.recoveryProbe = true
		s.nextPullAt = now.Add(followerRecoveryProbeDelay(key, interval, jitter))
	}
}

func (s *replicationState) clearParkedForHint(now time.Time) {
	s.markDirty(now)
	s.recoveryProbe = false
}
```

Add deterministic jitter:

```go
func followerRecoveryProbeDelay(key ch.ChannelKey, interval time.Duration, jitter time.Duration) time.Duration {
	if interval <= 0 {
		return 0
	}
	if jitter <= 0 {
		return interval
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return interval + time.Duration(h.Sum64()%uint64(jitter+1))
}
```

Import `hash/fnv`.

- [ ] **Step 5: Run recovery config tests**

Run:

```bash
go test ./pkg/channelv2/reactor ./pkg/channelv2/service ./pkg/clusterv2 -run 'Test(DefaultConfigSetsFollowerRecoveryProbe|FollowerRecoveryProbeDelayIsDeterministicAndBounded|Config)' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/channelv2/channel.go pkg/channelv2/service/service.go pkg/channelv2/reactor/group.go pkg/channelv2/reactor/reactor.go pkg/channelv2/reactor/replication_state.go pkg/channelv2/reactor/effect.go pkg/clusterv2/config.go pkg/clusterv2/channels/service.go pkg/clusterv2/node_defaults.go pkg/channelv2/reactor/replication_state_test.go
git commit -m "feat(channelv2): configure follower recovery probes"
```

## Task 7: Park Caught-Up Followers

**Files:**
- Modify: `pkg/channelv2/reactor/follower_replication.go`
- Modify: `pkg/channelv2/reactor/scheduler.go`
- Modify: `pkg/channelv2/reactor/metrics.go`
- Modify: `pkg/channelv2/reactor/replication_state_test.go`

- [ ] **Step 1: Add parked follower behavior tests**

In `pkg/channelv2/reactor/replication_state_test.go`, add:

```go
func TestFollowerParksAfterEmptyCaughtUpPull(t *testing.T) {
	factory := store.NewMemoryFactory()
	net := transport.NewLocalNetwork()
	g, err := NewGroup(Config{
		LocalNode: 2, ReactorCount: 1, MailboxSize: 16, Store: factory, Transport: net,
		FollowerRecoveryProbeInterval: time.Minute,
		FollowerRecoveryProbeJitter:   0,
	})
	require.NoError(t, err)
	defer g.Close()
	meta := ch.Meta{
		Key: ch.ChannelKey("1:park"), ID: ch.ChannelID{ID: "park", Type: 1},
		Epoch: 1, LeaderEpoch: 1, Leader: 1,
		Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive,
	}
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	r := g.reactors[g.router.PickIndex(meta.Key)]
	rc := r.channels[meta.Key]
	result := worker.Result{
		Kind:  worker.TaskRPCPull,
		Fence: ch.Fence{ChannelKey: meta.Key, Generation: rc.state.Generation, Epoch: 1, LeaderEpoch: 1, OpID: rc.replication.pullOpID},
		RPCPull: &worker.RPCPullResult{Response: transport.PullResponse{
			ChannelKey: meta.Key, Epoch: 1, LeaderEpoch: 1, LeaderHW: 0, LeaderLEO: 0,
		}},
	}
	rc.replication.pullInflight = true
	rc.replication.pullOpID = result.Fence.OpID
	r.handleRPCPullResult(result)
	require.True(t, rc.replication.parked)
	require.False(t, rc.replication.dirty)
	require.True(t, rc.replication.recoveryProbe)
	require.False(t, rc.replication.nextPullAt.IsZero())
}

func TestParkedFollowerWakesOnValidPullHint(t *testing.T) {
	r, rc := newFollowerReplicationTestRuntime(t)
	now := time.Now()
	rc.replication.parkWithRecovery(rc.state.Key, now, time.Minute, 0)
	r.handleFollowerPullHint(Event{Kind: EventPullHint, Key: rc.state.Key, PullHint: transport.PullHintRequest{
		ChannelKey: rc.state.Key, ChannelID: rc.state.ID, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch,
		Leader: rc.state.Leader, LeaderLEO: rc.state.LEO + 1, ActivityVersion: 1,
	}, Future: NewFuture()})
	require.False(t, rc.replication.parked)
	require.True(t, rc.replication.dirty)
	require.False(t, rc.replication.recoveryProbe)
}
```

If `newFollowerReplicationTestRuntime` does not exist, add it near existing test helpers:

```go
func newFollowerReplicationTestRuntime(t *testing.T) (*Reactor, *runtimeChannel) {
	t.Helper()
	factory := store.NewMemoryFactory()
	pools := newDirectTestPools(t, factory, captureCompletionSink{results: make(chan worker.Result, 8)})
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16})
	meta := ch.Meta{Key: ch.ChannelKey("1:follower"), ID: ch.ChannelID{ID: "follower", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}
	f := NewFuture()
	r.handleApplyMeta(Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta, Future: f})
	_, err = f.Await(context.Background())
	require.NoError(t, err)
	return r, r.channels[meta.Key]
}
```

- [ ] **Step 2: Run failing parked follower tests**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'Test(FollowerParksAfterEmptyCaughtUpPull|ParkedFollowerWakesOnValidPullHint)' -count=1
```

Expected: FAIL because empty caught-up pulls still schedule ordinary idle polling.

- [ ] **Step 3: Park after caught-up empty pull**

In `handleRPCPullResult` in `pkg/channelv2/reactor/follower_replication.go`, replace the caught-up empty-pull branch:

```go
		rc.replication.dirty = false
		delay := resp.NextPullAfter
		if delay <= 0 {
			delay = r.cfg.ReplicationIdlePollInterval
		}
		rc.replication.parked = delay > 0
		rc.replication.nextPullAfter = delay
		rc.replication.nextPullAt = now.Add(delay)
		return
```

with:

```go
		rc.replication.parkWithRecovery(rc.state.Key, now, r.cfg.FollowerRecoveryProbeInterval, r.cfg.FollowerRecoveryProbeJitter)
		r.observeFollowerParkedCount(r.countParkedFollowers())
		return
```

Add helper in `reactor.go` or `follower_replication.go`:

```go
func (r *Reactor) countParkedFollowers() int {
	count := 0
	for _, rc := range r.channels {
		if rc != nil && rc.state != nil && rc.state.Role == ch.RoleFollower && rc.replication.parked {
			count++
		}
	}
	return count
}
```

- [ ] **Step 4: Wake parked follower on valid hint**

In `handleFollowerPullHint`, replace:

```go
	rc.replication.parked = false
	rc.replication.nextPullAfter = 0
	rc.replication.markDirty(now)
```

with:

```go
	rc.replication.clearParkedForHint(now)
	r.observeFollowerParkedCount(r.countParkedFollowers())
```

When `req.LeaderLEO <= rc.state.LEO`, keep the follower parked by adding:

```go
		if rc.replication.parked && rc.replication.recoveryProbe && rc.replication.nextPullAt.IsZero() {
			rc.replication.parkWithRecovery(rc.state.Key, now, r.cfg.FollowerRecoveryProbeInterval, r.cfg.FollowerRecoveryProbeJitter)
		}
```

- [ ] **Step 5: Record recovery probe and pull results**

In `trySubmitPull`, before submit, capture:

```go
	recoveryProbe := rc.replication.recoveryProbe && !rc.replication.nextPullAt.IsZero() && !now.Before(rc.replication.nextPullAt)
```

After successful submit:

```go
	if recoveryProbe {
		rc.replication.recoveryProbe = false
		r.observeRecoveryProbe("submitted")
	}
```

In `handleRPCPullResult`, after a fenced result is accepted:

```go
	if result.Err != nil {
		r.observePull("err", false)
		r.observeRecoveryProbe("err")
		r.backoffPull(rc, result.Err, now)
		return
	}
```

After `resp := result.RPCPull.Response`, add:

```go
	r.observePull("ok", len(resp.Records) == 0)
```

Only call `ObserveFollowerRecoveryProbe("ok")` for recovery probes by keeping a `recoveryProbeInflight bool` on `replicationState`.

- [ ] **Step 6: Run replication tests**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'Test(FollowerParksAfterEmptyCaughtUpPull|ParkedFollowerWakesOnValidPullHint|Follower|PullHint|StoppedAck)' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/channelv2/reactor/follower_replication.go pkg/channelv2/reactor/scheduler.go pkg/channelv2/reactor/metrics.go pkg/channelv2/reactor/replication_state_test.go
git commit -m "feat(channelv2): park caught-up followers"
```

## Task 8: Lazy Runtime Maps And Compact Follower State

**Files:**
- Modify: `pkg/channelv2/reactor/reactor.go`
- Modify: `pkg/channelv2/reactor/effect.go`
- Modify: `pkg/channelv2/reactor/lifecycle.go`
- Modify: `pkg/channelv2/reactor/lifecycle_controller.go`
- Modify: `pkg/channelv2/reactor/lifecycle_controller_test.go`
- Modify: `pkg/channelv2/reactor/group_test.go`

- [ ] **Step 1: Add lazy map tests**

In `pkg/channelv2/reactor/group_test.go`, add:

```go
func TestEnsureChannelKeepsIdleRuntimeMapsLazy(t *testing.T) {
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: store.NewMemoryFactory(), MailboxSize: 16})
	id := ch.ChannelID{ID: "lazy", Type: 1}
	meta := ch.Meta{Key: ch.ChannelKeyForID(id), ID: id, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	f := NewFuture()
	r.handleApplyMeta(Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta, Future: f})
	_, err := f.Await(context.Background())
	require.NoError(t, err)
	rc := r.channels[meta.Key]
	require.Nil(t, rc.waiters)
	require.Nil(t, rc.pullWaiters)
	require.Nil(t, rc.appendTimings)
	require.Nil(t, rc.appendCancelContexts)
}
```

- [ ] **Step 2: Run failing lazy map test**

Run:

```bash
go test ./pkg/channelv2/reactor -run TestEnsureChannelKeepsIdleRuntimeMapsLazy -count=1
```

Expected: FAIL because `ensureChannel` allocates several maps immediately.

- [ ] **Step 3: Stop eager map allocation**

In `ensureChannel`, remove eager allocations:

```go
		waiters:       make(map[ch.OpID]*Future),
		pullWaiters:   make(map[ch.OpID]*pullWaiter),
		appendTimings: make(map[ch.OpID]appendTiming),
```

- [ ] **Step 4: Add map ensure helpers**

In `pkg/channelv2/reactor/effect.go`, update `addWaiter`:

```go
func (rc *runtimeChannel) addWaiter(opID ch.OpID, future *Future) error {
	if rc == nil || future == nil {
		return nil
	}
	if rc.waiters == nil {
		rc.waiters = make(map[ch.OpID]*Future)
	}
	if _, ok := rc.waiters[opID]; ok {
		return ch.ErrInvalidConfig
	}
	rc.waiters[opID] = future
	return nil
}
```

In `handleAppend`, before assigning append timing:

```go
	if rc.appendTimings == nil {
		rc.appendTimings = make(map[ch.OpID]appendTiming)
	}
```

In `handleLeaderPull`, keep the existing lazy `pullWaiters` allocation.

- [ ] **Step 5: Keep follower lifecycle map nil for follower role**

In `handleApplyMeta`, when role is follower, the existing code sets `rc.lifecycle.followers = nil`. Keep that behavior. In `newChannelRuntimeLifecycle`, remove eager follower map allocation:

```go
	return channelRuntimeLifecycle{
		stage:    lifecycleLive,
		loadedAt: now,
		version:  version,
	}
```

Make `syncLeaderFollowers` allocate the map only when local role is leader. Existing tests that instantiate `channelRuntimeLifecycle` directly may need explicit `followers: make(map[ch.NodeID]*lifecycleFollower)` in the test setup.

- [ ] **Step 6: Run reactor tests**

Run:

```bash
go test ./pkg/channelv2/reactor -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/channelv2/reactor/reactor.go pkg/channelv2/reactor/effect.go pkg/channelv2/reactor/lifecycle.go pkg/channelv2/reactor/lifecycle_controller.go pkg/channelv2/reactor/lifecycle_controller_test.go pkg/channelv2/reactor/group_test.go
git commit -m "perf(channelv2): reduce idle runtime heap"
```

## Task 9: Flow Docs And Knowledge Notes

**Files:**
- Modify: `pkg/channelv2/FLOW.md`
- Modify: `pkg/channelv2/reactor/FLOW.md`
- Modify: `docs/development/PROJECT_KNOWLEDGE.md`

- [ ] **Step 1: Update root ChannelV2 flow**

In `pkg/channelv2/FLOW.md`, add after the lifecycle model section:

```markdown
## 10k Live Channel Runtime Rules

ChannelV2 can bound loaded local runtimes with `MaxChannels`. A limit of `0`
keeps unlimited behavior. Capacity checks happen before opening a new
channel-scoped store handle; metadata updates for already loaded runtimes remain
allowed at capacity.

Caught-up followers park instead of polling the leader on a short idle interval.
The leader wakes followers with PullHint on new activity, while followers keep a
low-frequency jittered recovery probe so a lost hint cannot leave a channel
stale forever.
```

- [ ] **Step 2: Update reactor flow**

In `pkg/channelv2/reactor/FLOW.md`, update the follower-side replication section with:

```markdown
When a follower observes an empty pull response and both `LeaderLEO` and the
latest hinted leader LEO are covered by local LEO, it enters parked state.
Parked followers do not schedule ordinary idle pulls. A valid PullHint clears the
parked state and schedules immediate pull; otherwise a deterministic jittered
recovery probe runs at the configured low-frequency interval.
```

- [ ] **Step 3: Add concise project knowledge**

In `docs/development/PROJECT_KNOWLEDGE.md`, under `### Send stress performance`, add:

```markdown
- `pkg/channelv2` high-channel idle scale depends on parked followers: caught-up followers should wake through PullHint plus low-frequency recovery probes, not short-interval empty pull polling.
- `clusterv2/channels` caches append ChannelRuntimeMeta with epoch and leader fences; Slot metadata remains authoritative and stale append errors invalidate the cache once before retry.
```

- [ ] **Step 4: Commit**

```bash
git add pkg/channelv2/FLOW.md pkg/channelv2/reactor/FLOW.md docs/development/PROJECT_KNOWLEDGE.md
git commit -m "docs(channelv2): document 10k live-channel runtime rules"
```

## Task 10: Final Verification

**Files:**
- No source changes expected.

- [ ] **Step 1: Run focused unit tests**

Run:

```bash
go test ./pkg/channelv2/... ./pkg/clusterv2/... ./pkg/metrics ./internalv2/app ./cmd/wukongimv2 -count=1
```

Expected: PASS.

- [ ] **Step 2: Run 10k in-memory benchmark once**

Run:

```bash
go test ./pkg/channelv2 -run '^$' -bench 'BenchmarkThreeNodeTenThousandChannels(Idle|SparseTraffic)' -benchtime=1x -count=1
```

Expected: PASS and benchmark output recorded in the final implementation report.

- [ ] **Step 3: Optional real-store perf run**

Only run this if the developer is intentionally doing a performance run and has time for it:

```bash
go test -tags=integration ./test/e2e/... -run '^$' -count=1
```

Expected: command verifies integration package discovery only. Real wkbench execution must follow `docs/development/PERF_TRIAGE.md` and record evidence under `docs/development/perf-runs/20260529-channelv2-10k-live-channels/`.

- [ ] **Step 4: Inspect git status**

Run:

```bash
git status --short
```

Expected: clean or only intentionally uncommitted perf evidence files.

## Self-Review Notes

- Spec coverage: runtime capacity is covered by Tasks 3-4; follower park and recovery probes by Tasks 6-7; metadata cache by Task 5; observability and benchmarks by Tasks 1-2 and 10; docs by Task 9; memory tightening by Task 8.
- Scope boundary: node-pair `PullBatch`, `PullHintBatch`, `ProgressAckBatch`, and binary Channel RPC codec are explicitly excluded from this plan.
- Type consistency: public config uses `MaxChannels`, `FollowerRecoveryProbeInterval`, and `FollowerRecoveryProbeJitter` consistently across ChannelV2 service, reactor, clusterv2, and wukongimv2 config.

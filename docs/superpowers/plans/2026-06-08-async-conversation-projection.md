# Async Conversation Projection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move conversation active projection and authority admission out of the SENDACK foreground path while deleting the obsolete synchronous foreground admission code.

**Architecture:** Add a node-local async conversation projector in `internalv2/app`. The committed sink becomes a thin metadata-only submitter; the background projector coalesces committed events, projects active patches, and admits them through the existing conversation authority client with bounded retry state. Route-authority lifecycle remains app-level but is separated from committed event handling.

**Tech Stack:** Go, internalv2 app composition root, internalv2 conversation usecase, clusterv2 conversation authority client, Prometheus metrics, shell benchmark scripts.

---

## Spec

Implement the approved design in:

`docs/superpowers/specs/2026-06-08-async-conversation-projection-design.md`

## File Structure

- Create `internalv2/app/conversation_async_projector.go`
  - Owns async projection runtime, foreground coalescing, background flush, retry bounds, and thin committed sink.
- Create `internalv2/app/conversation_async_projector_test.go`
  - Focused tests for foreground non-blocking submit, coalescing, dirty pressure, flush, retry, stop, and payload policy.
- Create `internalv2/app/conversation_route_lifecycle.go`
  - Owns route-authority watch/start/stop and local authority handoff. It must not handle committed events.
- Create `internalv2/app/conversation_route_lifecycle_test.go`
  - Tests route events mark active/warming and drain old local targets.
- Modify `internalv2/app/conversation.go`
  - Remove foreground authority committed sink implementation and keep only shared helpers that are still used.
- Modify `internalv2/app/conversation_authority.go`
  - Keep local authority cache/list/flush behavior. Do not add committed-event handling here.
- Modify `internalv2/app/app.go`
  - Wire async projector, thin sink, route lifecycle, and lifecycle fields.
- Modify `internalv2/app/lifecycle.go`
  - Start route lifecycle and async projector after cluster write readiness and before delivery/API/gateway.
- Modify `internalv2/app/config.go`
  - Replace foreground admission config fields with background projection config fields.
- Modify `cmd/wukongimv2/config.go`
  - Parse new `WK_CONVERSATION_PROJECTION_*` keys and remove old foreground admission keys.
- Modify `cmd/wukongimv2/config_test.go`
  - Cover defaults, env override, and parse validation for new keys. Remove old key expectations.
- Modify `cmd/wukongimv2/*.conf.example`, `scripts/wukongimv2/*.conf`, and `wukongim.conf.example`
  - Replace old conversation authority foreground admission settings with async projection settings.
- Modify `pkg/metrics/conversation.go`
  - Add projection submit/dirty/flush/member/admit/retry metrics. Rename authority foreground admit help text.
- Modify `pkg/metrics/registry_test.go`
  - Cover new low-cardinality projection metrics.
- Modify `internalv2/app/observability.go` and `internalv2/app/observability_test.go`
  - Map app projection observer events into metrics.
- Modify `internalv2/app/FLOW.md`, `internalv2/usecase/message/FLOW.md`, and related tests
  - Replace foreground admission descriptions with async projection flow.

## Task 1: Replace Conversation Projection Config

**Files:**
- Modify: `internalv2/app/config.go`
- Modify: `cmd/wukongimv2/config.go`
- Modify: `cmd/wukongimv2/config_test.go`

- [ ] **Step 1: Add failing app config tests**

Add these tests near the existing conversation config tests in `internalv2/app/app_test.go`:

```go
func TestDefaultConversationConfigUsesAsyncProjectionDefaults(t *testing.T) {
	cfg := defaultConversationConfig(ConversationConfig{})

	if cfg.ProjectionFlushInterval != 100*time.Millisecond {
		t.Fatalf("ProjectionFlushInterval = %v, want 100ms", cfg.ProjectionFlushInterval)
	}
	if cfg.ProjectionShardCount != 64 {
		t.Fatalf("ProjectionShardCount = %d, want 64", cfg.ProjectionShardCount)
	}
	if cfg.ProjectionMaxDirtyEvents != 100000 {
		t.Fatalf("ProjectionMaxDirtyEvents = %d, want 100000", cfg.ProjectionMaxDirtyEvents)
	}
	if cfg.ProjectionMaxRetryPatches != 100000 {
		t.Fatalf("ProjectionMaxRetryPatches = %d, want 100000", cfg.ProjectionMaxRetryPatches)
	}
	if cfg.ProjectionRetryMaxAge != 30*time.Second {
		t.Fatalf("ProjectionRetryMaxAge = %v, want 30s", cfg.ProjectionRetryMaxAge)
	}
	if cfg.ProjectionAdmitBatchRows != 512 {
		t.Fatalf("ProjectionAdmitBatchRows = %d, want 512", cfg.ProjectionAdmitBatchRows)
	}
	if cfg.ProjectionAdmitConcurrency != 16 {
		t.Fatalf("ProjectionAdmitConcurrency = %d, want 16", cfg.ProjectionAdmitConcurrency)
	}
	if cfg.ProjectionAdmitTimeout != 500*time.Millisecond {
		t.Fatalf("ProjectionAdmitTimeout = %v, want 500ms", cfg.ProjectionAdmitTimeout)
	}
}

func TestValidateConversationConfigRejectsInvalidAsyncProjectionValues(t *testing.T) {
	base := defaultConversationConfig(ConversationConfig{})
	tests := []struct {
		name   string
		mutate func(*ConversationConfig)
	}{
		{name: "flush interval", mutate: func(c *ConversationConfig) { c.ProjectionFlushInterval = -time.Millisecond }},
		{name: "shards", mutate: func(c *ConversationConfig) { c.ProjectionShardCount = 0 }},
		{name: "dirty", mutate: func(c *ConversationConfig) { c.ProjectionMaxDirtyEvents = 0 }},
		{name: "retry patches", mutate: func(c *ConversationConfig) { c.ProjectionMaxRetryPatches = 0 }},
		{name: "retry age", mutate: func(c *ConversationConfig) { c.ProjectionRetryMaxAge = 0 }},
		{name: "batch rows", mutate: func(c *ConversationConfig) { c.ProjectionAdmitBatchRows = 0 }},
		{name: "concurrency", mutate: func(c *ConversationConfig) { c.ProjectionAdmitConcurrency = 0 }},
		{name: "timeout", mutate: func(c *ConversationConfig) { c.ProjectionAdmitTimeout = 0 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base
			tt.mutate(&cfg)
			if err := validateConversationConfig(cfg); err == nil {
				t.Fatalf("validateConversationConfig() error = nil, want invalid config")
			}
		})
	}
}
```

- [ ] **Step 2: Run the failing app config tests**

Run:

```bash
go test ./internalv2/app -run 'TestDefaultConversationConfigUsesAsyncProjectionDefaults|TestValidateConversationConfigRejectsInvalidAsyncProjectionValues' -count=1
```

Expected: FAIL because `ConversationConfig` does not yet have the projection fields.

- [ ] **Step 3: Replace foreground config fields in `internalv2/app/config.go`**

Replace these fields on `ConversationConfig`:

```go
// AuthorityAdmissionTimeout bounds foreground cache admission after a durable message commit.
AuthorityAdmissionTimeout time.Duration
// AuthorityRPCTimeout bounds one conversation authority node RPC.
AuthorityRPCTimeout time.Duration
// AuthorityRPCBatchRows limits active patches carried by one authority admission RPC.
AuthorityRPCBatchRows int
// AuthorityRPCConcurrency limits concurrent authority admission RPCs per committed event.
AuthorityRPCConcurrency int
```

with:

```go
// ProjectionFlushInterval controls how often committed conversation events are projected in the background.
ProjectionFlushInterval time.Duration
// ProjectionShardCount bounds foreground lock contention while coalescing committed conversation events.
ProjectionShardCount int
// ProjectionMaxDirtyEvents bounds unprojected committed event keys retained in memory.
ProjectionMaxDirtyEvents int
// ProjectionMaxRetryPatches bounds active patches retained for background admission retry.
ProjectionMaxRetryPatches int
// ProjectionRetryMaxAge bounds how long retryable active patches remain in memory.
ProjectionRetryMaxAge time.Duration
// ProjectionAdmitBatchRows limits active patches in one background authority admission batch.
ProjectionAdmitBatchRows int
// ProjectionAdmitConcurrency limits concurrent background authority admission batches.
ProjectionAdmitConcurrency int
// ProjectionAdmitTimeout bounds one background authority admission call.
ProjectionAdmitTimeout time.Duration
```

In `defaultConversationConfig`, replace the old default block with:

```go
if cfg.ProjectionFlushInterval == 0 {
	cfg.ProjectionFlushInterval = 100 * time.Millisecond
}
if cfg.ProjectionShardCount == 0 {
	cfg.ProjectionShardCount = 64
}
if cfg.ProjectionMaxDirtyEvents == 0 {
	cfg.ProjectionMaxDirtyEvents = 100000
}
if cfg.ProjectionMaxRetryPatches == 0 {
	cfg.ProjectionMaxRetryPatches = 100000
}
if cfg.ProjectionRetryMaxAge == 0 {
	cfg.ProjectionRetryMaxAge = 30 * time.Second
}
if cfg.ProjectionAdmitBatchRows == 0 {
	cfg.ProjectionAdmitBatchRows = 512
}
if cfg.ProjectionAdmitConcurrency == 0 {
	cfg.ProjectionAdmitConcurrency = 16
}
if cfg.ProjectionAdmitTimeout == 0 {
	cfg.ProjectionAdmitTimeout = 500 * time.Millisecond
}
```

In `validateConversationConfig`, replace old foreground validations with:

```go
if cfg.ProjectionFlushInterval < 0 {
	return fmt.Errorf("%w: conversation projection flush interval must be non-negative", ErrInvalidConfig)
}
if cfg.ProjectionShardCount <= 0 {
	return fmt.Errorf("%w: conversation projection shard count must be positive", ErrInvalidConfig)
}
if cfg.ProjectionMaxDirtyEvents <= 0 {
	return fmt.Errorf("%w: conversation projection max dirty events must be positive", ErrInvalidConfig)
}
if cfg.ProjectionMaxRetryPatches <= 0 {
	return fmt.Errorf("%w: conversation projection max retry patches must be positive", ErrInvalidConfig)
}
if cfg.ProjectionRetryMaxAge <= 0 {
	return fmt.Errorf("%w: conversation projection retry max age must be positive", ErrInvalidConfig)
}
if cfg.ProjectionAdmitBatchRows <= 0 {
	return fmt.Errorf("%w: conversation projection admit batch rows must be positive", ErrInvalidConfig)
}
if cfg.ProjectionAdmitConcurrency <= 0 {
	return fmt.Errorf("%w: conversation projection admit concurrency must be positive", ErrInvalidConfig)
}
if cfg.ProjectionAdmitTimeout <= 0 {
	return fmt.Errorf("%w: conversation projection admit timeout must be positive", ErrInvalidConfig)
}
```

- [ ] **Step 4: Update `cmd/wukongimv2/config.go` config keys and parsing**

In `knownConfigKeys`, replace old keys:

```go
"WK_CONVERSATION_AUTHORITY_ADMISSION_TIMEOUT",
"WK_CONVERSATION_AUTHORITY_RPC_TIMEOUT",
"WK_CONVERSATION_AUTHORITY_RPC_BATCH_ROWS",
"WK_CONVERSATION_AUTHORITY_RPC_CONCURRENCY",
```

with:

```go
"WK_CONVERSATION_PROJECTION_FLUSH_INTERVAL",
"WK_CONVERSATION_PROJECTION_SHARD_COUNT",
"WK_CONVERSATION_PROJECTION_MAX_DIRTY_EVENTS",
"WK_CONVERSATION_PROJECTION_MAX_RETRY_PATCHES",
"WK_CONVERSATION_PROJECTION_RETRY_MAX_AGE",
"WK_CONVERSATION_PROJECTION_ADMIT_BATCH_ROWS",
"WK_CONVERSATION_PROJECTION_ADMIT_CONCURRENCY",
"WK_CONVERSATION_PROJECTION_ADMIT_TIMEOUT",
```

Replace the old parsing blocks with:

```go
if raw := configValue(values, "WK_CONVERSATION_PROJECTION_FLUSH_INTERVAL"); raw != "" {
	interval, err := parseDuration("WK_CONVERSATION_PROJECTION_FLUSH_INTERVAL", raw)
	if err != nil {
		return app.Config{}, err
	}
	if interval < 0 {
		return app.Config{}, fmt.Errorf("parse WK_CONVERSATION_PROJECTION_FLUSH_INTERVAL: value must be >= 0")
	}
	cfg.Conversation.ProjectionFlushInterval = interval
}
if raw := configValue(values, "WK_CONVERSATION_PROJECTION_SHARD_COUNT"); raw != "" {
	count, err := parseInt("WK_CONVERSATION_PROJECTION_SHARD_COUNT", raw)
	if err != nil {
		return app.Config{}, err
	}
	if count <= 0 {
		return app.Config{}, fmt.Errorf("parse WK_CONVERSATION_PROJECTION_SHARD_COUNT: value must be > 0")
	}
	cfg.Conversation.ProjectionShardCount = count
}
if raw := configValue(values, "WK_CONVERSATION_PROJECTION_MAX_DIRTY_EVENTS"); raw != "" {
	limit, err := parseInt("WK_CONVERSATION_PROJECTION_MAX_DIRTY_EVENTS", raw)
	if err != nil {
		return app.Config{}, err
	}
	if limit <= 0 {
		return app.Config{}, fmt.Errorf("parse WK_CONVERSATION_PROJECTION_MAX_DIRTY_EVENTS: value must be > 0")
	}
	cfg.Conversation.ProjectionMaxDirtyEvents = limit
}
if raw := configValue(values, "WK_CONVERSATION_PROJECTION_MAX_RETRY_PATCHES"); raw != "" {
	limit, err := parseInt("WK_CONVERSATION_PROJECTION_MAX_RETRY_PATCHES", raw)
	if err != nil {
		return app.Config{}, err
	}
	if limit <= 0 {
		return app.Config{}, fmt.Errorf("parse WK_CONVERSATION_PROJECTION_MAX_RETRY_PATCHES: value must be > 0")
	}
	cfg.Conversation.ProjectionMaxRetryPatches = limit
}
if raw := configValue(values, "WK_CONVERSATION_PROJECTION_RETRY_MAX_AGE"); raw != "" {
	age, err := parseDuration("WK_CONVERSATION_PROJECTION_RETRY_MAX_AGE", raw)
	if err != nil {
		return app.Config{}, err
	}
	if age <= 0 {
		return app.Config{}, fmt.Errorf("parse WK_CONVERSATION_PROJECTION_RETRY_MAX_AGE: value must be > 0")
	}
	cfg.Conversation.ProjectionRetryMaxAge = age
}
if raw := configValue(values, "WK_CONVERSATION_PROJECTION_ADMIT_BATCH_ROWS"); raw != "" {
	rows, err := parseInt("WK_CONVERSATION_PROJECTION_ADMIT_BATCH_ROWS", raw)
	if err != nil {
		return app.Config{}, err
	}
	if rows <= 0 {
		return app.Config{}, fmt.Errorf("parse WK_CONVERSATION_PROJECTION_ADMIT_BATCH_ROWS: value must be > 0")
	}
	cfg.Conversation.ProjectionAdmitBatchRows = rows
}
if raw := configValue(values, "WK_CONVERSATION_PROJECTION_ADMIT_CONCURRENCY"); raw != "" {
	concurrency, err := parseInt("WK_CONVERSATION_PROJECTION_ADMIT_CONCURRENCY", raw)
	if err != nil {
		return app.Config{}, err
	}
	if concurrency <= 0 {
		return app.Config{}, fmt.Errorf("parse WK_CONVERSATION_PROJECTION_ADMIT_CONCURRENCY: value must be > 0")
	}
	cfg.Conversation.ProjectionAdmitConcurrency = concurrency
}
if raw := configValue(values, "WK_CONVERSATION_PROJECTION_ADMIT_TIMEOUT"); raw != "" {
	timeout, err := parseDuration("WK_CONVERSATION_PROJECTION_ADMIT_TIMEOUT", raw)
	if err != nil {
		return app.Config{}, err
	}
	if timeout <= 0 {
		return app.Config{}, fmt.Errorf("parse WK_CONVERSATION_PROJECTION_ADMIT_TIMEOUT: value must be > 0")
	}
	cfg.Conversation.ProjectionAdmitTimeout = timeout
}
```

- [ ] **Step 5: Update `cmd/wukongimv2/config_test.go`**

Replace old authority foreground expectations with this helper assertion:

```go
func assertConversationProjectionConfig(t *testing.T, got app.ConversationConfig, want app.ConversationConfig) {
	t.Helper()
	if got.ProjectionFlushInterval != want.ProjectionFlushInterval ||
		got.ProjectionShardCount != want.ProjectionShardCount ||
		got.ProjectionMaxDirtyEvents != want.ProjectionMaxDirtyEvents ||
		got.ProjectionMaxRetryPatches != want.ProjectionMaxRetryPatches ||
		got.ProjectionRetryMaxAge != want.ProjectionRetryMaxAge ||
		got.ProjectionAdmitBatchRows != want.ProjectionAdmitBatchRows ||
		got.ProjectionAdmitConcurrency != want.ProjectionAdmitConcurrency ||
		got.ProjectionAdmitTimeout != want.ProjectionAdmitTimeout {
		t.Fatalf("conversation projection config = %#v, want %#v", got, want)
	}
}
```

Use this input in the env parsing test:

```go
"WK_CONVERSATION_PROJECTION_FLUSH_INTERVAL=250ms",
"WK_CONVERSATION_PROJECTION_SHARD_COUNT=32",
"WK_CONVERSATION_PROJECTION_MAX_DIRTY_EVENTS=50000",
"WK_CONVERSATION_PROJECTION_MAX_RETRY_PATCHES=60000",
"WK_CONVERSATION_PROJECTION_RETRY_MAX_AGE=45s",
"WK_CONVERSATION_PROJECTION_ADMIT_BATCH_ROWS=256",
"WK_CONVERSATION_PROJECTION_ADMIT_CONCURRENCY=8",
"WK_CONVERSATION_PROJECTION_ADMIT_TIMEOUT=300ms",
```

Assert:

```go
assertConversationProjectionConfig(t, cfg.Conversation, app.ConversationConfig{
	ProjectionFlushInterval:  250 * time.Millisecond,
	ProjectionShardCount:     32,
	ProjectionMaxDirtyEvents: 50000,
	ProjectionMaxRetryPatches: 60000,
	ProjectionRetryMaxAge:    45 * time.Second,
	ProjectionAdmitBatchRows: 256,
	ProjectionAdmitConcurrency: 8,
	ProjectionAdmitTimeout:   300 * time.Millisecond,
})
```

- [ ] **Step 6: Run config tests**

Run:

```bash
go test ./internalv2/app -run 'TestDefaultConversationConfigUsesAsyncProjectionDefaults|TestValidateConversationConfigRejectsInvalidAsyncProjectionValues' -count=1
go test ./cmd/wukongimv2 -run 'Test.*Conversation.*Config|TestLoadConfigConversation' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit config migration**

```bash
git add internalv2/app/config.go internalv2/app/app_test.go cmd/wukongimv2/config.go cmd/wukongimv2/config_test.go
git commit -m "refactor: rename conversation projection config"
```

## Task 2: Add Conversation Projection Metrics

**Files:**
- Modify: `pkg/metrics/conversation.go`
- Modify: `pkg/metrics/registry_test.go`
- Modify: `internalv2/app/observability.go`
- Modify: `internalv2/app/observability_test.go`

- [ ] **Step 1: Add failing metrics registry test**

Add this test near existing conversation metrics tests in `pkg/metrics/registry_test.go`:

```go
func TestConversationMetricsTrackAsyncProjection(t *testing.T) {
	reg := New(1, "node1")

	reg.Conversation.SetProjectionDirty(7, 100)
	reg.Conversation.ObserveProjectionSubmit("accepted")
	reg.Conversation.ObserveProjectionSubmit("coalesced")
	reg.Conversation.ObserveProjectionSubmit("dropped")
	reg.Conversation.ObserveProjectionFlush("ok", 12*time.Millisecond, 5, 9, 4, 1)
	reg.Conversation.ObserveProjectionMemberClassify("ok", true)
	reg.Conversation.ObserveProjectionAuthorityAdmit("timeout", 3, 1, 2)
	reg.Conversation.SetProjectionRetry(6)
	reg.Conversation.ObserveProjectionRetryDrop("age")

	families := gatherMetricFamilies(t, reg)
	requireMetricFamily(t, families, "wukongim_conversation_projection_dirty_keys")
	requireMetricFamily(t, families, "wukongim_conversation_projection_submit_total")
	requireMetricFamily(t, families, "wukongim_conversation_projection_flush_total")
	requireMetricFamily(t, families, "wukongim_conversation_projection_flush_duration_seconds")
	requireMetricFamily(t, families, "wukongim_conversation_projection_flush_events")
	requireMetricFamily(t, families, "wukongim_conversation_projection_flush_patches")
	requireMetricFamily(t, families, "wukongim_conversation_projection_member_classify_total")
	requireMetricFamily(t, families, "wukongim_conversation_projection_authority_admit_total")
	requireMetricFamily(t, families, "wukongim_conversation_projection_retry_patches")
	requireMetricFamily(t, families, "wukongim_conversation_projection_retry_drop_total")
	requireNoMetricFamily(t, families, "wukongim_conversation_projection_uid")
	requireNoMetricFamily(t, families, "wukongim_conversation_projection_channel_id")
}
```

- [ ] **Step 2: Run failing metrics test**

Run:

```bash
go test ./pkg/metrics -run TestConversationMetricsTrackAsyncProjection -count=1
```

Expected: FAIL because projection metric methods do not exist.

- [ ] **Step 3: Add metric fields and methods**

In `pkg/metrics/conversation.go`, add these fields to `ConversationMetrics`:

```go
projectionDirtyKeys        prometheus.Gauge
projectionDirtyCapacity    prometheus.Gauge
projectionSubmitTotal      *prometheus.CounterVec
projectionFlushTotal       *prometheus.CounterVec
projectionFlushDuration    *prometheus.HistogramVec
projectionFlushEvents      *prometheus.HistogramVec
projectionFlushPatches     *prometheus.HistogramVec
projectionMemberClassify   *prometheus.CounterVec
projectionAuthorityAdmit   *prometheus.CounterVec
projectionAuthorityTargets *prometheus.HistogramVec
projectionAuthorityBatches *prometheus.HistogramVec
projectionRetryPatches     prometheus.Gauge
projectionRetryDropTotal   *prometheus.CounterVec
```

Initialize and register them in `newConversationMetrics`. Use metric names from the failing test. Use labels:

```go
[]string{"result"}
[]string{"result", "cache_hit"}
[]string{"result"}
[]string{"side"}
[]string{"reason"}
```

Add these methods:

```go
func (m *ConversationMetrics) SetProjectionDirty(keys, capacity int) {
	if m == nil {
		return
	}
	m.projectionDirtyKeys.Set(float64(nonNegative(keys)))
	m.projectionDirtyCapacity.Set(float64(nonNegative(capacity)))
}

func (m *ConversationMetrics) ObserveProjectionSubmit(result string) {
	if m == nil {
		return
	}
	m.projectionSubmitTotal.WithLabelValues(conversationProjectionResult(result)).Inc()
}

func (m *ConversationMetrics) ObserveProjectionFlush(result string, dur time.Duration, events, patches, dense, sparse int) {
	if m == nil {
		return
	}
	label := conversationProjectionResult(result)
	m.projectionFlushTotal.WithLabelValues(label).Inc()
	m.projectionFlushDuration.WithLabelValues(label).Observe(dur.Seconds())
	m.projectionFlushEvents.WithLabelValues("drained").Observe(float64(nonNegative(events)))
	m.projectionFlushPatches.WithLabelValues("projected").Observe(float64(nonNegative(patches)))
	m.projectionFlushEvents.WithLabelValues("dense").Observe(float64(nonNegative(dense)))
	m.projectionFlushEvents.WithLabelValues("sparse").Observe(float64(nonNegative(sparse)))
}

func (m *ConversationMetrics) ObserveProjectionMemberClassify(result string, cacheHit bool) {
	if m == nil {
		return
	}
	m.projectionMemberClassify.WithLabelValues(conversationProjectionResult(result), strconv.FormatBool(cacheHit)).Inc()
}

func (m *ConversationMetrics) ObserveProjectionAuthorityAdmit(result string, targets, localBatches, remoteBatches int) {
	if m == nil {
		return
	}
	label := conversationProjectionResult(result)
	m.projectionAuthorityAdmit.WithLabelValues(label).Inc()
	m.projectionAuthorityTargets.WithLabelValues(label).Observe(float64(nonNegative(targets)))
	m.projectionAuthorityBatches.WithLabelValues("local").Observe(float64(nonNegative(localBatches)))
	m.projectionAuthorityBatches.WithLabelValues("remote").Observe(float64(nonNegative(remoteBatches)))
}

func (m *ConversationMetrics) SetProjectionRetry(patches int) {
	if m == nil {
		return
	}
	m.projectionRetryPatches.Set(float64(nonNegative(patches)))
}

func (m *ConversationMetrics) ObserveProjectionRetryDrop(reason string) {
	if m == nil {
		return
	}
	m.projectionRetryDropTotal.WithLabelValues(conversationProjectionRetryDropReason(reason)).Inc()
}
```

Add normalizers:

```go
func conversationProjectionResult(result string) string {
	switch result {
	case "ok", "error", "accepted", "coalesced", "dropped", "ignored", "cache_pressure", "route_not_ready", "stale_route", "not_leader", "timeout":
		return result
	default:
		return "other"
	}
}

func conversationProjectionRetryDropReason(reason string) string {
	switch reason {
	case "capacity", "age", "invalid":
		return reason
	default:
		return "other"
	}
}
```

- [ ] **Step 4: Add app observer event types and mapper**

In `internalv2/app/observability.go`, add:

```go
type conversationProjectionMetricsObserver struct {
	metrics *obsmetrics.Registry
}

func (o conversationProjectionMetricsObserver) SetConversationProjectionDirty(event conversationProjectionDirtyEvent) {
	if o.metrics == nil || o.metrics.Conversation == nil {
		return
	}
	o.metrics.Conversation.SetProjectionDirty(event.DirtyKeys, event.MaxDirtyEvents)
}

func (o conversationProjectionMetricsObserver) ObserveConversationProjectionSubmit(event conversationProjectionSubmitEvent) {
	if o.metrics == nil || o.metrics.Conversation == nil {
		return
	}
	o.metrics.Conversation.ObserveProjectionSubmit(event.Result)
}

func (o conversationProjectionMetricsObserver) ObserveConversationProjectionFlush(event conversationProjectionFlushEvent) {
	if o.metrics == nil || o.metrics.Conversation == nil {
		return
	}
	o.metrics.Conversation.ObserveProjectionFlush(event.Result, event.Duration, event.DrainedEvents, event.ProjectedPatches, event.DenseEvents, event.SparseEvents)
}

func (o conversationProjectionMetricsObserver) ObserveConversationProjectionMemberClassify(event conversationProjectionMemberClassifyEvent) {
	if o.metrics == nil || o.metrics.Conversation == nil {
		return
	}
	o.metrics.Conversation.ObserveProjectionMemberClassify(event.Result, event.CacheHit)
}

func (o conversationProjectionMetricsObserver) ObserveConversationProjectionAuthorityAdmit(event conversationProjectionAuthorityAdmitEvent) {
	if o.metrics == nil || o.metrics.Conversation == nil {
		return
	}
	o.metrics.Conversation.ObserveProjectionAuthorityAdmit(event.Result, event.TargetGroups, event.LocalBatches, event.RemoteBatches)
}

func (o conversationProjectionMetricsObserver) SetConversationProjectionRetry(event conversationProjectionRetryEvent) {
	if o.metrics == nil || o.metrics.Conversation == nil {
		return
	}
	o.metrics.Conversation.SetProjectionRetry(event.Patches)
}

func (o conversationProjectionMetricsObserver) ObserveConversationProjectionRetryDrop(event conversationProjectionRetryDropEvent) {
	if o.metrics == nil || o.metrics.Conversation == nil {
		return
	}
	o.metrics.Conversation.ObserveProjectionRetryDrop(event.Reason)
}
```

In `internalv2/app/app.go`, add:

```go
func (a *App) conversationProjectionObserver() conversationProjectionObserver {
	if a == nil || a.metrics == nil {
		return nil
	}
	return conversationProjectionMetricsObserver{metrics: a.metrics}
}
```

- [ ] **Step 5: Run metrics tests**

Run:

```bash
go test ./pkg/metrics -run 'TestConversationMetrics' -count=1
go test ./internalv2/app -run 'TestObservabilityConversation' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit metrics**

```bash
git add pkg/metrics/conversation.go pkg/metrics/registry_test.go internalv2/app/observability.go internalv2/app/observability_test.go internalv2/app/app.go
git commit -m "feat: observe async conversation projection"
```

## Task 3: Add Async Projector Runtime

**Files:**
- Create: `internalv2/app/conversation_async_projector.go`
- Create: `internalv2/app/conversation_async_projector_test.go`

- [ ] **Step 1: Add failing tests for foreground submit**

Create `internalv2/app/conversation_async_projector_test.go` with:

```go
package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
)

func TestConversationAsyncProjectorSubmitCoalescesNewestEvent(t *testing.T) {
	observer := &recordingConversationProjectionObserver{}
	projector := newConversationAsyncProjector(conversationAsyncProjectorOptions{
		ShardCount:     4,
		MaxDirtyEvents: 10,
		Observer:       observer,
	})

	projector.Submit(context.Background(), committedProjectionEvent("g1", 2, "u1", 1, 100))
	projector.Submit(context.Background(), committedProjectionEvent("g1", 2, "u1", 2, 200))

	events := projector.drainEvents()
	if len(events) != 1 {
		t.Fatalf("drained events = %d, want 1", len(events))
	}
	if events[0].MessageSeq != 2 || events[0].ServerTimestampMS != 200 {
		t.Fatalf("drained event = %#v, want newest seq/time", events[0])
	}
	if got := observer.submitResults(); !containsString(got, conversationProjectionResultAccepted) || !containsString(got, conversationProjectionResultCoalesced) {
		t.Fatalf("submit results = %v, want accepted and coalesced", got)
	}
}

func TestConversationAsyncProjectorSubmitDropsNewKeyWhenDirtyFull(t *testing.T) {
	observer := &recordingConversationProjectionObserver{}
	projector := newConversationAsyncProjector(conversationAsyncProjectorOptions{
		ShardCount:     2,
		MaxDirtyEvents: 1,
		Observer:       observer,
	})

	projector.Submit(context.Background(), committedProjectionEvent("g1", 2, "u1", 1, 100))
	projector.Submit(context.Background(), committedProjectionEvent("g2", 2, "u2", 1, 100))

	events := projector.drainEvents()
	if len(events) != 1 || events[0].ChannelID != "g1" {
		t.Fatalf("drained events = %#v, want only first dirty key", events)
	}
	if got := observer.submitResults(); !containsString(got, conversationProjectionResultDropped) {
		t.Fatalf("submit results = %v, want dropped", got)
	}
}

func TestConversationAsyncProjectorSubmitDoesNotCallProjectorOrAuthority(t *testing.T) {
	projector := newConversationAsyncProjector(conversationAsyncProjectorOptions{
		Projector:      panicConversationProjector{},
		Authority:      panicConversationPatchAuthority{},
		ShardCount:     2,
		MaxDirtyEvents: 10,
	})

	projector.Submit(context.Background(), committedProjectionEvent("g1", 2, "u1", 1, 100))
}
```

Add helper fakes in the same file:

```go
func committedProjectionEvent(channelID string, channelType uint8, fromUID string, seq uint64, ts int64) messageevents.MessageCommitted {
	return messageevents.MessageCommitted{
		MessageID:         seq,
		MessageSeq:        seq,
		ChannelID:         channelID,
		ChannelType:       channelType,
		FromUID:           fromUID,
		ServerTimestampMS: ts,
		Payload:           []byte("payload"),
		MessageScopedUIDs: []string{"scoped"},
	}
}

type panicConversationProjector struct{}

func (panicConversationProjector) ProjectActivePatches(context.Context, messageevents.MessageCommitted) ([]conversationusecase.ActivePatch, error) {
	panic("ProjectActivePatches must not run in Submit")
}

type panicConversationPatchAuthority struct{}

func (panicConversationPatchAuthority) AdmitPatches(context.Context, []conversationusecase.ActivePatch) error {
	panic("AdmitPatches must not run in Submit")
}
```

- [ ] **Step 2: Run failing foreground tests**

Run:

```bash
go test ./internalv2/app -run 'TestConversationAsyncProjectorSubmit' -count=1
```

Expected: FAIL because `conversationAsyncProjector` does not exist.

- [ ] **Step 3: Implement foreground runtime state**

Create `internalv2/app/conversation_async_projector.go` with the package, imports, constants, options, observer, and core state:

```go
package app

import (
	"context"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
)

const (
	conversationProjectionResultAccepted      = "accepted"
	conversationProjectionResultCoalesced     = "coalesced"
	conversationProjectionResultDropped       = "dropped"
	conversationProjectionResultIgnored       = "ignored"
	conversationProjectionResultOK            = "ok"
	conversationProjectionResultError         = "error"
	conversationProjectionResultRouteNotReady = "route_not_ready"
	conversationProjectionResultStaleRoute    = "stale_route"
	conversationProjectionResultNotLeader     = "not_leader"
	conversationProjectionResultTimeout       = "timeout"
	conversationProjectionRetryDropCapacity   = "capacity"
	conversationProjectionRetryDropAge        = "age"
	conversationProjectionRetryDropInvalid    = "invalid"
)

type conversationAsyncProjectorOptions struct {
	Projector              conversationPatchProjector
	Authority              conversationPatchAuthority
	Flusher                conversationAuthorityFlusher
	FlushInterval          time.Duration
	ShardCount             int
	MaxDirtyEvents         int
	MaxRetryPatches        int
	RetryMaxAge            time.Duration
	AdmitBatchRows         int
	AdmitConcurrency       int
	AdmitTimeout           time.Duration
	Observer               conversationProjectionObserver
}

type conversationProjectionObserver interface {
	SetConversationProjectionDirty(conversationProjectionDirtyEvent)
	ObserveConversationProjectionSubmit(conversationProjectionSubmitEvent)
	ObserveConversationProjectionFlush(conversationProjectionFlushEvent)
	ObserveConversationProjectionMemberClassify(conversationProjectionMemberClassifyEvent)
	ObserveConversationProjectionAuthorityAdmit(conversationProjectionAuthorityAdmitEvent)
	SetConversationProjectionRetry(conversationProjectionRetryEvent)
	ObserveConversationProjectionRetryDrop(conversationProjectionRetryDropEvent)
}

type conversationProjectionDirtyEvent struct {
	DirtyKeys      int
	MaxDirtyEvents int
}

type conversationProjectionSubmitEvent struct {
	Result string
}

type conversationProjectionFlushEvent struct {
	Result           string
	Duration         time.Duration
	DrainedEvents    int
	ProjectedPatches int
	DenseEvents      int
	SparseEvents     int
	RetryPatches     int
}

type conversationProjectionMemberClassifyEvent struct {
	Result   string
	CacheHit bool
}

type conversationProjectionAuthorityAdmitEvent struct {
	Result        string
	TargetGroups  int
	LocalBatches  int
	RemoteBatches int
}

type conversationProjectionRetryEvent struct {
	Patches int
}

type conversationProjectionRetryDropEvent struct {
	Reason string
}

type conversationAsyncProjector struct {
	projector        conversationPatchProjector
	authority        conversationPatchAuthority
	flusher          conversationAuthorityFlusher
	flushInterval    time.Duration
	maxDirtyEvents   int
	maxRetryPatches  int
	retryMaxAge      time.Duration
	admitBatchRows   int
	admitConcurrency int
	admitTimeout     time.Duration
	observer         conversationProjectionObserver

	shards      []conversationAsyncProjectorShard
	dirtyEvents atomic.Int64

	retryMu sync.Mutex
	retry   map[conversationAuthorityPendingKey]conversationProjectionRetryPatch

	runMu     sync.Mutex
	started   bool
	cancelRun context.CancelFunc
	doneCh    chan struct{}
	flushMu   sync.Mutex
}

type conversationAsyncProjectorShard struct {
	mu     sync.Mutex
	events map[conversationAsyncProjectorKey]messageevents.MessageCommitted
}

type conversationAsyncProjectorKey struct {
	channelID   string
	channelType uint8
	fromUID     string
}

type conversationProjectionRetryPatch struct {
	patch     conversationusecase.ActivePatch
	createdAt time.Time
}
```

Add constructor, submit, drain, and helpers:

```go
func newConversationAsyncProjector(opts conversationAsyncProjectorOptions) *conversationAsyncProjector {
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = 100 * time.Millisecond
	}
	if opts.ShardCount <= 0 {
		opts.ShardCount = 64
	}
	if opts.MaxDirtyEvents <= 0 {
		opts.MaxDirtyEvents = 100000
	}
	if opts.MaxRetryPatches <= 0 {
		opts.MaxRetryPatches = 100000
	}
	if opts.RetryMaxAge <= 0 {
		opts.RetryMaxAge = 30 * time.Second
	}
	if opts.AdmitBatchRows <= 0 {
		opts.AdmitBatchRows = 512
	}
	if opts.AdmitConcurrency <= 0 {
		opts.AdmitConcurrency = 16
	}
	if opts.AdmitTimeout <= 0 {
		opts.AdmitTimeout = 500 * time.Millisecond
	}
	p := &conversationAsyncProjector{
		projector:        opts.Projector,
		authority:        opts.Authority,
		flusher:          opts.Flusher,
		flushInterval:    opts.FlushInterval,
		maxDirtyEvents:   opts.MaxDirtyEvents,
		maxRetryPatches:  opts.MaxRetryPatches,
		retryMaxAge:      opts.RetryMaxAge,
		admitBatchRows:   opts.AdmitBatchRows,
		admitConcurrency: opts.AdmitConcurrency,
		admitTimeout:     opts.AdmitTimeout,
		observer:         opts.Observer,
		shards:           make([]conversationAsyncProjectorShard, opts.ShardCount),
		retry:            make(map[conversationAuthorityPendingKey]conversationProjectionRetryPatch),
	}
	for i := range p.shards {
		p.shards[i].events = make(map[conversationAsyncProjectorKey]messageevents.MessageCommitted)
	}
	p.observeDirty()
	p.observeRetry()
	return p
}

func (p *conversationAsyncProjector) Submit(_ context.Context, event messageevents.MessageCommitted) error {
	if p == nil || len(p.shards) == 0 || event.ChannelID == "" || event.ChannelType == 0 {
		p.observeSubmit(conversationProjectionResultIgnored)
		return nil
	}
	event.Payload = nil
	event.MessageScopedUIDs = nil
	key := conversationAsyncProjectorKey{channelID: event.ChannelID, channelType: event.ChannelType, fromUID: event.FromUID}
	shard := &p.shards[p.shardIndex(key)]
	shard.mu.Lock()
	existing, exists := shard.events[key]
	if !exists && !p.tryReserveDirtyEvent() {
		shard.mu.Unlock()
		p.observeSubmit(conversationProjectionResultDropped)
		return nil
	}
	if !exists || committedEventAfter(event, existing) {
		shard.events[key] = event
	}
	shard.mu.Unlock()
	if exists {
		p.observeSubmit(conversationProjectionResultCoalesced)
		return nil
	}
	p.observeDirty()
	p.observeSubmit(conversationProjectionResultAccepted)
	return nil
}

func (p *conversationAsyncProjector) drainEvents() []messageevents.MessageCommitted {
	if p == nil {
		return nil
	}
	var events []messageevents.MessageCommitted
	for i := range p.shards {
		shard := &p.shards[i]
		shard.mu.Lock()
		for key, event := range shard.events {
			events = append(events, event)
			delete(shard.events, key)
			p.dirtyEvents.Add(-1)
		}
		shard.mu.Unlock()
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].ChannelID != events[j].ChannelID {
			return events[i].ChannelID < events[j].ChannelID
		}
		if events[i].ChannelType != events[j].ChannelType {
			return events[i].ChannelType < events[j].ChannelType
		}
		return events[i].FromUID < events[j].FromUID
	})
	p.observeDirty()
	return events
}

func (p *conversationAsyncProjector) tryReserveDirtyEvent() bool {
	limit := int64(p.maxDirtyEvents)
	for {
		current := p.dirtyEvents.Load()
		if current >= limit {
			return false
		}
		if p.dirtyEvents.CompareAndSwap(current, current+1) {
			return true
		}
	}
}

func committedEventAfter(next, existing messageevents.MessageCommitted) bool {
	if next.MessageSeq != existing.MessageSeq {
		return next.MessageSeq > existing.MessageSeq
	}
	return next.ServerTimestampMS >= existing.ServerTimestampMS
}
```

Add observer helpers:

```go
func (p *conversationAsyncProjector) observeDirty() {
	if p == nil || p.observer == nil {
		return
	}
	p.observer.SetConversationProjectionDirty(conversationProjectionDirtyEvent{DirtyKeys: int(p.dirtyEvents.Load()), MaxDirtyEvents: p.maxDirtyEvents})
}

func (p *conversationAsyncProjector) observeSubmit(result string) {
	if p == nil || p.observer == nil {
		return
	}
	p.observer.ObserveConversationProjectionSubmit(conversationProjectionSubmitEvent{Result: result})
}

func (p *conversationAsyncProjector) observeRetry() {
	if p == nil || p.observer == nil {
		return
	}
	p.retryMu.Lock()
	count := len(p.retry)
	p.retryMu.Unlock()
	p.observer.SetConversationProjectionRetry(conversationProjectionRetryEvent{Patches: count})
}
```

- [ ] **Step 4: Add test helper observer**

Append this to `conversation_async_projector_test.go`:

```go
type recordingConversationProjectionObserver struct {
	mu      sync.Mutex
	submits []string
	dirty   []conversationProjectionDirtyEvent
}

func (o *recordingConversationProjectionObserver) SetConversationProjectionDirty(event conversationProjectionDirtyEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.dirty = append(o.dirty, event)
}

func (o *recordingConversationProjectionObserver) ObserveConversationProjectionSubmit(event conversationProjectionSubmitEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.submits = append(o.submits, event.Result)
}

func (o *recordingConversationProjectionObserver) ObserveConversationProjectionFlush(conversationProjectionFlushEvent) {}
func (o *recordingConversationProjectionObserver) ObserveConversationProjectionMemberClassify(conversationProjectionMemberClassifyEvent) {}
func (o *recordingConversationProjectionObserver) ObserveConversationProjectionAuthorityAdmit(conversationProjectionAuthorityAdmitEvent) {}
func (o *recordingConversationProjectionObserver) SetConversationProjectionRetry(conversationProjectionRetryEvent) {}
func (o *recordingConversationProjectionObserver) ObserveConversationProjectionRetryDrop(conversationProjectionRetryDropEvent) {}

func (o *recordingConversationProjectionObserver) submitResults() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.submits...)
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
```

- [ ] **Step 5: Run foreground submit tests**

Run:

```bash
go test ./internalv2/app -run 'TestConversationAsyncProjectorSubmit' -count=1
```

Expected: PASS.

- [ ] **Step 6: Add failing flush and retry tests**

Add:

```go
func TestConversationAsyncProjectorFlushProjectsAndAdmits(t *testing.T) {
	projectorPort := &recordingConversationPatchProjector{
		patches: []conversationusecase.ActivePatch{
			{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 100, UpdatedAt: 100, MessageSeq: 1},
			{UID: "u2", ChannelID: "g1", ChannelType: 2, ActiveAt: 100, UpdatedAt: 100, MessageSeq: 1},
		},
	}
	authority := &recordingConversationPatchAuthority{}
	projector := newConversationAsyncProjector(conversationAsyncProjectorOptions{
		Projector:      projectorPort,
		Authority:      authority,
		ShardCount:     2,
		MaxDirtyEvents: 10,
	})

	projector.Submit(context.Background(), committedProjectionEvent("g1", 2, "u1", 1, 100))
	if err := projector.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	if projectorPort.calls != 1 {
		t.Fatalf("projector calls = %d, want 1", projectorPort.calls)
	}
	if got := authority.patchCount(); got != 2 {
		t.Fatalf("authority patch count = %d, want 2", got)
	}
}

func TestConversationAsyncProjectorFlushRetriesAdmissionFailure(t *testing.T) {
	projectorPort := &recordingConversationPatchProjector{
		patches: []conversationusecase.ActivePatch{{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 100, UpdatedAt: 100, MessageSeq: 1}},
	}
	authority := &recordingConversationPatchAuthority{err: errors.New("temporary")}
	projector := newConversationAsyncProjector(conversationAsyncProjectorOptions{
		Projector:         projectorPort,
		Authority:         authority,
		ShardCount:        2,
		MaxDirtyEvents:    10,
		MaxRetryPatches:   10,
		RetryMaxAge:       time.Minute,
		AdmitBatchRows:    10,
		AdmitConcurrency:  1,
		AdmitTimeout:      time.Second,
	})

	projector.Submit(context.Background(), committedProjectionEvent("g1", 2, "u1", 1, 100))
	if err := projector.Flush(context.Background()); err == nil {
		t.Fatalf("Flush() error = nil, want admission error")
	}
	if got := projector.retryCount(); got != 1 {
		t.Fatalf("retry count = %d, want 1", got)
	}

	authority.err = nil
	if err := projector.Flush(context.Background()); err != nil {
		t.Fatalf("second Flush() error = %v", err)
	}
	if got := projector.retryCount(); got != 0 {
		t.Fatalf("retry count after success = %d, want 0", got)
	}
}
```

Add fakes:

```go
type recordingConversationPatchProjector struct {
	calls   int
	patches []conversationusecase.ActivePatch
	err     error
}

func (p *recordingConversationPatchProjector) ProjectActivePatches(_ context.Context, event messageevents.MessageCommitted) ([]conversationusecase.ActivePatch, error) {
	p.calls++
	if p.err != nil {
		return nil, p.err
	}
	out := append([]conversationusecase.ActivePatch(nil), p.patches...)
	for i := range out {
		if out[i].MessageSeq == 0 {
			out[i].MessageSeq = event.MessageSeq
		}
		if out[i].ActiveAt == 0 {
			out[i].ActiveAt = event.ServerTimestampMS
		}
	}
	return out, nil
}

type recordingConversationPatchAuthority struct {
	mu      sync.Mutex
	patches []conversationusecase.ActivePatch
	err     error
}

func (a *recordingConversationPatchAuthority) AdmitPatches(_ context.Context, patches []conversationusecase.ActivePatch) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return a.err
	}
	a.patches = append(a.patches, patches...)
	return nil
}

func (a *recordingConversationPatchAuthority) patchCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.patches)
}
```

- [ ] **Step 7: Implement flush, admit, and retry**

Add to `conversation_async_projector.go`:

```go
func (p *conversationAsyncProjector) Flush(ctx context.Context) error {
	if p == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	p.flushMu.Lock()
	defer p.flushMu.Unlock()
	startedAt := time.Now()
	events := p.drainEvents()
	retryPatches := p.drainRetry(time.Now())
	patches, dense, sparse, projectErr := p.projectEvents(ctx, events)
	patches = append(patches, retryPatches...)
	if len(patches) == 0 {
		p.observeFlush(conversationProjectionFlushEvent{
			Result:        projectionFlushResult(projectErr),
			Duration:      time.Since(startedAt),
			DrainedEvents: len(events),
			DenseEvents:   dense,
			SparseEvents:  sparse,
		})
		return projectErr
	}
	admitErr := p.admitPatches(ctx, patches)
	if admitErr != nil {
		p.rememberRetry(patches, time.Now())
	}
	err := errors.Join(projectErr, admitErr)
	p.observeFlush(conversationProjectionFlushEvent{
		Result:           projectionFlushResult(err),
		Duration:         time.Since(startedAt),
		DrainedEvents:    len(events),
		ProjectedPatches: len(patches),
		DenseEvents:      dense,
		SparseEvents:     sparse,
		RetryPatches:     p.retryCount(),
	})
	return err
}

func (p *conversationAsyncProjector) projectEvents(ctx context.Context, events []messageevents.MessageCommitted) ([]conversationusecase.ActivePatch, int, int, error) {
	if p == nil || p.projector == nil {
		return nil, 0, 0, nil
	}
	var patches []conversationusecase.ActivePatch
	var firstErr error
	dense := 0
	sparse := 0
	for _, event := range events {
		projected, err := p.projector.ProjectActivePatches(ctx, event)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if len(projected) == 0 {
			continue
		}
		if conversationPatchesSparse(projected) {
			sparse++
		} else {
			dense++
		}
		patches = append(patches, projected...)
	}
	return coalesceConversationPatches(patches), dense, sparse, firstErr
}

func (p *conversationAsyncProjector) admitPatches(ctx context.Context, patches []conversationusecase.ActivePatch) error {
	if p == nil || p.authority == nil || len(patches) == 0 {
		return nil
	}
	batches := conversationActivePatchBatches(patches, p.admitBatchRows)
	if len(batches) == 0 {
		return nil
	}
	if p.admitConcurrency <= 1 || len(batches) == 1 {
		for _, batch := range batches {
			if err := p.admitBatch(ctx, batch); err != nil {
				p.observeAuthorityAdmit(conversationProjectionResultFromError(err), 1, 0, 0)
				return err
			}
		}
		p.observeAuthorityAdmit(conversationProjectionResultOK, len(batches), len(batches), 0)
		return nil
	}
	workers := p.admitConcurrency
	if workers > len(batches) {
		workers = len(batches)
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan []conversationusecase.ActivePatch)
	var wg sync.WaitGroup
	var firstErr error
	var firstErrOnce sync.Once
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for batch := range jobs {
				if err := p.admitBatch(workCtx, batch); err != nil {
					firstErrOnce.Do(func() {
						firstErr = err
						cancel()
					})
				}
			}
		}()
	}
send:
	for _, batch := range batches {
		select {
		case <-workCtx.Done():
			break send
		case jobs <- batch:
		}
	}
	close(jobs)
	wg.Wait()
	p.observeAuthorityAdmit(conversationProjectionResultFromError(firstErr), len(batches), len(batches), 0)
	return firstErr
}

func (p *conversationAsyncProjector) admitBatch(ctx context.Context, patches []conversationusecase.ActivePatch) error {
	if p.admitTimeout <= 0 {
		return p.authority.AdmitPatches(ctx, patches)
	}
	admitCtx, cancel := context.WithTimeout(ctx, p.admitTimeout)
	defer cancel()
	return p.authority.AdmitPatches(admitCtx, patches)
}
```

Add retry helpers:

```go
func (p *conversationAsyncProjector) rememberRetry(patches []conversationusecase.ActivePatch, now time.Time) {
	if p == nil || len(patches) == 0 {
		return
	}
	p.retryMu.Lock()
	defer p.retryMu.Unlock()
	for _, patch := range patches {
		if patch.UID == "" || patch.ChannelID == "" || patch.ChannelType == 0 {
			p.observeRetryDrop(conversationProjectionRetryDropInvalid)
			continue
		}
		key := pendingConversationPatchKey(patch)
		if len(p.retry) >= p.maxRetryPatches {
			p.dropOldestRetryLocked()
		}
		existing := p.retry[key]
		if existing.createdAt.IsZero() {
			existing.createdAt = now
		}
		existing.patch = mergeConversationActivePatch(existing.patch, patch)
		p.retry[key] = existing
	}
	p.observeRetryLocked()
}

func (p *conversationAsyncProjector) drainRetry(now time.Time) []conversationusecase.ActivePatch {
	p.retryMu.Lock()
	defer p.retryMu.Unlock()
	if len(p.retry) == 0 {
		return nil
	}
	patches := make(map[conversationAuthorityPendingKey]conversationusecase.ActivePatch, len(p.retry))
	for key, retry := range p.retry {
		if p.retryMaxAge > 0 && now.Sub(retry.createdAt) > p.retryMaxAge {
			delete(p.retry, key)
			p.observeRetryDrop(conversationProjectionRetryDropAge)
			continue
		}
		patches[key] = retry.patch
		delete(p.retry, key)
	}
	p.observeRetryLocked()
	return sortedPendingConversationPatches(patches)
}

func (p *conversationAsyncProjector) retryCount() int {
	if p == nil {
		return 0
	}
	p.retryMu.Lock()
	defer p.retryMu.Unlock()
	return len(p.retry)
}
```

Add helper functions:

```go
func coalesceConversationPatches(patches []conversationusecase.ActivePatch) []conversationusecase.ActivePatch {
	if len(patches) == 0 {
		return nil
	}
	merged := make(map[conversationAuthorityPendingKey]conversationusecase.ActivePatch, len(patches))
	for _, patch := range patches {
		if patch.UID == "" || patch.ChannelID == "" || patch.ChannelType == 0 {
			continue
		}
		key := pendingConversationPatchKey(patch)
		merged[key] = mergeConversationActivePatch(merged[key], patch)
	}
	return sortedPendingConversationPatches(merged)
}

func conversationPatchesSparse(patches []conversationusecase.ActivePatch) bool {
	for _, patch := range patches {
		if patch.SparseActive {
			return true
		}
	}
	return false
}

func projectionFlushResult(err error) string {
	if err == nil {
		return conversationProjectionResultOK
	}
	return conversationProjectionResultFromError(err)
}

func conversationProjectionResultFromError(err error) string {
	switch {
	case err == nil:
		return conversationProjectionResultOK
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return conversationProjectionResultTimeout
	case errors.Is(err, conversationusecase.ErrRouteNotReady):
		return conversationProjectionResultRouteNotReady
	case errors.Is(err, conversationusecase.ErrStaleRoute):
		return conversationProjectionResultStaleRoute
	case errors.Is(err, conversationusecase.ErrNotLeader):
		return conversationProjectionResultNotLeader
	default:
		return conversationProjectionResultError
	}
}
```

- [ ] **Step 8: Add lifecycle and thin sink**

Add:

```go
func (p *conversationAsyncProjector) Start(ctx context.Context) error {
	if p == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	p.runMu.Lock()
	defer p.runMu.Unlock()
	if p.started {
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	p.cancelRun = cancel
	p.doneCh = make(chan struct{})
	p.started = true
	go p.run(runCtx, p.doneCh)
	return nil
}

func (p *conversationAsyncProjector) Stop(ctx context.Context) error {
	if p == nil {
		return nil
	}
	p.runMu.Lock()
	cancel := p.cancelRun
	doneCh := p.doneCh
	started := p.started
	p.cancelRun = nil
	p.doneCh = nil
	p.started = false
	p.runMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if started && doneCh != nil {
		select {
		case <-doneCh:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return p.Flush(ctx)
}

func (p *conversationAsyncProjector) run(ctx context.Context, doneCh chan<- struct{}) {
	ticker := time.NewTicker(p.flushInterval)
	defer func() {
		ticker.Stop()
		close(doneCh)
	}()
	for {
		select {
		case <-ticker.C:
			_ = p.Flush(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (p *conversationAsyncProjector) RequiresCommittedPayload() bool {
	return false
}

func (p *conversationAsyncProjector) SubmitMetadata(ctx context.Context, event messageevents.MessageCommitted) error {
	return p.Submit(ctx, event)
}
```

Ensure `conversationAsyncProjector` satisfies:

```go
var _ message.CommittedSink = (*conversationAsyncProjector)(nil)
var _ message.CommittedPayloadPolicy = (*conversationAsyncProjector)(nil)
```

- [ ] **Step 9: Run async projector tests**

Run:

```bash
go test ./internalv2/app -run 'TestConversationAsyncProjector' -count=1
```

Expected: PASS.

- [ ] **Step 10: Commit async runtime**

```bash
git add internalv2/app/conversation_async_projector.go internalv2/app/conversation_async_projector_test.go
git commit -m "feat: add async conversation projector"
```

## Task 4: Split Route Authority Lifecycle From Projection

**Files:**
- Create: `internalv2/app/conversation_route_lifecycle.go`
- Create: `internalv2/app/conversation_route_lifecycle_test.go`
- Modify: `internalv2/app/conversation.go`

- [ ] **Step 1: Add route lifecycle tests**

Create `internalv2/app/conversation_route_lifecycle_test.go`:

```go
package app

import (
	"context"
	"testing"
	"time"

	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
)

func TestConversationAuthorityRouteLifecycleAppliesInitialAuthorities(t *testing.T) {
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: &recordingConversationAuthorityStore{}})
	lifecycle := newConversationAuthorityRouteLifecycle(conversationAuthorityRouteLifecycleOptions{
		LocalAuthority: authority,
		LocalNodeID:    1,
		Initial: func() []clusterv2.RouteAuthority {
			return []clusterv2.RouteAuthority{{HashSlot: 7, SlotID: 3, LeaderNodeID: 1, RouteRevision: 10, AuthorityEpoch: 2}}
		},
	})

	if err := lifecycle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = lifecycle.Stop(context.Background()) })

	err := authority.AdmitPatches(context.Background(), conversationusecase.RouteTarget{HashSlot: 7, SlotID: 3, LeaderNodeID: 1, RouteRevision: 10, AuthorityEpoch: 2}, []conversationusecase.ActivePatch{
		{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 100},
	})
	if err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
}

func TestConversationAuthorityRouteLifecycleDrainsPreviousLocalTarget(t *testing.T) {
	store := &recordingConversationAuthorityStore{}
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store})
	oldTarget := conversationusecase.RouteTarget{HashSlot: 7, SlotID: 3, LeaderNodeID: 1, RouteRevision: 10, AuthorityEpoch: 1}
	newTarget := conversationusecase.RouteTarget{HashSlot: 7, SlotID: 3, LeaderNodeID: 2, RouteRevision: 11, AuthorityEpoch: 2}
	authority.markActive(oldTarget)
	if err := authority.AdmitPatches(context.Background(), oldTarget, []conversationusecase.ActivePatch{{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 100}}); err != nil {
		t.Fatalf("seed AdmitPatches() error = %v", err)
	}
	lifecycle := newConversationAuthorityRouteLifecycle(conversationAuthorityRouteLifecycleOptions{
		LocalAuthority: authority,
		LocalNodeID:    1,
		HandoffTimeout: time.Second,
	})

	lifecycle.handleRouteAuthority(context.Background(), clusterv2.RouteAuthority{
		HashSlot: newTarget.HashSlot, SlotID: newTarget.SlotID, LeaderNodeID: newTarget.LeaderNodeID, RouteRevision: newTarget.RouteRevision, AuthorityEpoch: newTarget.AuthorityEpoch,
	})

	if len(store.touched) != 1 {
		t.Fatalf("flushed patches = %d, want 1", len(store.touched))
	}
}
```

- [ ] **Step 2: Run failing route lifecycle tests**

Run:

```bash
go test ./internalv2/app -run 'TestConversationAuthorityRouteLifecycle' -count=1
```

Expected: FAIL because the lifecycle type does not exist.

- [ ] **Step 3: Implement route lifecycle**

Move route watch, initial apply, accept target, and drain logic from `conversation.go` into `conversation_route_lifecycle.go`:

```go
package app

import (
	"context"
	"sync"
	"time"

	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
)

type conversationAuthorityRouteLifecycleOptions struct {
	LocalAuthority *conversationAuthority
	LocalNodeID    uint64
	Initial        func() []clusterv2.RouteAuthority
	Watch          func() <-chan clusterv2.RouteAuthorityEvent
	HandoffTimeout time.Duration
}

type conversationAuthorityRouteLifecycle struct {
	localAuthority *conversationAuthority
	localNodeID    uint64
	initial        func() []clusterv2.RouteAuthority
	watch          func() <-chan clusterv2.RouteAuthorityEvent
	handoffTimeout time.Duration

	mu     sync.Mutex
	cancel context.CancelFunc
	wg     sync.WaitGroup
	latest map[uint16]conversationusecase.RouteTarget
}

func newConversationAuthorityRouteLifecycle(opts conversationAuthorityRouteLifecycleOptions) *conversationAuthorityRouteLifecycle {
	if opts.HandoffTimeout <= 0 {
		opts.HandoffTimeout = 3 * time.Second
	}
	return &conversationAuthorityRouteLifecycle{
		localAuthority: opts.LocalAuthority,
		localNodeID:    opts.LocalNodeID,
		initial:        opts.Initial,
		watch:          opts.Watch,
		handoffTimeout: opts.HandoffTimeout,
		latest:         make(map[uint16]conversationusecase.RouteTarget),
	}
}
```

Add `Start`, `Stop`, `watchRouteAuthorities`, `handleRouteAuthority`, `acceptRouteAuthorityTarget`, `drainAuthorityTarget`, and existing target helper functions from the old committed sink. The methods should be the same behavior as the previous sink route methods, but receiver type changes to `*conversationAuthorityRouteLifecycle`.

- [ ] **Step 4: Delete route lifecycle methods from `conversation.go`**

Remove old committed-sink-owned methods from `conversation.go`:

```text
initialAuthorities
watchRouteAuthorities
applyRouteAuthorities
handleRouteAuthority
acceptRouteAuthorityTarget
localAuthorityCapable
drainAuthorityTarget
```

Keep `conversationRouteTarget` if the new lifecycle uses it. Move it to `conversation_route_lifecycle.go` if `conversation.go` no longer needs it.

- [ ] **Step 5: Run route lifecycle tests**

Run:

```bash
go test ./internalv2/app -run 'TestConversationAuthorityRouteLifecycle|TestConversationAuthority' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit route lifecycle split**

```bash
git add internalv2/app/conversation_route_lifecycle.go internalv2/app/conversation_route_lifecycle_test.go internalv2/app/conversation.go
git commit -m "refactor: split conversation authority route lifecycle"
```

## Task 5: Wire Async Projector In App

**Files:**
- Modify: `internalv2/app/app.go`
- Modify: `internalv2/app/lifecycle.go`
- Modify: `internalv2/app/app_test.go`
- Modify: `internalv2/app/conversation_api_smoke_test.go`

- [ ] **Step 1: Add wiring tests**

Add to `internalv2/app/app_test.go`:

```go
func TestNewWiresAsyncConversationProjector(t *testing.T) {
	cluster := newFakeConversationAuthorityCluster(1)
	app, err := newTestApp(t, Config{
		Cluster: clusterv2.Config{NodeID: 1},
	}, WithCluster(cluster))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app.conversationProjector == nil {
		t.Fatal("conversationProjector is nil, want async projector")
	}
	if _, ok := app.conversationProjector.(*conversationAsyncProjector); !ok {
		t.Fatalf("conversationProjector = %T, want *conversationAsyncProjector", app.conversationProjector)
	}
}

func TestConversationAsyncProjectorDoesNotRequireCommittedPayload(t *testing.T) {
	projector := newConversationAsyncProjector(conversationAsyncProjectorOptions{ShardCount: 1, MaxDirtyEvents: 1})
	if projector.RequiresCommittedPayload() {
		t.Fatal("RequiresCommittedPayload() = true, want false")
	}
}
```

Use existing fake cluster helpers if present. If no single helper exposes all needed interfaces, add a test fake that implements:

```go
type fakeConversationAuthorityCluster struct {
	*fakeCluster
	nodeID uint64
}

func newFakeConversationAuthorityCluster(nodeID uint64) *fakeConversationAuthorityCluster {
	return &fakeConversationAuthorityCluster{fakeCluster: &fakeCluster{}, nodeID: nodeID}
}
```

Implement the methods already used by existing conversation authority wiring tests in `app_test.go`.

- [ ] **Step 2: Run failing wiring test**

Run:

```bash
go test ./internalv2/app -run 'TestNewWiresAsyncConversationProjector|TestConversationAsyncProjectorDoesNotRequireCommittedPayload' -count=1
```

Expected: FAIL until app wiring uses the async projector.

- [ ] **Step 3: Update `App` fields**

In `internalv2/app/app.go`, add:

```go
conversationRouteLifecycle WorkerRuntime
```

near `conversationProjector`.

- [ ] **Step 4: Wire route lifecycle and async projector**

In the conversation authority wiring block in `New`, replace `newConversationAuthorityCommittedSink` with:

```go
routeLifecycle := newConversationAuthorityRouteLifecycle(conversationAuthorityRouteLifecycleOptions{
	LocalAuthority: authority,
	LocalNodeID:    authorityNode.NodeID(),
	Initial:        app.currentPresenceAuthorities,
	Watch:          authorityNode.WatchRouteAuthorities,
	HandoffTimeout: app.cfg.Conversation.AuthorityHandoffTimeout,
})
app.conversationRouteLifecycle = routeLifecycle
```

When `hasProjectionNode && app.conversationProjector == nil`, create:

```go
projectionStore := clusterinfra.NewConversationProjectionStore(projectionNode)
app.conversationProjector = newConversationAsyncProjector(conversationAsyncProjectorOptions{
	Projector: conversationusecase.NewProjector(conversationusecase.ProjectorOptions{
		Members:               projectionStore,
		SmallGroupFanoutLimit: app.cfg.Conversation.SmallGroupFanoutLimit,
	}),
	Authority:              client,
	Flusher:                authority,
	FlushInterval:          app.cfg.Conversation.ProjectionFlushInterval,
	ShardCount:             app.cfg.Conversation.ProjectionShardCount,
	MaxDirtyEvents:         app.cfg.Conversation.ProjectionMaxDirtyEvents,
	MaxRetryPatches:        app.cfg.Conversation.ProjectionMaxRetryPatches,
	RetryMaxAge:            app.cfg.Conversation.ProjectionRetryMaxAge,
	AdmitBatchRows:         app.cfg.Conversation.ProjectionAdmitBatchRows,
	AdmitConcurrency:       app.cfg.Conversation.ProjectionAdmitConcurrency,
	AdmitTimeout:           app.cfg.Conversation.ProjectionAdmitTimeout,
	Observer:               app.conversationProjectionObserver(),
})
```

Do not call `sink.applyRouteAuthorities`.

- [ ] **Step 5: Start and stop route lifecycle**

In `lifecycle.go`, after cluster write readiness and before `presenceWorker`, start:

```go
if a.conversationRouteLifecycle != nil {
	if err := a.conversationRouteLifecycle.Start(ctx); err != nil {
		a.logLifecycleError("conversation_route_lifecycle", "start", err)
		stopErr := a.rollbackStarted(ctx)
		return errors.Join(err, stopErr)
	}
}
```

Add a boolean field only if needed to avoid double stop. If adding one, name it:

```go
conversationRouteStarted bool
```

Stop it after `conversationProjector.Stop(ctx)` and before `presenceWorker.Stop(ctx)`:

```go
if a.conversationRouteStarted && a.conversationRouteLifecycle != nil {
	if stopErr := a.conversationRouteLifecycle.Stop(ctx); stopErr != nil {
		a.logLifecycleWarn("conversation_route_lifecycle", "stop", stopErr)
		err = errors.Join(err, stopErr)
	} else {
		a.conversationRouteStarted = false
	}
}
```

Mirror the same stop block in `rollbackStarted`.

- [ ] **Step 6: Run wiring and lifecycle tests**

Run:

```bash
go test ./internalv2/app -run 'TestNewWiresAsyncConversationProjector|TestConversationAsyncProjectorDoesNotRequireCommittedPayload|TestStartOrder|TestGatewayStartFailureStopsCluster|TestConversation' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit app wiring**

```bash
git add internalv2/app/app.go internalv2/app/lifecycle.go internalv2/app/app_test.go internalv2/app/conversation_api_smoke_test.go
git commit -m "feat: wire async conversation projection"
```

## Task 6: Delete Foreground Admission Code

**Files:**
- Modify: `internalv2/app/conversation.go`
- Modify: `internalv2/app/conversation_authority_test.go`
- Modify: `internalv2/app/app_test.go`
- Modify: `internalv2/app/observability_test.go`

- [ ] **Step 1: Remove old foreground sink code**

From `internalv2/app/conversation.go`, delete:

```text
conversationAuthorityCommittedOptions
conversationAuthorityCommittedSink
newConversationAuthorityCommittedSink
conversationAuthorityPendingKey
conversationAuthorityCommittedSink.Start
conversationAuthorityCommittedSink.Stop
conversationAuthorityCommittedSink.Submit
conversationAuthorityCommittedSink.SubmitMetadata
conversationAuthorityCommittedSink.RequiresCommittedPayload
conversationAuthorityCommittedSink.Flush
conversationAuthorityCommittedSink.retryPending
conversationAuthorityCommittedSink.admitBatches
conversationAuthorityCommittedSink.admitBatch
conversationAuthorityCommittedSink.pendingSnapshotWith
conversationAuthorityCommittedSink.rememberPending
conversationAuthorityCommittedSink.clearPending
conversationActivePatchDominates
conversationRouteTarget, if moved to conversation_route_lifecycle.go
observeAuthorityAdmit on conversationAuthorityCommittedSink
```

Keep or move these shared helpers if async projector or route lifecycle uses them:

```text
pendingConversationPatchKey
sortedPendingConversationPatches
conversationActivePatchBatches
mergeConversationActivePatch
targetKey
conversationAuthorityRouteTargetNewer
```

If a kept helper only exists because the async projector uses it, place it in
`conversation_async_projector.go` instead of leaving a mostly empty
`conversation.go`.

- [ ] **Step 2: Remove obsolete foreground tests**

Delete or rewrite tests whose expected behavior mentions foreground admission,
foreground pending retry, or `conversationAuthorityCommittedSink`.

Run this to find them:

```bash
rg -n 'conversationAuthorityCommittedSink|newConversationAuthorityCommittedSink|pendingSnapshotWith|rememberPending|clearPending|AuthorityAdmissionTimeout|AuthorityRPCTimeout|AuthorityRPCBatchRows|AuthorityRPCConcurrency' internalv2/app
```

Expected after cleanup: no matches except deliberate historical text in committed design or plan docs.

- [ ] **Step 3: Compile app package**

Run:

```bash
go test ./internalv2/app -run '^$' -count=1
```

Expected: PASS.

- [ ] **Step 4: Run focused conversation app tests**

Run:

```bash
go test ./internalv2/app -run 'TestConversation|TestNewWiresAsyncConversationProjector|TestObservabilityConversation' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit foreground deletion**

```bash
git add internalv2/app/conversation.go internalv2/app/conversation_async_projector.go internalv2/app/conversation_route_lifecycle.go internalv2/app/conversation_authority_test.go internalv2/app/app_test.go internalv2/app/observability_test.go
git commit -m "refactor: remove foreground conversation admission"
```

## Task 7: Update Config Examples And Docs

**Files:**
- Modify: `cmd/wukongimv2/wukongimv2.conf.example`
- Modify: `cmd/wukongimv2/wukongimv2-node1.conf.example`
- Modify: `cmd/wukongimv2/wukongimv2-node2.conf.example`
- Modify: `cmd/wukongimv2/wukongimv2-node3.conf.example`
- Modify: `scripts/wukongimv2/wukongimv2.conf`
- Modify: `scripts/wukongimv2/wukongimv2-node1.conf`
- Modify: `scripts/wukongimv2/wukongimv2-node2.conf`
- Modify: `scripts/wukongimv2/wukongimv2-node3.conf`
- Modify: `wukongim.conf.example`
- Modify: `internalv2/app/FLOW.md`
- Modify: `internalv2/usecase/message/FLOW.md`

- [ ] **Step 1: Replace config entries**

In every v2 config/example file, replace:

```conf
WK_CONVERSATION_AUTHORITY_ADMISSION_TIMEOUT=500ms
WK_CONVERSATION_AUTHORITY_RPC_TIMEOUT=500ms
WK_CONVERSATION_AUTHORITY_RPC_BATCH_ROWS=512
WK_CONVERSATION_AUTHORITY_RPC_CONCURRENCY=16
```

with:

```conf
# Conversation active projection runs asynchronously after durable append so SENDACK does not wait on member reads or authority RPC.
WK_CONVERSATION_PROJECTION_FLUSH_INTERVAL=100ms
WK_CONVERSATION_PROJECTION_SHARD_COUNT=64
WK_CONVERSATION_PROJECTION_MAX_DIRTY_EVENTS=100000
WK_CONVERSATION_PROJECTION_MAX_RETRY_PATCHES=100000
WK_CONVERSATION_PROJECTION_RETRY_MAX_AGE=30s
WK_CONVERSATION_PROJECTION_ADMIT_BATCH_ROWS=512
WK_CONVERSATION_PROJECTION_ADMIT_CONCURRENCY=16
WK_CONVERSATION_PROJECTION_ADMIT_TIMEOUT=500ms
```

Keep:

```conf
WK_CONVERSATION_AUTHORITY_HANDOFF_TIMEOUT=3s
```

- [ ] **Step 2: Update FLOW docs**

In `internalv2/app/FLOW.md`, replace text that says committed messages run foreground authority admission with:

```text
After a durable append succeeds, metadata-only committed events enter the
node-local async conversation projector. The foreground SENDACK path only
coalesces the newest event per channel/fromUID key and returns. A background
flush projects active patches, reads group membership when needed, and admits
patches through the routed ConversationAuthorityClient with bounded retry
state. Conversation active rows are best-effort working-set hints and may be
delayed or dropped without affecting message durability.
```

In `internalv2/usecase/message/FLOW.md`, ensure the committed sink section says:

```text
Metadata-only committed sinks may choose asynchronous processing and must not
alter send results. The conversation projection sink does not require committed
payload bytes.
```

- [ ] **Step 3: Search old config names**

Run:

```bash
rg -n 'WK_CONVERSATION_AUTHORITY_(ADMISSION_TIMEOUT|RPC_TIMEOUT|RPC_BATCH_ROWS|RPC_CONCURRENCY)|AuthorityAdmissionTimeout|AuthorityRPCTimeout|AuthorityRPCBatchRows|AuthorityRPCConcurrency' .
```

Expected: matches only in historical design/plan docs under `docs/superpowers/`.

- [ ] **Step 4: Run config tests**

Run:

```bash
go test ./cmd/wukongimv2 -run 'Test.*Config' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit docs and config examples**

```bash
git add cmd/wukongimv2/*.conf.example scripts/wukongimv2/*.conf wukongim.conf.example internalv2/app/FLOW.md internalv2/usecase/message/FLOW.md
git commit -m "docs: describe async conversation projection"
```

## Task 8: Final Verification And Benchmark

**Files:**
- No code changes unless verification reveals a defect.

- [ ] **Step 1: Run focused unit tests**

Run:

```bash
go test ./internalv2/app ./internalv2/usecase/conversation ./internalv2/infra/cluster ./pkg/metrics ./cmd/wukongimv2
```

Expected: PASS.

- [ ] **Step 2: Run broader targeted tests**

Run:

```bash
go test ./internalv2/... ./pkg/metrics ./pkg/clusterv2 ./pkg/db/meta
```

Expected: PASS.

- [ ] **Step 3: Run old-code search**

Run:

```bash
rg -n 'conversationAuthorityCommittedSink|newConversationAuthorityCommittedSink|pendingSnapshotWith|rememberPending|clearPending|AuthorityAdmissionTimeout|AuthorityRPCTimeout|AuthorityRPCBatchRows|AuthorityRPCConcurrency' internalv2 cmd scripts wukongim.conf.example
```

Expected: no matches.

- [ ] **Step 4: Run real-QPS benchmark**

Run:

```bash
./scripts/bench-wukongimv2-three-nodes-real-qps.sh --qps 10000
```

Expected: result PASS, actual/offered ratio at least `0.95`, and SENDACK p99 no longer dominated by conversation authority admission.

- [ ] **Step 5: Inspect async projection metrics**

During or after the benchmark, scrape one node metrics endpoint and check:

```bash
curl -s http://127.0.0.1:5011/metrics | rg 'wukongim_conversation_projection_(submit|flush|authority|dirty|retry)'
```

Expected: projection submit and flush metrics are present. Retry/drop metrics may be zero on a healthy run.

- [ ] **Step 6: Commit final verification notes if docs changed**

If benchmark notes are added to a development report, use:

```bash
git add docs/development
git commit -m "docs: record async conversation projection benchmark"
```

If no docs changed, skip this commit.

## Self-Review Checklist

- [ ] Every spec goal maps to a task:
  - foreground SENDACK decoupling: Tasks 3, 5, 6, 8
  - eventual conversation active projection: Tasks 3, 5, 8
  - reuse existing projector and authority client: Tasks 3 and 5
  - bounded runtime and observability: Tasks 1, 2, 3, 8
  - delete obsolete foreground admission code: Tasks 6 and 7
- [ ] There are no old foreground admission config names outside historical docs after Task 7.
- [ ] `conversationAsyncProjector.Submit` does not call storage, member classification, route lookup, or authority admission.
- [ ] `ConversationAuthorityClient` and `conversationAuthority` remain the only authority admission/list/cache implementations.
- [ ] No durable projection log or replay queue is introduced.

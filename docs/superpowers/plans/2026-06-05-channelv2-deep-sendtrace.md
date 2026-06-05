# ChannelV2 Deep Sendtrace Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Correlate selected leader-side ChannelV2 reactor/store append sub-stages with internalv2 SEND trace ids without adding high-concurrency hot-path overhead when deep tracing is disabled.

**Architecture:** Add an optional detail-sampling contract to `pkg/observability/sendtrace` and implement it in the diagnostics sink. ChannelV2 reactor code builds bounded transient append trace sidecars only when the active detail policy selects items, then emits leader queue, local durable, and quorum wait sendtrace events from existing append timing points. Trace metadata remains request/sidecar-only and never becomes durable channel log or DB semantics.

**Tech Stack:** Go, existing `pkg/observability/sendtrace`, existing `internal/observability/diagnostics` sampler/store, ChannelV2 reactor tests, internalv2 app/cmd config tests.

---

## File Map

- Modify `pkg/observability/sendtrace/sendtrace.go`: define detail-sampling DTOs/interfaces and helper functions that consult the active sink.
- Modify `pkg/observability/sendtrace/sendtrace_test.go`: cover disabled, non-detail sink, and detail sink behavior.
- Modify `internal/observability/diagnostics/sampler.go`: add deep detail sampling knobs and lock-free detail keep decisions.
- Modify `internal/observability/diagnostics/sampler_test.go`: cover debug/tracking, deep sample rate, defaults, and max-item limit normalization.
- Modify `internal/observability/diagnostics/sendtrace_adapter.go`: implement `sendtrace.DetailSampler` on the diagnostics sink.
- Modify `internal/observability/diagnostics/sendtrace_adapter_test.go`: verify the adapter exposes detail decisions and limits.
- Modify `internalv2/app/config.go`: add diagnostics deep trace config fields, defaults, and validation.
- Modify `internalv2/app/diagnostics.go`: pass deep trace knobs into `diagnostics.SamplerOptions`.
- Modify `internalv2/app/observability_test.go`: cover defaults and validation for deep knobs.
- Modify `cmd/wukongimv2/config.go`: parse `WK_DIAGNOSTICS_DEEP_*` keys.
- Modify `cmd/wukongimv2/config_test.go`: cover file/env parsing and validation for deep knobs.
- Modify `cmd/wukongimv2/wukongimv2.conf.example`, `cmd/wukongimv2/wukongimv2-node1.conf.example`, `cmd/wukongimv2/wukongimv2-node2.conf.example`, `cmd/wukongimv2/wukongimv2-node3.conf.example`, `scripts/wukongimv2/wukongimv2.conf`, `scripts/wukongimv2/wukongimv2-node1.conf`, `scripts/wukongimv2/wukongimv2-node2.conf`, `scripts/wukongimv2/wukongimv2-node3.conf`: align examples.
- Modify `pkg/channelv2/reactor/append_queue.go`: add transient trace sidecar fields to append timing/batch types.
- Create `pkg/channelv2/reactor/sendtrace.go`: isolate trace sidecar selection, stage event emission, and error classification.
- Create `pkg/channelv2/reactor/sendtrace_event_test.go`: focused reactor trace sidecar and stage emission tests.
- Modify `pkg/channelv2/reactor/effect.go`: attach selected trace sidecars when flushing append batches, propagate trace sidecar into append timing, and emit queue/local durable stages after store completion.
- Modify `pkg/channelv2/reactor/metrics.go`: call quorum wait trace emission from append completion after existing low-cardinality stage observations.
- Modify `pkg/channelv2/FLOW.md` and `pkg/channelv2/reactor/FLOW.md`: document transient deep trace sidecars and leader-only scope.

## Task 1: Sendtrace Detail-Sampling Contract

**Files:**
- Modify: `pkg/observability/sendtrace/sendtrace.go`
- Test: `pkg/observability/sendtrace/sendtrace_test.go`

- [ ] **Step 1: Write failing detail helper tests**

Add the following tests to `pkg/observability/sendtrace/sendtrace_test.go`:

```go
type detailRecordingSink struct {
	recordingSink
	decision DetailDecision
	limits   DetailLimits
	keys     []DetailKey
}

func (s *detailRecordingSink) KeepSendTraceDetail(key DetailKey) DetailDecision {
	s.keys = append(s.keys, key)
	return s.decision
}

func (s *detailRecordingSink) SendTraceDetailLimits() DetailLimits {
	return s.limits
}

func TestDetailDecisionDisabledWithoutDetailSink(t *testing.T) {
	restore := SetSink(&recordingSink{})
	t.Cleanup(restore)

	decision, limits, ok := DetailDecisionFor(DetailKey{TraceID: "trace-1"})

	require.False(t, ok)
	require.False(t, decision.Keep)
	require.Zero(t, limits.MaxItemsPerBatch)
}

func TestDetailDecisionUsesActiveDetailSink(t *testing.T) {
	sink := &detailRecordingSink{
		decision: DetailDecision{Keep: true, Reason: "debug"},
		limits:   DetailLimits{SlowThreshold: time.Second, MaxItemsPerBatch: 3},
	}
	restore := SetSink(sink)
	t.Cleanup(restore)

	decision, limits, ok := DetailDecisionFor(DetailKey{
		TraceID:     "trace-1",
		ChannelKey:  "channel/1/cm9vbQ",
		ClientMsgNo: "c1",
		FromUID:     "u1",
	})

	require.True(t, ok)
	require.Equal(t, DetailDecision{Keep: true, Reason: "debug"}, decision)
	require.Equal(t, time.Second, limits.SlowThreshold)
	require.Equal(t, 3, limits.MaxItemsPerBatch)
	require.Equal(t, []DetailKey{{
		TraceID:     "trace-1",
		ChannelKey:  "channel/1/cm9vbQ",
		ClientMsgNo: "c1",
		FromUID:     "u1",
	}}, sink.keys)
}

func TestDetailLimitsNormalizeNegativeValues(t *testing.T) {
	sink := &detailRecordingSink{limits: DetailLimits{SlowThreshold: -time.Second, MaxItemsPerBatch: -1}}
	restore := SetSink(sink)
	t.Cleanup(restore)

	limits, ok := ActiveDetailLimits()

	require.True(t, ok)
	require.Zero(t, limits.SlowThreshold)
	require.Zero(t, limits.MaxItemsPerBatch)
}
```

Add `time` to the test imports.

- [ ] **Step 2: Run tests and verify red**

Run:

```bash
go test ./pkg/observability/sendtrace -run 'TestDetail' -count=1
```

Expected: compile failure for undefined `DetailDecision`, `DetailKey`, `DetailLimits`, `DetailDecisionFor`, and `ActiveDetailLimits`.

- [ ] **Step 3: Implement detail contract**

In `pkg/observability/sendtrace/sendtrace.go`, add these exported types after `Event`:

```go
// DetailKey identifies one high-cardinality SEND trace candidate before event construction.
type DetailKey struct {
	// TraceID correlates diagnostics events that belong to one SEND.
	TraceID string
	// ChannelKey is the diagnostics-safe channel identifier.
	ChannelKey string
	// ClientMsgNo is the client idempotency key.
	ClientMsgNo string
	// FromUID is the sender UID used for debug/tracking matches.
	FromUID string
}

// DetailDecision reports whether expensive deep trace detail should be collected.
type DetailDecision struct {
	// Keep reports whether the caller should build and retain a deep trace sidecar.
	Keep bool
	// Reason is a stable low-cardinality reason such as debug or sample.
	Reason string
}

// DetailLimits bounds deep trace work in hot paths.
type DetailLimits struct {
	// SlowThreshold allows lazy deep trace selection for slow completed stages.
	SlowThreshold time.Duration
	// MaxItemsPerBatch bounds how many traced items one batch may expand into events.
	MaxItemsPerBatch int
}

// DetailSampler is optionally implemented by sinks that can gate expensive deep trace detail.
type DetailSampler interface {
	KeepSendTraceDetail(DetailKey) DetailDecision
	SendTraceDetailLimits() DetailLimits
}
```

Add helper functions near `Enabled`:

```go
// DetailDecisionFor asks the active sink whether deep detail should be collected for key.
func DetailDecisionFor(key DetailKey) (DetailDecision, DetailLimits, bool) {
	sampler, ok := activeDetailSampler()
	if !ok {
		return DetailDecision{}, DetailLimits{}, false
	}
	limits := normalizeDetailLimits(sampler.SendTraceDetailLimits())
	return sampler.KeepSendTraceDetail(key), limits, true
}

// ActiveDetailLimits returns deep trace limits from the active sink when supported.
func ActiveDetailLimits() (DetailLimits, bool) {
	sampler, ok := activeDetailSampler()
	if !ok {
		return DetailLimits{}, false
	}
	return normalizeDetailLimits(sampler.SendTraceDetailLimits()), true
}

func activeDetailSampler() (DetailSampler, bool) {
	holder := activeSink.Load()
	if holder == nil || holder.sink == nil {
		return nil, false
	}
	sampler, ok := holder.sink.(DetailSampler)
	if !ok {
		return nil, false
	}
	return sampler, true
}

func normalizeDetailLimits(limits DetailLimits) DetailLimits {
	if limits.SlowThreshold < 0 {
		limits.SlowThreshold = 0
	}
	if limits.MaxItemsPerBatch < 0 {
		limits.MaxItemsPerBatch = 0
	}
	return limits
}
```

- [ ] **Step 4: Run tests and verify green**

Run:

```bash
go test ./pkg/observability/sendtrace -run 'TestDetail|TestSetSink|TestRecord' -count=1
```

Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add pkg/observability/sendtrace/sendtrace.go pkg/observability/sendtrace/sendtrace_test.go
git commit -m "feat: add sendtrace detail sampler contract"
```

## Task 2: Diagnostics Deep Detail Sampler

**Files:**
- Modify: `internal/observability/diagnostics/sampler.go`
- Modify: `internal/observability/diagnostics/sampler_test.go`
- Modify: `internal/observability/diagnostics/sendtrace_adapter.go`
- Modify: `internal/observability/diagnostics/sendtrace_adapter_test.go`

- [ ] **Step 1: Write failing sampler tests**

Add these tests to `internal/observability/diagnostics/sampler_test.go`:

```go
func TestSamplerKeepsDeepDetailForDebugMatch(t *testing.T) {
	now := time.Unix(10, 0)
	sampler := NewSampler(SamplerOptions{
		SampleRate:     0,
		DeepSampleRate: 0,
		Now:            func() time.Time { return now },
		DebugMatches: []DebugMatch{{
			TraceID:    "trace-deep",
			TTL:        time.Minute,
			SampleRate: 1,
		}},
	})

	keep, reason := sampler.KeepDetail(Event{TraceID: "trace-deep", Result: ResultOK})

	require.True(t, keep)
	require.Equal(t, "debug", reason)
}

func TestSamplerDeepSampleRateIsIndependentFromEventSampleRate(t *testing.T) {
	sampler := NewSampler(SamplerOptions{SampleRate: 0, DeepSampleRate: 1})

	keepEvent, _ := sampler.Keep(Event{TraceID: "trace-1", Result: ResultOK})
	keepDetail, reason := sampler.KeepDetail(Event{TraceID: "trace-1", Result: ResultOK})

	require.False(t, keepEvent)
	require.True(t, keepDetail)
	require.Equal(t, "sample", reason)
}

func TestSamplerDetailLimitsNormalizeDefaults(t *testing.T) {
	sampler := NewSampler(SamplerOptions{SlowThreshold: 500 * time.Millisecond})

	threshold, maxItems := sampler.DetailLimits()

	require.Equal(t, 500*time.Millisecond, threshold)
	require.Equal(t, DefaultDeepMaxItemsPerBatch, maxItems)
}
```

- [ ] **Step 2: Write failing adapter tests**

Add these tests to `internal/observability/diagnostics/sendtrace_adapter_test.go`:

```go
func TestSendTraceAdapterImplementsDetailSampler(t *testing.T) {
	adapter := NewSendTraceSink(NewStore(StoreOptions{Capacity: 8}), NewSampler(SamplerOptions{
		DeepSampleRate:       1,
		DeepSlowThreshold:    750 * time.Millisecond,
		DeepMaxItemsPerBatch: 4,
	}))

	decision := adapter.KeepSendTraceDetail(sendtrace.DetailKey{TraceID: "trace-detail"})
	limits := adapter.SendTraceDetailLimits()

	require.True(t, decision.Keep)
	require.Equal(t, "sample", decision.Reason)
	require.Equal(t, 750*time.Millisecond, limits.SlowThreshold)
	require.Equal(t, 4, limits.MaxItemsPerBatch)
}

func TestSendTraceAdapterDetailSamplerUsesTrackingRules(t *testing.T) {
	rules := NewTrackingRules(TrackingRulesOptions{})
	_, err := rules.Add(TrackingRuleInput{ID: "channel", Target: TrackingTargetChannel, ChannelKey: "channel/1/cm9vbQ", TTL: time.Minute, SampleRate: 1})
	require.NoError(t, err)
	adapter := NewSendTraceSink(NewStore(StoreOptions{Capacity: 8}), NewSampler(SamplerOptions{
		DeepSampleRate: 0,
		TrackingRules:  rules,
	}))

	decision := adapter.KeepSendTraceDetail(sendtrace.DetailKey{ChannelKey: "channel/1/cm9vbQ"})

	require.True(t, decision.Keep)
	require.Equal(t, "debug", decision.Reason)
}
```

- [ ] **Step 3: Run tests and verify red**

Run:

```bash
go test ./internal/observability/diagnostics -run 'TestSampler.*Deep|TestSendTraceAdapter.*Detail' -count=1
```

Expected: compile failure for missing deep sampler fields/methods.

- [ ] **Step 4: Implement sampler fields and methods**

In `internal/observability/diagnostics/sampler.go`, add:

```go
const DefaultDeepMaxItemsPerBatch = 16
```

Extend `SamplerOptions`:

```go
// DeepSampleRate is the keep probability for expensive reactor/store detail sidecars.
DeepSampleRate float64
// DeepSlowThreshold enables lazy deep trace selection for slow reactor/store stages.
DeepSlowThreshold time.Duration
// DeepMaxItemsPerBatch bounds how many trace items one deep batch expands.
DeepMaxItemsPerBatch int
```

Extend `Sampler`:

```go
deepSampleRate       float64
deepSlowThreshold    time.Duration
deepMaxItemsPerBatch int
deepCounter          atomic.Uint64
```

In `NewSampler`, normalize defaults:

```go
deepSlowThreshold := opts.DeepSlowThreshold
if deepSlowThreshold <= 0 {
	deepSlowThreshold = opts.SlowThreshold
}
deepMaxItems := opts.DeepMaxItemsPerBatch
if deepMaxItems <= 0 {
	deepMaxItems = DefaultDeepMaxItemsPerBatch
}
```

Set the fields in the returned sampler.

Add methods:

```go
// KeepDetail returns whether expensive deep SEND trace detail should be collected.
func (s *Sampler) KeepDetail(event Event) (bool, string) {
	if s == nil {
		return false, ""
	}
	if s.trackingRules != nil && s.trackingRules.Keep(event) {
		return true, "debug"
	}
	if s.keepDebug(event) {
		return true, "debug"
	}
	if keepByRate(s.deepSampleRate, &s.deepCounter) {
		return true, "sample"
	}
	return false, ""
}

// DetailLimits returns normalized slow-stage and item-count limits for deep tracing.
func (s *Sampler) DetailLimits() (time.Duration, int) {
	if s == nil {
		return 0, 0
	}
	return s.deepSlowThreshold, s.deepMaxItemsPerBatch
}
```

- [ ] **Step 5: Implement adapter detail interface**

In `internal/observability/diagnostics/sendtrace_adapter.go`, add:

```go
// KeepSendTraceDetail implements sendtrace.DetailSampler for expensive reactor/store detail.
func (s *SendTraceSink) KeepSendTraceDetail(key sendtrace.DetailKey) sendtrace.DetailDecision {
	if s == nil || s.sampler == nil {
		return sendtrace.DetailDecision{}
	}
	keep, reason := s.sampler.KeepDetail(Event{
		TraceID:     key.TraceID,
		ChannelKey:  key.ChannelKey,
		ClientMsgNo: key.ClientMsgNo,
		FromUID:     key.FromUID,
		Result:      ResultOK,
	})
	return sendtrace.DetailDecision{Keep: keep, Reason: reason}
}

// SendTraceDetailLimits implements sendtrace.DetailSampler.
func (s *SendTraceSink) SendTraceDetailLimits() sendtrace.DetailLimits {
	if s == nil || s.sampler == nil {
		return sendtrace.DetailLimits{}
	}
	slow, maxItems := s.sampler.DetailLimits()
	return sendtrace.DetailLimits{SlowThreshold: slow, MaxItemsPerBatch: maxItems}
}
```

- [ ] **Step 6: Run tests and verify green**

Run:

```bash
go test ./internal/observability/diagnostics -run 'TestSampler.*Deep|TestSendTraceAdapter.*Detail|TestSamplerKeeps|TestSendTraceAdapterRecords' -count=1
```

Expected: pass.

- [ ] **Step 7: Commit**

```bash
git add internal/observability/diagnostics/sampler.go internal/observability/diagnostics/sampler_test.go internal/observability/diagnostics/sendtrace_adapter.go internal/observability/diagnostics/sendtrace_adapter_test.go
git commit -m "feat: add diagnostics deep trace sampling"
```

## Task 3: internalv2 Deep Diagnostics Config

**Files:**
- Modify: `internalv2/app/config.go`
- Modify: `internalv2/app/diagnostics.go`
- Modify: `internalv2/app/observability_test.go`
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

- [ ] **Step 1: Write failing app config tests**

In `internalv2/app/observability_test.go`, extend `TestDiagnosticsConfigDefaultsAndValidation` with:

```go
if cfg.Observability.Diagnostics.DeepSampleRate != 0 {
	t.Fatalf("diagnostics deep sample rate = %v, want 0", cfg.Observability.Diagnostics.DeepSampleRate)
}
if cfg.Observability.Diagnostics.DeepSlowThreshold != cfg.Observability.Diagnostics.SlowThreshold {
	t.Fatalf("diagnostics deep slow threshold = %v, want %v", cfg.Observability.Diagnostics.DeepSlowThreshold, cfg.Observability.Diagnostics.SlowThreshold)
}
if cfg.Observability.Diagnostics.DeepMaxItemsPerBatch != 16 {
	t.Fatalf("diagnostics deep max items = %d, want 16", cfg.Observability.Diagnostics.DeepMaxItemsPerBatch)
}
```

Add validation cases:

```go
{name: "deep sample rate low", cfg: DiagnosticsConfig{DeepSampleRate: -0.1}},
{name: "deep sample rate high", cfg: DiagnosticsConfig{DeepSampleRate: 1.1}},
{name: "deep sample rate nan", cfg: DiagnosticsConfig{DeepSampleRate: math.NaN()}},
{name: "deep slow threshold negative", cfg: DiagnosticsConfig{DeepSlowThreshold: -time.Millisecond}},
{name: "deep max items negative", cfg: DiagnosticsConfig{DeepMaxItemsPerBatch: -1}},
```

- [ ] **Step 2: Write failing cmd config tests**

In `cmd/wukongimv2/config_test.go`, extend explicit diagnostics file/env tests to include:

```go
"WK_DIAGNOSTICS_DEEP_SAMPLE_RATE=0.02",
"WK_DIAGNOSTICS_DEEP_SLOW_THRESHOLD_MS=125",
"WK_DIAGNOSTICS_DEEP_MAX_ITEMS_PER_BATCH=7",
```

Assert:

```go
if diagnostics.DeepSampleRate != 0.02 {
	t.Fatalf("Diagnostics.DeepSampleRate = %v, want 0.02", diagnostics.DeepSampleRate)
}
if diagnostics.DeepSlowThreshold != 125*time.Millisecond {
	t.Fatalf("Diagnostics.DeepSlowThreshold = %s, want 125ms", diagnostics.DeepSlowThreshold)
}
if diagnostics.DeepMaxItemsPerBatch != 7 {
	t.Fatalf("Diagnostics.DeepMaxItemsPerBatch = %d, want 7", diagnostics.DeepMaxItemsPerBatch)
}
```

Add invalid config cases:

```go
{name: "diagnostics deep sample rate", line: "WK_DIAGNOSTICS_DEEP_SAMPLE_RATE=often", wantKey: "WK_DIAGNOSTICS_DEEP_SAMPLE_RATE"},
{name: "diagnostics deep sample rate high", line: "WK_DIAGNOSTICS_DEEP_SAMPLE_RATE=1.5", wantKey: "WK_DIAGNOSTICS_DEEP_SAMPLE_RATE"},
{name: "diagnostics deep slow threshold negative", line: "WK_DIAGNOSTICS_DEEP_SLOW_THRESHOLD_MS=-1", wantKey: "WK_DIAGNOSTICS_DEEP_SLOW_THRESHOLD_MS"},
{name: "diagnostics deep max items negative", line: "WK_DIAGNOSTICS_DEEP_MAX_ITEMS_PER_BATCH=-1", wantKey: "WK_DIAGNOSTICS_DEEP_MAX_ITEMS_PER_BATCH"},
```

- [ ] **Step 3: Run tests and verify red**

Run:

```bash
go test ./internalv2/app ./cmd/wukongimv2 -run 'TestDiagnosticsConfigDefaultsAndValidation|TestLoadConfigExplicitDiagnosticsConfigFile|TestLoadConfigEnvOverridesFile|TestLoadConfigRejectsInvalidValues' -count=1
```

Expected: compile failure for missing config fields or unsupported keys.

- [ ] **Step 4: Add app diagnostics config fields**

In `internalv2/app/config.go`, extend `DiagnosticsConfig`:

```go
// DeepSampleRate is the keep probability for expensive reactor/store detail sidecars.
DeepSampleRate float64
// DeepSlowThreshold enables lazy deep trace selection for slow reactor/store stages.
DeepSlowThreshold time.Duration
// DeepMaxItemsPerBatch bounds how many traced messages one deep batch may expand into events.
DeepMaxItemsPerBatch int
```

In `defaultObservabilityConfig`, add after the existing diagnostics slow/error defaults:

```go
if cfg.Diagnostics.DeepSlowThreshold <= 0 {
	cfg.Diagnostics.DeepSlowThreshold = cfg.Diagnostics.SlowThreshold
}
if cfg.Diagnostics.DeepMaxItemsPerBatch <= 0 {
	cfg.Diagnostics.DeepMaxItemsPerBatch = 16
}
```

In validation, add:

```go
if !validDiagnosticsSampleRate(cfg.Diagnostics.DeepSampleRate) {
	return fmt.Errorf("diagnostics deep sample rate must be between 0 and 1")
}
if cfg.Diagnostics.DeepSlowThreshold < 0 {
	return fmt.Errorf("diagnostics deep slow threshold must be >= 0")
}
if cfg.Diagnostics.DeepMaxItemsPerBatch < 0 {
	return fmt.Errorf("diagnostics deep max items per batch must be >= 0")
}
```

- [ ] **Step 5: Pass deep knobs into diagnostics sampler**

In `internalv2/app/diagnostics.go`, extend `diagnosticsSamplerOptions`:

```go
DeepSampleRate:       cfg.Observability.Diagnostics.DeepSampleRate,
DeepSlowThreshold:    cfg.Observability.Diagnostics.DeepSlowThreshold,
DeepMaxItemsPerBatch: cfg.Observability.Diagnostics.DeepMaxItemsPerBatch,
```

- [ ] **Step 6: Parse cmd config keys**

In `cmd/wukongimv2/config.go`, add supported keys:

```go
"WK_DIAGNOSTICS_DEEP_SAMPLE_RATE",
"WK_DIAGNOSTICS_DEEP_SLOW_THRESHOLD_MS",
"WK_DIAGNOSTICS_DEEP_MAX_ITEMS_PER_BATCH",
```

In `buildConfig`, parse:

```go
if raw := configValue(values, "WK_DIAGNOSTICS_DEEP_SAMPLE_RATE"); raw != "" {
	deepSampleRate, err := parseFloat("WK_DIAGNOSTICS_DEEP_SAMPLE_RATE", raw)
	if err != nil {
		return app.Config{}, err
	}
	if deepSampleRate < 0 || deepSampleRate > 1 {
		return app.Config{}, fmt.Errorf("parse WK_DIAGNOSTICS_DEEP_SAMPLE_RATE: value must be between 0 and 1")
	}
	cfg.Observability.Diagnostics.DeepSampleRate = deepSampleRate
}
if raw := configValue(values, "WK_DIAGNOSTICS_DEEP_SLOW_THRESHOLD_MS"); raw != "" {
	deepSlowThresholdMS, err := parseInt("WK_DIAGNOSTICS_DEEP_SLOW_THRESHOLD_MS", raw)
	if err != nil {
		return app.Config{}, err
	}
	if deepSlowThresholdMS < 0 {
		return app.Config{}, fmt.Errorf("parse WK_DIAGNOSTICS_DEEP_SLOW_THRESHOLD_MS: value must be >= 0")
	}
	cfg.Observability.Diagnostics.DeepSlowThreshold = time.Duration(deepSlowThresholdMS) * time.Millisecond
}
if raw := configValue(values, "WK_DIAGNOSTICS_DEEP_MAX_ITEMS_PER_BATCH"); raw != "" {
	deepMaxItems, err := parseInt("WK_DIAGNOSTICS_DEEP_MAX_ITEMS_PER_BATCH", raw)
	if err != nil {
		return app.Config{}, err
	}
	if deepMaxItems < 0 {
		return app.Config{}, fmt.Errorf("parse WK_DIAGNOSTICS_DEEP_MAX_ITEMS_PER_BATCH: value must be >= 0")
	}
	cfg.Observability.Diagnostics.DeepMaxItemsPerBatch = deepMaxItems
}
```

- [ ] **Step 7: Update example configs**

In these files, add the deep diagnostics keys immediately after `WK_DIAGNOSTICS_ERROR_SAMPLE_RATE=1.0`:

- `cmd/wukongimv2/wukongimv2.conf.example`
- `cmd/wukongimv2/wukongimv2-node1.conf.example`
- `cmd/wukongimv2/wukongimv2-node2.conf.example`
- `cmd/wukongimv2/wukongimv2-node3.conf.example`
- `scripts/wukongimv2/wukongimv2.conf`
- `scripts/wukongimv2/wukongimv2-node1.conf`
- `scripts/wukongimv2/wukongimv2-node2.conf`
- `scripts/wukongimv2/wukongimv2-node3.conf`

```conf
# WK_DIAGNOSTICS_DEEP_SAMPLE_RATE controls expensive ChannelV2 reactor/store detail sampling for ordinary successful events; 0 disables ordinary deep detail.
WK_DIAGNOSTICS_DEEP_SAMPLE_RATE=0
# WK_DIAGNOSTICS_DEEP_SLOW_THRESHOLD_MS keeps bounded deep trace detail for slow reactor/store stages; defaults to WK_DIAGNOSTICS_SLOW_THRESHOLD_MS.
WK_DIAGNOSTICS_DEEP_SLOW_THRESHOLD_MS=500
# WK_DIAGNOSTICS_DEEP_MAX_ITEMS_PER_BATCH bounds how many traced items one deep ChannelV2 batch expands.
WK_DIAGNOSTICS_DEEP_MAX_ITEMS_PER_BATCH=16
```

- [ ] **Step 8: Run tests and verify green**

Run:

```bash
go test ./internalv2/app ./cmd/wukongimv2 -count=1
```

Expected: pass.

- [ ] **Step 9: Commit**

```bash
git add internalv2/app/config.go internalv2/app/diagnostics.go internalv2/app/observability_test.go cmd/wukongimv2/config.go cmd/wukongimv2/config_test.go cmd/wukongimv2/wukongimv2.conf.example cmd/wukongimv2/wukongimv2-node1.conf.example cmd/wukongimv2/wukongimv2-node2.conf.example cmd/wukongimv2/wukongimv2-node3.conf.example scripts/wukongimv2/wukongimv2.conf scripts/wukongimv2/wukongimv2-node1.conf scripts/wukongimv2/wukongimv2-node2.conf scripts/wukongimv2/wukongimv2-node3.conf
git commit -m "feat: wire internalv2 deep diagnostics config"
```

## Task 4: Reactor Trace Sidecar Selection

**Files:**
- Create: `pkg/channelv2/reactor/sendtrace.go`
- Create: `pkg/channelv2/reactor/sendtrace_event_test.go`
- Modify: `pkg/channelv2/reactor/append_queue.go`

- [ ] **Step 1: Write failing sidecar selection tests**

Create `pkg/channelv2/reactor/sendtrace_event_test.go` with:

```go
package reactor

import (
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
	"github.com/stretchr/testify/require"
)

type reactorDetailSink struct {
	decisions map[string]sendtrace.DetailDecision
	limits    sendtrace.DetailLimits
}

func (s *reactorDetailSink) RecordSendTrace(sendtrace.Event) {}

func (s *reactorDetailSink) KeepSendTraceDetail(key sendtrace.DetailKey) sendtrace.DetailDecision {
	if s.decisions == nil {
		return sendtrace.DetailDecision{}
	}
	return s.decisions[key.TraceID]
}

func (s *reactorDetailSink) SendTraceDetailLimits() sendtrace.DetailLimits {
	return s.limits
}

func TestAppendTraceBatchSelectsOnlyDetailKeptItems(t *testing.T) {
	batch := appendBatch{
		requests: []appendRequest{{
			req: ch.AppendBatchRequest{
				ChannelID:  ch.ChannelID{ID: "room", Type: 1},
				ChannelKey: "channel/1/cm9vbQ",
				Attempt:    2,
				Messages: []ch.Message{
					{MessageID: 10, TraceID: "trace-drop", ChannelKey: "channel/1/cm9vbQ", ClientMsgNo: "drop", FromUID: "u1"},
					{MessageID: 11, TraceID: "trace-keep", ChannelKey: "channel/1/cm9vbQ", ClientMsgNo: "keep", FromUID: "u1"},
				},
			},
			records: []ch.Record{{ID: 10}, {ID: 11}},
		}},
		records: []ch.Record{{ID: 10}, {ID: 11}},
	}
	restore := sendtrace.SetSink(&reactorDetailSink{
		limits: sendtrace.DetailLimits{MaxItemsPerBatch: 8},
		decisions: map[string]sendtrace.DetailDecision{
			"trace-keep": {Keep: true, Reason: "debug"},
		},
	})
	t.Cleanup(restore)

	traceBatch := selectAppendTraceBatch(batch)

	require.Len(t, traceBatch.items, 1)
	require.Equal(t, "trace-keep", traceBatch.items[0].traceID)
	require.Equal(t, 0, traceBatch.items[0].requestIdx)
	require.Equal(t, 1, traceBatch.items[0].recordIdx)
	require.Equal(t, 1, traceBatch.items[0].localRecordIdx)
	require.Equal(t, 2, traceBatch.items[0].attempt)
}

func TestAppendTraceBatchSelectionHonorsMaxItems(t *testing.T) {
	batch := appendBatch{
		requests: []appendRequest{{
			req: ch.AppendBatchRequest{
				ChannelID: ch.ChannelID{ID: "room", Type: 1},
				Messages: []ch.Message{
					{TraceID: "trace-1", ClientMsgNo: "a"},
					{TraceID: "trace-2", ClientMsgNo: "b"},
				},
			},
			records: []ch.Record{{ID: 1}, {ID: 2}},
		}},
		records: []ch.Record{{ID: 1}, {ID: 2}},
	}
	restore := sendtrace.SetSink(&reactorDetailSink{
		limits: sendtrace.DetailLimits{MaxItemsPerBatch: 1},
		decisions: map[string]sendtrace.DetailDecision{
			"trace-1": {Keep: true, Reason: "sample"},
			"trace-2": {Keep: true, Reason: "sample"},
		},
	})
	t.Cleanup(restore)

	traceBatch := selectAppendTraceBatch(batch)

	require.Len(t, traceBatch.items, 1)
	require.Equal(t, "trace-1", traceBatch.items[0].traceID)
}

func TestAppendTraceBatchSelectionDisabledDoesNotAllocate(t *testing.T) {
	batch := appendBatch{
		requests: []appendRequest{{
			req:     ch.AppendBatchRequest{Messages: []ch.Message{{TraceID: "trace-1"}}},
			records: []ch.Record{{ID: 1}},
		}},
		records: []ch.Record{{ID: 1}},
	}

	allocs := testing.AllocsPerRun(1000, func() {
		_ = selectAppendTraceBatch(batch)
	})

	require.Zero(t, allocs)
}
```

- [ ] **Step 2: Run tests and verify red**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'TestAppendTraceBatchSelection' -count=1
```

Expected: compile failure for missing `selectAppendTraceBatch`.

- [ ] **Step 3: Add sidecar types**

In `pkg/channelv2/reactor/append_queue.go`, extend `appendTiming` and `appendBatch`:

```go
	traceItems  []appendTraceItem
	recordCount int
```

on `appendTiming`, and:

```go
	trace appendTraceBatch
```

on `appendBatch`.

- [ ] **Step 4: Implement sidecar selection helper**

Create `pkg/channelv2/reactor/sendtrace.go`:

```go
package reactor

import (
	"context"
	"errors"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
)

const (
	channelAppendTraceErrorCodeChannelNotFound = "channel_not_found"
	channelAppendTraceErrorCodeBackpressured   = "backpressured"
	channelAppendTraceErrorCodeAppendFailed    = "append_failed"
	channelAppendTraceErrorCodeCanceled        = "canceled"
	channelAppendTraceErrorCodeTimeout         = "timeout"
	channelAppendTraceErrorCodeNotLeader       = "not_leader"
	channelAppendTraceErrorCodeOther           = "other"
)

type appendTraceItem struct {
	traceID        string
	channelKey     string
	clientMsgNo    string
	fromUID        string
	attempt        int
	requestIdx     int
	recordIdx      int
	localRecordIdx int
}

type appendTraceBatch struct {
	items []appendTraceItem
}

func selectAppendTraceBatch(batch appendBatch) appendTraceBatch {
	limits, ok := sendtrace.ActiveDetailLimits()
	if !ok || limits.MaxItemsPerBatch <= 0 || !appendBatchHasTrace(batch) {
		return appendTraceBatch{}
	}
	items := make([]appendTraceItem, 0, minInt(limits.MaxItemsPerBatch, requestsRecordCount(batch.requests)))
	for reqIdx, req := range batch.requests {
		for msgIdx, msg := range req.req.Messages {
			if len(items) >= limits.MaxItemsPerBatch {
				return appendTraceBatch{items: items}
			}
			key := appendMessageDetailKey(req.req, msg)
			if key.TraceID == "" {
				continue
			}
			decision, _, ok := sendtrace.DetailDecisionFor(key)
			if !ok || !decision.Keep {
				continue
			}
			items = append(items, appendTraceItem{
				traceID:        key.TraceID,
				channelKey:     key.ChannelKey,
				clientMsgNo:    key.ClientMsgNo,
				fromUID:        key.FromUID,
				attempt:        normalizedAppendAttempt(req.req.Attempt),
				requestIdx:     reqIdx,
				recordIdx:      appendRequestRecordIndex(batch.requests, reqIdx, msgIdx),
				localRecordIdx: msgIdx,
			})
		}
	}
	return appendTraceBatch{items: items}
}

func appendBatchHasTrace(batch appendBatch) bool {
	for _, req := range batch.requests {
		if req.req.TraceID != "" {
			return true
		}
		for _, msg := range req.req.Messages {
			if msg.TraceID != "" {
				return true
			}
		}
	}
	return false
}

func appendMessageDetailKey(req ch.AppendBatchRequest, msg ch.Message) sendtrace.DetailKey {
	traceID := msg.TraceID
	if traceID == "" {
		traceID = req.TraceID
	}
	channelKey := msg.ChannelKey
	if channelKey == "" {
		channelKey = req.ChannelKey
	}
	if channelKey == "" {
		channelKey = sendtrace.ChannelKeyFromID(req.ChannelID.ID, req.ChannelID.Type)
	}
	return sendtrace.DetailKey{TraceID: traceID, ChannelKey: channelKey, ClientMsgNo: msg.ClientMsgNo, FromUID: msg.FromUID}
}

func normalizedAppendAttempt(attempt int) int {
	if attempt <= 0 {
		return 1
	}
	return attempt
}

func appendRequestRecordIndex(requests []appendRequest, reqIdx int, msgIdx int) int {
	index := msgIdx
	for i := 0; i < reqIdx && i < len(requests); i++ {
		index += len(requests[i].records)
	}
	return index
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
```

- [ ] **Step 5: Run tests and verify green**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'TestAppendTraceBatchSelection' -count=1
```

Expected: pass.

- [ ] **Step 6: Commit**

```bash
git add pkg/channelv2/reactor/append_queue.go pkg/channelv2/reactor/sendtrace.go pkg/channelv2/reactor/sendtrace_event_test.go
git commit -m "feat: select channelv2 append trace sidecars"
```

## Task 5: Reactor Queue and Local Durable Sendtrace Stages

**Files:**
- Modify: `pkg/channelv2/reactor/sendtrace.go`
- Modify: `pkg/channelv2/reactor/effect.go`
- Modify: `pkg/channelv2/reactor/sendtrace_event_test.go`

- [ ] **Step 1: Write failing queue/local durable event test**

Add to `pkg/channelv2/reactor/sendtrace_event_test.go`:

```go
type recordingTraceSink struct {
	reactorDetailSink
	events []sendtrace.Event
}

func (s *recordingTraceSink) RecordSendTrace(event sendtrace.Event) {
	s.events = append(s.events, event)
}

func TestReactorRecordsLeaderQueueAndLocalDurableTrace(t *testing.T) {
	factory := newCountingStoreFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()
	traceSink := &recordingTraceSink{
		reactorDetailSink: reactorDetailSink{
			limits: sendtrace.DetailLimits{MaxItemsPerBatch: 4},
			decisions: map[string]sendtrace.DetailDecision{
				"trace-leader": {Keep: true, Reason: "debug"},
			},
		},
	}
	restore := sendtrace.SetSink(traceSink)
	t.Cleanup(restore)

	meta := testMeta("deep-trace-leader", 1, 1)
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16, AppendBatchMaxRecords: 1})
	require.NoError(t, applyMetaDirect(t, r, meta))
	future := NewFuture()
	event := appendEventWithFuture(meta, 10, "payload", future)
	event.Append.TraceID = "trace-leader"
	event.Append.ChannelKey = "channel/1/ZGVlcC10cmFjZS1sZWFkZXI"
	event.Append.Attempt = 2
	event.Append.Messages[0].TraceID = "trace-leader"
	event.Append.Messages[0].ChannelKey = event.Append.ChannelKey
	event.Append.Messages[0].ClientMsgNo = "client-1"
	event.Append.Messages[0].FromUID = "u1"

	r.handleAppend(event)
	r.handleStoreAppendResult(sink.awaitResultKind(t, worker.TaskStoreAppend))
	result := awaitFutureResult(t, future)

	require.NoError(t, result.Err)
	requireTraceEvent(t, traceSink.events, sendtrace.StageReplicaLeaderQueueWait, "trace-leader", 1)
	requireTraceEvent(t, traceSink.events, sendtrace.StageReplicaLeaderLocalDurable, "trace-leader", 1)
}

func requireTraceEvent(t *testing.T, events []sendtrace.Event, stage sendtrace.Stage, traceID string, messageSeq uint64) {
	t.Helper()
	for _, event := range events {
		if event.Stage != stage || event.TraceID != traceID {
			continue
		}
		require.Equal(t, messageSeq, event.MessageSeq)
		require.Equal(t, sendtrace.ResultOK, event.Result)
		require.Equal(t, 2, event.Attempt)
		require.Equal(t, "client-1", event.ClientMsgNo)
		require.Equal(t, "u1", event.FromUID)
		return
	}
	t.Fatalf("trace event %s/%s not found in %#v", stage, traceID, events)
}
```

Add missing imports to the test file:

```go
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
```

- [ ] **Step 2: Run test and verify red**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'TestReactorRecordsLeaderQueueAndLocalDurableTrace' -count=1
```

Expected: failure because no deep trace events are emitted.

- [ ] **Step 3: Attach trace sidecar at append flush**

In `pkg/channelv2/reactor/effect.go`, after `batch.records = task.StoreAppend.Records` in `tryFlushAppend`, add:

```go
batch.trace = selectAppendTraceBatch(batch)
```

Keep this before `submitStoreAppend` so restored batches preserve their selected sidecar.

- [ ] **Step 4: Store selected sidecar in append timings**

In `markAppendStoreSubmitted`, while iterating batch requests, add:

```go
timing.traceItems = traceItemsForRequest(batch.trace.items, reqIdx)
timing.recordCount = len(req.records)
```

Update the loop to include the request index:

```go
for reqIdx, req := range batch.requests {
	...
}
```

Implement helper in `sendtrace.go`:

```go
func traceItemsForRequest(items []appendTraceItem, reqIdx int) []appendTraceItem {
	var out []appendTraceItem
	for _, item := range items {
		if item.requestIdx != reqIdx {
			continue
		}
		out = append(out, item)
	}
	return out
}
```

- [ ] **Step 5: Emit queue and local durable stages**

In `observeAppendStoreCompleted`, after existing append wait stage observation and timing updates, call:

```go
r.recordLeaderQueueAndLocalDurableTrace(req, timing, batch, completedAt, nextOffset, err)
```

Place the call inside the per-request loop before `nextOffset += uint64(recordCount)`, because `nextOffset` is the first durable sequence for that request segment.

Implement in `sendtrace.go`:

```go
func (r *Reactor) recordLeaderQueueAndLocalDurableTrace(req appendRequest, timing appendTiming, batch appendBatch, completedAt time.Time, baseOffset uint64, err error) {
	items := timing.traceItems
	if len(items) == 0 {
		items = lazyTraceItemsForStoreStage(req, batch, timing, completedAt, err)
	}
	if len(items) == 0 {
		return
	}
	queueDur := sendtrace.Elapsed(req.enqueuedAt, timing.storeSubmittedAt)
	localDur := sendtrace.Elapsed(timing.storeSubmittedAt, completedAt)
	for _, item := range items {
		seq := baseOffset + uint64(item.localRecordIdx)
		recordLeaderTraceEvent(sendtrace.StageReplicaLeaderQueueWait, completedAt, item, seq, queueDur, len(batch.records), nil)
		recordLeaderTraceEvent(sendtrace.StageReplicaLeaderLocalDurable, completedAt, item, seq, localDur, len(batch.records), err)
	}
}
```

Then implement `recordLeaderTraceEvent`:

```go
func recordLeaderTraceEvent(stage sendtrace.Stage, at time.Time, item appendTraceItem, messageSeq uint64, duration time.Duration, recordCount int, err error) {
	result, errorCode := leaderTraceOutcome(err)
	event := sendtrace.Event{
		Stage:       stage,
		TraceID:     item.traceID,
		At:          at,
		Duration:    duration,
		ChannelKey:  item.channelKey,
		ClientMsgNo: item.clientMsgNo,
		MessageSeq:  messageSeq,
		FromUID:     item.fromUID,
		Result:      result,
		ErrorCode:   errorCode,
		Attempt:     item.attempt,
		RecordCount: recordCount,
	}
	if err != nil {
		event.Error = err.Error()
	}
	sendtrace.Record(event)
}
```

Add error classification:

```go
func leaderTraceOutcome(err error) (sendtrace.Result, string) {
	switch {
	case err == nil:
		return sendtrace.ResultOK, ""
	case errors.Is(err, context.Canceled):
		return sendtrace.ResultCanceled, channelAppendTraceErrorCodeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return sendtrace.ResultTimeout, channelAppendTraceErrorCodeTimeout
	case errors.Is(err, ch.ErrNotLeader):
		return sendtrace.ResultError, channelAppendTraceErrorCodeNotLeader
	case errors.Is(err, ch.ErrChannelNotFound):
		return sendtrace.ResultError, channelAppendTraceErrorCodeChannelNotFound
	case errors.Is(err, ch.ErrBackpressured):
		return sendtrace.ResultError, channelAppendTraceErrorCodeBackpressured
	default:
		return sendtrace.ResultError, channelAppendTraceErrorCodeOther
	}
}
```

For `lazyTraceItemsForStoreStage`, implement bounded lazy scan only when `err != nil` or either queue/local duration meets active detail slow threshold:

```go
func lazyTraceItemsForStoreStage(req appendRequest, batch appendBatch, timing appendTiming, completedAt time.Time, err error) []appendTraceItem {
	limits, ok := sendtrace.ActiveDetailLimits()
	if !ok || limits.MaxItemsPerBatch <= 0 {
		return nil
	}
	queueDur := sendtrace.Elapsed(req.enqueuedAt, timing.storeSubmittedAt)
	localDur := sendtrace.Elapsed(timing.storeSubmittedAt, completedAt)
	if err == nil && (limits.SlowThreshold <= 0 || (queueDur < limits.SlowThreshold && localDur < limits.SlowThreshold)) {
		return nil
	}
	return lazyTraceItemsForRequest(req, batch, limits.MaxItemsPerBatch)
}

func lazyTraceItemsForRequest(req appendRequest, batch appendBatch, limit int) []appendTraceItem {
	if limit <= 0 || len(req.req.Messages) == 0 {
		return nil
	}
	reqIdx := appendRequestIndex(batch.requests, req.opID)
	if reqIdx < 0 {
		return nil
	}
	items := make([]appendTraceItem, 0, minInt(limit, len(req.req.Messages)))
	for msgIdx, msg := range req.req.Messages {
		if len(items) >= limit {
			return items
		}
		key := appendMessageDetailKey(req.req, msg)
		if key.TraceID == "" {
			continue
		}
		items = append(items, appendTraceItem{
			traceID:        key.TraceID,
			channelKey:     key.ChannelKey,
			clientMsgNo:    key.ClientMsgNo,
			fromUID:        key.FromUID,
			attempt:        normalizedAppendAttempt(req.req.Attempt),
			requestIdx:     reqIdx,
			recordIdx:      appendRequestRecordIndex(batch.requests, reqIdx, msgIdx),
			localRecordIdx: msgIdx,
		})
	}
	return items
}

func appendRequestIndex(requests []appendRequest, opID ch.OpID) int {
	for idx, req := range requests {
		if req.opID == opID {
			return idx
		}
	}
	return -1
}
```

- [ ] **Step 6: Run test and verify green**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'TestReactorRecordsLeaderQueueAndLocalDurableTrace|TestAppendTraceBatchSelection' -count=1
```

Expected: pass.

- [ ] **Step 7: Commit**

```bash
git add pkg/channelv2/reactor/effect.go pkg/channelv2/reactor/sendtrace.go pkg/channelv2/reactor/sendtrace_event_test.go pkg/channelv2/reactor/append_queue.go
git commit -m "feat: trace channelv2 leader local append stages"
```

## Task 6: Reactor Quorum Wait Sendtrace Stage

**Files:**
- Modify: `pkg/channelv2/reactor/sendtrace.go`
- Modify: `pkg/channelv2/reactor/metrics.go`
- Modify: `pkg/channelv2/reactor/sendtrace_event_test.go`

- [ ] **Step 1: Write failing quorum trace test**

Add to `pkg/channelv2/reactor/sendtrace_event_test.go`:

```go
func TestReactorRecordsLeaderQuorumWaitTrace(t *testing.T) {
	factory := newCountingStoreFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()
	traceSink := &recordingTraceSink{
		reactorDetailSink: reactorDetailSink{
			limits: sendtrace.DetailLimits{MaxItemsPerBatch: 4},
			decisions: map[string]sendtrace.DetailDecision{
				"trace-quorum": {Keep: true, Reason: "debug"},
			},
		},
	}
	restore := sendtrace.SetSink(traceSink)
	t.Cleanup(restore)

	meta := testMeta("deep-trace-quorum", 1, 1)
	meta.Replicas = []ch.NodeID{1}
	meta.ISR = []ch.NodeID{1}
	meta.MinISR = 1
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16, AppendBatchMaxRecords: 1})
	require.NoError(t, applyMetaDirect(t, r, meta))
	future := NewFuture()
	event := appendEventWithFuture(meta, 12, "payload", future)
	event.Append.CommitMode = ch.CommitModeQuorum
	event.Append.TraceID = "trace-quorum"
	event.Append.Messages[0].TraceID = "trace-quorum"
	event.Append.Messages[0].ClientMsgNo = "client-quorum"

	r.handleAppend(event)
	r.handleStoreAppendResult(sink.awaitResultKind(t, worker.TaskStoreAppend))
	result := awaitFutureResult(t, future)

	require.NoError(t, result.Err)
	requireTraceEvent(t, traceSink.events, sendtrace.StageReplicaLeaderQuorumWait, "trace-quorum", 1)
}

func TestReactorSkipsQuorumWaitTraceForLocalCommit(t *testing.T) {
	factory := newCountingStoreFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()
	traceSink := &recordingTraceSink{
		reactorDetailSink: reactorDetailSink{
			limits: sendtrace.DetailLimits{MaxItemsPerBatch: 4},
			decisions: map[string]sendtrace.DetailDecision{
				"trace-local": {Keep: true, Reason: "debug"},
			},
		},
	}
	restore := sendtrace.SetSink(traceSink)
	t.Cleanup(restore)

	meta := testMeta("deep-trace-local-skip-quorum", 1, 1)
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16, AppendBatchMaxRecords: 1})
	require.NoError(t, applyMetaDirect(t, r, meta))
	future := NewFuture()
	event := appendEventWithFuture(meta, 13, "payload", future)
	event.Append.CommitMode = ch.CommitModeLocal
	event.Append.TraceID = "trace-local"
	event.Append.Messages[0].TraceID = "trace-local"

	r.handleAppend(event)
	r.handleStoreAppendResult(sink.awaitResultKind(t, worker.TaskStoreAppend))
	result := awaitFutureResult(t, future)

	require.NoError(t, result.Err)
	requireNoTraceEvent(t, traceSink.events, sendtrace.StageReplicaLeaderQuorumWait, "trace-local")
}

func requireNoTraceEvent(t *testing.T, events []sendtrace.Event, stage sendtrace.Stage, traceID string) {
	t.Helper()
	for _, event := range events {
		if event.Stage == stage && event.TraceID == traceID {
			t.Fatalf("unexpected trace event %s/%s in %#v", stage, traceID, events)
		}
	}
}
```

- [ ] **Step 2: Run tests and verify red**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'TestReactorRecordsLeaderQuorumWaitTrace|TestReactorSkipsQuorumWaitTraceForLocalCommit' -count=1
```

Expected: first test fails because quorum wait trace event is missing.

- [ ] **Step 3: Emit quorum trace from append completion**

In `pkg/channelv2/reactor/metrics.go`, update `observeAppendComplete` after `completedAt := time.Now()`:

```go
r.recordLeaderQuorumTrace(timing, completedAt, err)
```

Implement in `sendtrace.go`:

```go
func (r *Reactor) recordLeaderQuorumTrace(timing appendTiming, completedAt time.Time, err error) {
	if timing.mode != ch.CommitModeQuorum || timing.storeCompletedAt.IsZero() || len(timing.traceItems) == 0 {
		return
	}
	duration := sendtrace.Elapsed(timing.storeCompletedAt, completedAt)
	for _, item := range timing.traceItems {
		var messageSeq uint64
		if timing.targetOffset > 0 && timing.recordCount > 0 && item.localRecordIdx >= 0 && item.localRecordIdx < timing.recordCount {
			messageSeq = timing.targetOffset - uint64(timing.recordCount-1-item.localRecordIdx)
		}
		recordLeaderTraceEvent(sendtrace.StageReplicaLeaderQuorumWait, completedAt, item, messageSeq, duration, timing.recordCount, err)
	}
}
```

- [ ] **Step 4: Run tests and verify green**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'TestReactorRecordsLeaderQuorumWaitTrace|TestReactorSkipsQuorumWaitTraceForLocalCommit|TestReactorRecordsLeaderQueueAndLocalDurableTrace' -count=1
```

Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add pkg/channelv2/reactor/metrics.go pkg/channelv2/reactor/sendtrace.go pkg/channelv2/reactor/sendtrace_event_test.go
git commit -m "feat: trace channelv2 leader quorum wait"
```

## Task 7: Performance Guards and Persistence Boundaries

**Files:**
- Modify: `pkg/channelv2/reactor/sendtrace_event_test.go`
- Modify: `pkg/channelv2/store/contract_test.go`
- Modify: `pkg/channelv2/FLOW.md`
- Modify: `pkg/channelv2/reactor/FLOW.md`

- [ ] **Step 1: Add disabled-path allocation guard**

In `pkg/channelv2/reactor/sendtrace_event_test.go`, add:

```go
func TestAppendTraceDisabledPathDoesNotAllocate(t *testing.T) {
	batch := appendBatch{
		requests: []appendRequest{{
			req: ch.AppendBatchRequest{
				ChannelID: ch.ChannelID{ID: "room", Type: 1},
				Messages: []ch.Message{{TraceID: "trace-disabled", Payload: []byte("x")}},
			},
			records: []ch.Record{{ID: 1, Payload: []byte("x"), SizeBytes: 1}},
		}},
		records: []ch.Record{{ID: 1, Payload: []byte("x"), SizeBytes: 1}},
	}

	allocs := testing.AllocsPerRun(1000, func() {
		_ = selectAppendTraceBatch(batch)
	})

	require.Zero(t, allocs)
}
```

- [ ] **Step 2: Add disabled-path benchmark**

In `pkg/channelv2/reactor/sendtrace_event_test.go`, add:

```go
func BenchmarkAppendTraceSelectionDisabled(b *testing.B) {
	batch := appendBatch{
		requests: []appendRequest{{
			req: ch.AppendBatchRequest{
				ChannelID: ch.ChannelID{ID: "room", Type: 1},
				Messages: []ch.Message{{TraceID: "trace-disabled", Payload: []byte("x")}},
			},
			records: []ch.Record{{ID: 1, Payload: []byte("x"), SizeBytes: 1}},
		}},
		records: []ch.Record{{ID: 1, Payload: []byte("x"), SizeBytes: 1}},
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = selectAppendTraceBatch(batch)
	}
}
```

- [ ] **Step 3: Add no-persistence regression test**

In `pkg/channelv2/store/contract_test.go`, add:

```go
func TestTraceMetadataIsNotStoredInDBCompatibleMessage(t *testing.T) {
	ctx := context.Background()
	factory := NewMemoryFactory()
	id := ch.ChannelID{ID: "trace-not-persisted", Type: 1}
	key := ch.ChannelKeyForID(id)
	cs, err := factory.ChannelStore(key, id)
	require.NoError(t, err)

	_, err = cs.AppendLeader(ctx, AppendLeaderRequest{
		Records: []ch.Record{{ID: 10, Payload: []byte("payload"), SizeBytes: len("payload")}},
		Sync:    true,
	})
	require.NoError(t, err)

	lookup, ok := cs.(MessageLookup)
	require.True(t, ok)
	msg, found, err := lookup.LookupMessageByID(ctx, 10)
	require.NoError(t, err)
	require.True(t, found)
	require.Empty(t, msg.TraceID)
	require.Empty(t, msg.ChannelKey)
}
```

- [ ] **Step 4: Run tests and verify green**

Run:

```bash
go test ./pkg/channelv2/reactor ./pkg/channelv2/store -run 'TestAppendTraceDisabledPathDoesNotAllocate|TestTraceMetadataIsNotStored' -count=1
go test ./pkg/channelv2/reactor -run '^$' -bench 'BenchmarkAppendTraceSelectionDisabled' -benchmem -count=1
```

Expected: tests pass; benchmark output includes `0 B/op` and `0 allocs/op`.

- [ ] **Step 5: Update FLOW docs**

In `pkg/channelv2/FLOW.md`, add under the append trace metadata paragraph:

```markdown
Leader-side deep sendtrace detail is gated by the active diagnostics detail
sampler. The reactor builds bounded transient sidecars only for selected traced
items, records leader queue/local durable/quorum wait stages from existing
append timing points, and drops the sidecar before durable channel records or
DB-compatible messages are written.
```

In `pkg/channelv2/reactor/FLOW.md`, add a short section:

```markdown
## Deep Sendtrace Sidecars

Reactor append tracing uses transient `appendTraceBatch` sidecars selected by
`pkg/observability/sendtrace` detail sampling. Disabled or unselected appends do
not allocate sidecars. Queue and local durable stages may lazily scan the
in-flight batch for error or slow-stage traces, bounded by
`MaxItemsPerBatch`. Quorum wait emits only for preselected sidecars so pending
quorum waiters do not retain unsampled high-cardinality candidates.
```

- [ ] **Step 6: Run boundary tests and verify green**

Run:

```bash
go test ./pkg/channelv2/reactor ./pkg/channelv2/store -count=1
```

Expected: pass.

- [ ] **Step 7: Commit**

```bash
git add pkg/channelv2/reactor/sendtrace_event_test.go pkg/channelv2/store/contract_test.go pkg/channelv2/FLOW.md pkg/channelv2/reactor/FLOW.md
git commit -m "test: guard channelv2 deep trace performance boundaries"
```

## Task 8: Final Verification

**Files:**
- No source edits expected unless verification exposes a bug.

- [ ] **Step 1: Run focused unit tests**

Run:

```bash
go test ./pkg/observability/sendtrace ./internal/observability/diagnostics ./internalv2/app ./cmd/wukongimv2 ./pkg/channelv2/reactor ./pkg/channelv2/worker ./pkg/channelv2/store -count=1
```

Expected: pass.

- [ ] **Step 2: Run integration-adjacent package tests**

Run:

```bash
go test ./internalv2/... ./pkg/channelv2 ./pkg/clusterv2/channels -count=1
```

Expected: pass.

- [ ] **Step 3: Run disabled-path benchmark smoke**

Run:

```bash
go test ./pkg/channelv2/reactor -run '^$' -bench 'BenchmarkAppendTraceSelectionDisabled' -benchmem -count=1
```

Expected: benchmark output includes `0 B/op` and `0 allocs/op`.

- [ ] **Step 4: Check worktree cleanliness**

Run:

```bash
git status --short
```

Expected: no output.

- [ ] **Step 5: Final review checkpoint**

Request a read-only code review focused on:

- detail sampling disabled path allocations
- no trace metadata persistence
- no Prometheus high-cardinality labels
- correct stage durations and message sequences
- config defaults and parsing

If the reviewer finds Critical or Important issues, fix them before reporting completion.

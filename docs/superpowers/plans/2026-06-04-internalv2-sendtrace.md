# internalv2 Sendtrace Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add internalv2 SEND sendtrace capture backed by the existing bounded diagnostics store without exposing HTTP diagnostics APIs.

**Architecture:** Reuse `pkg/observability/sendtrace` as the hot-path event API and reuse `internal/observability/diagnostics` as the node-local sampled store. `internalv2/app` owns sink wiring and lifecycle restore; `internalv2/access/gateway` owns gateway trace id creation and SEND/SENDACK stages; `internalv2/usecase/message` owns entry-agnostic durable append stages; `internalv2/infra/cluster` forwards trace metadata into channelv2 append requests.

**Tech Stack:** Go, `testing`, existing `sendtrace` package, existing diagnostics store/sampler, internalv2 gateway/message/app test harnesses.

---

## File Map

- Modify `pkg/observability/sendtrace/sendtrace.go`: add a reusable diagnostics-safe channel key helper so internalv2 does not import legacy channel handler packages.
- Modify `pkg/observability/sendtrace/sendtrace_test.go`: cover the new helper.
- Modify `internalv2/app/config.go`: add `DiagnosticsConfig`, debug match config, defaults, and validation.
- Create `internalv2/app/diagnostics.go`: app-local query/tracking methods, store/sampler option helpers, and sink restore helper.
- Modify `internalv2/app/app.go`: add diagnostics fields, install sink during construction, pass trace recorder to gateway, and cleanup on construction errors.
- Modify `internalv2/app/lifecycle.go`: restore diagnostics sink during `Stop`.
- Modify `internalv2/app/app_test.go` and `internalv2/app/observability_test.go`: add tests and cleanup for global sink.
- Modify `cmd/wukongimv2/config.go`: parse `WK_DIAGNOSTICS_*` keys.
- Modify `cmd/wukongimv2/config_test.go`: verify file/env parsing.
- Modify `cmd/wukongimv2/*.conf.example` and `scripts/wukongimv2/*.conf`: align examples/configs.
- Modify `internalv2/access/gateway/handler.go`, `batch.go`, `mapper.go`, and `observer.go`: add trace recorder, generate trace ids only when enabled, record gateway send and SENDACK stages.
- Modify `internalv2/access/gateway/handler_test.go`: test trace id generation, disabled behavior, and emitted stages.
- Modify `internalv2/usecase/message/types.go`, `ports.go`, `send.go`, and `send_test.go`: add trace metadata and record durable append events.
- Modify `internalv2/infra/cluster/appender.go` and `appender_test.go`: forward trace metadata and record channel append adapter stage.
- Modify `pkg/channelv2/types.go`: add trace metadata to append requests.
- Modify `pkg/clusterv2/channels/codec.go` and `channels_test.go`: encode/decode trace metadata across forwarded append batch RPCs.
- Modify `internalv2/FLOW.md`, `internalv2/app/FLOW.md`, `internalv2/access/gateway/FLOW.md`, and `internalv2/usecase/message/FLOW.md`: update flow docs if the implemented flow differs from current text.

## Task 1: Shared Sendtrace Channel Key Helper

**Files:**
- Modify: `pkg/observability/sendtrace/sendtrace.go`
- Test: `pkg/observability/sendtrace/sendtrace_test.go`

- [ ] **Step 1: Write the failing helper tests**

Add tests that lock the v2 diagnostics key format to the legacy `channel/<type>/<base64url(channel_id)>` taxonomy:

```go
func TestChannelKeyFromIDEncodesDiagnosticsSafeKey(t *testing.T) {
	got := ChannelKeyFromID("room/a b", 2)
	require.Equal(t, "channel/2/cm9vbS9hIGI", got)
}

func TestChannelKeyFromIDHandlesEmptyChannelID(t *testing.T) {
	require.Empty(t, ChannelKeyFromID("", 2))
	require.Empty(t, ChannelKeyFromID("room", 0))
}
```

- [ ] **Step 2: Run the tests and verify red**

Run:

```bash
go test ./pkg/observability/sendtrace -run 'TestChannelKeyFromID' -count=1
```

Expected: compile failure with `undefined: ChannelKeyFromID`.

- [ ] **Step 3: Implement the helper**

Add imports and helper to `pkg/observability/sendtrace/sendtrace.go`:

```go
import (
	"encoding/base64"
	"strconv"
	"sync/atomic"
	"time"
)

const channelKeyPrefix = "channel/"

// ChannelKeyFromID returns the diagnostics-safe channel lookup key shared by SEND traces.
func ChannelKeyFromID(channelID string, channelType uint8) string {
	if channelID == "" || channelType == 0 {
		return ""
	}
	return channelKeyPrefix + strconv.Itoa(int(channelType)) + "/" + base64.RawURLEncoding.EncodeToString([]byte(channelID))
}
```

- [ ] **Step 4: Run the tests and verify green**

Run:

```bash
go test ./pkg/observability/sendtrace -run 'TestChannelKeyFromID|TestSetSink|TestRecord' -count=1
```

Expected: pass.

- [ ] **Step 5: Commit guard**

If the diff contains only this task's `pkg/observability/sendtrace` changes, commit:

```bash
git add pkg/observability/sendtrace/sendtrace.go pkg/observability/sendtrace/sendtrace_test.go
git commit -m "feat: add sendtrace channel key helper"
```

If unrelated pre-existing changes appear in either file, skip the commit and note that the task remains uncommitted.

## Task 2: internalv2 App Diagnostics Config and Sink Wiring

**Files:**
- Modify: `internalv2/app/config.go`
- Create: `internalv2/app/diagnostics.go`
- Modify: `internalv2/app/app.go`
- Modify: `internalv2/app/lifecycle.go`
- Test: `internalv2/app/observability_test.go`
- Test helper: `internalv2/app/app_test.go`

- [ ] **Step 1: Write failing app wiring tests**

Add tests to `internalv2/app/observability_test.go`:

```go
func TestNewWiresDiagnosticsStoreAndSendTraceSink(t *testing.T) {
	cfg := Config{
		NodeID: 7,
		Observability: ObservabilityConfig{
			Diagnostics: DiagnosticsConfig{Enabled: true, SampleRate: 1},
		},
	}
	cfg.Observability.SetDiagnosticsExplicitFlags(true, true, false)
	app, err := newTestApp(t, cfg, WithCluster(&fakeCluster{}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	sendtrace.Record(sendtrace.Event{
		TraceID: "trace-v2",
		Stage:   sendtrace.StageMessageSendDurable,
		Result:  sendtrace.ResultOK,
	})
	result := app.QueryDiagnostics(context.Background(), diagnostics.Query{TraceID: "trace-v2", Limit: 10})
	if result.Status != diagnostics.StatusOK || len(result.Events) != 1 {
		t.Fatalf("diagnostics result = %#v, want one ok event", result)
	}
}

func TestStopRestoresDiagnosticsSendTraceSink(t *testing.T) {
	cfg := Config{Observability: ObservabilityConfig{Diagnostics: DiagnosticsConfig{Enabled: true, SampleRate: 1}}}
	cfg.Observability.SetDiagnosticsExplicitFlags(true, true, false)
	app, err := newTestApp(t, cfg, WithCluster(&fakeCluster{}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := app.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	sendtrace.Record(sendtrace.Event{TraceID: "trace-after-stop", Stage: sendtrace.StageMessageSendDurable})
	result := app.QueryDiagnostics(context.Background(), diagnostics.Query{TraceID: "trace-after-stop", Limit: 10})
	if result.Status != diagnostics.StatusNotFound {
		t.Fatalf("status = %s, want not_found after sink restore", result.Status)
	}
}
```

Add imports to `internalv2/app/observability_test.go`:

```go
	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
```

Update `newTestApp` in `internalv2/app/app_test.go` so tests that create default-enabled diagnostics do not leak the global sink:

```go
func newTestApp(t *testing.T, cfg Config, opts ...Option) (*App, error) {
	t.Helper()
	opts = append([]Option{WithLogger(wklog.NewNop())}, opts...)
	app, err := New(cfg, opts...)
	if app != nil {
		t.Cleanup(app.restoreDiagnosticsSink)
	}
	return app, err
}
```

- [ ] **Step 2: Run tests and verify red**

Run:

```bash
go test ./internalv2/app -run 'TestNewWiresDiagnosticsStoreAndSendTraceSink|TestStopRestoresDiagnosticsSendTraceSink' -count=1
```

Expected: compile failure for missing `DiagnosticsConfig`, `SetDiagnosticsExplicitFlags`, `QueryDiagnostics`, and app diagnostics fields.

- [ ] **Step 3: Add diagnostics config types, defaults, and validation**

In `internalv2/app/config.go`, extend `ObservabilityConfig` and add config types:

```go
type ObservabilityConfig struct {
	// MetricsEnabled exposes Prometheus metrics and wires runtime observers.
	MetricsEnabled bool
	// PProfEnabled exposes net/http/pprof endpoints on the API listener.
	PProfEnabled bool
	// Diagnostics configures the bounded local diagnostics event store and sampling policy.
	Diagnostics DiagnosticsConfig

	diagnosticsEnabledSet         bool
	diagnosticsSampleRateSet      bool
	diagnosticsErrorSampleRateSet bool
}

// SetDiagnosticsExplicitFlags records which diagnostics values were explicitly configured.
func (c *ObservabilityConfig) SetDiagnosticsExplicitFlags(enabledSet, sampleRateSet, errorSampleRateSet bool) {
	if c == nil {
		return
	}
	c.diagnosticsEnabledSet = enabledSet
	c.diagnosticsSampleRateSet = sampleRateSet
	c.diagnosticsErrorSampleRateSet = errorSampleRateSet
}

// DiagnosticsConfig controls local diagnostics event retention.
type DiagnosticsConfig struct {
	// Enabled turns local diagnostics event capture on or off.
	Enabled bool
	// BufferSize is the maximum number of diagnostics events retained in memory.
	BufferSize int
	// SampleRate is the baseline keep probability for successful diagnostics events.
	SampleRate float64
	// SlowThreshold keeps successful events whose duration is at least this threshold.
	SlowThreshold time.Duration
	// ErrorSampleRate is the keep probability for diagnostics events with non-ok results.
	ErrorSampleRate float64
	// DebugMatches configures temporary high-priority sampling rules.
	DebugMatches []DiagnosticsDebugMatchConfig
}

// DiagnosticsDebugMatchConfig defines one temporary diagnostics sampling override rule.
type DiagnosticsDebugMatchConfig struct {
	// UID matches the sender UID when it is set.
	UID string `json:"uid,omitempty"`
	// ChannelKey matches the diagnostics-safe channel identifier when it is set.
	ChannelKey string `json:"channel_key,omitempty"`
	// ClientMsgNo matches the client message number when it is set.
	ClientMsgNo string `json:"client_msg_no,omitempty"`
	// TraceID matches the trace identifier when it is set.
	TraceID string `json:"trace_id,omitempty"`
	// TTLSeconds controls how long the temporary debug sampling rule stays active.
	TTLSeconds int `json:"ttl_seconds,omitempty"`
	// SampleRate is the keep probability applied when the rule matches.
	SampleRate float64 `json:"sample_rate,omitempty"`
}
```

Add defaults and validation:

```go
func defaultObservabilityConfig(cfg ObservabilityConfig) ObservabilityConfig {
	if !cfg.Diagnostics.Enabled && !cfg.diagnosticsEnabledSet {
		cfg.Diagnostics.Enabled = true
	}
	if cfg.Diagnostics.BufferSize <= 0 {
		cfg.Diagnostics.BufferSize = 50000
	}
	if cfg.Diagnostics.SampleRate == 0 && !cfg.diagnosticsSampleRateSet {
		cfg.Diagnostics.SampleRate = 0.01
	}
	if cfg.Diagnostics.SlowThreshold <= 0 {
		cfg.Diagnostics.SlowThreshold = 500 * time.Millisecond
	}
	if cfg.Diagnostics.ErrorSampleRate == 0 && !cfg.diagnosticsErrorSampleRateSet {
		cfg.Diagnostics.ErrorSampleRate = 1.0
	}
	return cfg
}

func validateObservabilityConfig(cfg ObservabilityConfig) error {
	if cfg.Diagnostics.BufferSize < 0 {
		return fmt.Errorf("%w: diagnostics buffer size must be non-negative", ErrInvalidConfig)
	}
	if cfg.Diagnostics.SampleRate < 0 || cfg.Diagnostics.SampleRate > 1 {
		return fmt.Errorf("%w: diagnostics sample rate must be between 0 and 1", ErrInvalidConfig)
	}
	if cfg.Diagnostics.SlowThreshold < 0 {
		return fmt.Errorf("%w: diagnostics slow threshold must be non-negative", ErrInvalidConfig)
	}
	if cfg.Diagnostics.ErrorSampleRate < 0 || cfg.Diagnostics.ErrorSampleRate > 1 {
		return fmt.Errorf("%w: diagnostics error sample rate must be between 0 and 1", ErrInvalidConfig)
	}
	for _, match := range cfg.Diagnostics.DebugMatches {
		if match.SampleRate < 0 || match.SampleRate > 1 {
			return fmt.Errorf("%w: diagnostics debug match sample rate must be between 0 and 1", ErrInvalidConfig)
		}
		if match.TTLSeconds < 0 {
			return fmt.Errorf("%w: diagnostics debug match ttl seconds must be non-negative", ErrInvalidConfig)
		}
	}
	return nil
}
```

Call these from `New` before logger/cluster construction:

```go
app.cfg.Observability = defaultObservabilityConfig(app.cfg.Observability)
if err := validateObservabilityConfig(app.cfg.Observability); err != nil {
	return nil, err
}
```

- [ ] **Step 4: Add app diagnostics helpers and fields**

Create `internalv2/app/diagnostics.go`:

```go
package app

import (
	"context"
	"fmt"
	"time"

	obsdiagnostics "github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
)

func (a *App) QueryDiagnostics(ctx context.Context, query obsdiagnostics.Query) obsdiagnostics.QueryResult {
	if a == nil || a.diagnostics == nil {
		var nodeID uint64
		if a != nil {
			nodeID = a.cfg.NodeID
			if nodeID == 0 {
				nodeID = a.cfg.Cluster.NodeID
			}
		}
		return obsdiagnostics.QueryResult{
			Scope:  "local_node",
			NodeID: nodeID,
			Query:  query,
			Status: obsdiagnostics.StatusNotFound,
			Events: []obsdiagnostics.Event{},
			Notes:  []string{"diagnostics store is disabled on this node"},
		}
	}
	return a.diagnostics.Query(ctx, query)
}

func (a *App) AddDiagnosticsTrackingRule(_ context.Context, input obsdiagnostics.TrackingRuleInput) (obsdiagnostics.TrackingRule, error) {
	if a == nil || a.diagnosticsTracking == nil {
		return obsdiagnostics.TrackingRule{}, fmt.Errorf("internalv2/app: diagnostics tracking not configured")
	}
	return a.diagnosticsTracking.Add(input)
}

func (a *App) ListDiagnosticsTrackingRules(context.Context) ([]obsdiagnostics.TrackingRule, error) {
	if a == nil || a.diagnosticsTracking == nil {
		return nil, fmt.Errorf("internalv2/app: diagnostics tracking not configured")
	}
	return a.diagnosticsTracking.List(), nil
}

func (a *App) DeleteDiagnosticsTrackingRule(_ context.Context, ruleID string) error {
	if a == nil || a.diagnosticsTracking == nil {
		return fmt.Errorf("internalv2/app: diagnostics tracking not configured")
	}
	a.diagnosticsTracking.Delete(ruleID)
	return nil
}

func diagnosticsStoreOptions(cfg Config) obsdiagnostics.StoreOptions {
	nodeID := cfg.Cluster.NodeID
	if nodeID == 0 {
		nodeID = cfg.NodeID
	}
	return obsdiagnostics.StoreOptions{NodeID: nodeID, Capacity: cfg.Observability.Diagnostics.BufferSize}
}

func diagnosticsSamplerOptions(cfg Config) obsdiagnostics.SamplerOptions {
	debugMatches := make([]obsdiagnostics.DebugMatch, 0, len(cfg.Observability.Diagnostics.DebugMatches))
	for _, match := range cfg.Observability.Diagnostics.DebugMatches {
		debugMatches = append(debugMatches, obsdiagnostics.DebugMatch{
			UID:         match.UID,
			ChannelKey:  match.ChannelKey,
			ClientMsgNo: match.ClientMsgNo,
			TraceID:     match.TraceID,
			TTL:         time.Duration(match.TTLSeconds) * time.Second,
			SampleRate:  match.SampleRate,
		})
	}
	return obsdiagnostics.SamplerOptions{
		SampleRate:      cfg.Observability.Diagnostics.SampleRate,
		SlowThreshold:   cfg.Observability.Diagnostics.SlowThreshold,
		ErrorSampleRate: cfg.Observability.Diagnostics.ErrorSampleRate,
		ErrorSampleRateSet: cfg.Observability.diagnosticsErrorSampleRateSet,
		DebugMatches:    debugMatches,
	}
}

func (a *App) restoreDiagnosticsSink() {
	if a == nil || a.diagnosticsRestore == nil {
		return
	}
	a.diagnosticsRestore()
	a.diagnosticsRestore = nil
}
```

Add fields to `App` in `internalv2/app/app.go`:

```go
diagnostics         *obsdiagnostics.Store
diagnosticsTracking *obsdiagnostics.TrackingRules
diagnosticsRestore  func()
```

Add imports to `app.go`:

```go
obsdiagnostics "github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
```

- [ ] **Step 5: Wire and restore the sink**

In `New`, after metrics setup and before cluster construction, install diagnostics:

```go
if cfg.Observability.Diagnostics.Enabled {
	app.diagnostics = obsdiagnostics.NewStore(diagnosticsStoreOptions(cfg))
	app.diagnosticsTracking = obsdiagnostics.NewTrackingRules(obsdiagnostics.TrackingRulesOptions{})
	samplerOptions := diagnosticsSamplerOptions(cfg)
	samplerOptions.TrackingRules = app.diagnosticsTracking
	app.diagnosticsRestore = sendtrace.SetSink(obsdiagnostics.NewSendTraceSink(app.diagnostics, obsdiagnostics.NewSampler(samplerOptions)))
	defer func() {
		if err != nil {
			app.restoreDiagnosticsSink()
		}
	}()
}
```

Because the current `New` uses short `return nil, err` paths, implement the cleanup with a named return:

```go
func New(cfg Config, opts ...Option) (appOut *App, err error) {
	app := &App{cfg: cfg}
	defer func() {
		if err != nil {
			app.restoreDiagnosticsSink()
		}
	}()
	...
	return app, nil
}
```

In `Stop`, call `a.restoreDiagnosticsSink()` before `syncLogger()` returns:

```go
a.restoreDiagnosticsSink()
if !a.started {
	return a.syncLogger()
}
```

Also call `a.restoreDiagnosticsSink()` before the final return in `Stop` after the cluster stop block.

- [ ] **Step 6: Run app tests**

Run:

```bash
go test ./internalv2/app -run 'TestNewWiresDiagnosticsStoreAndSendTraceSink|TestStopRestoresDiagnosticsSendTraceSink' -count=1
```

Expected: pass.

- [ ] **Step 7: Commit guard**

If the diff contains only this task's app diagnostics changes and no unrelated pre-existing hunks, commit:

```bash
git add internalv2/app/config.go internalv2/app/diagnostics.go internalv2/app/app.go internalv2/app/lifecycle.go internalv2/app/app_test.go internalv2/app/observability_test.go
git commit -m "feat: wire internalv2 diagnostics sink"
```

If any file already had user changes in adjacent hunks, skip the commit and keep the task uncommitted.

## Task 3: wukongimv2 Diagnostics Configuration Parsing

**Files:**
- Modify: `cmd/wukongimv2/config.go`
- Test: `cmd/wukongimv2/config_test.go`
- Modify: `cmd/wukongimv2/wukongimv2.conf.example`
- Modify: `cmd/wukongimv2/wukongimv2-node1.conf.example`
- Modify: `cmd/wukongimv2/wukongimv2-node2.conf.example`
- Modify: `cmd/wukongimv2/wukongimv2-node3.conf.example`
- Modify: `scripts/wukongimv2/wukongimv2.conf`
- Modify: `scripts/wukongimv2/wukongimv2-node1.conf`
- Modify: `scripts/wukongimv2/wukongimv2-node2.conf`
- Modify: `scripts/wukongimv2/wukongimv2-node3.conf`

- [ ] **Step 1: Write failing config parse test**

Extend the existing full config test in `cmd/wukongimv2/config_test.go` with these config lines:

```go
"WK_DIAGNOSTICS_ENABLE=false",
"WK_DIAGNOSTICS_BUFFER_SIZE=1234",
"WK_DIAGNOSTICS_SAMPLE_RATE=0.25",
"WK_DIAGNOSTICS_SLOW_THRESHOLD=750ms",
"WK_DIAGNOSTICS_ERROR_SAMPLE_RATE=0.5",
`WK_DIAGNOSTICS_DEBUG_MATCHES=[{"client_msg_no":"c1","ttl_seconds":60,"sample_rate":1.0}]`,
```

Add assertions near the observability assertions:

```go
if cfg.Observability.Diagnostics.Enabled {
	t.Fatalf("Diagnostics.Enabled = true, want false")
}
if cfg.Observability.Diagnostics.BufferSize != 1234 {
	t.Fatalf("Diagnostics.BufferSize = %d, want 1234", cfg.Observability.Diagnostics.BufferSize)
}
if cfg.Observability.Diagnostics.SampleRate != 0.25 {
	t.Fatalf("Diagnostics.SampleRate = %f, want 0.25", cfg.Observability.Diagnostics.SampleRate)
}
if cfg.Observability.Diagnostics.SlowThreshold != 750*time.Millisecond {
	t.Fatalf("Diagnostics.SlowThreshold = %s, want 750ms", cfg.Observability.Diagnostics.SlowThreshold)
}
if cfg.Observability.Diagnostics.ErrorSampleRate != 0.5 {
	t.Fatalf("Diagnostics.ErrorSampleRate = %f, want 0.5", cfg.Observability.Diagnostics.ErrorSampleRate)
}
if len(cfg.Observability.Diagnostics.DebugMatches) != 1 || cfg.Observability.Diagnostics.DebugMatches[0].ClientMsgNo != "c1" {
	t.Fatalf("Diagnostics.DebugMatches = %#v", cfg.Observability.Diagnostics.DebugMatches)
}
```

- [ ] **Step 2: Run test and verify red**

Run:

```bash
go test ./cmd/wukongimv2 -run TestLoadConfig -count=1
```

Expected: failure because the new `WK_DIAGNOSTICS_*` keys are unsupported or not parsed.

- [ ] **Step 3: Parse keys in `cmd/wukongimv2/config.go`**

Add supported keys:

```go
"WK_DIAGNOSTICS_ENABLE",
"WK_DIAGNOSTICS_BUFFER_SIZE",
"WK_DIAGNOSTICS_SAMPLE_RATE",
"WK_DIAGNOSTICS_SLOW_THRESHOLD",
"WK_DIAGNOSTICS_ERROR_SAMPLE_RATE",
"WK_DIAGNOSTICS_DEBUG_MATCHES",
```

Add parsing after `WK_PPROF_ENABLE`:

```go
if raw := configValue(values, "WK_DIAGNOSTICS_ENABLE"); raw != "" {
	enabled, err := parseBool("WK_DIAGNOSTICS_ENABLE", raw)
	if err != nil {
		return app.Config{}, err
	}
	cfg.Observability.Diagnostics.Enabled = enabled
}
if raw := configValue(values, "WK_DIAGNOSTICS_BUFFER_SIZE"); raw != "" {
	bufferSize, err := parseInt("WK_DIAGNOSTICS_BUFFER_SIZE", raw)
	if err != nil {
		return app.Config{}, err
	}
	cfg.Observability.Diagnostics.BufferSize = bufferSize
}
if raw := configValue(values, "WK_DIAGNOSTICS_SAMPLE_RATE"); raw != "" {
	sampleRate, err := parseFloat("WK_DIAGNOSTICS_SAMPLE_RATE", raw)
	if err != nil {
		return app.Config{}, err
	}
	cfg.Observability.Diagnostics.SampleRate = sampleRate
}
if raw := configValue(values, "WK_DIAGNOSTICS_SLOW_THRESHOLD"); raw != "" {
	threshold, err := parseDuration("WK_DIAGNOSTICS_SLOW_THRESHOLD", raw)
	if err != nil {
		return app.Config{}, err
	}
	cfg.Observability.Diagnostics.SlowThreshold = threshold
}
if raw := configValue(values, "WK_DIAGNOSTICS_ERROR_SAMPLE_RATE"); raw != "" {
	errorRate, err := parseFloat("WK_DIAGNOSTICS_ERROR_SAMPLE_RATE", raw)
	if err != nil {
		return app.Config{}, err
	}
	cfg.Observability.Diagnostics.ErrorSampleRate = errorRate
}
if raw := configValue(values, "WK_DIAGNOSTICS_DEBUG_MATCHES"); raw != "" {
	var matches []app.DiagnosticsDebugMatchConfig
	if err := json.Unmarshal([]byte(raw), &matches); err != nil {
		return app.Config{}, fmt.Errorf("parse WK_DIAGNOSTICS_DEBUG_MATCHES as JSON: %w", err)
	}
	cfg.Observability.Diagnostics.DebugMatches = matches
}
cfg.Observability.SetDiagnosticsExplicitFlags(
	configValue(values, "WK_DIAGNOSTICS_ENABLE") != "",
	configValue(values, "WK_DIAGNOSTICS_SAMPLE_RATE") != "",
	configValue(values, "WK_DIAGNOSTICS_ERROR_SAMPLE_RATE") != "",
)
```

Add this helper near the other parse helpers:

```go
func parseFloat(key, raw string) (float64, error) {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return value, nil
}
```

- [ ] **Step 4: Update examples**

Add this block near `WK_METRICS_ENABLE` / `WK_PPROF_ENABLE` in each v2 example and script config:

```text
# Diagnostics captures sampled SEND trace events in a bounded node-local memory store.
WK_DIAGNOSTICS_ENABLE=true
WK_DIAGNOSTICS_BUFFER_SIZE=50000
WK_DIAGNOSTICS_SAMPLE_RATE=0.01
WK_DIAGNOSTICS_SLOW_THRESHOLD=500ms
WK_DIAGNOSTICS_ERROR_SAMPLE_RATE=1.0
WK_DIAGNOSTICS_DEBUG_MATCHES=[]
```

- [ ] **Step 5: Run config tests**

Run:

```bash
go test ./cmd/wukongimv2 -run 'TestLoadConfig|TestRejectsUnsupportedConfigKeys' -count=1
```

Expected: pass.

- [ ] **Step 6: Commit guard**

If diffs in config files contain only diagnostics additions and no unrelated pre-existing hunks, commit:

```bash
git add cmd/wukongimv2/config.go cmd/wukongimv2/config_test.go cmd/wukongimv2/*.conf.example scripts/wukongimv2/*.conf
git commit -m "feat: add wukongimv2 diagnostics config"
```

If these files contain unrelated user changes, skip the commit.

## Task 4: Gateway SEND and SENDACK Trace Events

**Files:**
- Modify: `internalv2/access/gateway/observer.go`
- Modify: `internalv2/access/gateway/handler.go`
- Modify: `internalv2/access/gateway/batch.go`
- Modify: `internalv2/access/gateway/mapper.go`
- Test: `internalv2/access/gateway/handler_test.go`

- [ ] **Step 1: Write failing gateway trace tests**

Add imports to `handler_test.go`:

```go
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
```

Add a recording sink helper:

```go
type recordingSendTraceSink struct {
	mu     sync.Mutex
	events []sendtrace.Event
}

func (s *recordingSendTraceSink) RecordSendTrace(event sendtrace.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func (s *recordingSendTraceSink) snapshot() []sendtrace.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sendtrace.Event(nil), s.events...)
}
```

Add test for enabled tracing:

```go
func TestOnFrameSendRecordsSendTraceWhenEnabled(t *testing.T) {
	sink := &recordingSendTraceSink{}
	restore := sendtrace.SetSink(sink)
	defer restore()

	var written []frame.Frame
	sess := newTestSession(t, &written)
	sess.SetValue(coregateway.SessionValueUID, "u1")
	usecase := &recordingMessages{sendResult: message.SendResult{MessageID: 10, MessageSeq: 7, Reason: message.ReasonSuccess}}
	handler := New(Options{Messages: usecase, SendTimeout: time.Second, OwnerNodeID: 1})
	pkt := &frame.SendPacket{ClientSeq: 1, ClientMsgNo: "client-1", ChannelID: "room", ChannelType: 2, Payload: []byte("hello")}

	if err := handler.OnFrame(coregateway.Context{Session: sess, RequestContext: context.Background()}, pkt); err != nil {
		t.Fatalf("OnFrame() error = %v", err)
	}
	if len(usecase.sendCommands) != 1 || usecase.sendCommands[0].TraceID == "" {
		t.Fatalf("TraceID was not assigned: %#v", usecase.sendCommands)
	}
	events := sink.snapshot()
	requireTraceStage(t, events, sendtrace.StageGatewayMessagesSend, usecase.sendCommands[0].TraceID)
	requireTraceStage(t, events, sendtrace.StageGatewayWriteSendack, usecase.sendCommands[0].TraceID)
}
```

Add disabled behavior test:

```go
func TestOnFrameSendSkipsTraceIDWhenSendTraceDisabled(t *testing.T) {
	var written []frame.Frame
	sess := newTestSession(t, &written)
	sess.SetValue(coregateway.SessionValueUID, "u1")
	usecase := &recordingMessages{sendResult: message.SendResult{MessageID: 10, MessageSeq: 7, Reason: message.ReasonSuccess}}
	handler := New(Options{Messages: usecase, SendTimeout: time.Second, OwnerNodeID: 1})
	pkt := &frame.SendPacket{ClientSeq: 1, ClientMsgNo: "client-1", ChannelID: "room", ChannelType: 2, Payload: []byte("hello")}

	if err := handler.OnFrame(coregateway.Context{Session: sess, RequestContext: context.Background()}, pkt); err != nil {
		t.Fatalf("OnFrame() error = %v", err)
	}
	if got := usecase.sendCommands[0].TraceID; got != "" {
		t.Fatalf("TraceID = %q, want empty when sendtrace disabled", got)
	}
}
```

Add helper:

```go
func requireTraceStage(t *testing.T, events []sendtrace.Event, stage sendtrace.Stage, traceID string) {
	t.Helper()
	for _, event := range events {
		if event.Stage == stage && event.TraceID == traceID {
			return
		}
	}
	t.Fatalf("missing stage %s for trace %q in %#v", stage, traceID, events)
}
```

- [ ] **Step 2: Run tests and verify red**

Run:

```bash
go test ./internalv2/access/gateway -run 'TestOnFrameSendRecordsSendTraceWhenEnabled|TestOnFrameSendSkipsTraceIDWhenSendTraceDisabled' -count=1
```

Expected: compile failure for `SendCommand.TraceID` or missing trace behavior.

- [ ] **Step 3: Add gateway trace helper and record single SEND**

Add a private helper file or functions in `handler.go`:

```go
func traceEnabled() bool {
	return sendtrace.Enabled()
}

func ensureTraceID(ctx context.Context) (context.Context, string) {
	traceCtx, ok := tracectx.FromContext(ctx)
	if ok {
		if traceID, valid := tracectx.ValidateHeaderTraceID(traceCtx.TraceID); valid {
			return tracectx.WithContext(ctx, tracectx.Context{TraceID: traceID, Sampled: true}), traceID
		}
	}
	nextCtx, traceCtx := tracectx.Ensure(ctx, nil)
	return nextCtx, traceCtx.TraceID
}
```

Use imports:

```go
	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics/tracectx"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
```

In `handleSend`, after creating `reqCtx`, assign trace only when enabled:

```go
	traceOn := sendtrace.Enabled()
	channelKey := ""
	if traceOn {
		reqCtx, cmd.TraceID = ensureTraceID(reqCtx)
		channelKey = sendtrace.ChannelKeyFromID(cmd.ChannelID, cmd.ChannelType)
	}
	startedAt := time.Now()
	result, source, class := h.sendOne(reqCtx, cmd)
	if traceOn {
		sendtrace.Record(sendtrace.Event{
			TraceID:     cmd.TraceID,
			Stage:       sendtrace.StageGatewayMessagesSend,
			At:          startedAt,
			Duration:    sendtrace.Elapsed(startedAt, time.Now()),
			NodeID:      h.ownerNodeID,
			ChannelKey:  channelKey,
			ClientMsgNo: cmd.ClientMsgNo,
			MessageSeq:  result.MessageSeq,
			FromUID:     cmd.FromUID,
			Result:      sendtrace.ResultOK,
		})
	}
	return h.writeSendack(ctx, pkt, result, source, class)
```

Change `writeSendack` to accept trace context:

```go
func (h *Handler) writeSendack(ctx *coregateway.Context, pkt *frame.SendPacket, result message.SendResult, source string, class string, traceID string, channelKey string, fromUID string) error
```

Record after successful write only when `traceID != ""`:

```go
if traceID != "" {
	sendtrace.Record(sendtrace.Event{
		TraceID:     traceID,
		Stage:       sendtrace.StageGatewayWriteSendack,
		At:          startedAt,
		Duration:    sendtrace.Elapsed(startedAt, time.Now()),
		NodeID:      h.ownerNodeID,
		ChannelKey:  channelKey,
		ClientMsgNo: clientMsgNoFromPacket(pkt),
		MessageSeq:  result.MessageSeq,
		FromUID:     fromUID,
		Result:      sendtrace.ResultOK,
	})
}
```

Add helper:

```go
func clientMsgNoFromPacket(pkt *frame.SendPacket) string {
	if pkt == nil {
		return ""
	}
	return pkt.ClientMsgNo
}
```

Update call sites that do not have trace context to pass empty strings.

- [ ] **Step 4: Add batch trace assignment**

In `OnSendBatch`, create `traceIDs`, `channelKeys`, and assign trace ids only when enabled:

```go
traceOn := sendtrace.Enabled()
traceIDs := make([]string, len(items))
channelKeys := make([]string, len(items))
...
if traceOn {
	var traceID string
	reqCtx, traceID = ensureTraceID(ctx.RequestContext)
	cmd.TraceID = traceID
	traceIDs[i] = traceID
	channelKeys[i] = sendtrace.ChannelKeyFromID(cmd.ChannelID, cmd.ChannelType)
}
validItems = append(validItems, message.SendBatchItem{Context: reqCtx, Deadline: deadline, Command: cmd})
```

Record `gateway.messages_send` after `SendBatch` returns for each valid item and pass trace metadata to `writeSendack`.

- [ ] **Step 5: Run gateway tests**

Run:

```bash
go test ./internalv2/access/gateway -run 'TestOnFrameSend|TestOnSendBatch|Test.*SendTrace' -count=1
```

Expected: pass.

- [ ] **Step 6: Commit guard**

If diffs contain only gateway trace changes, commit:

```bash
git add internalv2/access/gateway/handler.go internalv2/access/gateway/batch.go internalv2/access/gateway/mapper.go internalv2/access/gateway/observer.go internalv2/access/gateway/handler_test.go
git commit -m "feat: record internalv2 gateway send traces"
```

If unrelated user changes are present in the same files, skip the commit.

## Task 5: Message Usecase Durable Append Trace Events

**Files:**
- Modify: `internalv2/usecase/message/types.go`
- Modify: `internalv2/usecase/message/send.go`
- Test: `internalv2/usecase/message/send_test.go`

- [ ] **Step 1: Write failing message trace tests**

Add import:

```go
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
```

Add a recording sink helper if the package does not already have one:

```go
type recordingSendTraceSink struct {
	mu     sync.Mutex
	events []sendtrace.Event
}

func (s *recordingSendTraceSink) RecordSendTrace(event sendtrace.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func (s *recordingSendTraceSink) snapshot() []sendtrace.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sendtrace.Event(nil), s.events...)
}
```

Add success test:

```go
func TestSendRecordsDurableSendTrace(t *testing.T) {
	sink := &recordingSendTraceSink{}
	restore := sendtrace.SetSink(sink)
	defer restore()

	app := New(Options{Appender: &recordingAppender{}, MessageID: &sequenceIDs{next: 1}})
	_, err := app.Send(context.Background(), SendCommand{
		TraceID: "trace-message",
		FromUID: "u1", ClientMsgNo: "client-1",
		ChannelID: "room", ChannelType: 2,
		Payload: []byte("hello"),
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	events := sink.snapshot()
	event := requireMessageTraceStage(t, events, sendtrace.StageMessageSendDurable)
	if event.TraceID != "trace-message" || event.ClientMsgNo != "client-1" || event.MessageSeq != 1 {
		t.Fatalf("event = %#v", event)
	}
}
```

Add append error test:

```go
func TestSendRecordsDurableSendTraceOnAppendError(t *testing.T) {
	sink := &recordingSendTraceSink{}
	restore := sendtrace.SetSink(sink)
	defer restore()

	app := New(Options{Appender: &recordingAppender{err: ErrRouteNotReady}, MessageID: &sequenceIDs{next: 1}})
	_, err := app.Send(context.Background(), SendCommand{
		TraceID: "trace-error",
		FromUID: "u1", ClientMsgNo: "client-err",
		ChannelID: "room", ChannelType: 2,
		Payload: []byte("hello"),
	})
	if !errors.Is(err, ErrRouteNotReady) {
		t.Fatalf("Send() error = %v, want route not ready", err)
	}
	event := requireMessageTraceStage(t, sink.snapshot(), sendtrace.StageMessageSendDurable)
	if event.Result != sendtrace.ResultError {
		t.Fatalf("event result = %s, want error", event.Result)
	}
}
```

Add helper:

```go
func requireMessageTraceStage(t *testing.T, events []sendtrace.Event, stage sendtrace.Stage) sendtrace.Event {
	t.Helper()
	for _, event := range events {
		if event.Stage == stage {
			return event
		}
	}
	t.Fatalf("missing stage %s in %#v", stage, events)
	return sendtrace.Event{}
}
```

- [ ] **Step 2: Run tests and verify red**

Run:

```bash
go test ./internalv2/usecase/message -run 'TestSendRecordsDurableSendTrace' -count=1
```

Expected: compile failure for missing `TraceID` or no trace event.

- [ ] **Step 3: Add trace fields**

In `SendCommand`:

```go
// TraceID is the diagnostics trace identifier propagated through the send path.
TraceID string
```

In `AppendBatchRequest`:

```go
// TraceID correlates diagnostics events for this append batch.
TraceID string
// Attempt records the append retry attempt associated with diagnostics.
Attempt int
```

Update `cloneAppendRequest` to preserve these scalar fields automatically by copying the struct first, as it already does.

- [ ] **Step 4: Record durable events**

In `appendSegment`, track attempt:

```go
attempt := 0
for {
	req := a.appendRequest(segment.channel, active, attempt)
	...
	if err != nil {
		...
		if ok {
			attempt++
			backoff = nextBackoff
			continue
		}
		for _, item := range active {
			a.recordMessageSendDurable(item, 0, appendDur, err)
			...
		}
		return
	}
	for i, item := range active {
		...
		a.recordMessageSendDurable(item, appended.MessageSeq, appendDur, appended.Err)
		...
	}
}
```

Change `appendRequest` signature:

```go
func (a *App) appendRequest(channel ChannelID, active []preparedSend, attempt int) AppendBatchRequest
```

Set fields:

```go
TraceID: firstTraceID(active),
Attempt: attempt,
```

Add helpers:

```go
func firstTraceID(items []preparedSend) string {
	for _, item := range items {
		if item.cmd.TraceID != "" {
			return item.cmd.TraceID
		}
	}
	return ""
}

func (a *App) recordMessageSendDurable(item preparedSend, seq uint64, dur time.Duration, err error) {
	if item.cmd.TraceID == "" {
		return
	}
	sendtrace.Record(sendtrace.Event{
		TraceID:     item.cmd.TraceID,
		Stage:       sendtrace.StageMessageSendDurable,
		Duration:    dur,
		ChannelKey:  sendtrace.ChannelKeyFromID(item.cmd.ChannelID, item.cmd.ChannelType),
		ClientMsgNo: item.cmd.ClientMsgNo,
		MessageSeq:  seq,
		FromUID:     item.cmd.FromUID,
		Result:      sendTraceResultFromError(err),
		ErrorCode:   sendTraceErrorCode(err),
		Error:       shortTraceError(err),
	})
}

func sendTraceResultFromError(err error) sendtrace.Result {
	switch {
	case err == nil:
		return sendtrace.ResultOK
	case errors.Is(err, context.Canceled):
		return sendtrace.ResultCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return sendtrace.ResultTimeout
	default:
		return sendtrace.ResultError
	}
}

func sendTraceErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled):
		return "context_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	default:
		return "unknown_error"
	}
}

func shortTraceError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if len(msg) > 256 {
		return msg[:256]
	}
	return msg
}
```

- [ ] **Step 5: Run message tests**

Run:

```bash
go test ./internalv2/usecase/message -run 'TestSendRecordsDurableSendTrace|TestSendBatch' -count=1
```

Expected: pass.

- [ ] **Step 6: Commit guard**

If diffs contain only message trace changes, commit:

```bash
git add internalv2/usecase/message/types.go internalv2/usecase/message/send.go internalv2/usecase/message/send_test.go
git commit -m "feat: record internalv2 message send traces"
```

If unrelated user changes are present in the same files, skip the commit.

## Task 6: ChannelV2 Trace Metadata and Infra Channel Append Stage

**Files:**
- Modify: `pkg/channelv2/types.go`
- Modify: `pkg/clusterv2/channels/codec.go`
- Test: `pkg/clusterv2/channels/channels_test.go`
- Modify: `internalv2/infra/cluster/appender.go`
- Test: `internalv2/infra/cluster/appender_test.go`

- [ ] **Step 1: Write failing ChannelV2 codec test**

In the existing append batch codec test in `pkg/clusterv2/channels/channels_test.go`, include trace fields:

```go
req := ch.AppendBatchRequest{
	ChannelID: ch.ChannelID{ID: "room", Type: 1},
	Messages: []ch.Message{sampleMessage},
	CommitMode: ch.CommitModeLocal,
	ExpectedChannelEpoch: 1,
	ExpectedLeaderEpoch: 2,
	OmitResultPayload: true,
	TraceID: "trace-codec",
	Attempt: 3,
}
data, err := encodeAppendBatchRequest(req)
...
if got.TraceID != req.TraceID || got.Attempt != req.Attempt {
	t.Fatalf("trace metadata = %q/%d, want %q/%d", got.TraceID, got.Attempt, req.TraceID, req.Attempt)
}
```

- [ ] **Step 2: Run codec test and verify red**

Run:

```bash
go test ./pkg/clusterv2/channels -run 'Test.*AppendBatch.*Codec|TestCodec' -count=1
```

Expected: compile failure for missing `TraceID` and `Attempt`.

- [ ] **Step 3: Add ChannelV2 request fields and codec support**

In `pkg/channelv2/types.go`:

```go
// TraceID correlates diagnostics events that belong to this append request.
TraceID string
// Attempt records the append retry attempt associated with diagnostics.
Attempt int
```

In `appendAppendBatchRequest`:

```go
dst = appendString(dst, req.TraceID)
dst = appendUvarint(dst, uint64(req.Attempt))
```

In `readAppendBatchRequest`:

```go
if req.TraceID, offset, err = readString(body, offset); err != nil {
	return ch.AppendBatchRequest{}, offset, err
}
var attempt uint64
if attempt, offset, err = readUvarint(body, offset); err != nil {
	return ch.AppendBatchRequest{}, offset, err
}
req.Attempt = int(attempt)
```

- [ ] **Step 4: Write failing infra appender trace test**

In `internalv2/infra/cluster/appender_test.go`, extend `TestChannelAppenderMapsAppendBatchRequestAndResult` or add:

```go
func TestChannelAppenderForwardsTraceMetadata(t *testing.T) {
	node := &recordingNode{result: channelv2.AppendBatchResult{Items: []channelv2.AppendBatchItemResult{{MessageID: 10, MessageSeq: 1}}}}
	appender := NewChannelAppender(node)
	_, err := appender.AppendBatch(context.Background(), message.AppendBatchRequest{
		ChannelID: message.ChannelID{ID: "room", Type: 2},
		TraceID: "trace-appender",
		Attempt: 4,
		Messages: []message.Message{{MessageID: 10, ChannelID: "room", ChannelType: 2, Payload: []byte("hello")}},
	})
	if err != nil {
		t.Fatalf("AppendBatch() error = %v", err)
	}
	if node.last.TraceID != "trace-appender" || node.last.Attempt != 4 {
		t.Fatalf("trace metadata = %q/%d", node.last.TraceID, node.last.Attempt)
	}
}
```

- [ ] **Step 5: Forward trace metadata and record channel append stage**

In `internalv2/infra/cluster/appender.go`, capture start time and pass fields:

```go
startedAt := time.Now()
res, err := a.node.AppendChannelBatch(ctx, channelv2.AppendBatchRequest{
	ChannelID: channelv2.ChannelID{ID: req.ChannelID.ID, Type: req.ChannelID.Type},
	Messages: toChannelMessages(req.Messages),
	CommitMode: toChannelCommitMode(req.CommitMode),
	OmitResultPayload: req.OmitResultPayload,
	TraceID: req.TraceID,
	Attempt: req.Attempt,
})
duration := sendtrace.Elapsed(startedAt, time.Now())
```

After successful append, record one `channel.append.local` event per successful item when `req.TraceID != ""`:

```go
recordChannelAppendTrace(req, res, duration, err)
```

Implement helper:

```go
func recordChannelAppendTrace(req message.AppendBatchRequest, res channelv2.AppendBatchResult, dur time.Duration, err error) {
	if req.TraceID == "" {
		return
	}
	result := sendTraceResultFromError(err)
	for i, item := range res.Items {
		if item.Err != nil {
			result = sendTraceResultFromError(item.Err)
		}
		var msg message.Message
		if i < len(req.Messages) {
			msg = req.Messages[i]
		}
		sendtrace.Record(sendtrace.Event{
			TraceID:     req.TraceID,
			Stage:       sendtrace.StageChannelAppendLocal,
			Duration:    dur,
			ChannelKey:  sendtrace.ChannelKeyFromID(req.ChannelID.ID, req.ChannelID.Type),
			ClientMsgNo: msg.ClientMsgNo,
			MessageSeq:  item.MessageSeq,
			FromUID:     msg.FromUID,
			Attempt:     req.Attempt,
			RecordCount: len(res.Items),
			Result:      result,
		})
	}
}
```

Add local `sendTraceResultFromError` helper or move a shared helper to `pkg/observability/sendtrace` if duplicate code becomes noisy.

- [ ] **Step 6: Run channel/infra tests**

Run:

```bash
go test ./pkg/clusterv2/channels ./internalv2/infra/cluster -run 'Test.*AppendBatch|TestChannelAppender' -count=1
```

Expected: pass.

- [ ] **Step 7: Commit guard**

If diffs contain only channelv2/infra trace changes, commit:

```bash
git add pkg/channelv2/types.go pkg/clusterv2/channels/codec.go pkg/clusterv2/channels/channels_test.go internalv2/infra/cluster/appender.go internalv2/infra/cluster/appender_test.go
git commit -m "feat: propagate internalv2 append trace metadata"
```

If unrelated user changes are present in the same files, skip the commit.

## Task 7: Flow Docs and Final Verification

**Files:**
- Modify: `internalv2/FLOW.md`
- Modify: `internalv2/app/FLOW.md`
- Modify: `internalv2/access/gateway/FLOW.md`
- Modify: `internalv2/usecase/message/FLOW.md`

- [ ] **Step 1: Update FLOW docs**

Add concise bullets to the relevant SEND flow sections:

```text
When diagnostics are enabled, gateway SEND assigns a diagnostics TraceID,
records gateway.messages_send and gateway.write_sendack, and propagates TraceID
through the message usecase to channel append. The diagnostics sink is wired only
by internalv2/app and restored during Stop.
```

Add to `internalv2/app/FLOW.md` construction flow:

```text
-> when Observability.Diagnostics.Enabled=true, create the bounded diagnostics
   store/sampler/tracking rules and install the global sendtrace sink
```

- [ ] **Step 2: Run targeted tests**

Run:

```bash
go test ./pkg/observability/sendtrace ./internal/observability/diagnostics ./internalv2/access/gateway ./internalv2/usecase/message ./internalv2/infra/cluster ./pkg/clusterv2/channels ./internalv2/app ./cmd/wukongimv2 -count=1
```

Expected: pass.

- [ ] **Step 3: Run broader internalv2 tests**

Run:

```bash
go test ./internalv2/...
```

Expected: pass.

- [ ] **Step 4: Inspect final diff for unrelated changes**

Run:

```bash
git diff --stat
git diff -- internalv2 pkg/observability/sendtrace pkg/channelv2 pkg/clusterv2/channels cmd/wukongimv2 scripts/wukongimv2 docs/superpowers/plans/2026-06-04-internalv2-sendtrace.md
```

Expected: diffs are limited to sendtrace migration and config/doc alignment. Any pre-existing unrelated hunks must remain unstaged.

- [ ] **Step 5: Final commit guard**

If all remaining sendtrace migration changes are clean and not mixed with unrelated user changes, commit:

```bash
git add pkg/observability/sendtrace pkg/channelv2 pkg/clusterv2/channels internalv2 cmd/wukongimv2 scripts/wukongimv2 docs/superpowers/plans/2026-06-04-internalv2-sendtrace.md
git commit -m "feat: migrate sendtrace capture to internalv2"
```

If files contain unrelated user changes, do not commit. Report the verified test results and list the changed paths that remain unstaged.

## Self-Review

- Spec coverage: The plan covers diagnostics store/sampler wiring, trace id propagation, gateway/message/channel append stages, disabled-path optimization, config exposure, and FLOW/config alignment. It intentionally does not add HTTP diagnostics query APIs.
- Open-item scan: The plan contains no open-ended markers or unspecified implementation steps.
- Type consistency: Trace fields are consistently named `TraceID` and `Attempt` across message and channelv2 append requests. Diagnostics configuration uses `DiagnosticsConfig` and `DiagnosticsDebugMatchConfig`.

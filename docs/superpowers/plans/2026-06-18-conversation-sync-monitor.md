# Conversation Sync Monitor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `/conversation/sync` and conversation projection health metrics to the existing `/business/monitor` realtime card wall.

**Architecture:** Add low-cardinality sync observations at the `internalv2/access/api` boundary, expose them through `pkg/metrics`, wire them through `internalv2/app`, and extend the existing Prometheus-backed manager monitor card provider. The web UI keeps the existing generic card response contract and only adds new metric keys, stage labels, snapshot labels, preview data, and tests.

**Tech Stack:** Go, Gin, Prometheus client, internalv2 app wiring, React, TypeScript, Vitest, react-intl.

---

## File Structure

- Modify: `pkg/metrics/conversation.go`
  - Add sync request counters and histograms.
  - Add `ObserveSync` with normalized low-cardinality labels.
- Modify: `pkg/metrics/registry_test.go`
  - Add sync metric registration and label-normalization coverage.
- Modify: `internalv2/usecase/conversation/types.go`
  - Extend `SyncResult` with low-cardinality shape facts that are not serialized to clients.
- Modify: `internalv2/usecase/conversation/sync.go`
  - Populate sync overlay and recent-load duration facts.
- Modify: `internalv2/usecase/conversation/sync_test.go`
  - Verify sync shape facts without changing client-visible conversation selection.
- Modify: `internalv2/access/api/server.go`
  - Add `ConversationSyncObservation`, `ConversationSyncObserver`, `Options.ConversationSyncObserver`, and the server field.
- Modify: `internalv2/access/api/conversation_sync.go`
  - Record one observation for every `/conversation/sync` request path.
- Modify: `internalv2/access/api/conversation_sync_test.go`
  - Verify observation results for success, invalid request, parse error, not configured, and usecase error.
- Modify: `internalv2/access/api/FLOW.md`
  - Document that `/conversation/sync` emits low-cardinality observations.
- Modify: `internalv2/app/app.go`
  - Add a conversation sync observer provider.
- Modify: `internalv2/app/wiring.go`
  - Pass the sync observer to the API server.
- Modify: `internalv2/app/observability.go`
  - Map sync observations into `pkg/metrics`.
- Modify: `internalv2/app/observability_test.go`
  - Verify sync observations reach Prometheus metrics.
- Modify: `internalv2/access/manager/monitor.go`
  - Add the `conversationSync` realtime monitor stage constant.
- Modify: `internalv2/app/manager_monitor_prometheus.go`
  - Add ten conversation monitor card definitions and four snapshot entries.
- Modify: `internalv2/app/manager_monitor_prometheus_test.go`
  - Verify conversation card keys, PromQL sources, snapshot entries, partial no-data behavior, and `no_dirty` error exclusion.
- Modify: `internalv2/access/manager/FLOW.md`
  - Document the new conversation stage in the business realtime monitor.
- Modify: `web/src/pages/monitor/types.ts`
  - Add `conversationSync` stage and conversation metric keys.
- Modify: `web/src/pages/monitor/metric-config.ts`
  - Add metric labels, colors, snapshot labels, and no-data reasons.
- Modify: `web/src/pages/monitor/preview-data.ts`
  - Add deterministic conversation preview cards and snapshot entries.
- Modify: `web/src/pages/monitor/preview-data.test.ts`
  - Update required card order and snapshot order.
- Modify: `web/src/pages/monitor/page.test.tsx`
  - Verify API-provided conversation cards and no-data reason rendering.
- Modify: `web/src/i18n/messages/en.ts`
  - Add English monitor labels.
- Modify: `web/src/i18n/messages/zh-CN.ts`
  - Add Chinese monitor labels.

---

### Task 1: Add Conversation Sync Metric Families

**Files:**
- Modify: `pkg/metrics/conversation.go`
- Modify: `pkg/metrics/registry_test.go`

- [ ] **Step 1: Write the failing sync metrics registry test**

Add this test after `TestConversationMetricsTrackListShapeAndLatency` in `pkg/metrics/registry_test.go`:

```go
func TestConversationMetricsTrackSyncShapeAndLatency(t *testing.T) {
	reg := New(11, "node-11")

	reg.Conversation.ObserveSync("ok", true, true, 25*time.Millisecond, 3, 2, 4*time.Millisecond)
	reg.Conversation.ObserveSync("unexpected-database-label", false, false, -time.Second, -1, -2, 0)

	families, err := reg.Gather()
	require.NoError(t, err)

	total := requireMetricFamily(t, families, "wukongim_conversation_sync_total")
	require.Equal(t, float64(1), findMetricByLabels(t, total, map[string]string{
		"node_id":      "11",
		"node_name":    "node-11",
		"result":       "ok",
		"only_unread":  "true",
		"with_recents": "true",
	}).GetCounter().GetValue())
	require.Equal(t, float64(1), findMetricByLabels(t, total, map[string]string{
		"node_id":      "11",
		"node_name":    "node-11",
		"result":       "error",
		"only_unread":  "false",
		"with_recents": "false",
	}).GetCounter().GetValue())

	duration := requireMetricFamily(t, families, "wukongim_conversation_sync_duration_seconds")
	require.Equal(t, uint64(1), findMetricByLabels(t, duration, map[string]string{
		"node_id":      "11",
		"node_name":    "node-11",
		"result":       "ok",
		"only_unread":  "true",
		"with_recents": "true",
	}).GetHistogram().GetSampleCount())
	require.Equal(t, float64(0), findMetricByLabels(t, duration, map[string]string{
		"node_id":      "11",
		"node_name":    "node-11",
		"result":       "error",
		"only_unread":  "false",
		"with_recents": "false",
	}).GetHistogram().GetSampleSum())

	returned := requireMetricFamily(t, families, "wukongim_conversation_sync_returned_items")
	require.Equal(t, float64(3), findMetricByLabels(t, returned, map[string]string{
		"node_id":      "11",
		"node_name":    "node-11",
		"result":       "ok",
		"only_unread":  "true",
		"with_recents": "true",
	}).GetHistogram().GetSampleSum())

	overlay := requireMetricFamily(t, families, "wukongim_conversation_sync_overlay_items")
	require.Equal(t, float64(2), findMetricByLabels(t, overlay, map[string]string{
		"node_id":      "11",
		"node_name":    "node-11",
		"result":       "ok",
		"only_unread":  "true",
		"with_recents": "true",
	}).GetHistogram().GetSampleSum())

	recentLoad := requireMetricFamily(t, families, "wukongim_conversation_sync_recent_load_duration_seconds")
	require.Equal(t, uint64(1), findMetricByLabels(t, recentLoad, map[string]string{
		"node_id":     "11",
		"node_name":   "node-11",
		"result":      "ok",
		"only_unread": "true",
	}).GetHistogram().GetSampleCount())

	for _, family := range []*dto.MetricFamily{total, duration, returned, overlay, recentLoad} {
		for _, metric := range family.GetMetric() {
			requireNoMetricLabel(t, metric, "uid")
			requireNoMetricLabel(t, metric, "channel_id")
			requireNoMetricLabel(t, metric, "channelID")
			requireNoMetricLabel(t, metric, "device_id")
			requireNoMetricLabel(t, metric, "client_msg_no")
		}
	}
}
```

- [ ] **Step 2: Run the failing test**

Run:

```bash
go test ./pkg/metrics -run TestConversationMetricsTrackSyncShapeAndLatency -count=1
```

Expected: FAIL with `reg.Conversation.ObserveSync undefined`.

- [ ] **Step 3: Add sync metric fields and registration**

In `pkg/metrics/conversation.go`, extend `ConversationMetrics` with these fields:

```go
	syncTotal              *prometheus.CounterVec
	syncDuration           *prometheus.HistogramVec
	syncReturnedItems      *prometheus.HistogramVec
	syncOverlayItems       *prometheus.HistogramVec
	syncRecentLoadDuration *prometheus.HistogramVec
```

Inside `newConversationMetrics`, add these constructors after the list metrics and before authority metrics:

```go
		syncTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_conversation_sync_total",
			Help:        "Total number of conversation sync requests.",
			ConstLabels: labels,
		}, []string{"result", "only_unread", "with_recents"}),
		syncDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_conversation_sync_duration_seconds",
			Help:        "Conversation sync request latency in seconds.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"result", "only_unread", "with_recents"}),
		syncReturnedItems: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_conversation_sync_returned_items",
			Help:        "Conversation rows returned by conversation sync requests.",
			ConstLabels: labels,
			Buckets:     conversationListSizeBuckets,
		}, []string{"result", "only_unread", "with_recents"}),
		syncOverlayItems: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_conversation_sync_overlay_items",
			Help:        "Client-known overlay conversation candidates used by conversation sync requests.",
			ConstLabels: labels,
			Buckets:     conversationListSizeBuckets,
		}, []string{"result", "only_unread", "with_recents"}),
		syncRecentLoadDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_conversation_sync_recent_load_duration_seconds",
			Help:        "Recent-message batch load latency for conversation sync requests.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"result", "only_unread"}),
```

Add the new collectors to the `registry.MustRegister` call in this order:

```go
		m.syncTotal,
		m.syncDuration,
		m.syncReturnedItems,
		m.syncOverlayItems,
		m.syncRecentLoadDuration,
```

- [ ] **Step 4: Add sync observation normalization**

In `pkg/metrics/conversation.go`, add this method after `ObserveList`:

```go
// ObserveSync records one conversation sync request result and shape.
func (m *ConversationMetrics) ObserveSync(result string, onlyUnread, withRecents bool, dur time.Duration, returnedItems, overlayItems int, recentLoadDuration time.Duration) {
	if m == nil {
		return
	}
	result = conversationSyncResult(result)
	onlyUnreadLabel := strconv.FormatBool(onlyUnread)
	withRecentsLabel := strconv.FormatBool(withRecents)
	if dur < 0 {
		dur = 0
	}
	m.syncTotal.WithLabelValues(result, onlyUnreadLabel, withRecentsLabel).Inc()
	m.syncDuration.WithLabelValues(result, onlyUnreadLabel, withRecentsLabel).Observe(dur.Seconds())
	m.syncReturnedItems.WithLabelValues(result, onlyUnreadLabel, withRecentsLabel).Observe(float64(nonNegative(returnedItems)))
	m.syncOverlayItems.WithLabelValues(result, onlyUnreadLabel, withRecentsLabel).Observe(float64(nonNegative(overlayItems)))
	if withRecents && recentLoadDuration > 0 {
		m.syncRecentLoadDuration.WithLabelValues(result, onlyUnreadLabel).Observe(recentLoadDuration.Seconds())
	}
}
```

Add this helper near the other low-cardinality helpers:

```go
func conversationSyncResult(result string) string {
	switch result {
	case "ok", "invalid_request", "parse_last_msg_seqs_error", "not_configured", "store_error", "recent_message_error", "error":
		return result
	default:
		return "error"
	}
}
```

- [ ] **Step 5: Run the metrics test**

Run:

```bash
go test ./pkg/metrics -run 'TestConversationMetricsTrack(SyncShapeAndLatency|ListShapeAndLatency|AuthorityCountersAndLowCardinalityLabels|ActiveCacheAndFlush)' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit task 1**

```bash
git add pkg/metrics/conversation.go pkg/metrics/registry_test.go
git commit -m "feat: add conversation sync metrics"
```

---

### Task 2: Record `/conversation/sync` Observations

**Files:**
- Modify: `internalv2/usecase/conversation/types.go`
- Modify: `internalv2/usecase/conversation/sync.go`
- Modify: `internalv2/usecase/conversation/sync_test.go`
- Modify: `internalv2/access/api/server.go`
- Modify: `internalv2/access/api/conversation_sync.go`
- Modify: `internalv2/access/api/conversation_sync_test.go`
- Modify: `internalv2/access/api/FLOW.md`

- [ ] **Step 1: Write the failing usecase shape test**

Add this test to `internalv2/usecase/conversation/sync_test.go`:

```go
func TestSyncReportsOverlayAndRecentLoadShape(t *testing.T) {
	store := newConversationSyncStore()
	store.active = []metadb.UserConversationState{{
		UID:         "u1",
		ChannelID:   "active",
		ChannelType: 2,
		ActiveAt:    20,
	}}
	store.latest[ConversationKey{ChannelID: "active", ChannelType: 2}] = LastMessage{MessageSeq: 4, ServerTimestampMS: 4000}
	store.latest[ConversationKey{ChannelID: "overlay", ChannelType: 2}] = LastMessage{MessageSeq: 9, ServerTimestampMS: 9000}
	store.recents[ConversationKey{ChannelID: "overlay", ChannelType: 2}] = []SyncMessage{{
		MessageSeq:        9,
		ChannelID:         "overlay",
		ChannelType:       2,
		ServerTimestampMS: 9000,
	}}
	app := New(Options{Store: store, StateStore: store, Messages: store})

	got, err := app.Sync(context.Background(), SyncQuery{
		UID:      "u1",
		MsgCount: 1,
		LastMsgSeqs: map[ConversationKey]uint64{
			{ChannelID: "overlay", ChannelType: 2}: 7,
		},
	})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if got.OverlayItems != 1 {
		t.Fatalf("OverlayItems = %d, want 1", got.OverlayItems)
	}
	if got.RecentLoadDuration <= 0 {
		t.Fatalf("RecentLoadDuration = %v, want positive duration", got.RecentLoadDuration)
	}
}
```

- [ ] **Step 2: Run the failing usecase test**

Run:

```bash
go test ./internalv2/usecase/conversation -run TestSyncReportsOverlayAndRecentLoadShape -count=1
```

Expected: FAIL with `got.OverlayItems undefined` or `got.RecentLoadDuration undefined`.

- [ ] **Step 3: Extend the usecase sync result type**

In `internalv2/usecase/conversation/types.go`, add `time` to the imports:

```go
import (
	"errors"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)
```

Replace `SyncResult` with:

```go
// SyncResult contains the conversations selected for one sync response.
type SyncResult struct {
	// Conversations contains legacy-compatible synchronized conversations.
	Conversations []SyncConversation
	// OverlayItems counts client-known conversation candidates outside the active scan window.
	OverlayItems int
	// RecentLoadDuration records the recent-message load phase when MsgCount is positive.
	RecentLoadDuration time.Duration
}
```

- [ ] **Step 4: Populate sync shape facts**

In `internalv2/usecase/conversation/sync.go`, add `time` to imports:

```go
import (
	"context"
	"sort"
	"time"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)
```

After `addSyncActiveCandidates(active.Rows, candidates)`, keep the existing `addSyncOverlayCandidates` call and then add:

```go
	overlayItems := countSyncOverlayCandidates(candidates)
```

Replace the recent-message load block with:

```go
	var recentLoadDuration time.Duration
	if query.MsgCount > 0 {
		recentStarted := time.Now()
		if err := a.assignSyncRecents(ctx, views, query.MsgCount); err != nil {
			return SyncResult{}, err
		}
		recentLoadDuration = time.Since(recentStarted)
		if recentLoadDuration <= 0 {
			recentLoadDuration = time.Nanosecond
		}
	}
```

Replace the result initialization with:

```go
	result := SyncResult{
		Conversations:       make([]SyncConversation, 0, len(views)),
		OverlayItems:        overlayItems,
		RecentLoadDuration:  recentLoadDuration,
	}
```

Add this helper after `addSyncOverlayCandidates`:

```go
func countSyncOverlayCandidates(candidates map[ConversationKey]*syncCandidate) int {
	count := 0
	for _, candidate := range candidates {
		if candidate != nil && candidate.overlay {
			count++
		}
	}
	return count
}
```

- [ ] **Step 5: Run the usecase tests**

Run:

```bash
go test ./internalv2/usecase/conversation -run 'TestSync' -count=1
```

Expected: PASS.

- [ ] **Step 6: Write the failing access observer tests**

In `internalv2/access/api/conversation_sync_test.go`, add imports `errors` and `time` to the existing import block:

```go
import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)
```

Add these tests after `TestConversationSyncRejectsInvalidLegacyLastMsgSeqs`:

```go
func TestConversationSyncObserverRecordsShapeAndLatency(t *testing.T) {
	conversations := &recordingConversationUsecase{
		syncResult: conversationusecase.SyncResult{
			Conversations: []conversationusecase.SyncConversation{{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup}},
			OverlayItems:  2,
			RecentLoadDuration: 3 * time.Millisecond,
		},
	}
	observer := &recordingConversationSyncObserver{}
	srv := New(Options{Conversations: conversations, ConversationSyncObserver: observer})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/conversation/sync", bytes.NewBufferString(`{
		"uid":"u1",
		"last_msg_seqs":"g1:2:3|g2:2:4",
		"msg_count":1,
		"only_unread":1
	}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if len(observer.events) != 1 {
		t.Fatalf("observer events = %#v, want one", observer.events)
	}
	got := observer.events[0]
	if got.Result != "ok" || !got.OnlyUnread || !got.WithRecents || got.ReturnedItems != 1 || got.OverlayItems != 2 || got.RecentLoadDuration != 3*time.Millisecond {
		t.Fatalf("observer event = %#v, want sync shape", got)
	}
	if got.Duration <= 0 {
		t.Fatalf("observer duration = %v, want positive latency", got.Duration)
	}
}

func TestConversationSyncObserverRecordsFailures(t *testing.T) {
	for _, tt := range []struct {
		name          string
		conversations ConversationUsecase
		body          string
		wantResult    string
	}{
		{name: "invalid json", conversations: &recordingConversationUsecase{}, body: `{"uid":`, wantResult: "invalid_request"},
		{name: "missing uid", conversations: &recordingConversationUsecase{}, body: `{"msg_count":1}`, wantResult: "invalid_request"},
		{name: "missing usecase", body: `{"uid":"u1","msg_count":1}`, wantResult: "not_configured"},
		{name: "parse last msg seqs", conversations: &recordingConversationUsecase{}, body: `{"uid":"u1","last_msg_seqs":"bad"}`, wantResult: "parse_last_msg_seqs_error"},
		{name: "usecase error", conversations: &recordingConversationUsecase{syncErr: errors.New("sync failed")}, body: `{"uid":"u1","msg_count":1}`, wantResult: "error"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			observer := &recordingConversationSyncObserver{}
			srv := New(Options{Conversations: tt.conversations, ConversationSyncObserver: observer})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/conversation/sync", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")

			srv.Handler().ServeHTTP(rec, req)

			if len(observer.events) != 1 {
				t.Fatalf("observer events = %#v, want one", observer.events)
			}
			got := observer.events[0]
			if got.Result != tt.wantResult {
				t.Fatalf("observer result = %q, want %q", got.Result, tt.wantResult)
			}
			if got.Duration <= 0 {
				t.Fatalf("observer duration = %v, want positive latency", got.Duration)
			}
		})
	}
}

type recordingConversationSyncObserver struct {
	events []ConversationSyncObservation
}

func (r *recordingConversationSyncObserver) ObserveConversationSync(event ConversationSyncObservation) {
	r.events = append(r.events, event)
}
```

- [ ] **Step 7: Run the failing access tests**

Run:

```bash
go test ./internalv2/access/api -run 'TestConversationSync(ObserverRecordsShapeAndLatency|ObserverRecordsFailures)' -count=1
```

Expected: FAIL with `unknown field ConversationSyncObserver` or `undefined: ConversationSyncObservation`.

- [ ] **Step 8: Add sync observer types and server wiring**

In `internalv2/access/api/server.go`, add this type after `ConversationListObservation`:

```go
// ConversationSyncObservation captures one /conversation/sync request result.
type ConversationSyncObservation struct {
	// Result is a low-cardinality request result label.
	Result string
	// Duration is the end-to-end handler latency.
	Duration time.Duration
	// OnlyUnread reports whether the request asked for unread conversations only.
	OnlyUnread bool
	// WithRecents reports whether the request asked to include recent messages.
	WithRecents bool
	// ReturnedItems is the number of conversations returned to the client.
	ReturnedItems int
	// OverlayItems counts client-known overlay candidates outside the active scan window.
	OverlayItems int
	// RecentLoadDuration records the recent-message load phase when available.
	RecentLoadDuration time.Duration
}
```

Add this interface after `ConversationListObserver`:

```go
// ConversationSyncObserver receives performance observations for conversation sync reads.
type ConversationSyncObserver interface {
	ObserveConversationSync(ConversationSyncObservation)
}
```

Add this field to `Options` after `ConversationListObserver`:

```go
	// ConversationSyncObserver records conversation sync read performance.
	ConversationSyncObserver ConversationSyncObserver
```

Add this field to `Server` after `conversationObserver`:

```go
	conversationSyncObserver ConversationSyncObserver
```

Add this assignment in `New` after `conversationObserver: opts.ConversationListObserver`:

```go
		conversationSyncObserver: opts.ConversationSyncObserver,
```

- [ ] **Step 9: Record observations in `/conversation/sync`**

In `internalv2/access/api/conversation_sync.go`, add `time` to the imports:

```go
import (
	"net/http"
	"strconv"
	"strings"
	"time"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	messageusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/gin-gonic/gin"
)
```

Replace the beginning of `handleConversationSync` with:

```go
func (s *Server) handleConversationSync(c *gin.Context) {
	start := time.Now()
	var req syncConversationRequest
	if !bindJSON(c, &req) {
		s.observeConversationSync(ConversationSyncObservation{Result: "invalid_request", Duration: time.Since(start)})
		return
	}
	onlyUnread := req.OnlyUnread == 1
	withRecents := req.MsgCount > 0
	if req.UID == "" {
		writeJSONError(c, "invalid request")
		s.observeConversationSync(ConversationSyncObservation{Result: "invalid_request", Duration: time.Since(start), OnlyUnread: onlyUnread, WithRecents: withRecents})
		return
	}
	if s == nil || s.conversations == nil {
		writeJSONError(c, "conversation usecase not configured")
		s.observeConversationSync(ConversationSyncObservation{Result: "not_configured", Duration: time.Since(start), OnlyUnread: onlyUnread, WithRecents: withRecents})
		return
	}
	lastMsgSeqs, err := parseLegacyLastMsgSeqs(req.UID, req.LastMsgSeqs)
	if err != nil {
		writeJSONError(c, "invalid last_msg_seqs")
		s.observeConversationSync(ConversationSyncObservation{Result: "parse_last_msg_seqs_error", Duration: time.Since(start), OnlyUnread: onlyUnread, WithRecents: withRecents})
		return
	}
	result, err := s.conversations.Sync(c.Request.Context(), conversationusecase.SyncQuery{
		UID:                 req.UID,
		Version:             req.Version,
		LastMsgSeqs:         lastMsgSeqs,
		MsgCount:            req.MsgCount,
		OnlyUnread:          onlyUnread,
		ExcludeChannelTypes: append([]uint8(nil), req.ExcludeChannelTypes...),
		Limit:               req.Limit,
	})
	if err != nil {
		writeJSONError(c, err.Error())
		s.observeConversationSync(ConversationSyncObservation{Result: "error", Duration: time.Since(start), OnlyUnread: onlyUnread, WithRecents: withRecents})
		return
	}
	resp := make([]legacyConversationResponse, 0, len(result.Conversations))
	for _, item := range result.Conversations {
		resp = append(resp, newLegacyConversationResponse(req.UID, item))
	}
	s.observeConversationSync(ConversationSyncObservation{
		Result:             "ok",
		Duration:           time.Since(start),
		OnlyUnread:         onlyUnread,
		WithRecents:        withRecents,
		ReturnedItems:      len(result.Conversations),
		OverlayItems:       result.OverlayItems,
		RecentLoadDuration: result.RecentLoadDuration,
	})
	c.JSON(http.StatusOK, resp)
}
```

Add this helper after `handleConversationSync`:

```go
func (s *Server) observeConversationSync(event ConversationSyncObservation) {
	if s == nil || s.conversationSyncObserver == nil {
		return
	}
	if event.Result == "" {
		event.Result = "error"
	}
	if event.Duration <= 0 {
		event.Duration = time.Nanosecond
	}
	s.conversationSyncObserver.ObserveConversationSync(event)
}
```

- [ ] **Step 10: Update API FLOW.md**

In `internalv2/access/api/FLOW.md`, add this sentence to the `/conversation/sync` paragraph:

```markdown
The adapter records one low-cardinality sync observation for each request path,
including invalid JSON, invalid `last_msg_seqs`, missing usecase, usecase
errors, and successful responses. Observation labels never include UID,
channel ID, device, message ID, or error text.
```

- [ ] **Step 11: Run access and usecase tests**

Run:

```bash
go test ./internalv2/usecase/conversation ./internalv2/access/api -run 'TestSync|TestConversationSync' -count=1
```

Expected: PASS.

- [ ] **Step 12: Commit task 2**

```bash
git add internalv2/usecase/conversation/types.go internalv2/usecase/conversation/sync.go internalv2/usecase/conversation/sync_test.go internalv2/access/api/server.go internalv2/access/api/conversation_sync.go internalv2/access/api/conversation_sync_test.go internalv2/access/api/FLOW.md
git commit -m "feat: observe conversation sync requests"
```

---

### Task 3: Wire Sync Observations Through App Metrics

**Files:**
- Modify: `internalv2/app/app.go`
- Modify: `internalv2/app/wiring.go`
- Modify: `internalv2/app/observability.go`
- Modify: `internalv2/app/observability_test.go`

- [ ] **Step 1: Write the failing app observability test**

In `internalv2/app/observability_test.go`, add this import to the import block:

```go
	accessapi "github.com/WuKongIM/WuKongIM/internalv2/access/api"
```

Add this test after `TestObservabilityConversationAuthorityMetricsObserverMapsCounters` in `internalv2/app/observability_test.go`:

```go
func TestObservabilityConversationSyncMetricsObserverMapsCounters(t *testing.T) {
	reg := obsmetrics.New(1, "n1")
	observer := conversationSyncMetricsObserver{metrics: reg}

	observer.ObserveConversationSync(accessapi.ConversationSyncObservation{
		Result:             "ok",
		Duration:           15 * time.Millisecond,
		OnlyUnread:         true,
		WithRecents:        true,
		ReturnedItems:      4,
		OverlayItems:       2,
		RecentLoadDuration: 3 * time.Millisecond,
	})

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	total := requireAppMetricFamily(t, families, "wukongim_conversation_sync_total")
	if got := findAppMetricByLabels(t, total, map[string]string{"result": "ok", "only_unread": "true", "with_recents": "true"}).GetCounter().GetValue(); got != 1 {
		t.Fatalf("sync total metric = %v, want 1", got)
	}
	returned := requireAppMetricFamily(t, families, "wukongim_conversation_sync_returned_items")
	if got := findAppMetricByLabels(t, returned, map[string]string{"result": "ok", "only_unread": "true", "with_recents": "true"}).GetHistogram().GetSampleSum(); got != 4 {
		t.Fatalf("sync returned metric = %v, want 4", got)
	}
	recent := requireAppMetricFamily(t, families, "wukongim_conversation_sync_recent_load_duration_seconds")
	if got := findAppMetricByLabels(t, recent, map[string]string{"result": "ok", "only_unread": "true"}).GetHistogram().GetSampleCount(); got != 1 {
		t.Fatalf("sync recent load metric count = %v, want 1", got)
	}
}
```

- [ ] **Step 2: Run the failing app observability test**

Run:

```bash
go test ./internalv2/app -run TestObservabilityConversationSyncMetricsObserverMapsCounters -count=1
```

Expected: FAIL with `undefined: conversationSyncMetricsObserver`.

- [ ] **Step 3: Add app observer provider**

In `internalv2/app/app.go`, add this method after `conversationListObserver()`:

```go
func (a *App) conversationSyncObserver() accessapi.ConversationSyncObserver {
	if a == nil || a.metrics == nil {
		return nil
	}
	return conversationSyncMetricsObserver{metrics: a.metrics}
}
```

- [ ] **Step 4: Wire the observer into the API server**

In `internalv2/app/wiring.go`, find the `accessapi.Options` literal and add this field immediately after `ConversationListObserver`:

```go
			ConversationSyncObserver: a.conversationSyncObserver(),
```

- [ ] **Step 5: Map observations to metrics**

In `internalv2/app/observability.go`, add this method near `conversationListMetricsObserver`:

```go
func (o conversationSyncMetricsObserver) ObserveConversationSync(event accessapi.ConversationSyncObservation) {
	if o.metrics == nil || o.metrics.Conversation == nil {
		return
	}
	o.metrics.Conversation.ObserveSync(event.Result, event.OnlyUnread, event.WithRecents, event.Duration, event.ReturnedItems, event.OverlayItems, event.RecentLoadDuration)
}
```

Add this small type near the observer type declarations in the same file:

```go
type conversationSyncMetricsObserver struct {
	metrics *obsmetrics.Registry
}
```

- [ ] **Step 6: Run the app tests**

Run:

```bash
go test ./internalv2/app -run 'TestObservabilityConversation(SyncMetricsObserverMapsCounters|AuthorityMetricsObserverMapsCounters)' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit task 3**

```bash
git add internalv2/app/app.go internalv2/app/wiring.go internalv2/app/observability.go internalv2/app/observability_test.go
git commit -m "feat: wire conversation sync observability"
```

---

### Task 4: Add Conversation Cards To The Prometheus Monitor Provider

**Files:**
- Modify: `internalv2/access/manager/monitor.go`
- Modify: `internalv2/app/manager_monitor_prometheus.go`
- Modify: `internalv2/app/manager_monitor_prometheus_test.go`
- Modify: `internalv2/access/manager/FLOW.md`

- [ ] **Step 1: Write the failing monitor provider test**

Add this test to `internalv2/app/manager_monitor_prometheus_test.go` after `TestManagerMonitorPrometheusProviderReadsV2ChannelAppendBacklog`:

```go
func TestManagerMonitorPrometheusProviderIncludesConversationCardsAndSnapshots(t *testing.T) {
	var conversationQueries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		if strings.Contains(query, "wukongim_conversation_") {
			conversationQueries = append(conversationQueries, query)
		}
		switch {
		case strings.Contains(query, "wukongim_conversation_sync_recent_load_duration_seconds_bucket"):
			writePrometheusMatrixForMonitorTest(t, w, 8, 11)
		case strings.Contains(query, "wukongim_conversation_sync_duration_seconds_bucket"):
			writePrometheusMatrixForMonitorTest(t, w, 35, 41)
		case strings.Contains(query, "wukongim_conversation_active_flush_duration_seconds_bucket"):
			writePrometheusMatrixForMonitorTest(t, w, 4, 6)
		case strings.Contains(query, "wukongim_conversation_sync_total"),
			strings.Contains(query, "wukongim_conversation_sync_returned_items"),
			strings.Contains(query, "wukongim_conversation_active_cache_dirty_rows"),
			strings.Contains(query, "wukongim_conversation_active_cache_oldest_dirty_age_seconds"),
			strings.Contains(query, "wukongim_conversation_active_flush_total"),
			strings.Contains(query, "wukongim_conversation_authority_"):
			writePrometheusMatrixForMonitorTest(t, w, 2, 3)
		default:
			writePrometheusMatrixForMonitorTest(t, w, 1, 1)
		}
	}))
	defer server.Close()
	provider := newManagerPrometheusMonitorProvider(managerPrometheusMonitorOptions{
		Enabled: true,
		BaseURL: server.URL,
		Client:  server.Client(),
		Now:     func() time.Time { return time.Unix(1781767240, 0).UTC() },
	})

	resp, err := provider.RealtimeMonitor(context.Background(), accessmanager.RealtimeMonitorQuery{
		Window: 15 * time.Minute,
		Step:   20 * time.Second,
	})
	if err != nil {
		t.Fatalf("RealtimeMonitor() error = %v", err)
	}

	for _, key := range []string{
		"conversationSyncRate",
		"conversationSyncLatencyP99",
		"conversationSyncErrorRate",
		"conversationReturnedItems",
		"conversationRecentLoadLatencyP99",
		"conversationActiveDirtyRows",
		"conversationActiveOldestDirtyAge",
		"conversationActiveFlushLatencyP99",
		"conversationActiveFlushErrorRate",
		"conversationAuthorityPressureRate",
	} {
		card := requireMonitorCardForTest(t, resp.Cards, key)
		if card.Stage != accessmanager.RealtimeMonitorStageConversationSync {
			t.Fatalf("%s stage = %q, want conversationSync", key, card.Stage)
		}
		if !card.Available {
			t.Fatalf("%s card unavailable: %#v", key, card)
		}
	}
	for _, key := range []string{"conversationSyncP99", "conversationSyncErrors", "conversationDirtyAge", "conversationFlushErrors"} {
		requireMonitorSnapshotForTest(t, resp.Snapshot, key)
	}
	if len(conversationQueries) < 10 {
		t.Fatalf("conversation queries = %d, want at least 10: %#v", len(conversationQueries), conversationQueries)
	}
}
```

Add this helper near `requireMonitorCardForTest`:

```go
func requireMonitorSnapshotForTest(t *testing.T, snapshots []accessmanager.RealtimeMonitorSnapshotEntry, key string) accessmanager.RealtimeMonitorSnapshotEntry {
	t.Helper()
	for _, snapshot := range snapshots {
		if snapshot.Key == key {
			return snapshot
		}
	}
	t.Fatalf("snapshot %q not found in %#v", key, snapshots)
	return accessmanager.RealtimeMonitorSnapshotEntry{}
}
```

Add this test after the new inclusion test:

```go
func TestManagerMonitorPrometheusProviderConversationNoDataAndNoDirtyHandling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		switch {
		case strings.Contains(query, "wukongim_conversation_sync_recent_load_duration_seconds_bucket"):
			writePrometheusMatrixForMonitorTest(t, w)
		case strings.Contains(query, "wukongim_conversation_active_flush_total{result!~\"ok|no_dirty\"}"):
			if strings.Contains(query, "no_dirty") {
				writePrometheusMatrixForMonitorTest(t, w, 0, 0)
				return
			}
			t.Fatalf("flush error query = %q, want no_dirty excluded", query)
		default:
			writePrometheusMatrixForMonitorTest(t, w, 1, 1)
		}
	}))
	defer server.Close()
	provider := newManagerPrometheusMonitorProvider(managerPrometheusMonitorOptions{
		Enabled: true,
		BaseURL: server.URL,
		Client:  server.Client(),
		Now:     func() time.Time { return time.Unix(1781767240, 0).UTC() },
	})

	resp, err := provider.RealtimeMonitor(context.Background(), accessmanager.RealtimeMonitorQuery{
		Window: 15 * time.Minute,
		Step:   20 * time.Second,
	})
	if err != nil {
		t.Fatalf("RealtimeMonitor() error = %v", err)
	}
	recent := requireMonitorCardForTest(t, resp.Cards, "conversationRecentLoadLatencyP99")
	if recent.Available || recent.UnavailableReason != "no_conversation_recent_load_samples" {
		t.Fatalf("recent load card = %#v, want no-data recent load reason", recent)
	}
	flushErrors := requireMonitorCardForTest(t, resp.Cards, "conversationActiveFlushErrorRate")
	if !flushErrors.Available || flushErrors.Value != 0 {
		t.Fatalf("flush errors card = %#v, want available zero value", flushErrors)
	}
}
```

- [ ] **Step 2: Run the failing monitor provider tests**

Run:

```bash
go test ./internalv2/app -run 'TestManagerMonitorPrometheusProvider(IncludesConversationCardsAndSnapshots|ConversationNoDataAndNoDirtyHandling)' -count=1
```

Expected: FAIL with `RealtimeMonitorStageConversationSync undefined` or missing card keys.

- [ ] **Step 3: Add the manager monitor stage constant**

In `internalv2/access/manager/monitor.go`, add this constant after `RealtimeMonitorStageAppendCommit`:

```go
	// RealtimeMonitorStageConversationSync identifies conversation sync and projection cards.
	RealtimeMonitorStageConversationSync = "conversationSync"
```

- [ ] **Step 4: Add conversation card definitions**

In `internalv2/app/manager_monitor_prometheus.go`, insert these definitions in `managerMonitorMetricDefinitions()` after `pendingCommitBacklog` and before `deliveryRate`:

```go
		{
			key:               "conversationSyncRate",
			stage:             accessmanager.RealtimeMonitorStageConversationSync,
			tone:              accessmanager.RealtimeMonitorToneNormal,
			unit:              "req/s",
			unavailableReason: "no_conversation_sync_samples",
			noDataMessage:     "no conversation sync samples in selected window",
			query: func(rateWindow string) string {
				return "sum(rate(wukongim_conversation_sync_total[" + rateWindow + "]))"
			},
		},
		{
			key:               "conversationSyncLatencyP99",
			stage:             accessmanager.RealtimeMonitorStageConversationSync,
			tone:              accessmanager.RealtimeMonitorToneWarning,
			unit:              "ms",
			unavailableReason: "no_conversation_sync_latency_samples",
			noDataMessage:     "no conversation sync latency samples in selected window",
			query: func(rateWindow string) string {
				return "histogram_quantile(0.99, sum(rate(wukongim_conversation_sync_duration_seconds_bucket[" + rateWindow + "])) by (le)) * 1000"
			},
		},
		{
			key:               "conversationSyncErrorRate",
			stage:             accessmanager.RealtimeMonitorStageConversationSync,
			tone:              accessmanager.RealtimeMonitorToneCritical,
			unit:              "%",
			unavailableReason: "no_conversation_sync_samples",
			noDataMessage:     "no conversation sync samples in selected window",
			query: func(rateWindow string) string {
				errors := "sum(rate(wukongim_conversation_sync_total{result!=\"ok\"}[" + rateWindow + "]))"
				total := "sum(rate(wukongim_conversation_sync_total[" + rateWindow + "]))"
				return "(" + errors + " / clamp_min(" + total + ", 1)) * 100"
			},
		},
		{
			key:               "conversationReturnedItems",
			stage:             accessmanager.RealtimeMonitorStageConversationSync,
			tone:              accessmanager.RealtimeMonitorToneNormal,
			unit:              "items",
			unavailableReason: "no_conversation_sync_samples",
			noDataMessage:     "no conversation sync samples in selected window",
			query: func(rateWindow string) string {
				sum := "sum(rate(wukongim_conversation_sync_returned_items_sum{result=\"ok\"}[" + rateWindow + "]))"
				count := "sum(rate(wukongim_conversation_sync_returned_items_count{result=\"ok\"}[" + rateWindow + "]))"
				return sum + " / clamp_min(" + count + ", 1)"
			},
		},
		{
			key:               "conversationRecentLoadLatencyP99",
			stage:             accessmanager.RealtimeMonitorStageConversationSync,
			tone:              accessmanager.RealtimeMonitorToneWarning,
			unit:              "ms",
			unavailableReason: "no_conversation_recent_load_samples",
			noDataMessage:     "no conversation recent-message load samples in selected window",
			query: func(rateWindow string) string {
				return "histogram_quantile(0.99, sum(rate(wukongim_conversation_sync_recent_load_duration_seconds_bucket[" + rateWindow + "])) by (le)) * 1000"
			},
		},
		{
			key:   "conversationActiveDirtyRows",
			stage: accessmanager.RealtimeMonitorStageConversationSync,
			tone:  accessmanager.RealtimeMonitorToneWarning,
			unit:  "rows",
			query: func(string) string {
				return "sum(wukongim_conversation_active_cache_dirty_rows)"
			},
		},
		{
			key:   "conversationActiveOldestDirtyAge",
			stage: accessmanager.RealtimeMonitorStageConversationSync,
			tone:  accessmanager.RealtimeMonitorToneCritical,
			unit:  "s",
			query: func(string) string {
				return "max(wukongim_conversation_active_cache_oldest_dirty_age_seconds)"
			},
		},
		{
			key:               "conversationActiveFlushLatencyP99",
			stage:             accessmanager.RealtimeMonitorStageConversationSync,
			tone:              accessmanager.RealtimeMonitorToneWarning,
			unit:              "ms",
			unavailableReason: "no_conversation_active_flush_samples",
			noDataMessage:     "no conversation active flush samples in selected window",
			query: func(rateWindow string) string {
				return "histogram_quantile(0.99, sum(rate(wukongim_conversation_active_flush_duration_seconds_bucket[" + rateWindow + "])) by (le)) * 1000"
			},
		},
		{
			key:   "conversationActiveFlushErrorRate",
			stage: accessmanager.RealtimeMonitorStageConversationSync,
			tone:  accessmanager.RealtimeMonitorToneCritical,
			unit:  "%",
			query: func(rateWindow string) string {
				errors := prometheusZeroFallback("sum(rate(wukongim_conversation_active_flush_total{result!~\"ok|no_dirty\"}[" + rateWindow + "]))")
				total := prometheusZeroFallback("sum(rate(wukongim_conversation_active_flush_total[" + rateWindow + "]))")
				return "(" + errors + " / clamp_min(" + total + ", 1)) * 100"
			},
		},
		{
			key:   "conversationAuthorityPressureRate",
			stage: accessmanager.RealtimeMonitorStageConversationSync,
			tone:  accessmanager.RealtimeMonitorToneWarning,
			unit:  "events/s",
			query: func(rateWindow string) string {
				pressure := prometheusZeroFallback("sum(rate(wukongim_conversation_authority_cache_pressure_total{result!=\"ok\"}[" + rateWindow + "]))")
				admit := prometheusZeroFallback("sum(rate(wukongim_conversation_authority_admit_total{result=~\"cache_pressure|route_not_ready|stale_route|not_leader|timeout\"}[" + rateWindow + "]))")
				return pressure + " + " + admit
			},
		},
```

- [ ] **Step 5: Add conversation snapshots**

In `monitorSnapshotFromCards`, add these entries after `entryP99` and before `deliveryP99`:

```go
		{key: "conversationSyncP99", metricKey: "conversationSyncLatencyP99", unit: "ms", tone: accessmanager.RealtimeMonitorToneWarning},
		{key: "conversationSyncErrors", metricKey: "conversationSyncErrorRate", unit: "%", tone: accessmanager.RealtimeMonitorToneCritical},
		{key: "conversationDirtyAge", metricKey: "conversationActiveOldestDirtyAge", unit: "s", tone: accessmanager.RealtimeMonitorToneCritical},
		{key: "conversationFlushErrors", metricKey: "conversationActiveFlushErrorRate", unit: "%", tone: accessmanager.RealtimeMonitorToneCritical},
```

- [ ] **Step 6: Update manager FLOW.md**

In `internalv2/access/manager/FLOW.md`, update the `/manager/monitor/realtime` paragraph with:

```markdown
The business monitor includes a conversation sync stage between append/commit
and online delivery. That stage reads Prometheus metrics for `/conversation/sync`
client experience, active-cache dirty age, active flush health, and conversation
authority pressure.
```

- [ ] **Step 7: Run monitor provider tests**

Run:

```bash
go test ./internalv2/access/manager ./internalv2/app -run 'TestManagerRealtimeMonitor|TestManagerMonitorPrometheusProvider' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit task 4**

```bash
git add internalv2/access/manager/monitor.go internalv2/app/manager_monitor_prometheus.go internalv2/app/manager_monitor_prometheus_test.go internalv2/access/manager/FLOW.md
git commit -m "feat: add conversation monitor cards"
```

---

### Task 5: Extend The Web Monitor Model

**Files:**
- Modify: `web/src/pages/monitor/types.ts`
- Modify: `web/src/pages/monitor/metric-config.ts`
- Modify: `web/src/pages/monitor/preview-data.ts`
- Modify: `web/src/pages/monitor/preview-data.test.ts`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Write the failing preview order test**

In `web/src/pages/monitor/preview-data.test.ts`, replace the expected card order in `builds the business path monitor cards in the required order` with:

```ts
  expect(model.cards.map((card) => card.key)).toEqual([
    "sendRate",
    "sendSuccessRate",
    "entryLatencyP99",
    "commitRate",
    "commitLatencyP99",
    "pendingCommitBacklog",
    "conversationSyncRate",
    "conversationSyncLatencyP99",
    "conversationSyncErrorRate",
    "conversationReturnedItems",
    "conversationRecentLoadLatencyP99",
    "conversationActiveDirtyRows",
    "conversationActiveOldestDirtyAge",
    "conversationActiveFlushLatencyP99",
    "conversationActiveFlushErrorRate",
    "conversationAuthorityPressureRate",
    "deliveryRate",
    "deliveryLatencyP99",
    "fanOutRatio",
    "offlineEnqueueRate",
    "retryQueueDepth",
    "pathErrorRate",
  ])
```

Replace the expected snapshot order with:

```ts
  expect(model.snapshot.map((entry) => entry.labelId)).toEqual([
    "monitor.snapshot.send",
    "monitor.snapshot.delivery",
    "monitor.snapshot.entryP99",
    "monitor.snapshot.conversationSyncP99",
    "monitor.snapshot.conversationSyncErrors",
    "monitor.snapshot.conversationDirtyAge",
    "monitor.snapshot.conversationFlushErrors",
    "monitor.snapshot.deliveryP99",
    "monitor.snapshot.errors",
    "monitor.snapshot.retryDepth",
    "monitor.snapshot.online",
  ])
```

- [ ] **Step 2: Run the failing preview test**

Run:

```bash
cd web && bun run test -- src/pages/monitor/preview-data.test.ts
```

Expected: FAIL because the new conversation card keys are missing.

- [ ] **Step 3: Add monitor stage and metric keys**

In `web/src/pages/monitor/types.ts`, replace `MonitorStage` with:

```ts
export type MonitorStage = "sendEntry" | "appendCommit" | "conversationSync" | "onlineDelivery" | "offlineRetry" | "errorClosure"
```

Add these keys to `MonitorMetricKey` after `pendingCommitBacklog`:

```ts
  | "conversationSyncRate"
  | "conversationSyncLatencyP99"
  | "conversationSyncErrorRate"
  | "conversationReturnedItems"
  | "conversationRecentLoadLatencyP99"
  | "conversationActiveDirtyRows"
  | "conversationActiveOldestDirtyAge"
  | "conversationActiveFlushLatencyP99"
  | "conversationActiveFlushErrorRate"
  | "conversationAuthorityPressureRate"
```

- [ ] **Step 4: Add frontend metric configuration**

In `web/src/pages/monitor/metric-config.ts`, add these entries after `pendingCommitBacklog`:

```ts
  conversationSyncRate: { titleId: "monitor.metrics.conversationSyncRate", chartColor: "#0284c7", precision: 1 },
  conversationSyncLatencyP99: { titleId: "monitor.metrics.conversationSyncLatencyP99", chartColor: "#9333ea", precision: 1 },
  conversationSyncErrorRate: { titleId: "monitor.metrics.conversationSyncErrorRate", chartColor: "#dc2626", precision: 2 },
  conversationReturnedItems: { titleId: "monitor.metrics.conversationReturnedItems", chartColor: "#0f766e", precision: 1 },
  conversationRecentLoadLatencyP99: { titleId: "monitor.metrics.conversationRecentLoadLatencyP99", chartColor: "#c026d3", precision: 1 },
  conversationActiveDirtyRows: { titleId: "monitor.metrics.conversationActiveDirtyRows", chartColor: "#ca8a04", precision: 0 },
  conversationActiveOldestDirtyAge: { titleId: "monitor.metrics.conversationActiveOldestDirtyAge", chartColor: "#ea580c", precision: 1 },
  conversationActiveFlushLatencyP99: { titleId: "monitor.metrics.conversationActiveFlushLatencyP99", chartColor: "#7c3aed", precision: 1 },
  conversationActiveFlushErrorRate: { titleId: "monitor.metrics.conversationActiveFlushErrorRate", chartColor: "#b91c1c", precision: 2 },
  conversationAuthorityPressureRate: { titleId: "monitor.metrics.conversationAuthorityPressureRate", chartColor: "#64748b", precision: 1 },
```

Add the stage label:

```ts
  conversationSync: "monitor.stage.conversationSync",
```

Add snapshot labels:

```ts
  conversationSyncP99: "monitor.snapshot.conversationSyncP99",
  conversationSyncErrors: "monitor.snapshot.conversationSyncErrors",
  conversationDirtyAge: "monitor.snapshot.conversationDirtyAge",
  conversationFlushErrors: "monitor.snapshot.conversationFlushErrors",
```

Add unavailable reasons:

```ts
  no_conversation_sync_samples: "monitor.noData.conversationSyncSamples",
  no_conversation_sync_latency_samples: "monitor.noData.conversationSyncLatencySamples",
  no_conversation_recent_load_samples: "monitor.noData.conversationRecentLoadSamples",
  no_conversation_active_flush_samples: "monitor.noData.conversationActiveFlushSamples",
```

- [ ] **Step 5: Add preview card specs**

In `web/src/pages/monitor/preview-data.ts`, add `conversationSync` to `stageLabelIds`:

```ts
  conversationSync: "monitor.stage.conversationSync",
```

Insert these `cardSpecs` after `pendingCommitBacklog`:

```ts
  {
    key: "conversationSyncRate",
    titleId: "monitor.metrics.conversationSyncRate",
    stage: "conversationSync",
    tone: "normal",
    unit: "req/s",
    chartColor: "#0284c7",
    base: 420,
    amplitude: 54,
    pulse: 76,
    precision: 1,
    stats: [
      { labelId: "monitor.stat.avg", kind: "avg" },
      { labelId: "monitor.stat.peak", kind: "peak" },
      { labelId: "monitor.stat.total5m", value: "126k" },
    ],
  },
  {
    key: "conversationSyncLatencyP99",
    titleId: "monitor.metrics.conversationSyncLatencyP99",
    stage: "conversationSync",
    tone: "warning",
    unit: "ms",
    chartColor: "#9333ea",
    base: 42,
    amplitude: 6,
    pulse: 21,
    precision: 1,
    stats: [
      { labelId: "monitor.stat.p50", kind: "p50" },
      { labelId: "monitor.stat.p95", kind: "p95" },
      { labelId: "monitor.stat.peakP99", kind: "peakP99" },
    ],
  },
  {
    key: "conversationSyncErrorRate",
    titleId: "monitor.metrics.conversationSyncErrorRate",
    stage: "conversationSync",
    tone: "critical",
    unit: "%",
    chartColor: "#dc2626",
    base: 0.12,
    amplitude: 0.04,
    pulse: 0.14,
    precision: 2,
    stats: [
      { labelId: "monitor.stat.avg", kind: "avg" },
      { labelId: "monitor.stat.peak", kind: "peak" },
      { labelId: "monitor.stat.topReason", value: "parse_last_msg_seqs_error" },
    ],
  },
  {
    key: "conversationReturnedItems",
    titleId: "monitor.metrics.conversationReturnedItems",
    stage: "conversationSync",
    tone: "normal",
    unit: "items",
    chartColor: "#0f766e",
    base: 18,
    amplitude: 3.8,
    pulse: 5,
    precision: 1,
    stats: [
      { labelId: "monitor.stat.avg", kind: "avg" },
      { labelId: "monitor.stat.peak", kind: "peak" },
      { labelId: "monitor.stat.onlyUnread", value: "38%" },
    ],
  },
  {
    key: "conversationRecentLoadLatencyP99",
    titleId: "monitor.metrics.conversationRecentLoadLatencyP99",
    stage: "conversationSync",
    tone: "warning",
    unit: "ms",
    chartColor: "#c026d3",
    base: 24,
    amplitude: 5,
    pulse: 14,
    precision: 1,
    stats: [
      { labelId: "monitor.stat.p50", kind: "p50" },
      { labelId: "monitor.stat.p95", kind: "p95" },
      { labelId: "monitor.stat.withRecents", value: "64%" },
    ],
  },
  {
    key: "conversationActiveDirtyRows",
    titleId: "monitor.metrics.conversationActiveDirtyRows",
    stage: "conversationSync",
    tone: "warning",
    unit: "rows",
    chartColor: "#ca8a04",
    base: 380,
    amplitude: 74,
    drift: 1.4,
    pulse: 126,
    stats: [
      { labelId: "monitor.stat.avg", kind: "avg" },
      { labelId: "monitor.stat.peakQueue", kind: "peak" },
      { labelId: "monitor.stat.oldestWait", value: "7.2s" },
    ],
  },
  {
    key: "conversationActiveOldestDirtyAge",
    titleId: "monitor.metrics.conversationActiveOldestDirtyAge",
    stage: "conversationSync",
    tone: "critical",
    unit: "s",
    chartColor: "#ea580c",
    base: 6.8,
    amplitude: 1.6,
    pulse: 5.4,
    precision: 1,
    stats: [
      { labelId: "monitor.stat.avg", kind: "avg" },
      { labelId: "monitor.stat.peak", kind: "peak" },
      { labelId: "monitor.stat.dirtyRows", value: "418" },
    ],
  },
  {
    key: "conversationActiveFlushLatencyP99",
    titleId: "monitor.metrics.conversationActiveFlushLatencyP99",
    stage: "conversationSync",
    tone: "warning",
    unit: "ms",
    chartColor: "#7c3aed",
    base: 31,
    amplitude: 6,
    pulse: 15,
    precision: 1,
    stats: [
      { labelId: "monitor.stat.p50", kind: "p50" },
      { labelId: "monitor.stat.p95", kind: "p95" },
      { labelId: "monitor.stat.flushRows", value: "8.4k" },
    ],
  },
  {
    key: "conversationActiveFlushErrorRate",
    titleId: "monitor.metrics.conversationActiveFlushErrorRate",
    stage: "conversationSync",
    tone: "critical",
    unit: "%",
    chartColor: "#b91c1c",
    base: 0.08,
    amplitude: 0.03,
    pulse: 0.11,
    precision: 2,
    stats: [
      { labelId: "monitor.stat.avg", kind: "avg" },
      { labelId: "monitor.stat.peak", kind: "peak" },
      { labelId: "monitor.stat.topReason", value: "route_not_ready" },
    ],
  },
  {
    key: "conversationAuthorityPressureRate",
    titleId: "monitor.metrics.conversationAuthorityPressureRate",
    stage: "conversationSync",
    tone: "warning",
    unit: "events/s",
    chartColor: "#64748b",
    base: 4.2,
    amplitude: 1.1,
    pulse: 3.8,
    precision: 1,
    stats: [
      { labelId: "monitor.stat.avg", kind: "avg" },
      { labelId: "monitor.stat.peak", kind: "peak" },
      { labelId: "monitor.stat.topReason", value: "cache_pressure" },
    ],
  },
```

In `buildPreviewMonitorModel`, add the four snapshot entries after `entryP99`:

```ts
    { key: "conversationSyncP99", labelId: "monitor.snapshot.conversationSyncP99", value: "42.8", unit: "ms", tone: "warning" },
    { key: "conversationSyncErrors", labelId: "monitor.snapshot.conversationSyncErrors", value: "0.12", unit: "%", tone: "critical" },
    { key: "conversationDirtyAge", labelId: "monitor.snapshot.conversationDirtyAge", value: "6.8", unit: "s", tone: "critical" },
    { key: "conversationFlushErrors", labelId: "monitor.snapshot.conversationFlushErrors", value: "0.08", unit: "%", tone: "critical" },
```

- [ ] **Step 6: Add i18n labels**

In `web/src/i18n/messages/en.ts`, add:

```ts
  "monitor.stage.conversationSync": "Conversation Sync",
  "monitor.noData.conversationSyncSamples": "No conversation sync samples in the selected time range.",
  "monitor.noData.conversationSyncLatencySamples": "No conversation sync latency samples in the selected time range.",
  "monitor.noData.conversationRecentLoadSamples": "No recent-message load samples in the selected time range.",
  "monitor.noData.conversationActiveFlushSamples": "No conversation active flush samples in the selected time range.",
  "monitor.snapshot.conversationSyncP99": "Sync P99",
  "monitor.snapshot.conversationSyncErrors": "Sync Errors",
  "monitor.snapshot.conversationDirtyAge": "Dirty Age",
  "monitor.snapshot.conversationFlushErrors": "Flush Errors",
  "monitor.metrics.conversationSyncRate": "Conversation Sync Rate",
  "monitor.metrics.conversationSyncLatencyP99": "Conversation Sync Latency P99",
  "monitor.metrics.conversationSyncErrorRate": "Conversation Sync Error Rate",
  "monitor.metrics.conversationReturnedItems": "Conversation Returned Items",
  "monitor.metrics.conversationRecentLoadLatencyP99": "Recent Load Latency P99",
  "monitor.metrics.conversationActiveDirtyRows": "Conversation Active Dirty Rows",
  "monitor.metrics.conversationActiveOldestDirtyAge": "Conversation Oldest Dirty Age",
  "monitor.metrics.conversationActiveFlushLatencyP99": "Conversation Active Flush Latency P99",
  "monitor.metrics.conversationActiveFlushErrorRate": "Conversation Active Flush Error Rate",
  "monitor.metrics.conversationAuthorityPressureRate": "Conversation Authority Pressure Rate",
  "monitor.stat.onlyUnread": "Only Unread",
  "monitor.stat.withRecents": "With Recents",
  "monitor.stat.dirtyRows": "Dirty Rows",
  "monitor.stat.flushRows": "Flush Rows",
```

In `web/src/i18n/messages/zh-CN.ts`, add:

```ts
  "monitor.stage.conversationSync": "会话同步",
  "monitor.noData.conversationSyncSamples": "当前时间范围内尚未产生会话同步样本。",
  "monitor.noData.conversationSyncLatencySamples": "当前时间范围内尚未产生会话同步延迟样本。",
  "monitor.noData.conversationRecentLoadSamples": "当前时间范围内尚未产生最近消息加载样本。",
  "monitor.noData.conversationActiveFlushSamples": "当前时间范围内尚未产生会话活跃刷新样本。",
  "monitor.snapshot.conversationSyncP99": "同步 P99",
  "monitor.snapshot.conversationSyncErrors": "同步错误",
  "monitor.snapshot.conversationDirtyAge": "脏行等待",
  "monitor.snapshot.conversationFlushErrors": "刷新错误",
  "monitor.metrics.conversationSyncRate": "会话同步速率",
  "monitor.metrics.conversationSyncLatencyP99": "会话同步延迟 P99",
  "monitor.metrics.conversationSyncErrorRate": "会话同步错误率",
  "monitor.metrics.conversationReturnedItems": "同步返回会话数",
  "monitor.metrics.conversationRecentLoadLatencyP99": "最近消息加载 P99",
  "monitor.metrics.conversationActiveDirtyRows": "会话活跃脏行",
  "monitor.metrics.conversationActiveOldestDirtyAge": "最老脏行等待",
  "monitor.metrics.conversationActiveFlushLatencyP99": "会话活跃刷新 P99",
  "monitor.metrics.conversationActiveFlushErrorRate": "会话活跃刷新错误率",
  "monitor.metrics.conversationAuthorityPressureRate": "会话权威压力",
  "monitor.stat.onlyUnread": "只看未读",
  "monitor.stat.withRecents": "携带最近消息",
  "monitor.stat.dirtyRows": "脏行",
  "monitor.stat.flushRows": "刷新行",
```

- [ ] **Step 7: Run preview and type tests**

Run:

```bash
cd web && bun run test -- src/pages/monitor/preview-data.test.ts
cd web && bunx tsc -b
```

Expected: PASS.

- [ ] **Step 8: Commit task 5**

```bash
git add web/src/pages/monitor/types.ts web/src/pages/monitor/metric-config.ts web/src/pages/monitor/preview-data.ts web/src/pages/monitor/preview-data.test.ts web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git commit -m "feat: add conversation monitor web model"
```

---

### Task 6: Render API Conversation Cards And Run Final Verification

**Files:**
- Modify: `web/src/pages/monitor/page.test.tsx`
- Modify: `web/src/pages/page-shells.test.tsx` only if existing expectations require exact snapshot or text counts after the new labels.

- [ ] **Step 1: Add API fixture coverage for conversation cards**

In `web/src/pages/monitor/page.test.tsx`, add these snapshot entries to `readyMonitorResponse().snapshot` after `entryP99` when that fixture includes it, or after `send` when it only has two entries:

```ts
      { key: "conversationSyncP99", metric_key: "conversationSyncLatencyP99", value: 43.2, unit: "ms", tone: "warning" as const },
      { key: "conversationDirtyAge", metric_key: "conversationActiveOldestDirtyAge", value: 6.5, unit: "s", tone: "critical" as const },
```

Add these cards to `readyMonitorResponse().cards` after `sendRate`:

```ts
      {
        key: "conversationSyncLatencyP99",
        stage: "conversationSync",
        tone: "warning" as const,
        unit: "ms",
        value: 43.2,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 39 },
          { timestamp: 1781767220000, value: 43.2 },
        ],
        stats: [
          { key: "avg", value: 41.1 },
          { key: "peak", value: 43.2 },
          { key: "total", value: 1644 },
        ],
      },
      {
        key: "conversationActiveOldestDirtyAge",
        stage: "conversationSync",
        tone: "critical" as const,
        unit: "s",
        value: 6.5,
        available: true,
        error: "",
        series: [
          { timestamp: 1781767200000, value: 4.1 },
          { timestamp: 1781767220000, value: 6.5 },
        ],
        stats: [
          { key: "avg", value: 5.3 },
          { key: "peak", value: 6.5 },
          { key: "total", value: 212 },
        ],
      },
```

Update `renders realtime business monitor cards from prometheus data`:

```ts
  expect(cards).toHaveLength(4)
  expect(within(cards[1]).getByText("Conversation Sync Latency P99")).toBeInTheDocument()
  expect(within(cards[1]).getByText("43.2")).toBeInTheDocument()
  expect(within(cards[2]).getByText("Conversation Oldest Dirty Age")).toBeInTheDocument()
```

Extend the snapshot label assertion loop:

```ts
  for (const label of ["Send", "Sync P99", "Dirty Age", "Online"]) {
    expect(screen.getByText(label)).toBeInTheDocument()
  }
```

- [ ] **Step 2: Add no-data coverage for recent load card**

In `partialMonitorResponseWithNoDataCard()`, add this unavailable card to the `cards` array:

```ts
      {
        key: "conversationRecentLoadLatencyP99",
        stage: "conversationSync",
        tone: "warning" as const,
        unit: "ms",
        value: 0,
        available: false,
        unavailable_reason: "no_conversation_recent_load_samples",
        error: "no conversation recent-message load samples in selected window",
        series: [],
        stats: [],
      },
```

Update the no-data test:

```ts
  expect(cards).toHaveLength(3)
  expect(within(cards[2]).getByText("Recent Load Latency P99")).toBeInTheDocument()
  expect(within(cards[2]).getByText("No recent-message load samples in the selected time range.")).toBeInTheDocument()
```

- [ ] **Step 3: Run frontend monitor tests**

Run:

```bash
cd web && bun run test -- src/pages/monitor/page.test.tsx src/pages/monitor/preview-data.test.ts
```

Expected: PASS.

- [ ] **Step 4: Run focused backend verification**

Run:

```bash
go test ./pkg/metrics ./internalv2/usecase/conversation ./internalv2/access/api ./internalv2/access/manager ./internalv2/app
```

Expected: PASS.

- [ ] **Step 5: Run focused frontend verification**

Run:

```bash
cd web && bun run test -- src/pages/monitor/page.test.tsx src/pages/monitor/preview-data.test.ts src/pages/page-shells.test.tsx
cd web && bunx tsc -b
```

Expected: PASS.

- [ ] **Step 6: Review the changed file set**

Run:

```bash
git status --short
git diff --stat
```

Expected: only files listed in this plan are changed, plus any unrelated files that were already dirty before execution. Do not stage unrelated pre-existing changes.

- [ ] **Step 7: Commit task 6**

```bash
git add web/src/pages/monitor/page.test.tsx web/src/pages/page-shells.test.tsx
git commit -m "test: cover conversation monitor cards"
```

If `web/src/pages/page-shells.test.tsx` did not change, omit it from `git add` and commit only `web/src/pages/monitor/page.test.tsx`.

- [ ] **Step 8: Final integration commit check**

Run:

```bash
git log --oneline -n 6
git status --short
```

Expected: the latest commits are the task commits from this plan. The status output may still include unrelated pre-existing local modifications; those must remain unstaged unless the user explicitly asks to include them.

---

## Self-Review Checklist

- Spec coverage:
  - `/conversation/sync` is the client SLO path: Tasks 1, 2, and 3.
  - Active cache, dirty age, flush health, and authority pressure are reused: Task 4.
  - Existing `/manager/monitor/realtime` response shape is preserved: Task 4.
  - Web keeps `/business/monitor` and adds only keys, labels, preview data, and tests: Tasks 5 and 6.
  - Low-cardinality labels exclude UID, channel ID, device, message ID, client message number, and error text: Tasks 1 and 2.
  - `/conversation/list` remains untouched except shared conversation metric helpers: no task changes its behavior.
- Type consistency:
  - Backend card stage is `conversationSync`.
  - Frontend stage is `conversationSync`.
  - Snapshot keys are `conversationSyncP99`, `conversationSyncErrors`, `conversationDirtyAge`, and `conversationFlushErrors`.
  - No-data reason keys use `no_conversation_*` strings in backend and map to `monitor.noData.*` labels in frontend.
- Verification:
  - Backend focused command: `go test ./pkg/metrics ./internalv2/usecase/conversation ./internalv2/access/api ./internalv2/access/manager ./internalv2/app`
  - Frontend focused command: `cd web && bun run test -- src/pages/monitor/page.test.tsx src/pages/monitor/preview-data.test.ts src/pages/page-shells.test.tsx`
  - TypeScript command: `cd web && bunx tsc -b`

package top

import (
	"bytes"
	"strings"
	"testing"
	"time"

	accessapi "github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/top/topapi"
)

func TestAggregateOrdersNodesByPressureWithoutMutatingInput(t *testing.T) {
	base := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	nodes := []accessapi.TopSnapshot{
		{
			GeneratedAt: base,
			Node:        accessapi.TopNodeSnapshot{Name: "verdict-only", Ready: true},
			Verdict:     accessapi.TopVerdict{Level: "busy", Summary: "busy without detailed pressure"},
		},
		{
			GeneratedAt: base.Add(time.Second),
			Node:        accessapi.TopNodeSnapshot{Name: "component", Ready: false},
			Verdict:     accessapi.TopVerdict{Level: "degraded", Summary: "component pressure"},
			Pressure: &accessapi.TopPressure{
				OverallLevel:    "degraded",
				ComponentScores: map[string]float64{"delivery": 0.90},
				Top:             []accessapi.TopPressureItem{{Component: "delivery", Score: 0.80}},
			},
		},
		{
			GeneratedAt: base.Add(2 * time.Second),
			Node:        accessapi.TopNodeSnapshot{Name: "fallback", Ready: true},
			Verdict:     accessapi.TopVerdict{Level: "critical", Summary: "critical fallback"},
			Pressure:    &accessapi.TopPressure{OverallLevel: "critical"},
		},
	}

	got := aggregate(nodes)
	if got.TotalNodes != 3 || got.ReadyNodes != 2 {
		t.Fatalf("node counts = ready %d total %d", got.ReadyNodes, got.TotalNodes)
	}
	if !got.GeneratedAt.Equal(base.Add(2 * time.Second)) {
		t.Fatalf("generated at = %s", got.GeneratedAt)
	}
	if got.Verdict.Level != "critical" || got.Verdict.Summary != "critical fallback" {
		t.Fatalf("aggregate verdict = %#v", got.Verdict)
	}
	wantOrder := []string{"fallback", "component", "verdict-only"}
	for i, want := range wantOrder {
		if got.Nodes[i].Node.Name != want {
			t.Fatalf("node order[%d] = %q, want %q", i, got.Nodes[i].Node.Name, want)
		}
	}
	if nodes[0].Node.Name != "verdict-only" {
		t.Fatalf("aggregate mutated caller order: %#v", nodes)
	}

	empty := aggregate(nil)
	if empty.TotalNodes != 0 || empty.Verdict.Level != "critical" || empty.Verdict.Summary != "no nodes returned a snapshot" {
		t.Fatalf("empty aggregate = %#v", empty)
	}
}

func TestPressureScoreAndVerdictSeverityFallbacks(t *testing.T) {
	tests := []struct {
		name string
		node accessapi.TopSnapshot
		want float64
	}{
		{name: "unknown verdict", node: accessapi.TopSnapshot{}, want: 0},
		{name: "verdict", node: accessapi.TopSnapshot{Verdict: accessapi.TopVerdict{Level: "degraded"}}, want: 0.75},
		{name: "top item", node: accessapi.TopSnapshot{Pressure: &accessapi.TopPressure{Top: []accessapi.TopPressureItem{{Score: 0.7}, {Score: 0.9}}}}, want: 0.9},
		{name: "component", node: accessapi.TopSnapshot{Pressure: &accessapi.TopPressure{Top: []accessapi.TopPressureItem{{Score: 0.7}}, ComponentScores: map[string]float64{"channelv2": 0.95}}}, want: 0.95},
		{name: "pressure level", node: accessapi.TopSnapshot{Pressure: &accessapi.TopPressure{OverallLevel: "busy"}}, want: 0.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := topPressureScore(tt.node); got != tt.want {
				t.Fatalf("pressure score = %v, want %v", got, tt.want)
			}
		})
	}

	for level, want := range map[string]int{"critical": 4, "degraded": 3, "busy": 2, "ok": 1, "unknown": 0} {
		if got := verdictSeverity(level); got != want {
			t.Fatalf("severity(%q) = %d, want %d", level, got, want)
		}
	}
}

func TestAggregateAlertsNormalizesAndSortsOperatorEvidence(t *testing.T) {
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	resolvedAt := now.Add(-time.Minute)
	alerts := []accessapi.TopAlert{
		{ID: "resolved-critical", Active: false, ResolvedAt: &resolvedAt, Severity: "critical", NodeName: "z", Component: "z", Message: "z", LastSeen: now},
		{ID: "active-unknown", Active: true, Severity: "notice", NodeName: "a", Component: "a", Message: "a", LastSeen: now},
		{ID: "active-warn", Active: true, Severity: "warn", NodeName: "a", Component: "a", Message: "a", LastSeen: now},
		{ID: "active-error", Active: true, Severity: "error", NodeName: "a", Component: "a", Message: "a", LastSeen: now},
		{ID: "active-critical-new", Active: true, Severity: "critical", NodeName: "z", Component: "z", Message: "z", LastSeen: now},
		{ID: "active-critical-old", Active: true, Severity: "critical", NodeName: "a", Component: "a", Message: "a", LastSeen: now.Add(-time.Second)},
		{ID: "node-a-component-a-message-b", Active: true, Severity: "critical", NodeName: "a", Component: "a", Message: "b", LastSeen: now},
		{ID: "node-a-component-a-message-a", Active: true, Severity: "critical", NodeName: "a", Component: "a", Message: "a", LastSeen: now},
		{ID: "node-a-component-b", Active: true, Severity: "critical", NodeName: "a", Component: "b", Message: "a", LastSeen: now},
	}

	sortAlerts(alerts)
	want := []string{
		"node-a-component-a-message-a",
		"node-a-component-a-message-b",
		"node-a-component-b",
		"active-critical-new",
		"active-critical-old",
		"active-error",
		"active-warn",
		"active-unknown",
		"resolved-critical",
	}
	for i := range want {
		if alerts[i].ID != want[i] {
			t.Fatalf("alert order[%d] = %q, want %q; all=%#v", i, alerts[i].ID, want[i], alerts)
		}
	}

	fromActive := aggregateAlerts(aggregateSnapshot{Nodes: []accessapi.TopSnapshot{{
		Node: accessapi.TopNodeSnapshot{ID: 7, Name: "node-seven"},
		Alerts: &accessapi.TopAlerts{Active: []accessapi.TopAlert{{
			ID:       "inherited-node",
			Severity: "warn",
			Active:   true,
		}}},
	}}})
	if len(fromActive) != 1 || fromActive[0].NodeID != 7 || fromActive[0].NodeName != "node-seven" {
		t.Fatalf("normalized alerts = %#v", fromActive)
	}
}

func TestAlertSelectionSupportsStableIdentifiers(t *testing.T) {
	snapshot := aggregate([]accessapi.TopSnapshot{sampleSnapshot()})
	for _, filter := range []string{
		"alert-1",
		"critical|channelv2|pressure_high|append",
		"channelv2/pressure_high",
		"channel/pressure_high",
	} {
		got, err := selectAlerts(snapshot, "  "+filter+"  ")
		if err != nil || len(got) != 1 || got[0].ID != "alert-1" {
			t.Fatalf("select %q = %#v, %v", filter, got, err)
		}
	}
	if got, err := selectAlerts(snapshot, ""); err != nil || len(got) != 1 {
		t.Fatalf("select all = %#v, %v", got, err)
	}
}

func TestHumanRenderingHandlesEmptyAndResolvedSnapshots(t *testing.T) {
	var empty bytes.Buffer
	if err := renderHuman(&empty, aggregate(nil)); err != nil {
		t.Fatalf("render empty snapshot: %v", err)
	}
	for _, want := range []string{"ready: 0/0", "ALERTS\nnone", "HOT PRESSURE\nnone"} {
		if !strings.Contains(empty.String(), want) {
			t.Fatalf("empty rendering %q missing %q", empty.String(), want)
		}
	}

	resolvedAt := time.Date(2026, 7, 3, 11, 59, 0, 0, time.UTC)
	snapshot := aggregateSnapshot{
		GeneratedAt: resolvedAt.Add(time.Minute),
		Nodes: []accessapi.TopSnapshot{{
			Node: accessapi.TopNodeSnapshot{ID: 3},
			Alerts: &accessapi.TopAlerts{Recent: []accessapi.TopAlert{{
				ResolvedAt: &resolvedAt,
				Severity:   "error",
				Component:  "transportv2",
				LastSeen:   resolvedAt,
				Evidence:   map[string]string{"z": "last", "a": "first"},
			}}},
		}},
	}
	var detail bytes.Buffer
	if err := renderAlerts(&detail, snapshot, ""); err != nil {
		t.Fatalf("render resolved alert: %v", err)
	}
	for _, want := range []string{"id: -", "node: node-3", "state: resolved", "component: transport", "message: -", "resolved_at:", "age: 1m", "  a=first\n  z=last"} {
		if !strings.Contains(detail.String(), want) {
			t.Fatalf("resolved rendering %q missing %q", detail.String(), want)
		}
	}

	var none bytes.Buffer
	if err := renderAlerts(&none, aggregateSnapshot{}, ""); err != nil {
		t.Fatalf("render no alerts: %v", err)
	}
	if !strings.Contains(none.String(), "total: 0\nnone") {
		t.Fatalf("no-alert rendering = %q", none.String())
	}
}

func TestHumanValueFormattingCoversOperationalBoundaries(t *testing.T) {
	resolvedAt := time.Date(2026, 7, 3, 11, 59, 0, 0, time.UTC)
	if nodeName(accessapi.TopSnapshot{Node: accessapi.TopNodeSnapshot{Name: "named", ID: 1}}) != "named" ||
		nodeName(accessapi.TopSnapshot{Node: accessapi.TopNodeSnapshot{ID: 2}}) != "node-2" ||
		nodeName(accessapi.TopSnapshot{}) != "unknown" {
		t.Fatal("node-name fallback contract changed")
	}
	if alertNodeName(accessapi.TopAlert{NodeName: "named", NodeID: 1}) != "named" ||
		alertNodeName(accessapi.TopAlert{NodeID: 2}) != "node-2" ||
		alertNodeName(accessapi.TopAlert{}) != "unknown" {
		t.Fatal("alert node-name fallback contract changed")
	}

	bytesCases := map[uint64]string{
		512:           "512B",
		1024:          "1KiB",
		1536:          "1.5KiB",
		1024 * 1024:   "1MiB",
		1536 * 1024:   "1.5MiB",
		1 << 30:       "1GiB",
		3 * (1 << 29): "1.5GiB",
	}
	for value, want := range bytesCases {
		if got := formatBytes(value); got != want {
			t.Fatalf("formatBytes(%d) = %q, want %q", value, got, want)
		}
	}

	traffic := &accessapi.TopTraffic{SendPerSec: 1, SendackPerSec: 2, AppendPerSec: 3, DeliverPerSec: 4, AppendP99MS: 5}
	for name, want := range map[string]float64{"send": 1, "ack": 2, "append": 3, "deliver": 4, "unknown": 0} {
		if got := rateValue(traffic, name); got != want {
			t.Fatalf("rateValue(%q) = %v, want %v", name, got, want)
		}
	}
	if rateValue(nil, "send") != 0 || appendP99(nil) != 0 || formatMS(0) != "-" || formatMS(1.25) != "1.25ms" {
		t.Fatal("missing traffic formatting contract changed")
	}

	for _, tc := range []struct {
		node accessapi.TopSnapshot
		want string
	}{
		{node: accessapi.TopSnapshot{}, want: "unknown"},
		{node: accessapi.TopSnapshot{Verdict: accessapi.TopVerdict{Level: "busy"}}, want: "busy"},
		{node: accessapi.TopSnapshot{Pressure: &accessapi.TopPressure{OverallLevel: "ok"}}, want: "ok"},
		{node: accessapi.TopSnapshot{Pressure: &accessapi.TopPressure{OverallLevel: "busy", Top: []accessapi.TopPressureItem{{Component: "transportv2", Score: 0.6}}}}, want: "transport busy 0.60"},
	} {
		if got := pressureSummary(tc.node); got != tc.want {
			t.Fatalf("pressure summary = %q, want %q", got, tc.want)
		}
	}

	if alertState(accessapi.TopAlert{Active: true}) != "active" || alertState(accessapi.TopAlert{ResolvedAt: &resolvedAt}) != "resolved" || alertState(accessapi.TopAlert{}) != "recent" {
		t.Fatal("alert state contract changed")
	}
	if formatAlertAge(time.Time{}, time.Time{}) != "-" ||
		formatAlertAge(resolvedAt, resolvedAt.Add(time.Second)) != "0s" ||
		formatAlertAge(resolvedAt, resolvedAt.Add(-59*time.Second)) != "59s" ||
		formatAlertAge(resolvedAt, resolvedAt.Add(-61*time.Minute)) != "1h" {
		t.Fatal("alert-age boundary contract changed")
	}
	if displayComponent("channelv2") != "channel" || displayComponent("transportv2") != "transport" || displayComponent("delivery") != "delivery" {
		t.Fatal("component display compatibility changed")
	}
}

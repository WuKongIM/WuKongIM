package top

import (
	"reflect"
	"strings"
	"testing"
	"time"

	accessapi "github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/top/topapi"
	ui "github.com/gizak/termui/v3"
)

func TestEmptyTUIViewKeepsDashboardShapeAndUnknownStatus(t *testing.T) {
	view := buildTUIView(aggregateSnapshot{}, config{
		Window:   defaultWindow,
		Interval: defaultInterval,
		View:     defaultView,
	})

	for _, want := range []string{"generated -", "alerts none", "servers: -"} {
		if !strings.Contains(view.Header, want) {
			t.Fatalf("empty header %q missing %q", view.Header, want)
		}
	}
	if view.VerdictLevel != "unknown" || view.VerdictPercent != 0 || view.VerdictTitle != "VERDICT unknown ready 0/0" {
		t.Fatalf("empty verdict = %#v", view)
	}
	if got := view.NodeRows[1][8]; got != "no nodes" {
		t.Fatalf("empty node row = %#v", view.NodeRows)
	}
	if got := view.PressureRows[1][6]; got != "no hot pressure" {
		t.Fatalf("empty pressure row = %#v", view.PressureRows)
	}
	if got := view.AlertRows[1][7]; got != "no alerts" {
		t.Fatalf("empty alert row = %#v", view.AlertRows)
	}
	if got := view.RuntimeRows[1][6]; got != "no channel data" {
		t.Fatalf("empty runtime row = %#v", view.RuntimeRows)
	}
	for _, want := range []string{"ready: 0/0", "collector: unknown", "cluster snapshot: unknown", "metrics: unknown"} {
		if !listContains(view.StatusRows, want) {
			t.Fatalf("empty status %#v missing %q", view.StatusRows, want)
		}
	}
	if got := buildAlertDetailRows(aggregateSnapshot{}, 99); !reflect.DeepEqual(got, []string{"no alert selected"}) {
		t.Fatalf("empty alert detail = %#v", got)
	}
}

func TestDashboardAlertSelectionAndSizingAreBounded(t *testing.T) {
	dashboard := newTopDashboard(config{View: "all"})
	if dashboard.header == nil || dashboard.verdict == nil || dashboard.nodes == nil || dashboard.alerts == nil || dashboard.pressure == nil || dashboard.runtime == nil || dashboard.status == nil {
		t.Fatal("dashboard did not initialize every panel")
	}
	if dashboard.nodes.Title != " Nodes " || dashboard.alerts.Title != " Alerts " || dashboard.status.Title != " Status " {
		t.Fatalf("dashboard titles = %q %q %q", dashboard.nodes.Title, dashboard.alerts.Title, dashboard.status.Title)
	}

	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	snapshot := aggregateSnapshot{Nodes: []accessapi.TopSnapshot{{
		Alerts: &accessapi.TopAlerts{Recent: []accessapi.TopAlert{
			{ID: "a", Active: true, Severity: "critical", LastSeen: now},
			{ID: "b", Active: true, Severity: "error", LastSeen: now},
			{ID: "c", Active: true, Severity: "warn", LastSeen: now},
		}},
	}}}

	dashboard.selectPrevAlert(snapshot)
	if dashboard.selectedAlert != 2 {
		t.Fatalf("previous selection should wrap to 2, got %d", dashboard.selectedAlert)
	}
	dashboard.selectNextAlert(snapshot)
	if dashboard.selectedAlert != 0 {
		t.Fatalf("next selection should wrap to 0, got %d", dashboard.selectedAlert)
	}
	dashboard.selectedAlert = -2
	dashboard.clampSelectedAlert(snapshot)
	if dashboard.selectedAlert != 0 {
		t.Fatalf("negative selection clamped to %d", dashboard.selectedAlert)
	}
	dashboard.selectedAlert = 100
	dashboard.clampSelectedAlert(snapshot)
	if dashboard.selectedAlert != 2 {
		t.Fatalf("high selection clamped to %d", dashboard.selectedAlert)
	}
	dashboard.selectNextAlert(aggregateSnapshot{})
	if dashboard.selectedAlert != 0 {
		t.Fatalf("empty selection = %d", dashboard.selectedAlert)
	}
	dashboard.selectPrevAlert(aggregateSnapshot{})
	dashboard.selectedAlert = 7
	dashboard.clampSelectedAlert(aggregateSnapshot{})
	if dashboard.selectedAlert != 0 {
		t.Fatalf("empty clamp = %d", dashboard.selectedAlert)
	}
	dashboard.toggleAlertDetails()
	if !dashboard.showAlertDetail {
		t.Fatal("detail mode did not enable")
	}
	dashboard.toggleAlertDetails()
	if dashboard.showAlertDetail {
		t.Fatal("detail mode did not disable")
	}

	dashboard.resizeTables(80)
	if dashboard.nodes.ColumnWidths[0] != 10 || dashboard.alerts.ColumnWidths[7] != 25 {
		t.Fatalf("compact widths = nodes %#v alerts %#v", dashboard.nodes.ColumnWidths, dashboard.alerts.ColumnWidths)
	}
	dashboard.resizeTables(160)
	if dashboard.nodes.ColumnWidths[0] != 14 || dashboard.alerts.ColumnWidths[7] != 34 {
		t.Fatalf("wide widths = nodes %#v alerts %#v", dashboard.nodes.ColumnWidths, dashboard.alerts.ColumnWidths)
	}
}

func TestTUIRowsExposeCapacityLatencyAndResolvedAlertBoundaries(t *testing.T) {
	resolvedAt := time.Date(2026, 7, 5, 10, 0, 0, 0, time.FixedZone("UTC+2", 2*60*60))
	snapshot := aggregateSnapshot{
		Nodes: []accessapi.TopSnapshot{{
			Node: accessapi.TopNodeSnapshot{ID: 11},
			Pressure: &accessapi.TopPressure{Top: []accessapi.TopPressureItem{{
				Component:            "transportv2",
				Pool:                 "send",
				Queue:                "urgent",
				Priority:             "high",
				Level:                "critical",
				Score:                1,
				Depth:                9,
				Capacity:             10,
				Inflight:             4,
				Workers:              4,
				WaitP99MS:            12,
				TaskP99MS:            34,
				AdmissionErrorPerSec: 2,
			}}},
			ChannelRuntime: &accessapi.TopChannelRuntime{
				ActiveTotal:               3,
				WorkerQueueDepthByPool:    map[string]int64{"send": 9, "append": 2},
				WorkerQueueCapacityByPool: map[string]int64{"send": 10},
				WorkerInflightByPool:      map[string]int64{"send": 4},
				WorkerCapacityByPool:      map[string]int64{"send": 4},
				AppendP99MS:               45,
			},
			Alerts: &accessapi.TopAlerts{Recent: []accessapi.TopAlert{{
				ID:         "resolved",
				ResolvedAt: &resolvedAt,
				LastSeen:   resolvedAt,
			}}},
		}},
	}

	pressure := buildPressureRows(snapshot)[1]
	for _, want := range []string{"node-11", "transport send/urgent/high", "critical", "depth 9/10 inflight 4/4", "wait 12.00ms task 34.00ms"} {
		if !rowContains(pressure, want) {
			t.Fatalf("pressure row %#v missing %q", pressure, want)
		}
	}
	runtime := buildRuntimeRows(snapshot)[1]
	if runtime[4] != "append=2,send=9/10" || runtime[5] != "send=4/4" || runtime[6] != "append 45.00ms" {
		t.Fatalf("runtime row = %#v", runtime)
	}
	detail := buildAlertDetailRows(snapshot, -1)
	if !listContains(detail, "resolved_at: 2026-07-05T08:00:00Z") {
		t.Fatalf("resolved alert detail = %#v", detail)
	}
	if detailHigh := buildAlertDetailRows(snapshot, 100); !listContains(detailHigh, "id: resolved") {
		t.Fatalf("high selected index was not clamped: %#v", detailHigh)
	}
}

func TestTUIStatusAndFormattingBoundaries(t *testing.T) {
	nodes := []accessapi.TopSnapshot{
		{
			Node: accessapi.TopNodeSnapshot{Name: "one"},
			Sources: accessapi.TopSources{
				Collector:       accessapi.TopSourceStatus{Available: true},
				ClusterSnapshot: accessapi.TopSourceStatus{Available: false},
				Metrics:         accessapi.TopMetricsSource{Enabled: true, Required: true},
				Notes:           []string{"collector warming"},
			},
		},
		{
			Node: accessapi.TopNodeSnapshot{Name: "two"},
			Sources: accessapi.TopSources{
				Collector:       accessapi.TopSourceStatus{Available: false},
				ClusterSnapshot: accessapi.TopSourceStatus{Available: true},
			},
		},
	}
	snapshot := aggregateSnapshot{Nodes: nodes, ReadyNodes: 1, TotalNodes: 2}
	status := buildStatusRows(snapshot)
	for _, want := range []string{"ready: 1/2", "collector: 1/2 available", "cluster snapshot: 1/2 available", "metrics: 1/2 enabled, required by 1", "one: collector warming"} {
		if !listContains(status, want) {
			t.Fatalf("status %#v missing %q", status, want)
		}
	}
	if got := metricsStatus(aggregateSnapshot{Nodes: nodes[1:]}); got != "0/1 enabled, optional" {
		t.Fatalf("optional metrics status = %q", got)
	}
	if got := topServerSummary([]string{"a", "b", "c", "d", "e"}); got != "a, b, c, +2 more" {
		t.Fatalf("server summary = %q", got)
	}
	if got := topServerSummary([]string{"a", "b"}); got != "a, b" {
		t.Fatalf("short server summary = %q", got)
	}
	if readyPercent(1, 3) != 33 || readyPercent(1, 0) != 0 {
		t.Fatal("ready percentage boundary changed")
	}
	if emptyDash(" \t") != "-" || emptyDash(" value ") != " value " {
		t.Fatal("empty placeholder contract changed")
	}

	for _, tc := range []struct {
		name string
		got  string
		want string
	}{
		{name: "no signal", got: pressureSignal("", "", "", "none"), want: "-"},
		{name: "component", got: pressureSignal("channel", "", "", ""), want: "channel"},
		{name: "resource", got: pressureSignal("", "append", "worker", "none"), want: "append/worker"},
		{name: "priority", got: pressureSignal("channel", "append", "worker", "high"), want: "channel append/worker/high"},
		{name: "no capacity", got: formatCapacity(0, 0), want: "-"},
		{name: "unknown capacity", got: formatCapacity(3, 0), want: "3/-"},
		{name: "capacity", got: formatCapacity(3, 4), want: "3/4"},
		{name: "no load", got: pressureLoad(0, 0, 0, 0), want: "-"},
		{name: "inflight only", got: pressureLoad(0, 0, 2, 4), want: "inflight 2/4"},
		{name: "no latency", got: pressureP99(0, 0), want: "-"},
		{name: "task latency", got: pressureP99(0, 9), want: "task 9.00ms"},
		{name: "missing map", got: formatInt64Map(nil, nil), want: "-"},
		{name: "stage without latency", got: hotStageLabel("propose", nil, 10), want: "propose"},
		{name: "append latency", got: hotStageLabel("", nil, 10), want: "append 10.00ms"},
		{name: "no hot stage", got: hotStageLabel("", nil, 0), want: "-"},
		{name: "zero alert time", got: formatAlertTime(time.Time{}), want: "-"},
	} {
		if tc.got != tc.want {
			t.Fatalf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

func TestTUIColorAndRowStylesReflectSeverity(t *testing.T) {
	for level, want := range map[string]ui.Color{
		"critical": ui.ColorRed,
		"error":    ui.ColorRed,
		"degraded": ui.ColorMagenta,
		"warn":     ui.ColorYellow,
		"busy":     ui.ColorYellow,
		"ok":       ui.ColorGreen,
		"unknown":  ui.ColorWhite,
	} {
		if got := topLevelColor(level); got != want {
			t.Fatalf("color(%q) = %v, want %v", level, got, want)
		}
	}

	rows := [][]string{{"NODE", "LEVEL"}, {"one", "critical"}, {"short"}, {"two", "ok"}}
	styles := tableRowStyles(rows, 1)
	if len(styles) != 3 || styles[0].Fg != ui.ColorCyan || styles[1].Fg != ui.ColorRed || styles[3].Fg != ui.ColorGreen {
		t.Fatalf("row styles = %#v", styles)
	}
	headerOnly := tableRowStyles(rows, -1)
	if len(headerOnly) != 1 || headerOnly[0].Fg != ui.ColorCyan {
		t.Fatalf("header-only styles = %#v", headerOnly)
	}
}

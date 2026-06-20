package top

import (
	"strings"
	"testing"
	"time"

	accessapi "github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/top/topapi"
)

func TestBuildTUIViewShowsOperationalSignals(t *testing.T) {
	snapshot := aggregate([]accessapi.TopSnapshot{sampleSnapshot()})
	view := buildTUIView(snapshot, config{
		Servers:  []string{"http://127.0.0.1:5011"},
		Window:   10 * time.Second,
		Interval: time.Second,
		View:     "all",
	})

	for _, want := range []string{"WuKongIM top", "window 10s", "refresh 1s", "alerts C:1 E:0 W:0 active:1 recent:1", "http://127.0.0.1:5011"} {
		if !strings.Contains(view.Header, want) {
			t.Fatalf("expected header to contain %q, got %q", want, view.Header)
		}
	}
	if view.VerdictLevel != "degraded" {
		t.Fatalf("expected verdict level degraded, got %q", view.VerdictLevel)
	}
	if view.VerdictTitle != "VERDICT degraded ready 1/1" {
		t.Fatalf("expected verdict title, got %q", view.VerdictTitle)
	}
	if view.VerdictPercent != 100 {
		t.Fatalf("expected ready percent 100, got %d", view.VerdictPercent)
	}
	if len(view.NodeRows) < 2 {
		t.Fatalf("expected node table with data row, got %#v", view.NodeRows)
	}
	node := view.NodeRows[1]
	for _, want := range []string{"node-1", "yes", "42", "12.50", "128MiB", "100.50", "99.50", "45.00ms", "channelv2 degraded 0.91"} {
		if !rowContains(node, want) {
			t.Fatalf("expected node row to contain %q, got %#v", want, node)
		}
	}
	if len(view.PressureRows) < 2 {
		t.Fatalf("expected pressure table with data row, got %#v", view.PressureRows)
	}
	pressure := view.PressureRows[1]
	for _, want := range []string{"node-1", "channelv2 append/worker", "degraded", "0.91", "depth 91/100", "wait 77.00ms", "scale append workers"} {
		if !rowContains(pressure, want) {
			t.Fatalf("expected pressure row to contain %q, got %#v", want, pressure)
		}
	}
	if len(view.RuntimeRows) < 2 {
		t.Fatalf("expected runtime table with data row, got %#v", view.RuntimeRows)
	}
	runtime := view.RuntimeRows[1]
	for _, want := range []string{"node-1", "9", "5/4", "17", "append=91", "append=8", "propose 77.00ms"} {
		if !rowContains(runtime, want) {
			t.Fatalf("expected runtime row to contain %q, got %#v", want, runtime)
		}
	}
	if !listContains(view.StatusRows, "channelv2 worker queue saturated") {
		t.Fatalf("expected status rows to contain verdict reason, got %#v", view.StatusRows)
	}
	if len(view.AlertRows) < 2 {
		t.Fatalf("expected alerts table with data row, got %#v", view.AlertRows)
	}
	alert := view.AlertRows[1]
	for _, want := range []string{">", "critical", "node-1", "channelv2", "pressure_high", "active", "4", "append worker queue saturated"} {
		if !rowContains(alert, want) {
			t.Fatalf("expected alert row to contain %q, got %#v", want, alert)
		}
	}
}

func TestBuildAlertDetailRowsShowsSelectedAlertDetails(t *testing.T) {
	snapshot := aggregate([]accessapi.TopSnapshot{sampleSnapshot()})

	rows := buildAlertDetailRows(snapshot, 0)

	for _, want := range []string{
		"id: alert-1",
		"node: node-1",
		"severity: critical",
		"component: channelv2",
		"kind: pressure_high",
		"state: active",
		"count: 4",
		"first_seen: 2026-06-14T09:59:56Z",
		"last_seen: 2026-06-14T10:00:00Z",
		"message: append worker queue saturated",
		"hint: scale append workers",
		"evidence:",
		"  score: 0.91",
		"  depth: 91",
		"  capacity: 100",
		"  threshold.degraded: 0.80",
		"fingerprint: critical|channelv2|pressure_high|append",
	} {
		if !listContains(rows, want) {
			t.Fatalf("expected alert detail rows to contain %q, got %#v", want, rows)
		}
	}
}

func TestShouldRunTermUIOnlyForUnboundedHumanMode(t *testing.T) {
	if !shouldRunTermUI(config{}) {
		t.Fatalf("expected default interactive top to use termui")
	}
	if shouldRunTermUI(config{JSON: true}) {
		t.Fatalf("expected JSON interactive top to keep text rendering")
	}
	if shouldRunTermUI(config{MaxRefresh: 1}) {
		t.Fatalf("expected bounded refresh mode to keep text rendering")
	}
}

func rowContains(row []string, want string) bool {
	for _, cell := range row {
		if cell == want {
			return true
		}
	}
	return false
}

func listContains(rows []string, want string) bool {
	for _, row := range rows {
		if strings.Contains(row, want) {
			return true
		}
	}
	return false
}

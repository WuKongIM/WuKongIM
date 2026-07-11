package grafana

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var metricTokenPattern = regexp.MustCompile(`wukongim_[a-zA-Z0-9_]+`)
var metricDefinitionPattern = regexp.MustCompile(`Name:\s+"(wukongim_[^"]+)"`)

type dashboardAsset struct {
	UID string `json:"uid"`
}

func TestDashboardAssetsCoverAllExportedMetrics(t *testing.T) {
	dashboardDir := "dashboards"
	metricDir := filepath.Clean(filepath.Join("..", "..", "..", "pkg", "metrics"))

	dashboards := readDashboardAssets(t, dashboardDir)
	wantUIDs := []string{
		"wukongim-overview",
		"wukongim-transport-rpc",
		"wukongim-runtime-storage",
	}
	for _, uid := range wantUIDs {
		if _, ok := dashboards[uid]; !ok {
			t.Fatalf("dashboard uid %q not found in %s", uid, dashboardDir)
		}
	}

	referencedMetrics := make(map[string]struct{})
	for _, raw := range dashboards {
		for metric := range collectDashboardMetricNames(raw) {
			referencedMetrics[metric] = struct{}{}
		}
	}

	exportedMetrics := readExportedMetricNames(t, metricDir)
	missing := make([]string, 0)
	for metric := range exportedMetrics {
		if _, ok := referencedMetrics[metric]; ok {
			continue
		}
		missing = append(missing, metric)
	}
	slices.Sort(missing)
	if len(missing) > 0 {
		t.Fatalf("dashboard coverage missing metrics: %s", strings.Join(missing, ", "))
	}
}

func readDashboardAssets(t *testing.T, dir string) map[string]string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dashboards dir: %v", err)
	}
	out := make(map[string]string, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		rawBytes, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read dashboard %s: %v", path, err)
		}
		raw := string(rawBytes)
		var dashboard dashboardAsset
		if err := json.Unmarshal(rawBytes, &dashboard); err != nil {
			t.Fatalf("parse dashboard %s: %v", path, err)
		}
		if dashboard.UID == "" {
			t.Fatalf("dashboard %s missing uid", path)
		}
		out[dashboard.UID] = raw
	}
	return out
}

func readExportedMetricNames(t *testing.T, dir string) map[string]struct{} {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read metrics dir: %v", err)
	}
	out := make(map[string]struct{})
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		rawBytes, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read metrics file %s: %v", path, err)
		}
		matches := metricDefinitionPattern.FindAllStringSubmatch(string(rawBytes), -1)
		for _, match := range matches {
			if len(match) < 2 {
				continue
			}
			out[match[1]] = struct{}{}
		}
	}
	return out
}

func collectDashboardMetricNames(raw string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, token := range metricTokenPattern.FindAllString(raw, -1) {
		out[token] = struct{}{}
		normalized := normalizeDashboardMetricName(token)
		out[normalized] = struct{}{}
	}
	return out
}

func normalizeDashboardMetricName(metric string) string {
	for _, suffix := range []string{"_bucket", "_sum", "_count"} {
		if strings.HasSuffix(metric, suffix) {
			return strings.TrimSuffix(metric, suffix)
		}
	}
	return metric
}

func TestMessageDeliveryDashboardIncludesMessageEventPanels(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("dashboards", "wukongim-message-delivery.json"))
	if err != nil {
		t.Fatalf("read message delivery dashboard: %v", err)
	}

	type panel struct {
		ID      int    `json:"id"`
		Title   string `json:"title"`
		GridPos struct {
			H int `json:"h"`
			W int `json:"w"`
			X int `json:"x"`
			Y int `json:"y"`
		} `json:"gridPos"`
		FieldConfig struct {
			Defaults struct {
				Unit string `json:"unit"`
			} `json:"defaults"`
		} `json:"fieldConfig"`
		Targets []struct {
			Expr string `json:"expr"`
		} `json:"targets"`
	}
	var dashboard struct {
		Panels []panel `json:"panels"`
	}
	if err := json.Unmarshal(raw, &dashboard); err != nil {
		t.Fatalf("parse message delivery dashboard: %v", err)
	}

	want := map[string]struct {
		id      int
		x, y    int
		unit    string
		metrics []string
	}{
		"Message event append / propose rate": {
			id: 24, x: 0, y: 57, unit: "ops",
			metrics: []string{"wukongim_message_event_append_total", "wukongim_message_event_propose_total"},
		},
		"Message event append latency p95": {
			id: 25, x: 8, y: 57, unit: "s",
			metrics: []string{"wukongim_message_event_append_duration_seconds_bucket", "wukongim_message_event_append_stage_duration_seconds_bucket"},
		},
		"Message event proposal latency p95": {
			id: 26, x: 16, y: 57, unit: "s",
			metrics: []string{"wukongim_message_event_propose_duration_seconds_bucket", "wukongim_message_event_propose_stage_duration_seconds_bucket"},
		},
		"Message event proposal batch size p95": {
			id: 27, x: 0, y: 64, unit: "short",
			metrics: []string{"wukongim_message_event_propose_batch_events_bucket"},
		},
		"Message event stream cache pressure": {
			id: 28, x: 8, y: 64, unit: "short",
			metrics: []string{"wukongim_message_event_stream_cache_sessions", "wukongim_message_event_stream_cache_open_lanes", "wukongim_message_event_stream_cache_max_sessions"},
		},
		"Message event stream cache payload": {
			id: 29, x: 16, y: 64, unit: "bytes",
			metrics: []string{"wukongim_message_event_stream_cache_payload_bytes"},
		},
	}

	byTitle := make(map[string]panel, len(dashboard.Panels))
	for _, panel := range dashboard.Panels {
		byTitle[panel.Title] = panel
	}
	for title, expected := range want {
		got, ok := byTitle[title]
		if !ok {
			t.Errorf("message delivery dashboard missing panel %q", title)
			continue
		}
		if got.ID != expected.id || got.GridPos.X != expected.x || got.GridPos.Y != expected.y || got.GridPos.W != 8 || got.GridPos.H != 7 {
			t.Errorf("panel %q layout = id:%d x:%d y:%d w:%d h:%d", title, got.ID, got.GridPos.X, got.GridPos.Y, got.GridPos.W, got.GridPos.H)
		}
		if got.FieldConfig.Defaults.Unit != expected.unit {
			t.Errorf("panel %q unit = %q, want %q", title, got.FieldConfig.Defaults.Unit, expected.unit)
		}
		var expressions strings.Builder
		for _, target := range got.Targets {
			expressions.WriteString(target.Expr)
			expressions.WriteByte('\n')
		}
		for _, metric := range expected.metrics {
			if !strings.Contains(expressions.String(), metric) {
				t.Errorf("panel %q does not query %s", title, metric)
			}
		}
	}
}

func TestRuntimeOpsDashboardIncludesPresencePanels(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("dashboards", "wukongim-v2-runtime-ops.json"))
	if err != nil {
		t.Fatalf("read runtime ops dashboard: %v", err)
	}

	type panel struct {
		ID      int    `json:"id"`
		Title   string `json:"title"`
		GridPos struct {
			H int `json:"h"`
			W int `json:"w"`
			X int `json:"x"`
			Y int `json:"y"`
		} `json:"gridPos"`
		FieldConfig struct {
			Defaults struct {
				Unit string `json:"unit"`
			} `json:"defaults"`
		} `json:"fieldConfig"`
		Targets []struct {
			Expr string `json:"expr"`
			Hide bool   `json:"hide"`
		} `json:"targets"`
	}
	var dashboard struct {
		Panels []panel `json:"panels"`
	}
	if err := json.Unmarshal(raw, &dashboard); err != nil {
		t.Fatalf("parse runtime ops dashboard: %v", err)
	}

	want := []struct {
		title   string
		id      int
		x, y    int
		w, h    int
		metrics []string
	}{
		{
			title: "Presence expiry cost and index",
			id:    10, x: 12, y: 32, w: 12, h: 8,
			metrics: []string{
				"wukongim_presence_expiry_total",
				"wukongim_presence_expiry_duration_seconds_bucket",
				"wukongim_presence_expiry_due_buckets",
				"wukongim_presence_expiry_examined_routes",
				"wukongim_presence_expired_routes",
				"wukongim_presence_expiry_index_routes",
				"wukongim_presence_expiry_index_buckets",
			},
		},
		{
			title: "Presence touch work and budget",
			id:    11, x: 0, y: 40, w: 24, h: 8,
			metrics: []string{
				"wukongim_presence_touch_flush_total",
				"wukongim_presence_touch_flush_duration_seconds_bucket",
				"wukongim_presence_touch_flush_routes",
				"wukongim_presence_touch_flush_chunks",
				"wukongim_presence_touch_flush_target_groups",
			},
		},
	}

	byTitle := make(map[string]panel, len(dashboard.Panels))
	for _, panel := range dashboard.Panels {
		byTitle[panel.Title] = panel
	}
	findTarget := func(panel panel, metric string) (string, bool) {
		for _, target := range panel.Targets {
			if strings.Contains(target.Expr, metric) {
				return target.Expr, true
			}
		}
		return "", false
	}
	requireTarget := func(panel panel, metric string, tokens ...string) {
		t.Helper()
		expr, ok := findTarget(panel, metric)
		if !ok {
			t.Errorf("panel %q does not query %s", panel.Title, metric)
			return
		}
		for _, token := range tokens {
			if !strings.Contains(expr, token) {
				t.Errorf("panel %q query for %s does not contain %q: %s", panel.Title, metric, token, expr)
			}
		}
	}

	for _, expected := range want {
		got, ok := byTitle[expected.title]
		if !ok {
			t.Errorf("runtime ops dashboard missing panel %q", expected.title)
			continue
		}
		if got.ID != expected.id || got.GridPos.X != expected.x || got.GridPos.Y != expected.y || got.GridPos.W != expected.w || got.GridPos.H != expected.h {
			t.Errorf("panel %q layout = id:%d x:%d y:%d w:%d h:%d", expected.title, got.ID, got.GridPos.X, got.GridPos.Y, got.GridPos.W, got.GridPos.H)
		}
		if got.FieldConfig.Defaults.Unit != "mixed" && got.FieldConfig.Defaults.Unit != "short" {
			t.Errorf("panel %q unit = %q, want mixed or short", expected.title, got.FieldConfig.Defaults.Unit)
		}
		if len(got.Targets) != len(expected.metrics) {
			t.Errorf("panel %q targets = %d, want %d operational queries", expected.title, len(got.Targets), len(expected.metrics))
		}
		for i, target := range got.Targets {
			if target.Hide {
				t.Errorf("panel %q target %d is hidden", expected.title, i)
			}
			if !strings.Contains(target.Expr, `node_name=~"$node_name"`) {
				t.Errorf("panel %q target %d does not apply node filter: %s", expected.title, i, target.Expr)
			}
		}
		for _, metric := range expected.metrics {
			requireTarget(got, metric)
		}

		switch expected.id {
		case 10:
			requireTarget(got, "wukongim_presence_expiry_total", "rate(", "$__rate_interval", "result")
			requireTarget(got, "wukongim_presence_expiry_duration_seconds_bucket", "histogram_quantile(0.95", "rate(", "$__rate_interval", "le", "result")
		case 11:
			requireTarget(got, "wukongim_presence_touch_flush_total", "rate(", "$__rate_interval", "result", "budget_reached")
			requireTarget(got, "wukongim_presence_touch_flush_duration_seconds_bucket", "histogram_quantile(0.95", "rate(", "$__rate_interval", "le", "result", "budget_reached")
			requireTarget(got, "wukongim_presence_touch_flush_routes", "rate(", "$__rate_interval", "stage")
			requireTarget(got, "wukongim_presence_touch_flush_chunks", "rate(", "$__rate_interval")
			requireTarget(got, "wukongim_presence_touch_flush_target_groups", "rate(", "$__rate_interval")
		}
	}
}

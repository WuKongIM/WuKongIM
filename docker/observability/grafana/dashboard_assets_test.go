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

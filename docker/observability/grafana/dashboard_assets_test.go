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

	type target struct {
		Expr         string `json:"expr"`
		Hide         bool   `json:"hide"`
		LegendFormat string `json:"legendFormat"`
		RefID        string `json:"refId"`
	}
	type fieldOverride struct {
		Matcher struct {
			ID      string `json:"id"`
			Options string `json:"options"`
		} `json:"matcher"`
		Properties []struct {
			ID    string `json:"id"`
			Value string `json:"value"`
		} `json:"properties"`
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
			Overrides []fieldOverride `json:"overrides"`
		} `json:"fieldConfig"`
		Targets []target `json:"targets"`
	}
	var dashboard struct {
		UID        string  `json:"uid"`
		Panels     []panel `json:"panels"`
		Templating struct {
			List []struct {
				Definition string `json:"definition"`
				IncludeAll bool   `json:"includeAll"`
				Multi      bool   `json:"multi"`
				Name       string `json:"name"`
				Query      string `json:"query"`
			} `json:"list"`
		} `json:"templating"`
	}
	if err := json.Unmarshal(raw, &dashboard); err != nil {
		t.Fatalf("parse runtime ops dashboard: %v", err)
	}
	if dashboard.UID != "wukongim-v2-runtime-ops" {
		t.Errorf("runtime ops dashboard uid = %q, want wukongim-v2-runtime-ops", dashboard.UID)
	}

	const nodeVariableQuery = "label_values(wukongim_node_goroutines, node_name)"
	nodeVariableCount := 0
	for _, variable := range dashboard.Templating.List {
		if variable.Name != "node_name" {
			continue
		}
		nodeVariableCount++
		if variable.Query != nodeVariableQuery || variable.Definition != nodeVariableQuery || !variable.IncludeAll || !variable.Multi {
			t.Errorf("node_name variable = query:%q definition:%q includeAll:%t multi:%t", variable.Query, variable.Definition, variable.IncludeAll, variable.Multi)
		}
	}
	if nodeVariableCount != 1 {
		t.Errorf("runtime ops dashboard node_name variables = %d, want 1", nodeVariableCount)
	}

	baselinePanels := []struct {
		id    int
		title string
		x, y  int
		w, h  int
	}{
		{id: 1, title: "Node Resource Pressure", x: 0, y: 0, w: 12, h: 8},
		{id: 2, title: "Slot And Controller Gaps", x: 12, y: 0, w: 12, h: 8},
		{id: 3, title: "Channel Append Router And Admission", x: 0, y: 8, w: 12, h: 8},
		{id: 4, title: "Channel Append Writer And Effect Pool", x: 12, y: 8, w: 12, h: 8},
		{id: 5, title: "Channel Append Effect Results", x: 0, y: 16, w: 12, h: 8},
		{id: 6, title: "Conversation Active Cache And Flush", x: 12, y: 16, w: 12, h: 8},
		{id: 7, title: "Conversation Sync", x: 0, y: 24, w: 12, h: 8},
		{id: 8, title: "Recipient Worker", x: 12, y: 24, w: 12, h: 8},
		{id: 9, title: "Ants Pool Usage", x: 0, y: 32, w: 12, h: 8},
	}
	byID := make(map[int]panel, len(dashboard.Panels))
	for _, panel := range dashboard.Panels {
		if _, exists := byID[panel.ID]; exists {
			t.Errorf("runtime ops dashboard has duplicate panel id %d", panel.ID)
		}
		byID[panel.ID] = panel
	}
	for _, expected := range baselinePanels {
		got, ok := byID[expected.id]
		if !ok {
			t.Errorf("runtime ops dashboard missing baseline panel id %d", expected.id)
			continue
		}
		if got.Title != expected.title || got.GridPos.X != expected.x || got.GridPos.Y != expected.y || got.GridPos.W != expected.w || got.GridPos.H != expected.h {
			t.Errorf("baseline panel %d = title:%q x:%d y:%d w:%d h:%d", expected.id, got.Title, got.GridPos.X, got.GridPos.Y, got.GridPos.W, got.GridPos.H)
		}
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
	findTarget := func(panel panel, metric string) (target, bool) {
		for _, candidate := range panel.Targets {
			if strings.Contains(candidate.Expr, metric) {
				return candidate, true
			}
		}
		return target{}, false
	}
	requireTarget := func(panel panel, metric string, tokens ...string) target {
		t.Helper()
		target, ok := findTarget(panel, metric)
		if !ok {
			t.Errorf("panel %q does not query %s", panel.Title, metric)
			return target
		}
		for _, token := range tokens {
			if !strings.Contains(target.Expr, token) {
				t.Errorf("panel %q query for %s does not contain %q: %s", panel.Title, metric, token, target.Expr)
			}
		}
		return target
	}
	requireUnitOverride := func(panel panel, refID, wantUnit string) {
		t.Helper()
		matches := 0
		for _, override := range panel.FieldConfig.Overrides {
			if override.Matcher.ID != "byFrameRefID" || override.Matcher.Options != refID {
				continue
			}
			for _, property := range override.Properties {
				if property.ID != "unit" {
					continue
				}
				matches++
				if property.Value != wantUnit {
					t.Errorf("panel %q refId %s unit override = %q, want %q", panel.Title, refID, property.Value, wantUnit)
				}
			}
		}
		if matches != 1 {
			t.Errorf("panel %q refId %s unit overrides = %d, want 1", panel.Title, refID, matches)
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
		if len(got.Targets) != len(expected.metrics) {
			t.Errorf("panel %q targets = %d, want %d operational queries", expected.title, len(got.Targets), len(expected.metrics))
		}
		refIDs := make(map[string]struct{}, len(got.Targets))
		legends := make(map[string]struct{}, len(got.Targets))
		for i, target := range got.Targets {
			if target.Hide {
				t.Errorf("panel %q target %d is hidden", expected.title, i)
			}
			if strings.TrimSpace(target.RefID) == "" {
				t.Errorf("panel %q target %d has empty refId", expected.title, i)
			} else if _, exists := refIDs[target.RefID]; exists {
				t.Errorf("panel %q has duplicate refId %q", expected.title, target.RefID)
			}
			refIDs[target.RefID] = struct{}{}
			if strings.TrimSpace(target.LegendFormat) == "" {
				t.Errorf("panel %q target %d has empty legendFormat", expected.title, i)
			} else if _, exists := legends[target.LegendFormat]; exists {
				t.Errorf("panel %q has duplicate legendFormat %q", expected.title, target.LegendFormat)
			}
			legends[target.LegendFormat] = struct{}{}
			if !strings.Contains(target.Expr, `node_name=~"$node_name"`) {
				t.Errorf("panel %q target %d does not apply node filter: %s", expected.title, i, target.Expr)
			}
		}
		for _, metric := range expected.metrics {
			requireTarget(got, metric)
		}

		switch expected.id {
		case 10:
			if got.FieldConfig.Defaults.Unit != "short" {
				t.Errorf("panel %q default unit = %q, want short", got.Title, got.FieldConfig.Defaults.Unit)
			}
			if len(got.FieldConfig.Overrides) != 2 {
				t.Errorf("panel %q unit overrides = %d, want 2", got.Title, len(got.FieldConfig.Overrides))
			}
			expiryTotal := requireTarget(got, "wukongim_presence_expiry_total", "sum(rate(", "$__rate_interval", "by (node_name, result)")
			if expiryTotal.RefID != "A" {
				t.Errorf("panel %q expiry total refId = %q, want A", got.Title, expiryTotal.RefID)
			}
			expiryDuration := requireTarget(got, "wukongim_presence_expiry_duration_seconds_bucket", "histogram_quantile(0.95, sum(rate(", "$__rate_interval", "by (le, node_name, result)")
			if expiryDuration.RefID != "B" {
				t.Errorf("panel %q expiry duration refId = %q, want B", got.Title, expiryDuration.RefID)
			}
			requireUnitOverride(got, "A", "ops")
			requireUnitOverride(got, "B", "s")
			gauges := []struct {
				metric string
				expr   string
			}{
				{metric: "wukongim_presence_expiry_due_buckets", expr: `max(wukongim_presence_expiry_due_buckets{node_name=~"$node_name"}) by (node_name)`},
				{metric: "wukongim_presence_expiry_examined_routes", expr: `max(wukongim_presence_expiry_examined_routes{node_name=~"$node_name"}) by (node_name)`},
				{metric: "wukongim_presence_expired_routes", expr: `max(wukongim_presence_expired_routes{node_name=~"$node_name"}) by (node_name)`},
				{metric: "wukongim_presence_expiry_index_routes", expr: `max(wukongim_presence_expiry_index_routes{node_name=~"$node_name"}) by (node_name)`},
				{metric: "wukongim_presence_expiry_index_buckets", expr: `max(wukongim_presence_expiry_index_buckets{node_name=~"$node_name"}) by (node_name)`},
			}
			for _, gauge := range gauges {
				target := requireTarget(got, gauge.metric)
				if target.Expr != gauge.expr {
					t.Errorf("panel %q query for %s = %q, want %q", got.Title, gauge.metric, target.Expr, gauge.expr)
				}
				if strings.Contains(target.Expr, "rate(") {
					t.Errorf("panel %q gauge query for %s must not use rate: %s", got.Title, gauge.metric, target.Expr)
				}
			}
		case 11:
			if got.FieldConfig.Defaults.Unit != "ops" {
				t.Errorf("panel %q default unit = %q, want ops", got.Title, got.FieldConfig.Defaults.Unit)
			}
			if len(got.FieldConfig.Overrides) != 1 {
				t.Errorf("panel %q unit overrides = %d, want 1", got.Title, len(got.FieldConfig.Overrides))
			}
			touchTotal := requireTarget(got, "wukongim_presence_touch_flush_total", "sum(rate(", "$__rate_interval", "by (node_name, result, budget_reached)")
			if touchTotal.RefID != "A" {
				t.Errorf("panel %q touch total refId = %q, want A", got.Title, touchTotal.RefID)
			}
			touchDuration := requireTarget(got, "wukongim_presence_touch_flush_duration_seconds_bucket", "histogram_quantile(0.95, sum(rate(", "$__rate_interval", "by (le, node_name, result, budget_reached)")
			if touchDuration.RefID != "B" {
				t.Errorf("panel %q touch duration refId = %q, want B", got.Title, touchDuration.RefID)
			}
			requireUnitOverride(got, "B", "s")
			requireTarget(got, "wukongim_presence_touch_flush_routes", "sum(rate(", "$__rate_interval", "by (node_name, stage)")
			requireTarget(got, "wukongim_presence_touch_flush_chunks", "sum(rate(", "$__rate_interval", "by (node_name)")
			requireTarget(got, "wukongim_presence_touch_flush_target_groups", "sum(rate(", "$__rate_interval", "by (node_name)")
		}
	}
}

func TestRuntimeOpsConversationFlushHistogramsPreserveNodeAndKind(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("dashboards", "wukongim-v2-runtime-ops.json"))
	if err != nil {
		t.Fatalf("read runtime ops dashboard: %v", err)
	}
	type target struct {
		Expr         string `json:"expr"`
		LegendFormat string `json:"legendFormat"`
	}
	var dashboard struct {
		Panels []struct {
			Title   string   `json:"title"`
			Targets []target `json:"targets"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(raw, &dashboard); err != nil {
		t.Fatalf("parse runtime ops dashboard: %v", err)
	}
	var targets []target
	for _, panel := range dashboard.Panels {
		if panel.Title == "Conversation Active Cache And Flush" {
			targets = panel.Targets
			break
		}
	}
	if len(targets) == 0 {
		t.Fatal("runtime ops dashboard is missing the conversation-active panel")
	}
	assertTarget := func(metric string, exprTokens []string, legendTokens []string) {
		t.Helper()
		for _, candidate := range targets {
			if !strings.Contains(candidate.Expr, metric) {
				continue
			}
			for _, token := range exprTokens {
				if !strings.Contains(candidate.Expr, token) {
					t.Errorf("%s query does not preserve %q: %s", metric, token, candidate.Expr)
				}
			}
			for _, token := range legendTokens {
				if !strings.Contains(candidate.LegendFormat, token) {
					t.Errorf("%s legend does not preserve %q: %s", metric, token, candidate.LegendFormat)
				}
			}
			return
		}
		t.Errorf("conversation-active panel does not query %s", metric)
	}
	assertTarget(
		"wukongim_conversation_active_flush_duration_seconds_bucket",
		[]string{"by (le, node_name, result)"},
		[]string{"{{node_name}}", "{{result}}"},
	)
	assertTarget(
		"wukongim_conversation_active_flush_rows_bucket",
		[]string{"by (le, node_name, result, kind)"},
		[]string{"{{node_name}}", "{{result}}", "{{kind}}"},
	)
}

func TestRuntimeOpsDashboardIncludesChannelRegistryPanels(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("dashboards", "wukongim-v2-runtime-ops.json"))
	if err != nil {
		t.Fatalf("read runtime ops dashboard: %v", err)
	}

	type target struct {
		Expr         string `json:"expr"`
		LegendFormat string `json:"legendFormat"`
		RefID        string `json:"refId"`
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
		Targets []target `json:"targets"`
	}
	var dashboard struct {
		Panels []panel `json:"panels"`
	}
	if err := json.Unmarshal(raw, &dashboard); err != nil {
		t.Fatalf("parse runtime ops dashboard: %v", err)
	}

	byTitle := make(map[string]panel, len(dashboard.Panels))
	for _, item := range dashboard.Panels {
		byTitle[item.Title] = item
	}
	want := []struct {
		title   string
		id      int
		x       int
		unit    string
		metrics []string
	}{
		{
			title: "Channel registry ownership", id: 12, x: 0, unit: "short",
			metrics: []string{
				"wukongim_storage_channel_entries_active",
				"wukongim_storage_channel_leases_outstanding",
				"wukongim_storage_channel_background_pins",
			},
		},
		{
			title: "Channel registry lifecycle rate", id: 13, x: 12, unit: "ops",
			metrics: []string{
				"wukongim_storage_channel_acquires_total",
				"wukongim_storage_channel_releases_total",
				"wukongim_storage_channel_reclaims_total",
			},
		},
	}
	for _, expected := range want {
		got, ok := byTitle[expected.title]
		if !ok {
			t.Errorf("runtime ops dashboard missing panel %q", expected.title)
			continue
		}
		if got.ID != expected.id || got.GridPos.X != expected.x || got.GridPos.Y != 48 || got.GridPos.W != 12 || got.GridPos.H != 8 {
			t.Errorf("panel %q layout = id:%d x:%d y:%d w:%d h:%d", expected.title, got.ID, got.GridPos.X, got.GridPos.Y, got.GridPos.W, got.GridPos.H)
		}
		if got.FieldConfig.Defaults.Unit != expected.unit {
			t.Errorf("panel %q unit = %q, want %q", expected.title, got.FieldConfig.Defaults.Unit, expected.unit)
		}
		if len(got.Targets) != len(expected.metrics) {
			t.Errorf("panel %q targets = %d, want %d", expected.title, len(got.Targets), len(expected.metrics))
		}
		for _, metric := range expected.metrics {
			found := false
			for _, candidate := range got.Targets {
				if !strings.Contains(candidate.Expr, metric) {
					continue
				}
				found = true
				if !strings.Contains(candidate.Expr, `node_name=~"$node_name"`) || !strings.Contains(candidate.Expr, `store="channel_log"`) {
					t.Errorf("panel %q query for %s lacks fixed node/store scope: %s", expected.title, metric, candidate.Expr)
				}
				if strings.Contains(candidate.Expr, "channel_id") {
					t.Errorf("panel %q query for %s contains forbidden channel_id label: %s", expected.title, metric, candidate.Expr)
				}
				if strings.TrimSpace(candidate.RefID) == "" || strings.TrimSpace(candidate.LegendFormat) == "" {
					t.Errorf("panel %q query for %s lacks refId or legend", expected.title, metric)
				}
				if expected.id == 13 && (!strings.Contains(candidate.Expr, "rate(") || !strings.Contains(candidate.Expr, "$__rate_interval")) {
					t.Errorf("panel %q counter query for %s is not a rate: %s", expected.title, metric, candidate.Expr)
				}
			}
			if !found {
				t.Errorf("panel %q does not query %s", expected.title, metric)
			}
		}
	}
}

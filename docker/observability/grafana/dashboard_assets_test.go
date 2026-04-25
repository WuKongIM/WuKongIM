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

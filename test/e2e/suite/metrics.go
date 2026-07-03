//go:build e2e

package suite

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// RequireMetricAtLeastEventually waits for one public /metrics sample to reach at least want.
func RequireMetricAtLeastEventually(t *testing.T, node StartedNode, name string, labels map[string]string, want float64) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var last float64
	var lastErr error
	for {
		last, lastErr = FetchMetricValue(ctx, node.APIAddr(), name, labels)
		if lastErr == nil && last >= want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("metric %s%v = %v err=%v, want >= %v\n%s", name, labels, last, lastErr, want, node.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

// FetchMetricValue returns the first matching Prometheus text sample value.
func FetchMetricValue(ctx context.Context, apiAddr, name string, labels map[string]string) (float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+apiAddr+"/metrics", nil)
	if err != nil {
		return 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		metricName, metricLabels, value, ok := parseMetricSample(line)
		if !ok || metricName != name || !metricLabelsMatch(metricLabels, labels) {
			continue
		}
		return value, nil
	}
	return 0, fmt.Errorf("metric sample not found")
}

func parseMetricSample(line string) (string, map[string]string, float64, bool) {
	parts := strings.Fields(line)
	if len(parts) != 2 {
		return "", nil, 0, false
	}
	value, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return "", nil, 0, false
	}
	nameAndLabels := parts[0]
	labels := map[string]string{}
	if idx := strings.IndexByte(nameAndLabels, '{'); idx >= 0 {
		if !strings.HasSuffix(nameAndLabels, "}") {
			return "", nil, 0, false
		}
		name := nameAndLabels[:idx]
		labelBody := strings.TrimSuffix(nameAndLabels[idx+1:], "}")
		for _, raw := range strings.Split(labelBody, ",") {
			if raw == "" {
				continue
			}
			kv := strings.SplitN(raw, "=", 2)
			if len(kv) != 2 {
				return "", nil, 0, false
			}
			labels[kv[0]] = strings.Trim(kv[1], `"`)
		}
		return name, labels, value, true
	}
	return nameAndLabels, labels, value, true
}

func metricLabelsMatch(got, want map[string]string) bool {
	for key, value := range want {
		if got[key] != value {
			return false
		}
	}
	return true
}

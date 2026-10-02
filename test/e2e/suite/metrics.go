//go:build e2e

package suite

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/common/model"
)

// MetricSample is one parsed public Prometheus text sample.
type MetricSample struct {
	// Name is the exact metric family name.
	Name string
	// Labels contains the sample's low-cardinality label set.
	Labels map[string]string
	// Value is the parsed counter, gauge, or histogram bucket value.
	Value float64
}

// MetricHistogramSnapshot contains the cumulative count and sum of one histogram.
type MetricHistogramSnapshot struct {
	Count float64
	Sum   float64
}

// MetricScrapeReceipt identifies one public metrics observation without keeping
// its body or metric labels. A failed read's body hash describes only its prefix.
type MetricScrapeReceipt struct {
	// StatusCode is absent until an HTTP response is received.
	StatusCode int `json:"status_code,omitempty"`
	// RequestedEncoding records this observer's explicit identity negotiation.
	RequestedEncoding string `json:"requested_encoding"`
	// ReceivedEncoding is identity for a plain response, and absent before one.
	ReceivedEncoding string `json:"received_encoding,omitempty"`
	// BodyBytes is unknown before a logical body read and may describe a prefix.
	BodyBytes *int64 `json:"body_bytes,omitempty"`
	// BodySHA256 binds the same logical bytes returned by that read.
	BodySHA256 string `json:"body_sha256,omitempty"`
	// StartedAt and FinishedAt bound the entire observation in UTC.
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	// DurationNS uses the monotonic clock across the entire observation.
	DurationNS int64 `json:"duration_ns"`
}

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
	samples, err := FetchMetricSamples(ctx, apiAddr)
	if err != nil {
		return 0, err
	}
	for _, sample := range samples {
		if sample.Name == name && metricLabelsMatch(sample.Labels, labels) {
			return sample.Value, nil
		}
	}
	return 0, fmt.Errorf("metric sample not found")
}

// FetchMetricSamples reads one complete, fresh public /metrics snapshot without
// asking the observed node to compress it. Failed snapshots return no samples.
func FetchMetricSamples(ctx context.Context, apiAddr string) ([]MetricSample, error) {
	samples, _, err := FetchMetricSamplesWithReceipt(ctx, apiAddr)
	return samples, err
}

// FetchMetricSamplesWithReceipt binds samples and safe metadata to the same
// full HTTP request. On failure, available metadata remains but samples are nil.
func FetchMetricSamplesWithReceipt(ctx context.Context, apiAddr string) (samples []MetricSample, receipt MetricScrapeReceipt, err error) {
	started := time.Now()
	receipt.StartedAt = started.UTC()
	receipt.RequestedEncoding = "identity"
	defer func() {
		finished := time.Now()
		receipt.FinishedAt = finished.UTC()
		receipt.DurationNS = finished.Sub(started).Nanoseconds()
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+apiAddr+"/metrics", nil)
	if err != nil {
		return nil, receipt, err
	}
	req.Header.Set("Accept-Encoding", "identity")
	// Reuse the caller's transport and pool, but never turn one observation
	// into another request through an HTTP redirect.
	client := *http.DefaultClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, receipt, err
	}
	defer func() { _ = resp.Body.Close() }()
	receipt.StatusCode = resp.StatusCode
	receipt.ReceivedEncoding = strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding")))
	if receipt.ReceivedEncoding == "" {
		receipt.ReceivedEncoding = "identity"
	}
	if receipt.ReceivedEncoding != "identity" {
		return nil, receipt, fmt.Errorf("metrics response is not identity encoded")
	}
	body, err := io.ReadAll(resp.Body)
	bodyBytes := int64(len(body))
	receipt.BodyBytes = &bodyBytes
	receipt.BodySHA256 = fmt.Sprintf("%x", sha256.Sum256(body))
	if err != nil {
		return nil, receipt, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, receipt, fmt.Errorf("metrics HTTP status %d", resp.StatusCode)
	}
	samples = make([]MetricSample, 0, 64)
	for lineNumber, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		metricName, metricLabels, value, ok := parseMetricSample(line)
		if !ok || !model.IsValidMetricName(model.LabelValue(metricName)) {
			return nil, receipt, fmt.Errorf("invalid metrics sample at line %d", lineNumber+1)
		}
		samples = append(samples, MetricSample{
			Name:   metricName,
			Labels: metricLabels,
			Value:  value,
		})
	}
	if len(samples) == 0 {
		return nil, receipt, fmt.Errorf("metrics snapshot contains no samples")
	}
	return samples, receipt, nil
}

// SumMetricSamples returns the sum of one metric family matching the requested label subset.
func SumMetricSamples(samples []MetricSample, name string, labels map[string]string) float64 {
	total := float64(0)
	for _, sample := range samples {
		if sample.Name == name && metricLabelsMatch(sample.Labels, labels) {
			total += sample.Value
		}
	}
	return total
}

// HistogramSnapshot extracts one histogram's cumulative count and sum.
func HistogramSnapshot(samples []MetricSample, name string, labels map[string]string) MetricHistogramSnapshot {
	return MetricHistogramSnapshot{
		Count: SumMetricSamples(samples, name+"_count", labels),
		Sum:   SumMetricSamples(samples, name+"_sum", labels),
	}
}

func parseMetricSample(line string) (string, map[string]string, float64, bool) {
	line = strings.TrimSpace(line)
	separator := metricSampleValueSeparator(line)
	if separator <= 0 || separator == len(line)-1 {
		return "", nil, 0, false
	}
	nameAndLabels := strings.TrimSpace(line[:separator])
	valueFields := strings.Fields(line[separator+1:])
	if nameAndLabels == "" || len(valueFields) == 0 {
		return "", nil, 0, false
	}
	value, err := strconv.ParseFloat(valueFields[0], 64)
	if err != nil {
		return "", nil, 0, false
	}
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

func metricSampleValueSeparator(line string) int {
	braceDepth := 0
	inQuotes := false
	escaped := false
	for index := 0; index < len(line); index++ {
		character := line[index]
		if escaped {
			escaped = false
			continue
		}
		if inQuotes && character == '\\' {
			escaped = true
			continue
		}
		if character == '"' {
			inQuotes = !inQuotes
			continue
		}
		if inQuotes {
			continue
		}
		switch character {
		case '{':
			braceDepth++
		case '}':
			if braceDepth > 0 {
				braceDepth--
			}
		case ' ', '\t':
			if braceDepth == 0 {
				return index
			}
		}
	}
	return -1
}

func metricLabelsMatch(got, want map[string]string) bool {
	for key, value := range want {
		if got[key] != value {
			return false
		}
	}
	return true
}

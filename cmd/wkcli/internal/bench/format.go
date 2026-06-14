package bench

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

func formatHumanSummary(result sendResult) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "Finished %s\n\n", formatDuration(result.Duration))
	fmt.Fprintln(&b, "WKProto SEND summary")
	printKV(&b, "result:", upperResult(result.Result))
	printKV(&b, "success:", formatInt64(int64(result.Success)))
	printKV(&b, "errors:", formatInt64(int64(result.Errors)))
	printKV(&b, "throughput:", fmt.Sprintf("%.2f msgs/sec", result.Throughput))
	printKV(&b, "bandwidth:", fmt.Sprintf("%.2f MiB/sec", result.BandwidthBytes/(1024*1024)))
	printKV(&b, "sendack avg:", formatDuration(result.Latency.Average))
	printKV(&b, "sendack p50:", formatDuration(result.Latency.P50))
	printKV(&b, "sendack p95:", formatDuration(result.Latency.P95))
	printKV(&b, "sendack p99:", formatDuration(result.Latency.P99))
	printKV(&b, "max:", formatDuration(result.Latency.Max))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "Traffic")
	printKV(&b, "clients:", formatInt(result.Clients))
	printKV(&b, "gateways:", formatInt(result.Gateways))
	printKV(&b, "channels:", formatInt(result.Channels))
	printKV(&b, "channel_pick:", result.ChannelPick)
	printKV(&b, "msgs/channel:", fmt.Sprintf("min=%s avg=%.0f max=%s", formatInt(result.MinMessagesPerChannel), result.AvgMessagesPerChannel, formatInt(result.MaxMessagesPerChannel)))
	printKV(&b, "batch:", formatInt(result.BatchSize))
	if len(result.ErrorCounts) > 0 {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "Errors")
		printSortedCounts(&b, result.ErrorCounts)
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "SENDACK reasons")
	printSortedCounts(&b, result.SendackReasons)
	return b.String()
}

func formatJSONSummary(result sendResult) (string, error) {
	doc := map[string]any{
		"result":                  result.Result,
		"duration_ms":             result.Duration.Milliseconds(),
		"success":                 result.Success,
		"errors":                  result.Errors,
		"throughput_msgs_per_sec": result.Throughput,
		"bandwidth_bytes_per_sec": result.BandwidthBytes,
		"sendack_latency_ms": map[string]float64{
			"avg": durationMillis(result.Latency.Average),
			"p50": durationMillis(result.Latency.P50),
			"p95": durationMillis(result.Latency.P95),
			"p99": durationMillis(result.Latency.P99),
			"max": durationMillis(result.Latency.Max),
		},
		"traffic": map[string]any{
			"clients":       result.Clients,
			"gateways":      result.Gateways,
			"channels":      result.Channels,
			"channel_pick":  result.ChannelPick,
			"messages":      result.Messages,
			"payload_bytes": result.PayloadBytes,
			"batch":         result.BatchSize,
		},
		"sendack_reasons": result.SendackReasons,
	}
	if len(result.ErrorCounts) > 0 {
		doc["error_counts"] = result.ErrorCounts
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data) + "\n", nil
}

func formatCSVSummary(result sendResult) (string, error) {
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	header := []string{
		"result", "duration_ms", "clients", "gateways", "channels", "msgs",
		"success", "errors", "throughput_msgs_per_sec", "bandwidth_bytes_per_sec",
		"sendack_avg_ms", "sendack_p50_ms", "sendack_p95_ms", "sendack_p99_ms", "sendack_max_ms",
	}
	row := []string{
		result.Result,
		strconv.FormatInt(result.Duration.Milliseconds(), 10),
		strconv.Itoa(result.Clients),
		strconv.Itoa(result.Gateways),
		strconv.Itoa(result.Channels),
		strconv.Itoa(result.Messages),
		strconv.FormatUint(result.Success, 10),
		strconv.FormatUint(result.Errors, 10),
		fmt.Sprintf("%.2f", result.Throughput),
		fmt.Sprintf("%.0f", result.BandwidthBytes),
		fmt.Sprintf("%.2f", durationMillis(result.Latency.Average)),
		fmt.Sprintf("%.2f", durationMillis(result.Latency.P50)),
		fmt.Sprintf("%.2f", durationMillis(result.Latency.P95)),
		fmt.Sprintf("%.2f", durationMillis(result.Latency.P99)),
		fmt.Sprintf("%.2f", durationMillis(result.Latency.Max)),
	}
	if err := w.Write(header); err != nil {
		return "", err
	}
	if err := w.Write(row); err != nil {
		return "", err
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return "", err
	}
	return b.String(), nil
}

func formatProgressLine(cfg sendConfig, snapshot progressSnapshot) string {
	percent := clampPercent(snapshot.Percent)
	return fmt.Sprintf(
		"Sending %s %5.1f%% | %s/%s msgs | %s msg/s | %.2f MiB/s | p99=%s | err=%s",
		formatProgressBar(percent, 24),
		percent,
		formatInt64(int64(snapshot.Done)),
		formatInt(cfg.Messages),
		formatInt64(int64(snapshot.Throughput+0.5)),
		snapshot.BandwidthBytes/(1024*1024),
		formatDuration(snapshot.P99),
		formatInt64(int64(snapshot.Errors)),
	)
}

func formatProgressBar(percent float64, width int) string {
	if width < 1 {
		width = 1
	}
	percent = clampPercent(percent)
	if percent >= 100 {
		return "[" + strings.Repeat("=", width) + "]"
	}
	filled := int(percent / 100 * float64(width))
	if filled >= width {
		filled = width - 1
	}
	if filled < 0 {
		filled = 0
	}
	return "[" + strings.Repeat("=", filled) + ">" + strings.Repeat("-", width-filled-1) + "]"
}

func clampPercent(percent float64) float64 {
	if percent < 0 {
		return 0
	}
	if percent > 100 {
		return 100
	}
	return percent
}

func printKV(b *bytes.Buffer, label, value string) {
	fmt.Fprintf(b, "  %-14s %s\n", label, value)
}

func printSortedCounts(b *bytes.Buffer, counts map[string]uint64) {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		printKV(b, key+":", formatInt64(int64(counts[key])))
	}
}

func upperResult(result string) string {
	if result == resultPass {
		return "PASS"
	}
	return "FAIL"
}

func formatDuration(d time.Duration) string {
	if d == 0 {
		return "0s"
	}
	return d.String()
}

func durationMillis(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}

func formatInt(v int) string {
	return formatInt64(int64(v))
}

func formatInt64(v int64) string {
	s := strconv.FormatInt(v, 10)
	n := len(s)
	if n <= 3 {
		return s
	}
	rem := n % 3
	if rem == 0 {
		rem = 3
	}
	var b bytes.Buffer
	b.WriteString(s[:rem])
	for i := rem; i < n; i += 3 {
		b.WriteByte(',')
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

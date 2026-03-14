package main

import (
	"encoding/json"
	"fmt"
	"time"
)

// printBenchReport prints the benchmark results in the configured format.
func printBenchReport(cfg *benchConfig, m *benchMetrics) {
	if cfg.Output == "json" {
		printBenchJSON(cfg, m)
		return
	}
	printBenchText(cfg, m)
}

// printBenchText prints a human-readable ASCII report.
func printBenchText(cfg *benchConfig, m *benchMetrics) {
	line := "========================================"
	sep := fmt.Sprintf("  %s──────────────────────────────────────%s", colorGray, colorReset)

	fmt.Println()
	fmt.Println(line)
	fmt.Printf("  %sWuKongIM Benchmark Results%s\n", colorBold, colorReset)
	fmt.Println(line)
	fmt.Println()

	// Throughput section.
	fmt.Printf("  %sThroughput%s\n", colorBold, colorReset)
	fmt.Println(sep)
	printTable(
		[]string{"Direction", "Messages", "Rate", "MB/s"},
		[][]string{
			{"Send", commaFmt(m.totalSent.Load()), fmt.Sprintf("%.1f/s", m.sendRate), fmt.Sprintf("%.2f", m.sendMBps)},
			{"Receive", commaFmt(m.totalRecv.Load()), fmt.Sprintf("%.1f/s", m.recvRate), fmt.Sprintf("%.2f", m.recvMBps)},
		},
	)
	fmt.Println()

	// Latency section.
	if m.latCount > 0 {
		fmt.Printf("  %sLatency%s (%s samples)\n", colorBold, colorReset, commaFmt(int64(m.latCount)))
		fmt.Println(sep)
		fmt.Printf("  %-16s %s\n", "Min", fmtLatency(m.latMin))
		fmt.Printf("  %-16s %s\n", "Avg", fmtLatency(m.latAvg))
		fmt.Printf("  %-16s %s\n", "P50", fmtLatency(m.latP50))
		fmt.Printf("  %-16s %s\n", "P95", fmtLatency(m.latP95))
		fmt.Printf("  %-16s %s\n", "P99", fmtLatency(m.latP99))
		fmt.Printf("  %-16s %s\n", "Max", fmtLatency(m.latMax))
		fmt.Println()
	}

	// Summary section.
	fmt.Printf("  %sSummary%s\n", colorBold, colorReset)
	fmt.Println(sep)
	fmt.Printf("  %-16s %d\n", "Pairs", cfg.Pairs)
	fmt.Printf("  %-16s %s\n", "Mode", cfg.mode())
	fmt.Printf("  %-16s %.2fs\n", "Duration", m.elapsed.Seconds())
	fmt.Printf("  %-16s %s\n", "Send Errors", commaFmt(m.totalSendErr.Load()))
	fmt.Printf("  %-16s %.2f%%\n", "Loss Rate", m.lossRate)
	fmt.Println(line)
	fmt.Println()
}

// benchJSONReport is the JSON output structure.
type benchJSONReport struct {
	Mode       string  `json:"mode"`
	Pairs      int     `json:"pairs"`
	PayloadLen int     `json:"payload_bytes"`
	DurationS  float64 `json:"duration_s"`

	Sent     int64   `json:"sent"`
	Received int64   `json:"received"`
	SendErr  int64   `json:"send_errors"`
	SendRate float64 `json:"send_rate"`
	RecvRate float64 `json:"recv_rate"`
	SendMBps float64 `json:"send_mbps"`
	RecvMBps float64 `json:"recv_mbps"`
	LossRate float64 `json:"loss_rate_pct"`

	LatSamples int     `json:"lat_samples"`
	LatMinMs   float64 `json:"lat_min_ms"`
	LatAvgMs   float64 `json:"lat_avg_ms"`
	LatP50Ms   float64 `json:"lat_p50_ms"`
	LatP95Ms   float64 `json:"lat_p95_ms"`
	LatP99Ms   float64 `json:"lat_p99_ms"`
	LatMaxMs   float64 `json:"lat_max_ms"`
}

// printBenchJSON prints the benchmark results as JSON.
func printBenchJSON(cfg *benchConfig, m *benchMetrics) {
	report := benchJSONReport{
		Mode:       cfg.mode(),
		Pairs:      cfg.Pairs,
		PayloadLen: cfg.PayloadLen,
		DurationS:  m.elapsed.Seconds(),
		Sent:       m.totalSent.Load(),
		Received:   m.totalRecv.Load(),
		SendErr:    m.totalSendErr.Load(),
		SendRate:   m.sendRate,
		RecvRate:   m.recvRate,
		SendMBps:   m.sendMBps,
		RecvMBps:   m.recvMBps,
		LossRate:   m.lossRate,
		LatSamples: m.latCount,
		LatMinMs:   durationToMs(m.latMin),
		LatAvgMs:   durationToMs(m.latAvg),
		LatP50Ms:   durationToMs(m.latP50),
		LatP95Ms:   durationToMs(m.latP95),
		LatP99Ms:   durationToMs(m.latP99),
		LatMaxMs:   durationToMs(m.latMax),
	}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		printError(fmt.Sprintf("JSON marshal: %v", err))
		return
	}
	fmt.Println(string(data))
}

// commaFmt formats an int64 with thousand separators.
func commaFmt(n int64) string {
	if n < 0 {
		return "-" + commaFmt(-n)
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}

	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
}

// fmtLatency formats a duration into a human-readable latency string.
func fmtLatency(d time.Duration) string {
	switch {
	case d < time.Microsecond:
		return fmt.Sprintf("%.2f ns", float64(d.Nanoseconds()))
	case d < time.Millisecond:
		return fmt.Sprintf("%.2f us", float64(d.Nanoseconds())/1000)
	case d < time.Second:
		return fmt.Sprintf("%.2f ms", float64(d.Nanoseconds())/1e6)
	default:
		return fmt.Sprintf("%.2f s", d.Seconds())
	}
}

// durationToMs converts a time.Duration to milliseconds as float64.
func durationToMs(d time.Duration) float64 {
	return float64(d.Nanoseconds()) / 1e6
}

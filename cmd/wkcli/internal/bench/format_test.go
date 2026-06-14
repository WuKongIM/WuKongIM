package bench

import (
	"strings"
	"testing"
	"time"
)

func TestFormatHumanSummaryIncludesTrafficAndLatency(t *testing.T) {
	result := sendResultFixture()

	got := formatHumanSummary(result)

	for _, want := range []string{
		"WKProto SEND summary",
		"result:        PASS",
		"success:       1,000",
		"throughput:    500.00 msgs/sec",
		"sendack p99:   8ms",
		"channels:      10",
		"channel_pick:  round_robin",
		"msgs/channel:  min=100 avg=100 max=100",
		"SENDACK reasons",
		"success:       1,000",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected summary to contain %q, got:\n%s", want, got)
		}
	}
}

func TestFormatJSONSummaryIsStable(t *testing.T) {
	result := sendResultFixture()

	got, err := formatJSONSummary(result)

	if err != nil {
		t.Fatalf("formatJSONSummary() error = %v", err)
	}
	for _, want := range []string{
		`"result": "pass"`,
		`"throughput_msgs_per_sec": 500`,
		`"channels": 10`,
		`"sendack_reasons": {`,
		`"success": 1000`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected JSON to contain %q, got:\n%s", want, got)
		}
	}
}

func TestFormatCSVSummaryWritesHeaderAndRow(t *testing.T) {
	result := sendResultFixture()

	got, err := formatCSVSummary(result)

	if err != nil {
		t.Fatalf("formatCSVSummary() error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected header and one row, got %d lines:\n%s", len(lines), got)
	}
	if !strings.Contains(lines[0], "result,duration_ms,clients,gateways,channels") {
		t.Fatalf("unexpected header: %s", lines[0])
	}
	if !strings.Contains(lines[1], "pass,2000,4,2,10,1000,1000,0,500.00") {
		t.Fatalf("unexpected row: %s", lines[1])
	}
}

func TestFormatProgressLineShowsBarAndLiveStats(t *testing.T) {
	got := formatProgressLine(sendConfig{
		Messages:     1000,
		PayloadBytes: 128,
	}, progressSnapshot{
		Done:           500,
		Errors:         20,
		Percent:        50,
		Throughput:     2400,
		BandwidthBytes: 307200,
		P99:            8 * time.Millisecond,
	})

	for _, want := range []string{
		"Sending [",
		"=>",
		"]  50.0%",
		"500/1,000 msgs",
		"2,400 msg/s",
		"0.29 MiB/s",
		"p99=8ms",
		"err=20",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected progress line to contain %q, got:\n%s", want, got)
		}
	}
}

func sendResultFixture() sendResult {
	return sendResult{
		Result:                resultPass,
		Duration:              2 * time.Second,
		Success:               1000,
		Errors:                0,
		Throughput:            500,
		BandwidthBytes:        64000,
		Clients:               4,
		Gateways:              2,
		Channels:              10,
		ChannelPick:           channelPickRoundRobin,
		Messages:              1000,
		PayloadBytes:          128,
		BatchSize:             1,
		MinMessagesPerChannel: 100,
		AvgMessagesPerChannel: 100,
		MaxMessagesPerChannel: 100,
		SendackReasons:        map[string]uint64{"success": 1000},
		Latency: latencySummary{
			Average: time.Millisecond,
			P50:     500 * time.Microsecond,
			P95:     5 * time.Millisecond,
			P99:     8 * time.Millisecond,
			Max:     12 * time.Millisecond,
		},
	}
}

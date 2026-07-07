package messageevent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WriteResult writes deterministic machine and human-readable message event reports.
func WriteResult(reportDir string, result Result) error {
	if strings.TrimSpace(reportDir) == "" {
		reportDir = result.ReportDir
	}
	if err := os.MkdirAll(reportDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(reportDir, "message_event_report.json"), append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(reportDir, "summary.md"), []byte(SummaryMarkdown(result)), 0o644)
}

// ConsoleSummary returns the short human-readable CLI summary.
func ConsoleSummary(result Result) string {
	return fmt.Sprintf(
		"wkbench message-event: %s streams=%d deltas=%d finishes=%d errors=%d duration=%s report=%s\n",
		result.Status,
		result.Shape.Streams,
		result.Requests.DeltaEvents,
		result.Requests.FinishEvents,
		result.Requests.Errors,
		result.Duration,
		result.ReportDir,
	)
}

// SummaryMarkdown renders a deterministic message event pressure summary.
func SummaryMarkdown(result Result) string {
	var b strings.Builder
	fmt.Fprintln(&b, "# wkbench message-event")
	fmt.Fprintf(&b, "- status: %s\n", result.Status)
	fmt.Fprintf(&b, "- run_id: %s\n", result.RunID)
	fmt.Fprintf(&b, "- streams: %d\n", result.Shape.Streams)
	fmt.Fprintf(&b, "- warm_channel_upserts: %d\n", result.Requests.WarmChannelUpserts)
	fmt.Fprintf(&b, "- warm_runtime_messages: %d\n", result.Requests.WarmRuntimeMessages)
	fmt.Fprintf(&b, "- delta_events: %d\n", result.Requests.DeltaEvents)
	fmt.Fprintf(&b, "- finish_events: %d\n", result.Requests.FinishEvents)
	fmt.Fprintf(&b, "- expected_durable_events: %d\n", result.Shape.ExpectedDurableEvents)
	fmt.Fprintf(&b, "- max_finish_proposals: %d\n", result.Shape.ExpectedFinishProposals)
	fmt.Fprintf(&b, "- errors: %d\n", result.Requests.Errors)
	fmt.Fprintf(&b, "- delta_p95: %s\n", result.Requests.DeltaP95)
	fmt.Fprintf(&b, "- delta_p99: %s\n", result.Requests.DeltaP99)
	fmt.Fprintf(&b, "- finish_p95: %s\n", result.Requests.FinishP95)
	fmt.Fprintf(&b, "- finish_p99: %s\n", result.Requests.FinishP99)
	fmt.Fprintf(&b, "- cache_sessions_max: %.0f\n", result.Metrics.MessageEventStreamCacheSessionsMax)
	fmt.Fprintf(&b, "- cache_open_lanes_max: %.0f\n", result.Metrics.MessageEventStreamCacheOpenLanesMax)
	fmt.Fprintf(&b, "- cache_payload_bytes_max: %.0f\n", result.Metrics.MessageEventStreamCachePayloadBytesMax)
	fmt.Fprintf(&b, "- cache_miss_count: %.0f\n", result.Metrics.MessageEventCacheMissCount)
	fmt.Fprintf(&b, "- backpressured_count: %.0f\n", result.Metrics.MessageEventBackpressuredCount)
	if len(result.Gates) > 0 {
		fmt.Fprintln(&b, "\n## Gates")
		for _, gate := range result.Gates {
			status := "failed"
			if gate.Passed {
				status = "passed"
			}
			fmt.Fprintf(&b, "- %s: %s observed=%.0f expected=%.0f\n", gate.Name, status, gate.Observed, gate.Expected)
		}
	}
	if len(result.Errors) > 0 {
		fmt.Fprintln(&b, "\n## Errors")
		for _, err := range result.Errors {
			fmt.Fprintf(&b, "- %s\n", err)
		}
	}
	return b.String()
}

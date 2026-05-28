package capacity

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// ExitSuccess means capacity found at least one stable attempt.
	ExitSuccess = 0
	// ExitConfigValidationFailed means static config validation failed.
	ExitConfigValidationFailed = 1
	// ExitPreflightFailed means target discovery or preflight failed.
	ExitPreflightFailed = 2
	// ExitNoStableAttempt means no attempted QPS met the stability criteria.
	ExitNoStableAttempt = 3
	// ExitWorkerFailed means the temporary worker or workload failed.
	ExitWorkerFailed = 4
	// ExitTargetUnavailable means the target became unavailable.
	ExitTargetUnavailable = 5
	// ExitInternalError means an unexpected internal error occurred.
	ExitInternalError = 6
)

// ExitCode maps capacity status to the wkbench CLI exit code contract.
func (r Result) ExitCode() int {
	if r.Status == StatusPassed {
		return ExitSuccess
	}
	return ExitNoStableAttempt
}

// WriteResult writes capacity result artifacts into dir.
func WriteResult(dir string, result Result) error {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(dir, "result.json"), data, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "summary.md"), []byte(SummaryMarkdown(result)), 0o644)
}

// SummaryMarkdown renders a deterministic capacity summary.
func SummaryMarkdown(result Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# wkbench capacity %s\n\n", capacityCommandName(result))
	fmt.Fprintf(&b, "- status: %s\n", result.Status)
	fmt.Fprintf(&b, "- profile: %s\n", result.Profile)
	fmt.Fprintf(&b, "- max_stable_qps: %.2f\n", result.MaxStableQPS)
	if result.FirstFailedQPS > 0 {
		fmt.Fprintf(&b, "- first_failed_qps: %.2f\n", result.FirstFailedQPS)
	}
	fmt.Fprintf(&b, "\n## Attempts\n\n")
	for _, attempt := range result.Attempts {
		status := "fail"
		if attempt.Passed {
			status = "pass"
		}
		fmt.Fprintf(&b, "- %.2f qps: %s actual=%.2f scheduled=%d success=%d errors=%d backlog=%d p50=%s p95=%s p99=%s", attempt.Attempt.OfferedQPS, status, attempt.ActualQPS, attempt.ScheduledMessages, attempt.SendSuccess, attempt.SendErrors, attempt.BacklogMessages, formatDuration(attempt.SendackP50), formatDuration(attempt.SendackP95), formatDuration(attempt.SendackP99))
		if attempt.FailureReason != "" {
			fmt.Fprintf(&b, " reason=%s", attempt.FailureReason)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// ConsoleSummary renders concise terminal output for capacity results.
func ConsoleSummary(result Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "wkbench capacity %s\n\n", capacityCommandName(result))
	fmt.Fprintf(&b, "attempts:\n")
	for _, attempt := range result.Attempts {
		status := "fail"
		if attempt.Passed {
			status = "pass"
		}
		fmt.Fprintf(&b, "  %.0f qps\t%s\tactual=%.2f\tscheduled=%d\tsuccess=%d\terrors=%d\tbacklog=%d\tp50=%s\tp95=%s\tp99=%s", attempt.Attempt.OfferedQPS, status, attempt.ActualQPS, attempt.ScheduledMessages, attempt.SendSuccess, attempt.SendErrors, attempt.BacklogMessages, formatDuration(attempt.SendackP50), formatDuration(attempt.SendackP95), formatDuration(attempt.SendackP99))
		if attempt.FailureReason != "" {
			fmt.Fprintf(&b, "\treason=%s", attempt.FailureReason)
		}
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "\nresult:\n")
	fmt.Fprintf(&b, "  status: %s\n", result.Status)
	fmt.Fprintf(&b, "  max_stable_qps: %.2f\n", result.MaxStableQPS)
	if result.FirstFailedQPS > 0 {
		fmt.Fprintf(&b, "  first_failed_qps: %.2f\n", result.FirstFailedQPS)
	}
	if result.ReportDir != "" {
		fmt.Fprintf(&b, "  report: %s\n", result.ReportDir)
	}
	return b.String()
}

func capacityCommandName(result Result) string {
	if strings.TrimSpace(result.Profile) == "hot-channel" {
		return "hot-channel"
	}
	return "send"
}

func formatDuration(d time.Duration) string {
	if d == 0 {
		return "0s"
	}
	return d.String()
}

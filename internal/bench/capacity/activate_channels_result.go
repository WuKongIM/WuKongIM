package capacity

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/model"
)

// ActivateChannelsReportConfig is the token-free config written to activation reports.
type ActivateChannelsReportConfig struct {
	// RunID is the stable benchmark run identifier.
	RunID string `json:"run_id"`
	// Channels is the number of group channels targeted by activation.
	Channels int `json:"channels"`
	// Users is the number of online users prepared for the run.
	Users int `json:"users"`
	// GroupMembers is the number of members per generated group channel.
	GroupMembers int `json:"group_members"`
	// ActivationConcurrency is the maximum in-flight send operation count.
	ActivationConcurrency int `json:"activation_concurrency"`
	// ActivationWindow is the time window used to schedule one send per channel.
	ActivationWindow time.Duration `json:"activation_window"`
	// Hold is the post-activation observation duration.
	Hold time.Duration `json:"hold"`
	// ProbeBatchSize is the channel count checked per runtime probe batch.
	ProbeBatchSize int `json:"probe_batch_size"`
	// EvictAfter controls whether activated runtime state was evicted after probing.
	EvictAfter bool `json:"evict_after"`
}

// ActivateChannelsResult contains runtime evidence and the verdict for one activation run.
type ActivateChannelsResult struct {
	// Status is passed when the activation run satisfies every evidence check.
	Status Status `json:"status"`
	// Config is the token-free activation config captured for reproducibility.
	Config ActivateChannelsReportConfig `json:"config"`
	// Evaluation is the final activation-specific pass/fail verdict.
	Evaluation ActivateChannelsEvaluation `json:"evaluation"`
	// Cold contains pre-activation runtime snapshots.
	Cold []model.ChannelRuntimeSnapshot `json:"cold_snapshots,omitempty"`
	// Active contains post-activation runtime snapshots.
	Active []model.ChannelRuntimeSnapshot `json:"active_snapshots,omitempty"`
	// HoldSamples contains runtime snapshots captured during the post-activation hold.
	HoldSamples [][]model.ChannelRuntimeSnapshot `json:"hold_samples,omitempty"`
	// ProbeBatches contains all-node runtime probe results for generated channel ranges.
	ProbeBatches [][]model.ChannelRuntimeProbeResult `json:"probe_batches,omitempty"`
	// EvictBatches contains optional runtime eviction results.
	EvictBatches [][]model.ChannelRuntimeEvictResult `json:"evict_batches,omitempty"`
	// ReportDir is the directory where activation reports should be written.
	ReportDir string `json:"report_dir,omitempty"`
}

// ExitCode maps activation results to the wkbench capacity CLI exit contract.
func (r ActivateChannelsResult) ExitCode() int {
	if r.Status == StatusPassed {
		return ExitSuccess
	}
	if hasActivateChannelsEvaluation(r.Evaluation) {
		return ExitNoStableAttempt
	}
	return ExitWorkerFailed
}

// WriteActivateChannelsResult writes activation report artifacts into dir.
func WriteActivateChannelsResult(dir string, result ActivateChannelsResult) error {
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
	if err := os.WriteFile(filepath.Join(dir, "activation_report.json"), data, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "summary.md"), []byte(ActivateChannelsSummaryMarkdown(result)), 0o644)
}

// ActivateChannelsSummaryMarkdown renders a deterministic markdown activation summary.
func ActivateChannelsSummaryMarkdown(result ActivateChannelsResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# wkbench activate-channels\n\n")
	fmt.Fprintf(&b, "- status: %s\n", result.Status)
	fmt.Fprintf(&b, "- exit_code: %d\n", result.ExitCode())
	fmt.Fprintf(&b, "- run_id: %s\n", result.Config.RunID)
	fmt.Fprintf(&b, "- channels: %d\n", result.Config.Channels)
	fmt.Fprintf(&b, "- users: %d\n", result.Config.Users)
	fmt.Fprintf(&b, "- group_members: %d\n", result.Config.GroupMembers)
	fmt.Fprintf(&b, "- activation_concurrency: %d\n", result.Config.ActivationConcurrency)
	fmt.Fprintf(&b, "- activation_window: %s\n", formatDuration(result.Config.ActivationWindow))
	fmt.Fprintf(&b, "- hold: %s\n", formatDuration(result.Config.Hold))
	fmt.Fprintf(&b, "- probe_batch_size: %d\n", result.Config.ProbeBatchSize)
	fmt.Fprintf(&b, "- evict_after: %t\n", result.Config.EvictAfter)
	fmt.Fprintf(&b, "\n## Evaluation\n\n")
	fmt.Fprintf(&b, "- passed: %t\n", result.Evaluation.Passed)
	fmt.Fprintf(&b, "- activation_success: %d\n", result.Evaluation.ActivationSuccess)
	fmt.Fprintf(&b, "- activation_errors: %d\n", result.Evaluation.ActivationErrors)
	fmt.Fprintf(&b, "- activation_backlog: %d\n", result.Evaluation.ActivationBacklog)
	fmt.Fprintf(&b, "- sendack_p50: %s\n", formatDuration(result.Evaluation.SendackP50))
	fmt.Fprintf(&b, "- sendack_p95: %s\n", formatDuration(result.Evaluation.SendackP95))
	fmt.Fprintf(&b, "- sendack_p99: %s\n", formatDuration(result.Evaluation.SendackP99))
	fmt.Fprintf(&b, "- active_leader_total: %d\n", result.Evaluation.ActiveLeaderTotal)
	fmt.Fprintf(&b, "- activation_rejected_delta: %d\n", result.Evaluation.ActivationRejectedDelta)
	if len(result.Evaluation.FailureReasons) > 0 {
		fmt.Fprintf(&b, "\n## Failure Reasons\n\n")
		for _, reason := range result.Evaluation.FailureReasons {
			fmt.Fprintf(&b, "- %s\n", reason)
		}
	}
	if len(result.Evaluation.ProbeMissingAllNodes) > 0 {
		fmt.Fprintf(&b, "\n## Probe Missing On All Nodes\n\n")
		for _, channelID := range result.Evaluation.ProbeMissingAllNodes {
			fmt.Fprintf(&b, "- %s\n", channelID)
		}
	}
	return b.String()
}

// ActivateChannelsConsoleSummary renders concise terminal output for activation results.
func ActivateChannelsConsoleSummary(result ActivateChannelsResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "wkbench activate-channels\n\n")
	fmt.Fprintf(&b, "result:\n")
	fmt.Fprintf(&b, "  status: %s\n", result.Status)
	fmt.Fprintf(&b, "  exit_code: %d\n", result.ExitCode())
	fmt.Fprintf(&b, "  run_id: %s\n", result.Config.RunID)
	fmt.Fprintf(&b, "  channels: %d\n", result.Config.Channels)
	fmt.Fprintf(&b, "  users: %d\n", result.Config.Users)
	fmt.Fprintf(&b, "  activation: success=%d errors=%d backlog=%d active_leader=%d rejected_delta=%d p50=%s p95=%s p99=%s\n",
		result.Evaluation.ActivationSuccess,
		result.Evaluation.ActivationErrors,
		result.Evaluation.ActivationBacklog,
		result.Evaluation.ActiveLeaderTotal,
		result.Evaluation.ActivationRejectedDelta,
		formatDuration(result.Evaluation.SendackP50),
		formatDuration(result.Evaluation.SendackP95),
		formatDuration(result.Evaluation.SendackP99),
	)
	if len(result.Evaluation.FailureReasons) > 0 {
		fmt.Fprintf(&b, "  reasons: %s\n", strings.Join(result.Evaluation.FailureReasons, ", "))
	}
	if result.ReportDir != "" {
		fmt.Fprintf(&b, "  report: %s\n", result.ReportDir)
	}
	return b.String()
}

func activateChannelsReportConfigFromConfig(cfg ActivateChannelsConfig) ActivateChannelsReportConfig {
	return ActivateChannelsReportConfig{
		RunID:                 cfg.RunID,
		Channels:              cfg.Channels,
		Users:                 cfg.Users,
		GroupMembers:          cfg.GroupMembers,
		ActivationConcurrency: cfg.ActivationConcurrency,
		ActivationWindow:      cfg.ActivationWindow,
		Hold:                  cfg.Hold,
		ProbeBatchSize:        cfg.ProbeBatchSize,
		EvictAfter:            cfg.EvictAfter,
	}
}

func hasActivateChannelsEvaluation(eval ActivateChannelsEvaluation) bool {
	return eval.Passed ||
		len(eval.FailureReasons) > 0 ||
		eval.ActivationSuccess > 0 ||
		eval.ActivationErrors > 0 ||
		eval.ActivationBacklog > 0 ||
		eval.SendackP50 > 0 ||
		eval.SendackP95 > 0 ||
		eval.SendackP99 > 0 ||
		eval.ActiveLeaderTotal > 0 ||
		eval.ActivationRejectedDelta > 0 ||
		len(eval.ProbeMissingAllNodes) > 0
}

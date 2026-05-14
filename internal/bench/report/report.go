package report

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	"github.com/WuKongIM/WuKongIM/internal/bench/model"
	"gopkg.in/yaml.v3"
)

const (
	// ExitSuccess means the run passed all enforced limits.
	ExitSuccess = 0
	// ExitConfigValidationFailed means static config validation failed.
	ExitConfigValidationFailed = 1
	// ExitPreflightFailed means preflight failed before workloads started.
	ExitPreflightFailed = 2
	// ExitHardLimitFailed means at least one hard limit failed.
	ExitHardLimitFailed = 3
	// ExitWorkerFailed means a worker failed or was unreachable.
	ExitWorkerFailed = 4
	// ExitTargetUnavailable means the benchmark target became unavailable.
	ExitTargetUnavailable = 5
	// ExitInternalError means wkbench hit an unexpected internal error.
	ExitInternalError = 6
)

// Status is the benchmark report verdict.
type Status string

const (
	// StatusPassed means all enforced limits passed.
	StatusPassed Status = "passed"
	// StatusFailed means one or more enforced limits failed.
	StatusFailed Status = "failed"
)

// Summary contains aggregate run quality measurements used for limit checks.
type Summary struct {
	// ConnectErrorRate is failed connects divided by attempted connects.
	ConnectErrorRate float64 `json:"connect_error_rate"`
	// SendackErrorRate is failed send acknowledgements divided by attempted sends.
	SendackErrorRate float64 `json:"sendack_error_rate"`
	// RecvVerifyErrorRate is failed receive verifications divided by attempted verifications.
	RecvVerifyErrorRate float64 `json:"recv_verify_error_rate"`
	// WorkerFailed is the number of failed or unreachable workers.
	WorkerFailed int `json:"worker_failed"`
	// SendackP99 is the observed p99 send acknowledgement latency.
	SendackP99 time.Duration `json:"sendack_p99"`
	// RecvP99 is the observed p99 receive latency.
	RecvP99 time.Duration `json:"recv_p99"`
}

// Violation describes one failed or warning benchmark limit.
type Violation struct {
	// Name is the stable limit field name.
	Name string `json:"name"`
	// Actual is the observed value.
	Actual float64 `json:"actual"`
	// Limit is the configured threshold.
	Limit float64 `json:"limit"`
	// Hard indicates whether this violation affects the exit code without fail_on_soft.
	Hard bool `json:"hard"`
}

// WorkerReport contains one worker's raw report payload for writing under workers/.
type WorkerReport struct {
	// WorkerID identifies the reporting worker.
	WorkerID string `json:"worker_id"`
	// Report is the raw worker report JSON payload.
	Report json.RawMessage `json:"report"`
}

// Input carries all deterministic data needed to build and write a run report.
type Input struct {
	// RunID is the benchmark run identifier written to report metadata.
	RunID string
	// Scenario is the scenario config used for the run.
	Scenario model.Scenario
	// Target is the target config used for the run.
	Target model.Target
	// Workers is the worker set used for the run.
	Workers model.WorkerSet
	// Plan is the deterministic worker assignment used for the run.
	Plan model.Plan
	// Limits contains hard and soft failure thresholds.
	Limits model.LimitsConfig
	// Summary contains aggregate metrics used for limit evaluation.
	Summary Summary
	// Metrics contains aggregated safe metrics.
	Metrics metrics.SnapshotData
	// WorkerReports contains raw per-worker report payloads.
	WorkerReports []WorkerReport
	// WorkerMetrics contains per-worker metric snapshots for jsonl output.
	WorkerMetrics []metrics.WorkerSnapshot
	// TargetSnapshots contains raw target snapshots for jsonl output.
	TargetSnapshots []json.RawMessage
	// ErrorSamples contains bounded error samples for jsonl output.
	ErrorSamples []metrics.ErrorSample
	// CoordinatorLog contains optional coordinator log content.
	CoordinatorLog string
}

// Report is the deterministic JSON report written at the end of a run.
type Report struct {
	// RunID is the benchmark run identifier.
	RunID string `json:"run_id"`
	// Status is the final benchmark verdict.
	Status Status `json:"status"`
	// ExitCode is the wkbench process exit code associated with the verdict.
	ExitCode int `json:"exit_code"`
	// Summary contains aggregate run quality measurements.
	Summary Summary `json:"summary"`
	// Violations contains enforced limit failures.
	Violations []Violation `json:"violations"`
	// Warnings contains non-enforced soft limit warnings.
	Warnings []Violation `json:"warnings"`
	// Scenario is the scenario config used for the run.
	Scenario model.Scenario `json:"scenario"`
	// Target is the target config used for the run.
	Target model.Target `json:"target"`
	// Workers is the worker set used for the run.
	Workers model.WorkerSet `json:"workers"`
	// Plan is the deterministic worker assignment used for the run.
	Plan model.Plan `json:"plan"`
	// Metrics contains aggregate safe metrics.
	Metrics metrics.SnapshotData `json:"metrics"`
	// WorkerReports contains raw per-worker report payloads.
	WorkerReports []WorkerReport `json:"worker_reports,omitempty"`
	// WorkerMetrics contains per-worker metric snapshots for jsonl output.
	WorkerMetrics []metrics.WorkerSnapshot `json:"worker_metrics,omitempty"`
	// TargetSnapshots contains raw target snapshots for jsonl output.
	TargetSnapshots []json.RawMessage `json:"target_snapshots,omitempty"`
	// ErrorSamples contains bounded error samples for jsonl output.
	ErrorSamples []metrics.ErrorSample `json:"error_samples,omitempty"`
	// CoordinatorLog contains optional coordinator log content.
	CoordinatorLog string `json:"coordinator_log,omitempty"`
}

// Build evaluates configured limits and returns a deterministic report object.
func Build(in Input) Report {
	limits := in.Limits
	if (limits == model.LimitsConfig{}) {
		limits = in.Scenario.Limits
	}
	runID := strings.TrimSpace(in.RunID)
	if runID == "" {
		runID = in.Scenario.Run.ID
	}
	rep := Report{
		RunID:           runID,
		Status:          StatusPassed,
		ExitCode:        ExitSuccess,
		Summary:         in.Summary,
		Scenario:        in.Scenario,
		Target:          in.Target,
		Workers:         in.Workers,
		Plan:            in.Plan,
		Metrics:         in.Metrics,
		WorkerReports:   append([]WorkerReport(nil), in.WorkerReports...),
		WorkerMetrics:   append([]metrics.WorkerSnapshot(nil), in.WorkerMetrics...),
		TargetSnapshots: append([]json.RawMessage(nil), in.TargetSnapshots...),
		ErrorSamples:    append([]metrics.ErrorSample(nil), in.ErrorSamples...),
		CoordinatorLog:  in.CoordinatorLog,
	}
	rep.Violations = append(rep.Violations, hardLimitViolations(limits.Hard, in.Summary)...)
	soft := softLimitViolations(limits.Soft, in.Summary)
	if limits.FailOnSoft {
		for i := range soft {
			soft[i].Hard = true
		}
		rep.Violations = append(rep.Violations, soft...)
	} else {
		rep.Warnings = soft
	}
	if len(rep.Violations) > 0 {
		rep.Status = StatusFailed
		rep.ExitCode = ExitHardLimitFailed
	}
	return rep
}

// WriteDir writes all standard wkbench report artifacts into dir.
func WriteDir(dir string, rep Report) error {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(dir, "workers"), 0o755); err != nil {
		return err
	}
	for _, sub := range []string{"metrics", "errors"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return err
		}
	}
	if err := writeYAML(filepath.Join(dir, "scenario.yaml"), rep.Scenario); err != nil {
		return err
	}
	if err := writeYAML(filepath.Join(dir, "target.yaml"), rep.Target); err != nil {
		return err
	}
	if err := writeYAML(filepath.Join(dir, "workers.yaml"), rep.Workers); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(dir, "plan.json"), rep.Plan); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(dir, "report.json"), rep); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "summary.md"), []byte(summaryMarkdown(rep)), 0o644); err != nil {
		return err
	}
	logContent := rep.CoordinatorLog
	if logContent == "" {
		logContent = fmt.Sprintf("run_id=%s status=%s exit_code=%d\n", rep.RunID, rep.Status, rep.ExitCode)
	}
	if err := os.WriteFile(filepath.Join(dir, "coordinator.log"), []byte(logContent), 0o644); err != nil {
		return err
	}
	if err := writeWorkerReports(filepath.Join(dir, "workers"), rep.WorkerReports); err != nil {
		return err
	}
	if err := writeWorkerMetrics(filepath.Join(dir, "metrics", "worker-1s.jsonl"), rep.WorkerMetrics); err != nil {
		return err
	}
	if err := writeRawJSONLines(filepath.Join(dir, "metrics", "target-snapshots.jsonl"), rep.TargetSnapshots); err != nil {
		return err
	}
	return writeErrorSamples(filepath.Join(dir, "errors", "samples.jsonl"), rep.ErrorSamples)
}

func hardLimitViolations(l model.HardLimitsConfig, s Summary) []Violation {
	var out []Violation
	out = appendRateViolation(out, "max_sendack_error_rate", s.SendackErrorRate, l.MaxSendackErrorRate, true)
	out = appendRateViolation(out, "max_connect_error_rate", s.ConnectErrorRate, l.MaxConnectErrorRate, true)
	out = appendRateViolation(out, "max_recv_verify_error_rate", s.RecvVerifyErrorRate, l.MaxRecvVerifyErrorRate, true)
	if l.MaxWorkerFailed >= 0 && s.WorkerFailed > l.MaxWorkerFailed {
		out = append(out, Violation{Name: "max_worker_failed", Actual: float64(s.WorkerFailed), Limit: float64(l.MaxWorkerFailed), Hard: true})
	}
	return out
}

func softLimitViolations(l model.SoftLimitsConfig, s Summary) []Violation {
	var out []Violation
	if l.MaxSendackP99 > 0 && s.SendackP99 > l.MaxSendackP99 {
		out = append(out, Violation{Name: "max_sendack_p99", Actual: float64(s.SendackP99), Limit: float64(l.MaxSendackP99)})
	}
	if l.MaxRecvP99 > 0 && s.RecvP99 > l.MaxRecvP99 {
		out = append(out, Violation{Name: "max_recv_p99", Actual: float64(s.RecvP99), Limit: float64(l.MaxRecvP99)})
	}
	return out
}

func appendRateViolation(out []Violation, name string, actual, limit float64, hard bool) []Violation {
	if limit > 0 && actual > limit {
		out = append(out, Violation{Name: name, Actual: actual, Limit: limit, Hard: hard})
	}
	return out
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func writeYAML(path string, v any) error {
	data, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func summaryMarkdown(rep Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# wkbench report\n\n")
	fmt.Fprintf(&b, "- run_id: %s\n", rep.RunID)
	fmt.Fprintf(&b, "- status: %s\n", rep.Status)
	fmt.Fprintf(&b, "- exit_code: %d\n", rep.ExitCode)
	fmt.Fprintf(&b, "- sendack_error_rate: %.6f\n", rep.Summary.SendackErrorRate)
	fmt.Fprintf(&b, "- connect_error_rate: %.6f\n", rep.Summary.ConnectErrorRate)
	fmt.Fprintf(&b, "- recv_verify_error_rate: %.6f\n", rep.Summary.RecvVerifyErrorRate)
	fmt.Fprintf(&b, "- worker_failed: %d\n", rep.Summary.WorkerFailed)
	return b.String()
}

func writeWorkerReports(dir string, reports []WorkerReport) error {
	if len(reports) == 0 {
		return nil
	}
	sorted := append([]WorkerReport(nil), reports...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].WorkerID < sorted[j].WorkerID })
	for _, wr := range sorted {
		workerID := safeFilePart(wr.WorkerID)
		if workerID == "" {
			workerID = "worker"
		}
		data := wr.Report
		if len(data) == 0 {
			data = []byte(`{}`)
		}
		if !json.Valid(data) {
			return fmt.Errorf("worker %s report is not valid JSON", wr.WorkerID)
		}
		var pretty any
		if err := json.Unmarshal(data, &pretty); err != nil {
			return err
		}
		if err := writeJSON(filepath.Join(dir, workerID+".report.json"), pretty); err != nil {
			return err
		}
	}
	return nil
}

func writeWorkerMetrics(path string, snapshots []metrics.WorkerSnapshot) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	sorted := append([]metrics.WorkerSnapshot(nil), snapshots...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].WorkerID < sorted[j].WorkerID })
	enc := json.NewEncoder(w)
	for _, snap := range sorted {
		if err := enc.Encode(snap); err != nil {
			return err
		}
	}
	return w.Flush()
}

func writeRawJSONLines(path string, lines []json.RawMessage) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	for _, line := range lines {
		if len(line) == 0 {
			line = []byte(`{}`)
		}
		if !json.Valid(line) {
			return fmt.Errorf("jsonl payload is not valid JSON")
		}
		if _, err := w.Write(line); err != nil {
			return err
		}
		if err := w.WriteByte('\n'); err != nil {
			return err
		}
	}
	return w.Flush()
}

func writeErrorSamples(path string, samples []metrics.ErrorSample) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, sample := range samples {
		if err := enc.Encode(sample); err != nil {
			return err
		}
	}
	return w.Flush()
}

func safeFilePart(raw string) string {
	raw = strings.TrimSpace(raw)
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

package report

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
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

// Summary contains run quality measurements used for limit checks.
type Summary struct {
	// ConnectErrorRate is failed connects divided by attempted connects.
	ConnectErrorRate float64 `json:"connect_error_rate"`
	// SendackErrorRate is failed send acknowledgements divided by attempted sends.
	SendackErrorRate float64 `json:"sendack_error_rate"`
	// RecvVerifyErrorRate is failed receive verifications divided by attempted verifications.
	RecvVerifyErrorRate float64 `json:"recv_verify_error_rate"`
	// WorkerFailed is the number of failed or unreachable workers.
	WorkerFailed int `json:"worker_failed"`
	// SendackMaxWorkerP99 is the maximum worker-local send acknowledgement p99 latency.
	SendackMaxWorkerP99 time.Duration `json:"sendack_max_worker_p99"`
	// RecvMaxWorkerP99 is the maximum worker-local receive p99 latency.
	RecvMaxWorkerP99 time.Duration `json:"recv_max_worker_p99"`
}

// SendRunSummary contains measured-run send throughput and latency stats.
type SendRunSummary struct {
	// SendSuccess is the successful sendack count during measured run.
	SendSuccess uint64 `json:"send_success"`
	// SendErrors is the failed send/sendack count during measured run.
	SendErrors uint64 `json:"send_errors"`
	// IngressQPS is SendSuccess divided by measured duration seconds.
	IngressQPS float64 `json:"ingress_qps"`
	// SendackP50 is the maximum worker-local run-phase sendack p50 latency.
	SendackP50 time.Duration `json:"sendack_p50"`
	// SendackP95 is the maximum worker-local run-phase sendack p95 latency.
	SendackP95 time.Duration `json:"sendack_p95"`
	// SendackP99 is the maximum worker-local run-phase sendack p99 latency.
	SendackP99 time.Duration `json:"sendack_p99"`
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
	// Summary contains run metrics used for limit evaluation.
	Summary Summary
	// Metrics contains safely merged worker metrics.
	Metrics metrics.SnapshotData
	// WorkerReports contains raw per-worker report payloads.
	WorkerReports []WorkerReport
	// WorkerMetrics contains per-worker metric snapshots for jsonl output.
	WorkerMetrics []metrics.WorkerSnapshot
	// TargetSnapshots contains raw target snapshots for jsonl output.
	TargetSnapshots []json.RawMessage
	// PresenceSnapshots contains typed target presence snapshots for report JSON.
	PresenceSnapshots []model.PresenceSnapshot
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
	// Summary contains run quality measurements.
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
	// Metrics contains safely merged worker metrics.
	Metrics metrics.SnapshotData `json:"metrics"`
	// WorkerReports contains raw per-worker report payloads.
	WorkerReports []WorkerReport `json:"worker_reports,omitempty"`
	// WorkerMetrics contains per-worker metric snapshots for jsonl output.
	WorkerMetrics []metrics.WorkerSnapshot `json:"worker_metrics,omitempty"`
	// TargetSnapshots contains raw target snapshots for jsonl output.
	TargetSnapshots []json.RawMessage `json:"target_snapshots,omitempty"`
	// PresenceSnapshots contains typed target presence snapshots.
	PresenceSnapshots []model.PresenceSnapshot `json:"presence_snapshots,omitempty"`
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
		RunID:             runID,
		Status:            StatusPassed,
		ExitCode:          ExitSuccess,
		Summary:           in.Summary,
		Scenario:          in.Scenario,
		Target:            in.Target,
		Workers:           in.Workers,
		Plan:              in.Plan,
		Metrics:           in.Metrics,
		WorkerReports:     append([]WorkerReport(nil), in.WorkerReports...),
		WorkerMetrics:     append([]metrics.WorkerSnapshot(nil), in.WorkerMetrics...),
		TargetSnapshots:   append([]json.RawMessage(nil), in.TargetSnapshots...),
		PresenceSnapshots: append([]model.PresenceSnapshot(nil), in.PresenceSnapshots...),
		ErrorSamples:      append([]metrics.ErrorSample(nil), in.ErrorSamples...),
		CoordinatorLog:    in.CoordinatorLog,
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
	if l.MaxSendackP99 > 0 && s.SendackMaxWorkerP99 > l.MaxSendackP99 {
		out = append(out, Violation{Name: "max_sendack_p99", Actual: float64(s.SendackMaxWorkerP99), Limit: float64(l.MaxSendackP99)})
	}
	if l.MaxRecvP99 > 0 && s.RecvMaxWorkerP99 > l.MaxRecvP99 {
		out = append(out, Violation{Name: "max_recv_p99", Actual: float64(s.RecvMaxWorkerP99), Limit: float64(l.MaxRecvP99)})
	}
	return out
}

func appendRateViolation(out []Violation, name string, actual, limit float64, hard bool) []Violation {
	if limit >= 0 && actual > limit {
		out = append(out, Violation{Name: name, Actual: actual, Limit: limit, Hard: hard})
	}
	return out
}

// SummaryFromMetrics derives report limit inputs from aggregated worker metrics.
func SummaryFromMetrics(snapshot metrics.SnapshotData, workerFailed int) Summary {
	connectAttempts := counterSum(snapshot, "connect_attempt_total")
	connectSuccess := counterSum(snapshot, "connect_success_total")
	connectErrors := counterSum(snapshot, "connect_error_total")
	sendSuccess := counterSum(snapshot, "person_send_success_total", "group_send_success_total", "sendack_success_total")
	sendErrors := counterSum(snapshot, "person_send_error_total", "group_send_error_total", "sendack_error_total")
	recvSuccess := counterSum(snapshot, "person_recv_success_total", "group_recv_success_total", "recv_verify_success_total")
	recvErrors := counterSum(snapshot, "person_recv_error_total", "group_recv_error_total", "recv_verify_error_total")
	return Summary{
		ConnectErrorRate:    connectErrorRate(connectErrors, connectSuccess, connectAttempts),
		SendackErrorRate:    errorRate(sendErrors, sendSuccess),
		RecvVerifyErrorRate: errorRate(recvErrors, recvSuccess),
		WorkerFailed:        workerFailed,
		SendackMaxWorkerP99: maxHistogramP99(snapshot, "person_send_latency_seconds", "group_send_latency_seconds", "sendack_latency_seconds"),
		RecvMaxWorkerP99:    maxHistogramP99(snapshot, "person_recv_latency_seconds", "group_recv_latency_seconds", "recv_latency_seconds"),
	}
}

// SendRunSummaryFromMetrics derives measured-run send throughput and latency from metrics.
func SendRunSummaryFromMetrics(snapshot metrics.SnapshotData, duration time.Duration) SendRunSummary {
	success := counterSumWithPhase(snapshot, "run", "person_send_success_total", "group_send_success_total", "sendack_success_total")
	errors := counterSumWithPhase(snapshot, "run", "person_send_error_total", "group_send_error_total", "sendack_error_total")
	return SendRunSummary{
		SendSuccess: success,
		SendErrors:  errors,
		IngressQPS:  qps(success, duration),
		SendackP50:  maxHistogramPercentileWithPhase(snapshot, "run", 50, "person_send_latency_seconds", "group_send_latency_seconds", "sendack_latency_seconds"),
		SendackP95:  maxHistogramPercentileWithPhase(snapshot, "run", 95, "person_send_latency_seconds", "group_send_latency_seconds", "sendack_latency_seconds"),
		SendackP99:  maxHistogramPercentileWithPhase(snapshot, "run", 99, "person_send_latency_seconds", "group_send_latency_seconds", "sendack_latency_seconds"),
	}
}

func counterSum(snapshot metrics.SnapshotData, names ...string) uint64 {
	wanted := make(map[string]struct{}, len(names))
	for _, name := range names {
		wanted[name] = struct{}{}
	}
	var sum uint64
	for key, value := range snapshot.Counters {
		if _, ok := wanted[metricName(key)]; ok {
			sum += value
		}
	}
	return sum
}

func counterSumWithPhase(snapshot metrics.SnapshotData, phase string, names ...string) uint64 {
	wanted := make(map[string]struct{}, len(names))
	for _, name := range names {
		wanted[name] = struct{}{}
	}
	var sum uint64
	for key, value := range snapshot.Counters {
		if _, ok := wanted[metricName(key)]; ok && seriesHasPhase(key, phase) {
			sum += value
		}
	}
	return sum
}

func metricName(key string) string {
	if idx := strings.IndexByte(key, '{'); idx >= 0 {
		return strings.TrimSpace(key[:idx])
	}
	return strings.TrimSpace(key)
}

func maxHistogramPercentileWithPhase(snapshot metrics.SnapshotData, phase string, percentile int, names ...string) time.Duration {
	wanted := make(map[string]struct{}, len(names))
	for _, name := range names {
		wanted[name] = struct{}{}
	}
	var max float64
	for key, hist := range snapshot.Histograms {
		if _, ok := wanted[metricName(key)]; !ok || !seriesHasPhase(key, phase) {
			continue
		}
		var value float64
		switch percentile {
		case 50:
			value = hist.P50Seconds
		case 95:
			value = hist.P95Seconds
		case 99:
			value = hist.P99Seconds
		}
		if value > max {
			max = value
		}
	}
	return time.Duration(max * float64(time.Second))
}

func seriesHasPhase(key, phase string) bool {
	labels := seriesLabels(key)
	return labels["phase"] == phase
}

func seriesLabels(key string) map[string]string {
	labels := map[string]string{}
	open := strings.IndexByte(key, '{')
	if open < 0 || !strings.HasSuffix(key, "}") {
		return labels
	}
	raw := strings.TrimSuffix(key[open+1:], "}")
	for _, part := range strings.Split(raw, ",") {
		name, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		labels[strings.TrimSpace(name)] = strings.TrimSpace(value)
	}
	return labels
}

func qps(count uint64, duration time.Duration) float64 {
	if count == 0 || duration <= 0 {
		return 0
	}
	return float64(count) / duration.Seconds()
}

func connectErrorRate(errors, successes, attempts uint64) float64 {
	if attempts > 0 {
		return float64(errors) / float64(attempts)
	}
	return errorRate(errors, successes)
}

func errorRate(errors, successes uint64) float64 {
	total := errors + successes
	if total == 0 {
		return 0
	}
	return float64(errors) / float64(total)
}

func maxHistogramP99(snapshot metrics.SnapshotData, names ...string) time.Duration {
	wanted := make(map[string]struct{}, len(names))
	for _, name := range names {
		wanted[name] = struct{}{}
	}
	var max float64
	for key, hist := range snapshot.Histograms {
		if _, ok := wanted[metricName(key)]; ok && hist.P99Seconds > max {
			max = hist.P99Seconds
		}
	}
	return time.Duration(max * float64(time.Second))
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
	fmt.Fprintf(&b, "- sendack_max_worker_p99: %s\n", rep.Summary.SendackMaxWorkerP99)
	fmt.Fprintf(&b, "- recv_max_worker_p99: %s\n", rep.Summary.RecvMaxWorkerP99)
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
	w := bufio.NewWriter(f)
	sorted := append([]metrics.WorkerSnapshot(nil), snapshots...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].WorkerID < sorted[j].WorkerID })
	enc := json.NewEncoder(w)
	for _, snap := range sorted {
		if err := enc.Encode(snap); err != nil {
			return errors.Join(err, f.Close())
		}
	}
	return finishBufferedWrite(w, f)
}

func writeRawJSONLines(path string, lines []json.RawMessage) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for _, line := range lines {
		if len(line) == 0 {
			line = []byte(`{}`)
		}
		if !json.Valid(line) {
			return errors.Join(fmt.Errorf("jsonl payload is not valid JSON"), f.Close())
		}
		if _, err := w.Write(line); err != nil {
			return errors.Join(err, f.Close())
		}
		if err := w.WriteByte('\n'); err != nil {
			return errors.Join(err, f.Close())
		}
	}
	return finishBufferedWrite(w, f)
}

func writeErrorSamples(path string, samples []metrics.ErrorSample) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, sample := range samples {
		if err := enc.Encode(sample); err != nil {
			return errors.Join(err, f.Close())
		}
	}
	return finishBufferedWrite(w, f)
}

func finishBufferedWrite(w interface{ Flush() error }, c io.Closer) error {
	return errors.Join(w.Flush(), c.Close())
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

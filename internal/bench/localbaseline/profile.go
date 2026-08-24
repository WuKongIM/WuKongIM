package localbaseline

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// ProfileThresholdQuerySchema identifies the typed first-threshold reduction
	// consumed by the native single-node cluster profile watcher.
	ProfileThresholdQuerySchema = "wukongim/chat-lifecycle-local-single-node-profile-threshold/v1"
	// ProfileEvidenceSchema identifies the closed outcome of the optional
	// threshold-only single-node cluster profile capture.
	ProfileEvidenceSchema       = "wukongim/chat-lifecycle-local-single-node-threshold-pprof/v1"
	maximumProfileMetadataBytes = 64 << 10
	maximumProfileBlobBytes     = 128 << 20
)

// ErrAuthenticatedArtifactMissing lets a sealed-artifact reader distinguish
// an expected absent blob from an unsafe or unreadable artifact.
var ErrAuthenticatedArtifactMissing = errors.New("authenticated artifact is missing")

// ProfileTriggerKind is the closed set of measured conditions allowed to
// start the bounded local profile helper.
type ProfileTriggerKind string

const (
	ProfileTriggerActualOfferedRatio     ProfileTriggerKind = "actual_offered_ratio"
	ProfileTriggerTerminalProductFailure ProfileTriggerKind = "terminal_product_failure"
)

// ProfilePhase is the admission-aware phase exposed to the profile helper.
type ProfilePhase string

const (
	ProfilePhaseWarmup      ProfilePhase = "warmup"
	ProfilePhaseMeasurement ProfilePhase = "measurement"
	ProfilePhaseDrain       ProfilePhase = "drain"
	ProfilePhaseShutdown    ProfilePhase = "shutdown"
	ProfilePhaseUnknown     ProfilePhase = "unknown"
)

// ProfileThresholdTrigger records the first typed measured threshold and its
// exact worker-observed UTC bracket.
type ProfileThresholdTrigger struct {
	Kind                 ProfileTriggerKind `json:"kind"`
	PreviousAt           time.Time          `json:"previous_at"`
	CurrentAt            time.Time          `json:"current_at"`
	AcknowledgedDelta    uint64             `json:"acknowledged_delta"`
	TerminalFailureDelta uint64             `json:"terminal_product_failure_delta"`
	IntervalSeconds      float64            `json:"interval_seconds"`
	ExpectedOffered      float64            `json:"expected_offered"`
	ActualOfferedPercent float64            `json:"actual_offered_percent"`
}

// ProfileThresholdQuery is a deterministic reduction of the retained typed
// lifecycle stream. Human-readable errors and logs never influence Triggered.
type ProfileThresholdQuery struct {
	Schema                   string                  `json:"schema"`
	RunID                    string                  `json:"run_id"`
	AssignmentID             string                  `json:"assignment_id"`
	OfferedSendQPS           int                     `json:"offered_send_qps"`
	MinimumThroughputPercent int                     `json:"minimum_throughput_percent"`
	EvidenceComplete         bool                    `json:"evidence_complete"`
	PartialLine              bool                    `json:"partial_line"`
	Reason                   string                  `json:"reason"`
	LivePhase                ProfilePhase            `json:"live_phase"`
	Triggered                bool                    `json:"triggered"`
	Trigger                  ProfileThresholdTrigger `json:"trigger"`
}

// ParseProfileLifecycleSnapshot reads one bounded point-in-time view of an
// actively appended lifecycle JSONL file. A final incomplete line is reported
// and excluded; every earlier complete line remains subject to the strict
// lifecycle decoder.
func ParseProfileLifecycleSnapshot(reader io.Reader) ([]LifecycleCapture, bool, error) {
	if reader == nil {
		return nil, false, errors.New("profile lifecycle snapshot reader is required")
	}
	body, err := io.ReadAll(io.LimitReader(reader, MaximumLifecycleCaptureBytes+1))
	if err != nil {
		return nil, false, err
	}
	if len(body) > MaximumLifecycleCaptureBytes {
		return nil, false, fmt.Errorf("profile lifecycle snapshot exceeds %d bytes", MaximumLifecycleCaptureBytes)
	}
	partial := len(body) > 0 && body[len(body)-1] != '\n'
	if partial {
		lastComplete := bytes.LastIndexByte(body, '\n')
		if lastComplete < 0 {
			body = nil
		} else {
			body = body[:lastComplete+1]
		}
	}
	captures, err := ParseLifecycleCaptures(bytes.NewReader(body))
	return captures, partial, err
}

// ProfileEvidence binds a not-triggered or captured profile result to the
// first typed threshold. EvidenceComplete is true only for a proven
// not-triggered result or a complete valid capture.
type ProfileEvidence struct {
	Schema           string                   `json:"schema"`
	Status           string                   `json:"status"`
	EvidenceComplete bool                     `json:"evidence_complete"`
	CaptureValid     bool                     `json:"capture_valid"`
	Reason           string                   `json:"reason"`
	Triggered        bool                     `json:"triggered"`
	Trigger          *ProfileThresholdTrigger `json:"trigger,omitempty"`
	Metadata         string                   `json:"metadata"`
	HelperExitStatus *int                     `json:"helper_exit_status,omitempty"`
}

type thresholdProfileMetadata struct {
	Schema  string `json:"schema"`
	Trigger struct {
		Kind          ProfileTriggerKind `json:"kind"`
		ObservedPhase string             `json:"observed_phase"`
		PreviousUTC   time.Time          `json:"previous_utc"`
		CurrentUTC    time.Time          `json:"current_utc"`
	} `json:"trigger"`
	Capture struct {
		Status         string    `json:"status"`
		Valid          bool      `json:"valid"`
		Reason         string    `json:"reason"`
		StartPhase     string    `json:"start_phase"`
		EndPhase       string    `json:"end_phase"`
		StartedAtUTC   time.Time `json:"started_at_utc"`
		CompletedAtUTC time.Time `json:"completed_at_utc"`
		CPUSeconds     int       `json:"cpu_seconds"`
	} `json:"capture"`
	Nodes []struct {
		Node      string `json:"node"`
		CPU       string `json:"cpu"`
		Heap      string `json:"heap"`
		Goroutine string `json:"goroutine"`
	} `json:"nodes"`
}

// ReadSingleNodeProfileEvidence strictly verifies the wrapper status, helper
// metadata, and every declared profile blob. Symlinks and unbounded documents
// are rejected. A structurally valid partial helper result is returned as
// incomplete so the step evaluator fails closed without changing attribution.
func ReadSingleNodeProfileEvidence(statusPath string) (ProfileEvidence, error) {
	evidenceDir := filepath.Dir(filepath.Clean(statusPath))
	if err := requireProfilePathComponents(evidenceDir, filepath.Base(statusPath), false); err != nil {
		return ProfileEvidence{}, fmt.Errorf("profile status path: %w", err)
	}
	statusData, err := os.ReadFile(filepath.Clean(statusPath))
	if err != nil {
		return ProfileEvidence{}, fmt.Errorf("profile status: %w", err)
	}
	evidence, err := ParseSingleNodeProfileEvidence(bytes.NewReader(statusData), func(relative string, maximum int64) ([]byte, error) {
		path := filepath.Join(evidenceDir, filepath.FromSlash(relative))
		info, statErr := os.Lstat(path)
		if os.IsNotExist(statErr) {
			return nil, ErrAuthenticatedArtifactMissing
		}
		if err := requireProfilePathComponents(evidenceDir, relative, false); err != nil {
			return nil, err
		}
		if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > maximum {
			return nil, errors.New("profile artifact is unsafe")
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, readErr
		}
		return body, nil
	})
	if err != nil {
		return ProfileEvidence{}, err
	}
	if evidence.Status == "not_triggered" {
		if _, statErr := os.Lstat(filepath.Join(evidenceDir, "threshold-pprof")); !os.IsNotExist(statErr) {
			return ProfileEvidence{}, errors.New("not-triggered profile status has capture artifacts")
		}
	}
	return evidence, nil
}

// ParseSingleNodeProfileEvidence validates profile status, metadata, and all
// declared blobs through authenticated readers. A partial capture may retain
// any subset of blobs declared complete; each such blob remains mandatory.
func ParseSingleNodeProfileEvidence(status io.Reader, readArtifact ArtifactReader) (ProfileEvidence, error) {
	if status == nil || readArtifact == nil {
		return ProfileEvidence{}, errors.New("profile authenticated readers are required")
	}
	var evidence ProfileEvidence
	if err := decodeBoundedProfileJSON(status, &evidence); err != nil {
		return evidence, fmt.Errorf("profile status: %w", err)
	}
	if evidence.Schema != ProfileEvidenceSchema || strings.TrimSpace(evidence.Reason) == "" {
		return ProfileEvidence{}, errors.New("profile status identity is invalid")
	}
	switch evidence.Status {
	case "not_triggered":
		if !evidence.EvidenceComplete || !evidence.CaptureValid || evidence.Triggered ||
			evidence.Metadata != "" || evidence.Trigger != nil || evidence.HelperExitStatus != nil ||
			evidence.Reason != "no_measured_threshold" {
			return ProfileEvidence{}, errors.New("not-triggered profile status is inconsistent")
		}
		for _, relative := range []string{
			"threshold-pprof/metadata.json", "threshold-pprof/profiles/node-1-cpu.pb.gz",
			"threshold-pprof/profiles/node-1-heap.pb.gz", "threshold-pprof/profiles/node-1-goroutine.txt",
		} {
			if _, err := readArtifact(relative, maximumProfileBlobBytes); err == nil || !errors.Is(err, ErrAuthenticatedArtifactMissing) {
				return ProfileEvidence{}, errors.New("not-triggered profile status has capture artifacts")
			}
		}
		return evidence, nil
	case "complete", "partial":
		if !evidence.Triggered || evidence.Trigger == nil || !validProfileTrigger(*evidence.Trigger) ||
			evidence.Metadata != "threshold-pprof/metadata.json" || evidence.HelperExitStatus == nil || *evidence.HelperExitStatus != 0 {
			return ProfileEvidence{}, errors.New("triggered profile status is inconsistent")
		}
		metadataData, err := readArtifact(evidence.Metadata, maximumProfileMetadataBytes)
		if err != nil {
			return ProfileEvidence{}, fmt.Errorf("profile metadata: %w", err)
		}
		var metadata thresholdProfileMetadata
		if err := decodeBoundedProfileJSON(bytes.NewReader(metadataData), &metadata); err != nil {
			return ProfileEvidence{}, fmt.Errorf("profile metadata: %w", err)
		}
		complete, err := validateSingleNodeProfileMetadata(metadata, evidence, readArtifact)
		if err != nil {
			return ProfileEvidence{}, err
		}
		if evidence.Status == "complete" {
			if !complete || !evidence.EvidenceComplete || !evidence.CaptureValid {
				return ProfileEvidence{}, errors.New("complete profile evidence is inconsistent")
			}
		} else if complete || evidence.EvidenceComplete || evidence.CaptureValid {
			return ProfileEvidence{}, errors.New("partial profile evidence is inconsistent")
		}
		return evidence, nil
	default:
		return ProfileEvidence{}, errors.New("profile status is invalid")
	}
}

// ProfileEvidenceMatchesQuery binds the closed profile decision to the first
// typed threshold derived from the same sealed lifecycle stream.
func ProfileEvidenceMatchesQuery(evidence ProfileEvidence, query ProfileThresholdQuery) bool {
	if evidence.Schema != ProfileEvidenceSchema || !query.EvidenceComplete ||
		query.Schema != ProfileThresholdQuerySchema || !evidence.EvidenceComplete {
		return false
	}
	if !query.Triggered {
		return evidence.Status == "not_triggered" && !evidence.Triggered && evidence.CaptureValid
	}
	return evidence.Status == "complete" && evidence.Triggered && evidence.CaptureValid &&
		evidence.Trigger != nil && profileTriggerEqual(*evidence.Trigger, query.Trigger)
}

func decodeBoundedProfileJSON(reader io.Reader, destination any) error {
	limited := &io.LimitedReader{R: reader, N: maximumProfileMetadataBytes + 1}
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("file has trailing JSON")
	}
	if limited.N <= 0 {
		return errors.New("file is oversized")
	}
	return nil
}

func validateSingleNodeProfileMetadata(metadata thresholdProfileMetadata, evidence ProfileEvidence, readArtifact ArtifactReader) (bool, error) {
	if evidence.Trigger == nil {
		return false, errors.New("profile trigger is missing")
	}
	trigger := *evidence.Trigger
	if metadata.Schema != "wukongim.local_threshold_pprof/v1" || metadata.Trigger.ObservedPhase != "measurement" ||
		metadata.Trigger.Kind != trigger.Kind || !metadata.Trigger.PreviousUTC.Equal(trigger.PreviousAt) ||
		!metadata.Trigger.CurrentUTC.Equal(trigger.CurrentAt) || metadata.Capture.Status != evidence.Status ||
		metadata.Capture.Valid != evidence.CaptureValid || metadata.Capture.Reason != evidence.Reason ||
		metadata.Capture.StartedAtUTC.IsZero() || metadata.Capture.CompletedAtUTC.Before(metadata.Capture.StartedAtUTC) ||
		metadata.Capture.CPUSeconds < 1 || metadata.Capture.CPUSeconds > 30 || len(metadata.Nodes) != 1 ||
		metadata.Nodes[0].Node != "node-1" {
		return false, errors.New("profile metadata identity is inconsistent")
	}
	allComplete := true
	profiles := []struct {
		status string
		path   string
	}{
		{metadata.Nodes[0].CPU, "threshold-pprof/profiles/node-1-cpu.pb.gz"},
		{metadata.Nodes[0].Heap, "threshold-pprof/profiles/node-1-heap.pb.gz"},
		{metadata.Nodes[0].Goroutine, "threshold-pprof/profiles/node-1-goroutine.txt"},
	}
	for _, profile := range profiles {
		switch profile.status {
		case "complete":
			body, err := readArtifact(profile.path, maximumProfileBlobBytes)
			if err != nil || len(body) == 0 {
				return false, errors.New("profile blob is missing or unsafe")
			}
		case "missing":
			allComplete = false
			if _, err := readArtifact(profile.path, maximumProfileBlobBytes); err == nil || !errors.Is(err, ErrAuthenticatedArtifactMissing) {
				return false, errors.New("profile blob contradicts missing metadata")
			}
		default:
			return false, errors.New("profile blob status is invalid")
		}
	}
	switch metadata.Capture.Status {
	case "complete":
		if metadata.Capture.Reason != "ok" || metadata.Capture.StartPhase != "measurement" ||
			metadata.Capture.EndPhase != "measurement" || !allComplete {
			return false, errors.New("complete profile metadata is inconsistent")
		}
		return true, nil
	case "partial":
		switch metadata.Capture.Reason {
		case "capture_start_missed_measurement":
			if (metadata.Capture.StartPhase != "drain" && metadata.Capture.StartPhase != "shutdown") ||
				metadata.Capture.EndPhase != metadata.Capture.StartPhase || allComplete {
				return false, errors.New("late partial profile phase is inconsistent")
			}
		case "phase_changed_during_capture":
			if metadata.Capture.StartPhase != "measurement" || metadata.Capture.EndPhase == "measurement" {
				return false, errors.New("partial profile phase is inconsistent")
			}
		case "profile_capture_missing", "interrupted", "internal_error":
			if metadata.Capture.StartPhase != "measurement" {
				return false, errors.New("partial profile start phase is inconsistent")
			}
		default:
			return false, errors.New("partial profile reason is invalid")
		}
		return false, nil
	default:
		return false, errors.New("profile metadata capture status is invalid")
	}
}

func requireProfilePathComponents(root, relative string, allowMissingFinal bool) error {
	if filepath.IsAbs(relative) || strings.Contains(relative, "\\") {
		return errors.New("profile path is unsafe")
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(relative)))
	if clean != relative || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return errors.New("profile path is unsafe")
	}
	current := filepath.Clean(root)
	parts := strings.Split(relative, "/")
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if allowMissingFinal && index == len(parts)-1 && os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("profile path contains a symlink")
		}
		if index < len(parts)-1 && !info.IsDir() {
			return errors.New("profile path parent is not a directory")
		}
	}
	return nil
}

func profileTriggerEqual(left, right ProfileThresholdTrigger) bool {
	return left.Kind == right.Kind && left.PreviousAt.Equal(right.PreviousAt) && left.CurrentAt.Equal(right.CurrentAt) &&
		left.AcknowledgedDelta == right.AcknowledgedDelta &&
		left.TerminalFailureDelta == right.TerminalFailureDelta &&
		left.IntervalSeconds == right.IntervalSeconds && left.ExpectedOffered == right.ExpectedOffered &&
		left.ActualOfferedPercent == right.ActualOfferedPercent
}

func profileEvidenceClosed(evidence ProfileEvidence) bool {
	if evidence.Schema != ProfileEvidenceSchema || !evidence.EvidenceComplete || !evidence.CaptureValid ||
		strings.TrimSpace(evidence.Reason) == "" {
		return false
	}
	switch evidence.Status {
	case "not_triggered":
		return !evidence.Triggered && evidence.Trigger == nil && evidence.Metadata == "" &&
			evidence.HelperExitStatus == nil && evidence.Reason == "no_measured_threshold"
	case "complete":
		return evidence.Triggered && evidence.Trigger != nil && validProfileTrigger(*evidence.Trigger) &&
			evidence.Metadata == "threshold-pprof/metadata.json" && evidence.HelperExitStatus != nil &&
			*evidence.HelperExitStatus == 0 && evidence.Reason == "ok"
	default:
		return false
	}
}

func validProfileTrigger(trigger ProfileThresholdTrigger) bool {
	if trigger.Kind != ProfileTriggerActualOfferedRatio && trigger.Kind != ProfileTriggerTerminalProductFailure {
		return false
	}
	return !trigger.PreviousAt.IsZero() && trigger.CurrentAt.After(trigger.PreviousAt)
}

// QueryFirstMeasuredProfileThreshold finds the first typed threshold crossed
// while SEND admission is owned by the measured run phase. It fails closed on
// identity, time, or counter regressions rather than manufacturing a trigger.
func QueryFirstMeasuredProfileThreshold(
	captures []LifecycleCapture,
	expectedRunID string,
	offeredSendQPS int,
	minimumThroughputPercent int,
) ProfileThresholdQuery {
	query := ProfileThresholdQuery{
		Schema: ProfileThresholdQuerySchema, RunID: strings.TrimSpace(expectedRunID),
		OfferedSendQPS: offeredSendQPS, MinimumThroughputPercent: minimumThroughputPercent,
		EvidenceComplete: true, Reason: "no_measured_threshold", LivePhase: ProfilePhaseUnknown,
	}
	if offeredSendQPS <= 0 || minimumThroughputPercent < 1 || minimumThroughputPercent > 100 {
		query.EvidenceComplete = false
		query.Reason = "invalid_threshold_contract"
		return query
	}

	var previous *CapturedStatus
	for index := range captures {
		capture := captures[index]
		if capture.Schema != LifecycleCaptureSchema || capture.SampledAt.IsZero() || capture.Error != "" ||
			capture.Status == nil || capture.Status.ObservedAt.IsZero() || capture.Status.Lifecycle == nil {
			query.EvidenceComplete = false
			query.Reason = "invalid_lifecycle_evidence"
			return query
		}
		status := capture.Status
		currentRunID := strings.TrimSpace(status.Assignment.RunID)
		currentAssignmentID := strings.TrimSpace(status.Assignment.AssignmentID)
		if currentRunID == "" && currentAssignmentID == "" {
			query.LivePhase = profilePhaseForStatus(status)
			continue
		}
		if currentRunID == "" || currentAssignmentID == "" {
			query.EvidenceComplete = false
			query.Reason = "assignment_identity_incomplete"
			return query
		}
		if query.RunID == "" {
			query.RunID = currentRunID
		}
		if currentRunID != query.RunID {
			if query.AssignmentID == "" && status.Phase == "stopped" && status.ActivePhase == "" {
				query.LivePhase = ProfilePhaseWarmup
				continue
			}
			query.EvidenceComplete = false
			query.Reason = "run_identity_mismatch"
			return query
		}
		if query.AssignmentID == "" {
			query.AssignmentID = currentAssignmentID
		} else if currentAssignmentID != query.AssignmentID {
			query.EvidenceComplete = false
			query.Reason = "assignment_identity_changed"
			return query
		}
		query.LivePhase = profilePhaseForStatus(status)
		if previous != nil {
			if status.ObservedAt.Before(previous.ObservedAt) ||
				(status.ObservedAt.Equal(previous.ObservedAt) && status.ActivePhase == previous.ActivePhase) {
				query.EvidenceComplete = false
				query.Reason = "timestamp_not_monotonic"
				return query
			}
			if !profileTrafficMonotonic(previous.Lifecycle.Traffic, status.Lifecycle.Traffic) {
				query.EvidenceComplete = false
				query.Reason = "counter_reset"
				return query
			}
			if !query.Triggered && previous.ActivePhase == "run" && status.ActivePhase == "run" {
				trigger, crossed := measuredProfileThreshold(*previous, *status, offeredSendQPS, minimumThroughputPercent)
				if crossed {
					query.Triggered = true
					query.Trigger = trigger
					query.Reason = "threshold_crossed"
				}
			}
		}
		previous = status
	}
	return query
}

func measuredProfileThreshold(
	previous, current CapturedStatus,
	offeredSendQPS int,
	minimumThroughputPercent int,
) (ProfileThresholdTrigger, bool) {
	previousTraffic, currentTraffic := previous.Lifecycle.Traffic, current.Lifecycle.Traffic
	terminalDelta := currentTraffic.TerminalErrors - previousTraffic.TerminalErrors
	correctnessDelta := currentTraffic.CorrectnessErrors - previousTraffic.CorrectnessErrors
	if ^uint64(0)-terminalDelta < correctnessDelta {
		return ProfileThresholdTrigger{}, false
	}
	trigger := ProfileThresholdTrigger{
		PreviousAt: previous.ObservedAt.UTC(), CurrentAt: current.ObservedAt.UTC(),
		AcknowledgedDelta:    currentTraffic.SendACKs - previousTraffic.SendACKs,
		TerminalFailureDelta: terminalDelta + correctnessDelta,
		IntervalSeconds:      current.ObservedAt.Sub(previous.ObservedAt).Seconds(),
	}
	if trigger.TerminalFailureDelta > 0 {
		trigger.Kind = ProfileTriggerTerminalProductFailure
		return trigger, true
	}
	if trigger.IntervalSeconds <= 0 {
		return ProfileThresholdTrigger{}, false
	}
	trigger.ExpectedOffered = float64(offeredSendQPS) * trigger.IntervalSeconds
	if trigger.ExpectedOffered <= 0 || math.IsInf(trigger.ExpectedOffered, 0) || math.IsNaN(trigger.ExpectedOffered) {
		return ProfileThresholdTrigger{}, false
	}
	trigger.ActualOfferedPercent = float64(trigger.AcknowledgedDelta) * 100 / trigger.ExpectedOffered
	if trigger.ActualOfferedPercent < float64(minimumThroughputPercent) {
		trigger.Kind = ProfileTriggerActualOfferedRatio
		return trigger, true
	}
	return ProfileThresholdTrigger{}, false
}

func profilePhaseForStatus(status *CapturedStatus) ProfilePhase {
	if status == nil {
		return ProfilePhaseUnknown
	}
	switch status.ActivePhase {
	case "run":
		return ProfilePhaseMeasurement
	case "cooldown":
		return ProfilePhaseDrain
	case "warmup", "prepare", "connect":
		return ProfilePhaseWarmup
	}
	switch status.Phase {
	case "stopped":
		return ProfilePhaseShutdown
	case "run", "cooldown":
		return ProfilePhaseDrain
	case "assigned", "prepare", "connect", "warmup":
		return ProfilePhaseWarmup
	default:
		return ProfilePhaseUnknown
	}
}

func profileTrafficMonotonic(previous, current TrafficEvidence) bool {
	return current.Planned >= previous.Planned &&
		current.Dispatched >= previous.Dispatched &&
		current.LogicalSent >= previous.LogicalSent &&
		current.SendAttempts >= previous.SendAttempts &&
		current.SendACKs >= previous.SendACKs &&
		current.TerminalErrors >= previous.TerminalErrors &&
		current.CorrectnessErrors >= previous.CorrectnessErrors &&
		current.RetryAttempts >= previous.RetryAttempts &&
		current.RetryExhausted >= previous.RetryExhausted
}

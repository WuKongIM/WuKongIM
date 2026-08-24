package worker

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	// ErrTerminalCutNotReady means the exact assignment has not completed its
	// admitted and receive drain, so no external evidence may be bound yet.
	ErrTerminalCutNotReady = errors.New("terminal cut is not ready")
	// ErrTerminalCutAlreadyAcknowledged prevents a second evidence payload from
	// replacing the first exact-generation binding.
	ErrTerminalCutAlreadyAcknowledged = errors.New("terminal cut is already acknowledged")
)

// TerminalCutRequest binds one externally captured pre-close Prometheus cut
// to the exact active worker assignment. It deliberately contains no paths,
// URLs, metric names, or other unbounded operator input.
type TerminalCutRequest struct {
	RunID                string    `json:"run_id"`
	AssignmentID         string    `json:"assignment_id"`
	ObservedAt           time.Time `json:"observed_at"`
	ReceiveDrainSHA256   string    `json:"receive_drain_sha256"`
	ProductMetricsSHA256 string    `json:"product_metrics_sha256"`
	StorageOverlapSHA256 string    `json:"storage_overlap_sha256"`
}

// TerminalCutBinding is the immutable acknowledgement retained in the
// stopped lifecycle proof.
type TerminalCutBinding struct {
	RunID                string    `json:"run_id"`
	AssignmentID         string    `json:"assignment_id"`
	ReadyAt              time.Time `json:"ready_at"`
	DeadlineAt           time.Time `json:"deadline_at"`
	ObservedAt           time.Time `json:"observed_at"`
	ReceiveDrainSHA256   string    `json:"receive_drain_sha256"`
	ProductMetricsSHA256 string    `json:"product_metrics_sha256"`
	StorageOverlapSHA256 string    `json:"storage_overlap_sha256"`
	AcknowledgedAt       time.Time `json:"acknowledged_at"`
}

// TerminalCutStatus is the bounded runner-owned external barrier state.
type TerminalCutStatus struct {
	Required   bool                `json:"required"`
	Ready      bool                `json:"ready"`
	ReadyAt    time.Time           `json:"ready_at,omitempty"`
	DeadlineAt time.Time           `json:"deadline_at,omitempty"`
	Binding    *TerminalCutBinding `json:"binding,omitempty"`
}

// TerminalCutCoordinator is implemented by runners that can pause cooldown
// after internal convergence and accept one exact external evidence binding.
type TerminalCutCoordinator interface {
	TerminalCutStatus() TerminalCutStatus
	AcknowledgeTerminalCut(TerminalCutRequest) (TerminalCutBinding, error)
}

type terminalCutBarrier struct {
	mu           sync.Mutex
	identity     assignmentIdentity
	required     bool
	ready        bool
	accepting    bool
	readyAt      time.Time
	deadlineAt   time.Time
	binding      *TerminalCutBinding
	acknowledged chan struct{}
}

func (b *terminalCutBarrier) begin(assignment Assignment) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.identity = assignmentIdentity{runID: strings.TrimSpace(assignment.RunID), assignmentID: strings.TrimSpace(assignment.AssignmentID)}
	b.required = assignment.Scenario.Run.ExternalTerminalCut
	b.ready = false
	b.accepting = false
	b.readyAt = time.Time{}
	b.deadlineAt = time.Time{}
	b.binding = nil
	b.acknowledged = make(chan struct{})
}

func (b *terminalCutBarrier) status() TerminalCutStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	status := TerminalCutStatus{Required: b.required, Ready: b.ready, ReadyAt: b.readyAt, DeadlineAt: b.deadlineAt}
	if b.binding != nil {
		binding := *b.binding
		status.Binding = &binding
	}
	return status
}

func (b *terminalCutBarrier) wait(ctx context.Context, assignment Assignment) error {
	if !assignment.Scenario.Run.ExternalTerminalCut {
		return nil
	}
	expected, err := requiredAssignmentIdentity(assignment.RunID, assignment.AssignmentID)
	if err != nil {
		return err
	}
	b.mu.Lock()
	if b.identity != expected || !b.required {
		b.mu.Unlock()
		return fmt.Errorf("terminal cut assignment is not active")
	}
	if b.binding != nil {
		b.mu.Unlock()
		return nil
	}
	if !b.ready {
		deadline, ok := ctx.Deadline()
		if !ok || deadline.IsZero() {
			b.mu.Unlock()
			return fmt.Errorf("external terminal cut requires a bounded cooldown deadline")
		}
		b.ready = true
		b.accepting = true
		b.readyAt = time.Now().UTC()
		b.deadlineAt = deadline.UTC()
	}
	acknowledged := b.acknowledged
	b.mu.Unlock()

	select {
	case <-acknowledged:
		return nil
	case <-ctx.Done():
		b.mu.Lock()
		if b.binding != nil {
			b.mu.Unlock()
			return nil
		}
		b.accepting = false
		b.mu.Unlock()
		return fmt.Errorf("external terminal cut acknowledgement failed: %w", ctx.Err())
	}
}

func (b *terminalCutBarrier) acknowledge(request TerminalCutRequest) (TerminalCutBinding, error) {
	expected, err := requiredAssignmentIdentity(request.RunID, request.AssignmentID)
	if err != nil {
		return TerminalCutBinding{}, err
	}
	now := time.Now().UTC()
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.identity != expected {
		return TerminalCutBinding{}, assignmentIdentityConflict(Assignment{RunID: b.identity.runID, AssignmentID: b.identity.assignmentID}, expected.runID, expected.assignmentID)
	}
	if !b.required || !b.ready || !b.accepting {
		return TerminalCutBinding{}, ErrTerminalCutNotReady
	}
	if b.binding != nil {
		if terminalCutRequestMatchesBinding(request, *b.binding) {
			return *b.binding, nil
		}
		return TerminalCutBinding{}, ErrTerminalCutAlreadyAcknowledged
	}
	if err := validateTerminalCutRequest(request, b.readyAt, b.deadlineAt, now); err != nil {
		return TerminalCutBinding{}, err
	}
	binding := TerminalCutBinding{
		RunID: expected.runID, AssignmentID: expected.assignmentID,
		ReadyAt: b.readyAt, ObservedAt: request.ObservedAt.UTC(),
		DeadlineAt:           b.deadlineAt,
		ReceiveDrainSHA256:   request.ReceiveDrainSHA256,
		ProductMetricsSHA256: request.ProductMetricsSHA256,
		StorageOverlapSHA256: request.StorageOverlapSHA256,
		AcknowledgedAt:       now,
	}
	b.binding = &binding
	b.accepting = false
	close(b.acknowledged)
	return binding, nil
}

func validateTerminalCutRequest(request TerminalCutRequest, readyAt, deadlineAt, now time.Time) error {
	if _, err := requiredAssignmentIdentity(request.RunID, request.AssignmentID); err != nil {
		return err
	}
	if request.ObservedAt.IsZero() {
		return fmt.Errorf("observed_at is required")
	}
	_, offset := request.ObservedAt.Zone()
	if offset != 0 {
		return fmt.Errorf("observed_at must use UTC")
	}
	if readyAt.IsZero() || request.ObservedAt.Before(readyAt) {
		return fmt.Errorf("observed_at precedes terminal_cut_ready")
	}
	if request.ObservedAt.After(now) {
		return fmt.Errorf("observed_at is in the future")
	}
	if deadlineAt.IsZero() || request.ObservedAt.After(deadlineAt) || now.After(deadlineAt) {
		return fmt.Errorf("terminal cut deadline has elapsed")
	}
	if !validTerminalCutDigest(request.ProductMetricsSHA256) {
		return fmt.Errorf("product_metrics_sha256 must be 64 lowercase hexadecimal characters")
	}
	if !validTerminalCutDigest(request.ReceiveDrainSHA256) {
		return fmt.Errorf("receive_drain_sha256 must be 64 lowercase hexadecimal characters")
	}
	if !validTerminalCutDigest(request.StorageOverlapSHA256) {
		return fmt.Errorf("storage_overlap_sha256 must be 64 lowercase hexadecimal characters")
	}
	return nil
}

func terminalCutRequestMatchesBinding(request TerminalCutRequest, binding TerminalCutBinding) bool {
	return strings.TrimSpace(request.RunID) == binding.RunID &&
		strings.TrimSpace(request.AssignmentID) == binding.AssignmentID &&
		request.ObservedAt.Equal(binding.ObservedAt) &&
		request.ReceiveDrainSHA256 == binding.ReceiveDrainSHA256 &&
		request.ProductMetricsSHA256 == binding.ProductMetricsSHA256 &&
		request.StorageOverlapSHA256 == binding.StorageOverlapSHA256
}

func validTerminalCutDigest(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func validTerminalCutBinding(binding TerminalCutBinding, expected assignmentIdentity) bool {
	if binding.RunID != expected.runID || binding.AssignmentID != expected.assignmentID ||
		binding.ReadyAt.IsZero() || binding.DeadlineAt.IsZero() || binding.ObservedAt.IsZero() || binding.AcknowledgedAt.IsZero() ||
		binding.ObservedAt.Before(binding.ReadyAt) || binding.AcknowledgedAt.Before(binding.ObservedAt) ||
		binding.AcknowledgedAt.After(binding.DeadlineAt) ||
		!validTerminalCutDigest(binding.ReceiveDrainSHA256) || !validTerminalCutDigest(binding.ProductMetricsSHA256) || !validTerminalCutDigest(binding.StorageOverlapSHA256) {
		return false
	}
	observedOffset := 0
	_, observedOffset = binding.ObservedAt.Zone()
	ackOffset := 0
	_, ackOffset = binding.AcknowledgedAt.Zone()
	readyOffset := 0
	_, readyOffset = binding.ReadyAt.Zone()
	deadlineOffset := 0
	_, deadlineOffset = binding.DeadlineAt.Zone()
	return readyOffset == 0 && deadlineOffset == 0 && observedOffset == 0 && ackOffset == 0
}

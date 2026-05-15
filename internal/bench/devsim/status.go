package devsim

import (
	"sync"
	"time"
)

// State is the coarse lifecycle state exposed by the simulator status API.
type State string

const (
	// StateStarting means the process is alive but not fully connected yet.
	StateStarting State = "starting"
	// StateWaiting means the simulator is waiting for target readiness.
	StateWaiting State = "waiting"
	// StateRunning means simulated users are connected and traffic windows are running.
	StateRunning State = "running"
	// StateRetrying means the simulator hit a runtime error and is backing off.
	StateRetrying State = "retrying"
	// StateStopped means the simulator is shutting down or has stopped.
	StateStopped State = "stopped"
)

// Snapshot is a JSON-friendly immutable view of simulator status.
type Snapshot struct {
	// State is the simulator lifecycle state.
	State State `json:"state"`
	// RunID identifies the active deterministic simulator run.
	RunID string `json:"run_id"`
	// ConnectedUsers is the number of users connected in the latest successful run.
	ConnectedUsers int `json:"connected_users"`
	// PersonChannels is the configured number of person channels.
	PersonChannels int `json:"person_channels"`
	// GroupChannels is the configured number of group channels.
	GroupChannels int `json:"group_channels"`
	// MessagesSent is the cumulative count of sent messages observed by the simulator.
	MessagesSent uint64 `json:"messages_sent"`
	// SendErrors is the cumulative count of send-side failures.
	SendErrors uint64 `json:"send_errors"`
	// RecvErrors is the cumulative count of receive verification failures.
	RecvErrors uint64 `json:"recv_errors"`
	// LastError records the latest runtime error.
	LastError string `json:"last_error"`
	// LastTransitionAt is the time when State last changed.
	LastTransitionAt time.Time `json:"last_transition_at"`
}

// Status stores thread-safe simulator status counters and lifecycle state.
type Status struct {
	mu       sync.Mutex
	snapshot Snapshot
	now      func() time.Time
}

// NewStatus creates a simulator status model in the starting state.
func NewStatus(runID string) *Status {
	s := &Status{now: time.Now}
	s.snapshot = Snapshot{State: StateStarting, RunID: runID, LastTransitionAt: s.now()}
	return s
}

// Snapshot returns a stable copy of current status.
func (s *Status) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot
}

// SetState updates the simulator lifecycle state.
func (s *Status) SetState(state State) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setStateLocked(state)
}

// SetRunning records connected topology and marks the simulator running.
func (s *Status) SetRunning(connectedUsers, personChannels, groupChannels int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot.ConnectedUsers = connectedUsers
	s.snapshot.PersonChannels = personChannels
	s.snapshot.GroupChannels = groupChannels
	s.snapshot.LastError = ""
	s.setStateLocked(StateRunning)
}

// AddMessagesSent increments the cumulative sent-message counter.
func (s *Status) AddMessagesSent(delta uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot.MessagesSent += delta
}

// AddSendErrors increments the cumulative send-error counter.
func (s *Status) AddSendErrors(delta uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot.SendErrors += delta
}

// AddRecvErrors increments the cumulative receive-error counter.
func (s *Status) AddRecvErrors(delta uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot.RecvErrors += delta
}

// SetLastError stores the latest runtime error message.
func (s *Status) SetLastError(message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot.LastError = message
}

func (s *Status) setStateLocked(state State) {
	if s.snapshot.State == state {
		return
	}
	s.snapshot.State = state
	s.snapshot.LastTransitionAt = s.now()
}

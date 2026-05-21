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
	// ConnectedUsers is the configured steady-state online user pool for the run.
	ConnectedUsers int `json:"connected_users"`
	// ActiveUsers is the latest sampled number of currently connected users.
	ActiveUsers int `json:"active_users"`
	// ReconnectedUsers is the cumulative number of users that were repaired and reconnected.
	ReconnectedUsers uint64 `json:"reconnected_users"`
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
	// Config records the effective simulator configuration used for the run.
	Config *ConfigSnapshot `json:"config,omitempty"`
	// LastTransitionAt is the time when State last changed.
	LastTransitionAt time.Time `json:"last_transition_at"`
}

// ConfigSnapshot records the effective triage-relevant simulator configuration.
type ConfigSnapshot struct {
	// TargetAPIAddrs are the target HTTP API addresses used for readiness and bench routes.
	TargetAPIAddrs []string `json:"target_api_addrs"`
	// GatewayTCPAddrs are the target WKProto gateway addresses used for traffic.
	GatewayTCPAddrs []string `json:"gateway_tcp_addrs"`
	// UIDPrefix is the generated simulator user ID prefix.
	UIDPrefix string `json:"uid_prefix"`
	// DevicePrefix is the generated simulator device ID prefix.
	DevicePrefix string `json:"device_prefix"`
	// ClientMsgPrefix is the generated client message prefix.
	ClientMsgPrefix string `json:"client_msg_prefix"`
	// TokenMode describes how user tokens are prepared.
	TokenMode string `json:"token_mode"`
	// TotalUsers is the generated online user pool size.
	TotalUsers int `json:"total_users"`
	// ConnectRate is the simulator connection rate formatted as a per-second string.
	ConnectRate string `json:"connect_rate"`
	// PersonChannels is the configured number of one-to-one channels.
	PersonChannels int `json:"person_channels"`
	// GroupChannels is the configured number of group channels.
	GroupChannels int `json:"group_channels"`
	// GroupMembers is the configured number of members per generated group channel.
	GroupMembers int `json:"group_members"`
	// PayloadSizeBytes is the deterministic message payload size.
	PayloadSizeBytes int `json:"payload_size_bytes"`
	// PersonRatePerChannel is the person-channel send rate formatted as a per-second string.
	PersonRatePerChannel string `json:"person_rate_per_channel"`
	// GroupRatePerChannel is the group-channel send rate formatted as a per-second string.
	GroupRatePerChannel string `json:"group_rate_per_channel"`
	// Concurrency bounds in-flight send operations per traffic stream.
	Concurrency int `json:"concurrency"`
	// VerifyRecv is the receive verification mode used by the simulator.
	VerifyRecv string `json:"verify_recv"`
	// Warmup is the reduced-rate warmup duration formatted as a string.
	Warmup string `json:"warmup"`
	// Window is the active traffic window duration formatted as a string.
	Window string `json:"window"`
	// Cooldown is the drain duration between run windows formatted as a string.
	Cooldown string `json:"cooldown"`
	// ReadinessTimeout is the target readiness timeout formatted as a string.
	ReadinessTimeout string `json:"readiness_timeout"`
	// RestartBackoff is the reconnect backoff duration formatted as a string.
	RestartBackoff string `json:"restart_backoff"`
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
	return cloneSnapshot(s.snapshot)
}

// SetConfig records the effective simulator configuration exposed by /status.
func (s *Status) SetConfig(cfg ConfigSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot.Config = cloneConfigSnapshot(&cfg)
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
	s.snapshot.ActiveUsers = connectedUsers
	s.snapshot.PersonChannels = personChannels
	s.snapshot.GroupChannels = groupChannels
	s.snapshot.LastError = ""
	s.setStateLocked(StateRunning)
}

// SetConnectionStats records the latest sampled online connection state.
func (s *Status) SetConnectionStats(activeUsers int, reconnectedUsers uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot.ActiveUsers = activeUsers
	s.snapshot.ReconnectedUsers = reconnectedUsers
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

func cloneSnapshot(snapshot Snapshot) Snapshot {
	snapshot.Config = cloneConfigSnapshot(snapshot.Config)
	return snapshot
}

func cloneConfigSnapshot(snapshot *ConfigSnapshot) *ConfigSnapshot {
	if snapshot == nil {
		return nil
	}
	out := *snapshot
	out.TargetAPIAddrs = append([]string(nil), snapshot.TargetAPIAddrs...)
	out.GatewayTCPAddrs = append([]string(nil), snapshot.GatewayTCPAddrs...)
	return &out
}

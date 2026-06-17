package sim

import (
	"sync"
	"time"
)

// State is a simulation lifecycle state exposed by the status API.
type State string

const (
	stateStarting     State = "starting"
	statePreflighting State = "preflighting"
	stateSettingUp    State = "setting_up"
	stateConnecting   State = "connecting"
	stateRunning      State = "running"
	stateRetrying     State = "retrying"
	stateStopping     State = "stopping"
	stateStopped      State = "stopped"
)

// Snapshot describes the current wkcli simulation status.
type Snapshot struct {
	// State is the current simulation lifecycle state.
	State State `json:"state"`
	// RunID identifies the simulation run.
	RunID string `json:"run_id"`
	// TargetServers are normalized HTTP API targets.
	TargetServers []string `json:"target_servers"`
	// GatewayTCPAddrs are WKProto TCP gateway addresses used by clients.
	GatewayTCPAddrs []string `json:"gateway_tcp_addrs"`
	// Users is the configured number of simulated users.
	Users int `json:"users"`
	// ActiveUsers is the number of connected simulated users.
	ActiveUsers int `json:"active_users"`
	// Groups is the configured number of simulated group channels.
	Groups int `json:"groups"`
	// GroupMembers is the configured number of members per group.
	GroupMembers int `json:"group_members"`
	// MessagesSent is the number of SEND attempts accepted by the client layer.
	MessagesSent uint64 `json:"messages_sent"`
	// SendErrors is the number of failed SEND attempts.
	SendErrors uint64 `json:"send_errors"`
	// RecvMessages is the number of received messages observed by simulated clients.
	RecvMessages uint64 `json:"recv_messages"`
	// RecvDropped is the number of received messages dropped by local accounting.
	RecvDropped uint64 `json:"recv_dropped"`
	// Reconnects is the number of client reconnects.
	Reconnects uint64 `json:"reconnects"`
	// LastError is the most recent send or runtime error message.
	LastError string `json:"last_error"`
	// LastTransitionAt is when State last changed.
	LastTransitionAt time.Time `json:"last_transition_at"`
}

// statusModel stores simulation status behind a mutex for concurrent readers.
type statusModel struct {
	// mu protects data from concurrent simulation and HTTP access.
	mu sync.Mutex
	// data is the mutable status snapshot copied for readers.
	data Snapshot
}

// newStatus creates the initial status model for one simulation run.
func newStatus(runID string) *statusModel {
	return &statusModel{
		data: Snapshot{
			State:            stateStarting,
			RunID:            runID,
			LastTransitionAt: time.Now(),
		},
	}
}

// snapshot returns a copy of the current status.
func (m *statusModel) snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneSnapshot(m.data)
}

// setState records a lifecycle state transition.
func (m *statusModel) setState(state State) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data.State == state {
		return
	}
	m.data.State = state
	m.data.LastTransitionAt = time.Now()
}

// setTarget records the resolved HTTP API servers and gateway addresses.
func (m *statusModel) setTarget(servers []string, gateways []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.TargetServers = append([]string(nil), servers...)
	m.data.GatewayTCPAddrs = append([]string(nil), gateways...)
}

// setTopology records configured simulation topology counts.
func (m *statusModel) setTopology(users int, groups int, groupMembers int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.Users = users
	m.data.ActiveUsers = users
	m.data.Groups = groups
	m.data.GroupMembers = groupMembers
}

// addMessagesSent increments the successful SEND counter.
func (m *statusModel) addMessagesSent(n uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.MessagesSent += n
}

// addSendErrors increments send failures and stores the latest error text.
func (m *statusModel) addSendErrors(n uint64, lastError string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.SendErrors += n
	m.data.LastError = lastError
}

// addRecv increments receive accounting counters.
func (m *statusModel) addRecv(messages uint64, dropped uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.RecvMessages += messages
	m.data.RecvDropped += dropped
}

// addReconnect increments reconnect accounting and records the latest error when present.
func (m *statusModel) addReconnect(lastError string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.Reconnects++
	if lastError != "" {
		m.data.LastError = lastError
	}
}

// cloneSnapshot copies slice fields so callers cannot mutate model state.
func cloneSnapshot(snapshot Snapshot) Snapshot {
	snapshot.TargetServers = append([]string(nil), snapshot.TargetServers...)
	snapshot.GatewayTCPAddrs = append([]string(nil), snapshot.GatewayTCPAddrs...)
	return snapshot
}

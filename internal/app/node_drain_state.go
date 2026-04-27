package app

import (
	"fmt"
	"sync"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
)

const defaultNodeDrainStateStaleAfter = 15 * time.Second

// nodeDrainState caches the local node drain status reported by the controller observer.
type nodeDrainState struct {
	localNodeID uint64
	staleAfter  time.Duration
	now         func() time.Time

	mu         sync.RWMutex
	status     controllermeta.NodeStatus
	observedAt time.Time
	known      bool
}

func newNodeDrainState(localNodeID uint64, now func() time.Time) *nodeDrainState {
	if now == nil {
		now = time.Now
	}
	return &nodeDrainState{
		localNodeID: localNodeID,
		staleAfter:  defaultNodeDrainStateStaleAfter,
		now:         now,
	}
}

// Observe records controller status changes for the local node only.
func (s *nodeDrainState) Observe(nodeID uint64, status controllermeta.NodeStatus) {
	if s == nil || nodeID == 0 || nodeID != s.localNodeID {
		return
	}
	s.mu.Lock()
	s.status = status
	s.observedAt = s.now()
	s.known = true
	s.mu.Unlock()
}

// Ready reports whether the local node is known, fresh, alive, and not draining.
func (s *nodeDrainState) Ready() (bool, string) {
	if s == nil {
		return false, "unknown"
	}
	s.mu.RLock()
	status := s.status
	observedAt := s.observedAt
	known := s.known
	s.mu.RUnlock()
	if !known || observedAt.IsZero() {
		return false, "unknown"
	}
	if s.staleAfter > 0 && s.now().Sub(observedAt) > s.staleAfter {
		return false, "stale"
	}
	switch status {
	case controllermeta.NodeStatusAlive:
		return true, "alive"
	case controllermeta.NodeStatusDraining:
		return false, "draining"
	case controllermeta.NodeStatusSuspect:
		return false, "suspect"
	case controllermeta.NodeStatusDead:
		return false, "dead"
	default:
		return false, fmt.Sprintf("status_%d", status)
	}
}

// Draining reports whether the fresh local-node observation is Draining.
func (s *nodeDrainState) Draining() bool {
	if s == nil {
		return false
	}
	ready, reason := s.Ready()
	return !ready && reason == "draining"
}

// KnownNotDraining reports whether readiness has a fresh non-draining local-node status.
func (s *nodeDrainState) KnownNotDraining() bool {
	ready, _ := s.Ready()
	return ready
}

func (a *App) observeNodeStatusChange(nodeID uint64, status controllermeta.NodeStatus) {
	if a == nil {
		return
	}
	if a.nodeDrainState != nil {
		a.nodeDrainState.Observe(nodeID, status)
	}
	if nodeID == a.cfg.Node.ID {
		a.updateGatewayAdmissionFromDrainState()
	}
}

func (a *App) updateGatewayAdmissionFromDrainState() {
	if a == nil || a.gateway == nil {
		return
	}
	accepting := false
	if a.nodeDrainState != nil {
		accepting = a.nodeDrainState.KnownNotDraining()
	}
	a.gateway.SetAcceptingNewSessions(accepting)
}

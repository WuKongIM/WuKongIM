package cluster

import (
	"context"
	"errors"
	"sync"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	slotcontroller "github.com/WuKongIM/WuKongIM/pkg/controller/plane"
)

type healthTimer interface {
	Stop() bool
}

type nodeHealthSchedulerConfig struct {
	suspectTimeout time.Duration
	deadTimeout    time.Duration
	now            func() time.Time
	afterFunc      func(time.Duration, func()) healthTimer
	loadNodes      func(context.Context) ([]controllermeta.ClusterNode, error)
	loadNode       func(context.Context, uint64) (controllermeta.ClusterNode, error)
	propose        func(context.Context, slotcontroller.Command) error
}

type nodeHealthScheduler struct {
	cfg nodeHealthSchedulerConfig

	mu         sync.Mutex
	nodes      map[uint64]*nodeHealthState
	nodeMirror map[uint64]controllermeta.ClusterNode
}

type nodeHealthState struct {
	observation   nodeObservation
	generation    uint64
	suspectAt     time.Time
	deadAt        time.Time
	suspectTimer  healthTimer
	deadTimer     healthTimer
	pendingStatus *controllermeta.NodeStatus
}

func newNodeHealthScheduler(cfg nodeHealthSchedulerConfig) *nodeHealthScheduler {
	if cfg.now == nil {
		cfg.now = time.Now
	}
	if cfg.afterFunc == nil {
		cfg.afterFunc = func(delay time.Duration, fn func()) healthTimer {
			if delay < 0 {
				delay = 0
			}
			return time.AfterFunc(delay, fn)
		}
	}
	return &nodeHealthScheduler{
		cfg:        cfg,
		nodes:      make(map[uint64]*nodeHealthState),
		nodeMirror: make(map[uint64]controllermeta.ClusterNode),
	}
}

func (s *nodeHealthScheduler) observe(observation nodeObservation) {
	if s == nil || observation.NodeID == 0 {
		return
	}

	s.mu.Lock()
	state := s.ensureNodeLocked(observation.NodeID)
	if !state.observation.ObservedAt.IsZero() && observation.ObservedAt.Before(state.observation.ObservedAt) {
		s.mu.Unlock()
		return
	}

	state.observation = observation
	state.generation++
	state.suspectAt = observation.ObservedAt.Add(s.cfg.suspectTimeout)
	state.deadAt = observation.ObservedAt.Add(s.cfg.deadTimeout)
	if state.suspectTimer != nil {
		state.suspectTimer.Stop()
	}
	if state.deadTimer != nil {
		state.deadTimer.Stop()
	}
	generation := state.generation
	if s.cfg.suspectTimeout > 0 {
		suspectAt := state.suspectAt
		delay := suspectAt.Sub(s.cfg.now())
		state.suspectTimer = s.cfg.afterFunc(delay, func() {
			s.handleDeadline(observation.NodeID, generation, controllermeta.NodeStatusSuspect, suspectAt)
		})
	}
	if s.cfg.deadTimeout > 0 {
		deadAt := state.deadAt
		delay := deadAt.Sub(s.cfg.now())
		state.deadTimer = s.cfg.afterFunc(delay, func() {
			s.handleDeadline(observation.NodeID, generation, controllermeta.NodeStatusDead, deadAt)
		})
	}
	s.mu.Unlock()

	s.proposeStatusTransition(observation, controllermeta.NodeStatusAlive, observation.ObservedAt)
}

func (s *nodeHealthScheduler) handleDeadline(nodeID uint64, generation uint64, targetStatus controllermeta.NodeStatus, evaluatedAt time.Time) {
	if s == nil || nodeID == 0 {
		return
	}

	s.mu.Lock()
	state, ok := s.nodes[nodeID]
	if !ok || state.generation != generation {
		s.mu.Unlock()
		return
	}
	switch targetStatus {
	case controllermeta.NodeStatusSuspect:
		if !state.suspectAt.Equal(evaluatedAt) {
			s.mu.Unlock()
			return
		}
	case controllermeta.NodeStatusDead:
		if !state.deadAt.Equal(evaluatedAt) {
			s.mu.Unlock()
			return
		}
	default:
		s.mu.Unlock()
		return
	}
	observation := state.observation
	s.mu.Unlock()

	s.proposeStatusTransition(observation, targetStatus, evaluatedAt)
}

func (s *nodeHealthScheduler) reset() {
	if s == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, state := range s.nodes {
		if state.suspectTimer != nil {
			state.suspectTimer.Stop()
		}
		if state.deadTimer != nil {
			state.deadTimer.Stop()
		}
	}
	s.nodes = make(map[uint64]*nodeHealthState)
	s.nodeMirror = make(map[uint64]controllermeta.ClusterNode)
}

func (s *nodeHealthScheduler) handleCommittedCommand(cmd slotcontroller.Command) {
	if s == nil || s.cfg.loadNode == nil {
		return
	}

	switch cmd.Kind {
	case slotcontroller.CommandKindNodeStatusUpdate:
		if cmd.NodeStatusUpdate == nil {
			return
		}
		for _, transition := range cmd.NodeStatusUpdate.Transitions {
			s.refreshNodeFromStore(context.Background(), transition.NodeID)
		}
	case slotcontroller.CommandKindOperatorRequest:
		if cmd.Op == nil {
			return
		}
		s.refreshNodeFromStore(context.Background(), cmd.Op.NodeID)
	}
}

func (s *nodeHealthScheduler) ensureNodeLocked(nodeID uint64) *nodeHealthState {
	state, ok := s.nodes[nodeID]
	if ok {
		return state
	}
	state = &nodeHealthState{}
	s.nodes[nodeID] = state
	return state
}

func (s *nodeHealthScheduler) mirrorNode(node controllermeta.ClusterNode) {
	if s == nil || node.NodeID == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.nodeMirror[node.NodeID] = node
}

func (s *nodeHealthScheduler) mirroredNode(nodeID uint64) (controllermeta.ClusterNode, bool) {
	if s == nil || nodeID == 0 {
		return controllermeta.ClusterNode{}, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	node, ok := s.nodeMirror[nodeID]
	return node, ok
}

func (s *nodeHealthScheduler) loadNodeForTransition(nodeID uint64) (controllermeta.ClusterNode, error) {
	if node, ok := s.mirroredNode(nodeID); ok {
		return node, nil
	}
	if s == nil || s.cfg.loadNode == nil {
		return controllermeta.ClusterNode{}, controllermeta.ErrClosed
	}
	node, err := s.cfg.loadNode(context.Background(), nodeID)
	if err == nil {
		s.mirrorNode(node)
	}
	return node, err
}

func (s *nodeHealthScheduler) refreshNodeFromStore(ctx context.Context, nodeID uint64) {
	if s == nil || s.cfg.loadNode == nil || nodeID == 0 {
		return
	}

	node, err := s.cfg.loadNode(ctx, nodeID)
	if errors.Is(err, controllermeta.ErrNotFound) {
		s.mu.Lock()
		delete(s.nodeMirror, nodeID)
		s.mu.Unlock()
		return
	}
	if err != nil {
		return
	}
	s.mirrorNode(node)
}

func (s *nodeHealthScheduler) reloadAllNodes(ctx context.Context) error {
	if s == nil || s.cfg.loadNodes == nil {
		return nil
	}

	nodes, err := s.cfg.loadNodes(ctx)
	if err != nil {
		return err
	}

	nodeMirror := make(map[uint64]controllermeta.ClusterNode, len(nodes))
	for _, node := range nodes {
		if node.NodeID == 0 {
			continue
		}
		nodeMirror[node.NodeID] = node
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.nodeMirror = nodeMirror
	return nil
}

type healthDeadlineSpec struct {
	nodeID     uint64
	generation uint64
	status     controllermeta.NodeStatus
	evaluated  time.Time
}

func (s *nodeHealthScheduler) primeFromNodes(nodes []controllermeta.ClusterNode) {
	if s == nil {
		return
	}

	now := s.cfg.now()
	nodeMirror := make(map[uint64]controllermeta.ClusterNode, len(nodes))
	deadlines := make([]healthDeadlineSpec, 0, len(nodes)*2)

	s.mu.Lock()
	for _, state := range s.nodes {
		if state.suspectTimer != nil {
			state.suspectTimer.Stop()
		}
		if state.deadTimer != nil {
			state.deadTimer.Stop()
		}
	}
	s.nodes = make(map[uint64]*nodeHealthState, len(nodes))
	for _, node := range nodes {
		if node.NodeID == 0 {
			continue
		}
		nodeMirror[node.NodeID] = node
		if node.Status != controllermeta.NodeStatusAlive && node.Status != controllermeta.NodeStatusSuspect {
			continue
		}
		observedAt := node.LastHeartbeatAt
		if observedAt.IsZero() {
			observedAt = now
		}
		state := &nodeHealthState{
			observation: nodeObservation{
				NodeID:         node.NodeID,
				Addr:           node.Addr,
				ObservedAt:     observedAt,
				CapacityWeight: node.CapacityWeight,
			},
			generation: 1,
		}
		if node.Status == controllermeta.NodeStatusAlive && s.cfg.suspectTimeout > 0 {
			state.suspectAt = observedAt.Add(s.cfg.suspectTimeout)
			deadlines = append(deadlines, healthDeadlineSpec{
				nodeID:     node.NodeID,
				generation: state.generation,
				status:     controllermeta.NodeStatusSuspect,
				evaluated:  state.suspectAt,
			})
		}
		if s.cfg.deadTimeout > 0 {
			state.deadAt = observedAt.Add(s.cfg.deadTimeout)
			deadlines = append(deadlines, healthDeadlineSpec{
				nodeID:     node.NodeID,
				generation: state.generation,
				status:     controllermeta.NodeStatusDead,
				evaluated:  state.deadAt,
			})
		}
		s.nodes[node.NodeID] = state
	}
	s.nodeMirror = nodeMirror
	s.mu.Unlock()

	for _, spec := range deadlines {
		spec := spec
		delay := spec.evaluated.Sub(now)
		timer := s.cfg.afterFunc(delay, func() {
			s.handleDeadline(spec.nodeID, spec.generation, spec.status, spec.evaluated)
		})
		s.mu.Lock()
		state, ok := s.nodes[spec.nodeID]
		if !ok || state.generation != spec.generation {
			s.mu.Unlock()
			timer.Stop()
			continue
		}
		switch spec.status {
		case controllermeta.NodeStatusSuspect:
			state.suspectTimer = timer
		case controllermeta.NodeStatusDead:
			state.deadTimer = timer
		default:
			timer.Stop()
		}
		s.mu.Unlock()
	}
}

func (s *nodeHealthScheduler) proposeStatusTransition(observation nodeObservation, desired controllermeta.NodeStatus, evaluatedAt time.Time) {
	if s == nil || s.cfg.propose == nil || s.cfg.loadNode == nil {
		return
	}

	var (
		node controllermeta.ClusterNode
		err  error
	)
	if desired == controllermeta.NodeStatusAlive {
		node, err = s.loadNodeForTransition(observation.NodeID)
	} else {
		node, err = s.cfg.loadNode(context.Background(), observation.NodeID)
		if err == nil {
			s.mirrorNode(node)
		}
	}
	if err != nil && !errors.Is(err, controllermeta.ErrNotFound) {
		return
	}

	transition, ok := buildNodeStatusTransition(observation, node, err, desired, evaluatedAt)
	if !ok {
		return
	}

	if !s.beginPending(observation.NodeID, desired) {
		return
	}
	defer s.clearPending(observation.NodeID, desired)

	_ = s.cfg.propose(context.Background(), slotcontroller.Command{
		Kind: slotcontroller.CommandKindNodeStatusUpdate,
		NodeStatusUpdate: &slotcontroller.NodeStatusUpdate{
			Transitions: []slotcontroller.NodeStatusTransition{transition},
		},
	})
}

func (s *nodeHealthScheduler) beginPending(nodeID uint64, desired controllermeta.NodeStatus) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	state := s.ensureNodeLocked(nodeID)
	if state.pendingStatus != nil && *state.pendingStatus == desired {
		return false
	}
	next := desired
	state.pendingStatus = &next
	return true
}

func (s *nodeHealthScheduler) clearPending(nodeID uint64, desired controllermeta.NodeStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, ok := s.nodes[nodeID]
	if !ok || state.pendingStatus == nil || *state.pendingStatus != desired {
		return
	}
	state.pendingStatus = nil
}

func buildNodeStatusTransition(observation nodeObservation, node controllermeta.ClusterNode, loadErr error, desired controllermeta.NodeStatus, evaluatedAt time.Time) (slotcontroller.NodeStatusTransition, bool) {
	switch {
	case errors.Is(loadErr, controllermeta.ErrNotFound):
		if desired != controllermeta.NodeStatusAlive || observation.Addr == "" {
			return slotcontroller.NodeStatusTransition{}, false
		}
		return slotcontroller.NodeStatusTransition{
			NodeID:         observation.NodeID,
			NewStatus:      desired,
			EvaluatedAt:    evaluatedAt,
			Addr:           observation.Addr,
			CapacityWeight: normalizeCapacityWeight(observation.CapacityWeight),
		}, true
	case loadErr != nil:
		return slotcontroller.NodeStatusTransition{}, false
	}

	if node.Status == controllermeta.NodeStatusDraining && desired != controllermeta.NodeStatusDraining {
		return slotcontroller.NodeStatusTransition{}, false
	}
	if node.Status == desired {
		return slotcontroller.NodeStatusTransition{}, false
	}
	if desired == controllermeta.NodeStatusSuspect && node.Status != controllermeta.NodeStatusAlive {
		return slotcontroller.NodeStatusTransition{}, false
	}
	if desired == controllermeta.NodeStatusDead && node.Status != controllermeta.NodeStatusAlive && node.Status != controllermeta.NodeStatusSuspect {
		return slotcontroller.NodeStatusTransition{}, false
	}
	if desired == controllermeta.NodeStatusAlive && node.Status == controllermeta.NodeStatusAlive {
		return slotcontroller.NodeStatusTransition{}, false
	}

	expected := node.Status
	return slotcontroller.NodeStatusTransition{
		NodeID:         observation.NodeID,
		NewStatus:      desired,
		ExpectedStatus: &expected,
		EvaluatedAt:    evaluatedAt,
		Addr:           observation.Addr,
		CapacityWeight: normalizeCapacityWeight(observation.CapacityWeight),
	}, true
}

func normalizeCapacityWeight(weight int) int {
	if weight <= 0 {
		return 1
	}
	return weight
}

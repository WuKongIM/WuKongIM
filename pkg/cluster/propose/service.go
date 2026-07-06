package propose

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
)

const (
	stageMetaCreateProposeLocal   = "meta_create_propose_local"
	stageMetaCreateProposeForward = "meta_create_propose_forward"
	leaderChangeRetryAttempts     = 8
	leaderChangeRetryBackoff      = 10 * time.Millisecond
)

// Config wires a Service.
type Config struct {
	// LocalNode is this node's stable cluster identity.
	LocalNode uint64
	// Router resolves request targets.
	Router Router
	// Slots proposes to local Slot leaders.
	Slots SlotRuntime
	// Forward forwards requests to remote leaders.
	Forward ForwardClient
}

// Service routes Slot metadata proposals to local or remote leaders.
type Service struct {
	localNode uint64
	router    Router
	slots     SlotRuntime
	forward   ForwardClient
}

// NewService creates a Service from cfg.
func NewService(cfg Config) *Service {
	return &Service{localNode: cfg.LocalNode, router: cfg.Router, slots: cfg.Slots, forward: cfg.Forward}
}

// Propose submits req to the current Slot leader.
func (s *Service) Propose(ctx context.Context, req Request) error {
	_, err := s.ProposeResult(ctx, req)
	return err
}

// ProposeResult submits req to the current Slot leader and returns apply bytes when supported.
func (s *Service) ProposeResult(ctx context.Context, req Request) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateRequest(req); err != nil {
		return nil, err
	}
	if s == nil || s.router == nil || s.slots == nil {
		return nil, ErrInvalidRequest
	}
	var lastErr error
	for attempt := 0; attempt < leaderChangeRetryAttempts; attempt++ {
		result, err := s.proposeOnceResult(ctx, req)
		if !isLeaderChangeRetryable(err) {
			return result, err
		}
		lastErr = err
		if attempt == leaderChangeRetryAttempts-1 {
			break
		}
		if err := waitLeaderChangeRetry(ctx); err != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

func (s *Service) proposeOnce(ctx context.Context, req Request) error {
	_, err := s.proposeOnceResult(ctx, req)
	return err
}

func (s *Service) proposeOnceResult(ctx context.Context, req Request) ([]byte, error) {
	route, err := s.route(req)
	if err != nil {
		return nil, err
	}
	payload := EncodePayload(route.HashSlot, req.Command)
	var lastNotLeader error
	for _, leader := range routeLeaderCandidates(route) {
		var result []byte
		result, err = s.proposeToLeaderResult(ctx, route, leader, payload)
		if !errors.Is(err, ErrNotLeader) {
			return result, err
		}
		lastNotLeader = err
	}
	if lastNotLeader != nil {
		return nil, lastNotLeader
	}
	return nil, ErrNotLeader
}

func (s *Service) proposeToLeader(ctx context.Context, route routing.Route, leader uint64, payload []byte) error {
	_, err := s.proposeToLeaderResult(ctx, route, leader, payload)
	return err
}

func (s *Service) proposeToLeaderResult(ctx context.Context, route routing.Route, leader uint64, payload []byte) ([]byte, error) {
	if leader == s.localNode || s.slots.IsLocalLeader(route.SlotID) {
		started := time.Now()
		var result []byte
		var err error
		if slots, ok := s.slots.(ResultSlotRuntime); ok {
			result, err = slots.ProposeResult(ctx, route.SlotID, payload)
		} else {
			err = s.slots.Propose(ctx, route.SlotID, payload)
		}
		ObserveStage(ctx, stageMetaCreateProposeLocal, err, time.Since(started))
		return result, err
	}
	if s.forward == nil {
		return nil, fmt.Errorf("%w: missing forward client", ErrInvalidRequest)
	}
	started := time.Now()
	req := ForwardRequest{
		SlotID:   route.SlotID,
		HashSlot: route.HashSlot,
		Class:    ProposalClassFromContext(ctx),
		Payload:  payload,
	}
	var result []byte
	var err error
	if forward, ok := s.forward.(ResultForwardClient); ok {
		result, err = forward.ForwardProposeResult(ctx, leader, req)
	} else {
		err = s.forward.ForwardPropose(ctx, leader, req)
	}
	ObserveStage(ctx, stageMetaCreateProposeForward, err, time.Since(started))
	return result, err
}

func routeLeaderCandidates(route routing.Route) []uint64 {
	candidates := make([]uint64, 0, 1+len(route.Peers))
	seen := make(map[uint64]struct{}, 1+len(route.Peers))
	add := func(nodeID uint64) {
		if nodeID == 0 {
			return
		}
		if _, ok := seen[nodeID]; ok {
			return
		}
		seen[nodeID] = struct{}{}
		candidates = append(candidates, nodeID)
	}
	add(route.Leader)
	for _, peer := range route.Peers {
		add(peer)
	}
	return candidates
}

func isLeaderChangeRetryable(err error) bool {
	return errors.Is(err, ErrNotLeader)
}

func waitLeaderChangeRetry(ctx context.Context) error {
	timer := time.NewTimer(leaderChangeRetryBackoff)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *Service) route(req Request) (routing.Route, error) {
	if req.Target.HasSlotID {
		return s.router.RouteSlot(req.Target.SlotID, req.Target.HashSlot)
	}
	if req.Target.HasHashSlot {
		return s.router.RouteHashSlot(req.Target.HashSlot)
	}
	return s.router.RouteKey(req.Key)
}

func validateRequest(req Request) error {
	if len(req.Command) == 0 {
		return ErrInvalidRequest
	}
	if req.Target.HasSlotID && !req.Target.HasHashSlot {
		return ErrInvalidRequest
	}
	if !req.Target.HasHashSlot && req.Key == "" {
		return ErrInvalidRequest
	}
	return nil
}

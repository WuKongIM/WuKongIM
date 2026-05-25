package propose

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/routing"
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
	if err := validateRequest(req); err != nil {
		return err
	}
	if s == nil || s.router == nil || s.slots == nil {
		return ErrInvalidRequest
	}
	route, err := s.route(req)
	if err != nil {
		return err
	}
	payload := EncodePayload(route.HashSlot, req.Command)
	if route.Leader == s.localNode || s.slots.IsLocalLeader(route.SlotID) {
		return s.slots.Propose(ctx, route.SlotID, payload)
	}
	if s.forward == nil {
		return fmt.Errorf("%w: missing forward client", ErrInvalidRequest)
	}
	return s.forward.ForwardPropose(ctx, route.Leader, ForwardRequest{SlotID: route.SlotID, HashSlot: route.HashSlot, Payload: payload})
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

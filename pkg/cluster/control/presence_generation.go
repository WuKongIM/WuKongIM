package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
)

const presenceControlMaxBytes = 512 << 10

// PresenceControl is a rare control-plane protocol, separate from hot UID RPCs.
type PresenceControl interface {
	PresenceCapabilities() controller.PresenceCapabilities
	EnsurePresenceOwnerSet(context.Context) (controller.PresenceOwnerSet, error)
	ClaimPresenceGeneration(context.Context, controller.ClaimPresenceGenerationRequest) (controller.PresenceGeneration, error)
	GetPresenceGeneration(context.Context, uint16) (controller.PresenceGenerationSnapshot, error)
}

type presenceControlRequest struct {
	Version   uint32                                     `json:"version"`
	Operation string                                     `json:"operation"`
	HashSlot  uint16                                     `json:"hash_slot,omitempty"`
	Claim     *controller.ClaimPresenceGenerationRequest `json:"claim,omitempty"`
}
type presenceControlResponse struct {
	Version      uint32                                `json:"version"`
	Error        string                                `json:"error,omitempty"`
	Capabilities controller.PresenceCapabilities       `json:"capabilities,omitempty"`
	Owners       controller.PresenceOwnerSet           `json:"owners,omitempty"`
	Generation   controller.PresenceGeneration         `json:"generation,omitempty"`
	Snapshot     controller.PresenceGenerationSnapshot `json:"snapshot,omitempty"`
}

// PresenceControlClient never silently falls back to legacy presence semantics.
type PresenceControlClient struct{ caller clusternet.Caller }

func NewPresenceControlClient(caller clusternet.Caller) *PresenceControlClient {
	return &PresenceControlClient{caller: caller}
}
func (c *PresenceControlClient) Capabilities(ctx context.Context, nodeID uint64) (controller.PresenceCapabilities, error) {
	r, e := c.call(ctx, nodeID, presenceControlRequest{Operation: "capabilities"})
	return r.Capabilities, e
}
func (c *PresenceControlClient) Ensure(ctx context.Context, nodeID uint64) (controller.PresenceOwnerSet, error) {
	r, e := c.call(ctx, nodeID, presenceControlRequest{Operation: "ensure"})
	return r.Owners, e
}
func (c *PresenceControlClient) Claim(ctx context.Context, nodeID uint64, req controller.ClaimPresenceGenerationRequest) (controller.PresenceGeneration, error) {
	r, e := c.call(ctx, nodeID, presenceControlRequest{Operation: "claim", Claim: &req})
	return r.Generation, e
}
func (c *PresenceControlClient) Get(ctx context.Context, nodeID uint64, hashSlot uint16) (controller.PresenceGenerationSnapshot, error) {
	r, e := c.call(ctx, nodeID, presenceControlRequest{Operation: "get", HashSlot: hashSlot})
	return r.Snapshot, e
}
func (c *PresenceControlClient) call(ctx context.Context, nodeID uint64, req presenceControlRequest) (presenceControlResponse, error) {
	var out presenceControlResponse
	if c == nil || c.caller == nil || nodeID == 0 {
		return out, controller.ErrPresenceGenerationNotReady
	}
	req.Version = 1
	payload, err := json.Marshal(req)
	if err != nil {
		return out, err
	}
	response, err := clusternet.CallOwnedPayload(ctx, c.caller, nodeID, clusternet.RPCPresenceControl, payload)
	if err != nil {
		return out, err
	}
	if err = decodePresenceControl(response, &out); err != nil {
		return out, err
	}
	if out.Version != 1 {
		return out, controller.ErrPresenceGenerationNotReady
	}
	switch out.Error {
	case "":
		return out, nil
	case "conflict":
		return out, controller.ErrPresenceGenerationConflict
	case "not_leader":
		return out, controller.ErrNotLeader
	default:
		return out, controller.ErrPresenceGenerationNotReady
	}
}

// NewPresenceControlHandler bounds inputs and rejects unknown formats before dispatch.
func NewPresenceControlHandler(backend PresenceControl) clusternet.Handler {
	return clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		var req presenceControlRequest
		if err := decodePresenceControl(payload, &req); err != nil {
			return nil, err
		}
		if req.Version != 1 || backend == nil {
			return nil, controller.ErrPresenceGenerationNotReady
		}
		var out presenceControlResponse
		out.Version = 1
		var err error
		switch req.Operation {
		case "capabilities":
			if req.Claim != nil || req.HashSlot != 0 {
				return nil, controller.ErrPresenceGenerationNotReady
			}
			out.Capabilities = backend.PresenceCapabilities()
		case "ensure":
			if req.Claim != nil || req.HashSlot != 0 {
				return nil, controller.ErrPresenceGenerationNotReady
			}
			out.Owners, err = backend.EnsurePresenceOwnerSet(ctx)
		case "claim":
			if req.Claim == nil || req.HashSlot != 0 {
				return nil, controller.ErrPresenceGenerationNotReady
			}
			out.Generation, err = backend.ClaimPresenceGeneration(ctx, *req.Claim)
		case "get":
			if req.Claim != nil {
				return nil, controller.ErrPresenceGenerationNotReady
			}
			out.Snapshot, err = backend.GetPresenceGeneration(ctx, req.HashSlot)
		default:
			return nil, controller.ErrPresenceGenerationNotReady
		}
		if err != nil {
			switch {
			case errors.Is(err, controller.ErrPresenceGenerationConflict):
				out.Error = "conflict"
			case errors.Is(err, controller.ErrNotLeader):
				out.Error = "not_leader"
			default:
				out.Error = "not_ready"
			}
		}
		result, err := json.Marshal(out)
		if len(result) > presenceControlMaxBytes {
			return nil, controller.ErrPresenceGenerationNotReady
		}
		return result, err
	})
}
func decodePresenceControl(data []byte, out any) error {
	if len(data) == 0 || len(data) > presenceControlMaxBytes {
		return fmt.Errorf("presence control: invalid payload size")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("presence control: trailing payload")
	}
	return nil
}

// These facade calls are local-leader only; Node owns exact-leader forwarding.
func (r *Runtime) EnsurePresenceOwnerSet(ctx context.Context) (controller.PresenceOwnerSet, error) {
	if r == nil || r.backend == nil {
		return controller.PresenceOwnerSet{}, controller.ErrNotStarted
	}
	return r.backend.EnsurePresenceOwnerSet(ctx)
}
func (r *Runtime) ClaimPresenceGeneration(ctx context.Context, req controller.ClaimPresenceGenerationRequest) (controller.PresenceGeneration, error) {
	if r == nil || r.backend == nil {
		return controller.PresenceGeneration{}, controller.ErrNotStarted
	}
	return r.backend.ClaimPresenceGeneration(ctx, req)
}
func (r *Runtime) GetPresenceGeneration(ctx context.Context, slot uint16) (controller.PresenceGenerationSnapshot, error) {
	if r == nil || r.backend == nil {
		return controller.PresenceGenerationSnapshot{}, controller.ErrNotStarted
	}
	return r.backend.GetPresenceGeneration(ctx, slot)
}

package control

import (
	"context"
	"fmt"
	"time"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
	cv2 "github.com/WuKongIM/WuKongIM/pkg/controllerv2"
	"go.etcd.io/raft/v3/raftpb"
)

const defaultControlRPCTimeout = 200 * time.Millisecond

type raftStepper interface {
	Step(context.Context, raftpb.Message) error
}

// RaftTransport sends ControllerV2 Raft messages over clusterv2 typed messages.
type RaftTransport struct {
	sender  clusternet.Sender
	timeout time.Duration
}

// NewRaftTransport creates a ControllerV2 Raft transport backed by sender.
func NewRaftTransport(sender clusternet.Sender) *RaftTransport {
	return &RaftTransport{sender: sender, timeout: defaultControlRPCTimeout}
}

// Send sends messages grouped by destination node without blocking indefinitely.
func (t *RaftTransport) Send(messages []raftpb.Message) {
	if t == nil || t.sender == nil {
		return
	}
	byNode := make(map[uint64][]raftpb.Message)
	for _, msg := range messages {
		if msg.To == 0 {
			continue
		}
		byNode[msg.To] = append(byNode[msg.To], msg)
	}
	for nodeID, batch := range byNode {
		payload, err := EncodeRaftBatch(batch)
		if err != nil {
			continue
		}
		go t.sendBatch(nodeID, payload)
	}
}

func (t *RaftTransport) sendBatch(nodeID uint64, payload []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), t.timeout)
	err := t.sender.Send(ctx, nodeID, clusternet.RPCControlRaft, payload)
	if err != nil {
		fmt.Printf("control raft send failed %v\n", err)
	}
	cancel()
}

// NewRaftHandler creates an RPC handler that steps decoded ControllerV2 Raft messages.
func NewRaftHandler(stepper raftStepper) clusternet.Handler {
	return clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		messages, err := DecodeRaftBatch(payload)
		if err != nil {
			return nil, err
		}
		for _, msg := range messages {
			if stepper == nil {
				continue
			}
			if err := stepper.Step(ctx, msg); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
}

// StateSyncEndpoint adapts clusterv2 RPC to controllerv2/sync.Endpoint.
type StateSyncEndpoint struct {
	caller clusternet.Caller
	nodeID uint64
}

// NewStateSyncEndpoint creates an endpoint for one remote ControllerV2 state peer.
func NewStateSyncEndpoint(caller clusternet.Caller, nodeID uint64) *StateSyncEndpoint {
	return &StateSyncEndpoint{caller: caller, nodeID: nodeID}
}

// GetState sends a ControllerV2 state sync request to the remote node.
func (e *StateSyncEndpoint) GetState(ctx context.Context, req cv2.GetStateRequest) (cv2.GetStateResponse, error) {
	payload, err := EncodeStateSyncRequest(req)
	if err != nil {
		return cv2.GetStateResponse{}, err
	}
	resp, err := e.caller.Call(ctx, e.nodeID, clusternet.RPCControlStateSync, payload)
	if err != nil {
		return cv2.GetStateResponse{}, err
	}
	return DecodeStateSyncResponse(resp)
}

// NewStateSyncHandler creates an RPC handler for a ControllerV2 state sync endpoint.
func NewStateSyncHandler(endpoint cv2.Endpoint) clusternet.Handler {
	return clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		req, err := DecodeStateSyncRequest(payload)
		if err != nil {
			return nil, err
		}
		resp, err := endpoint.GetState(ctx, req)
		if err != nil {
			return nil, err
		}
		return EncodeStateSyncResponse(resp)
	})
}

// StaticPeerPicker resolves a fixed ControllerV2 voter set to clusterv2 sync endpoints.
type StaticPeerPicker struct {
	endpoints map[uint64]cv2.Endpoint
	ids       []uint64
}

// NewStaticPeerPicker creates a fixed peer picker backed by caller.
func NewStaticPeerPicker(caller clusternet.Caller, voters []RuntimeVoter) *StaticPeerPicker {
	picker := &StaticPeerPicker{
		endpoints: make(map[uint64]cv2.Endpoint, len(voters)),
		ids:       make([]uint64, 0, len(voters)),
	}
	for _, voter := range voters {
		picker.ids = append(picker.ids, voter.NodeID)
		picker.endpoints[voter.NodeID] = NewStateSyncEndpoint(caller, voter.NodeID)
	}
	return picker
}

// Endpoint returns the sync endpoint for nodeID.
func (p *StaticPeerPicker) Endpoint(nodeID uint64) (cv2.Endpoint, bool) {
	if p == nil {
		return nil, false
	}
	endpoint, ok := p.endpoints[nodeID]
	return endpoint, ok
}

// PeerIDs returns the fixed ControllerV2 voter IDs.
func (p *StaticPeerPicker) PeerIDs() []uint64 {
	if p == nil {
		return nil
	}
	return append([]uint64(nil), p.ids...)
}

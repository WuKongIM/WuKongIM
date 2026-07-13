package node

import (
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

func TestManagerConnectionRuntimeSummaryCodecCarriesControlRevision(t *testing.T) {
	want := managementusecase.NodeRuntimeSummary{
		NodeID:               2,
		ActiveOnline:         7,
		GatewaySessions:      9,
		PendingActivations:   3,
		SessionsByListener:   map[string]int{"tcp": 9},
		AcceptingNewSessions: true,
		ControlRevision:      42,
		ChannelRuntime: managementusecase.NodeChannelRuntimeSummary{
			ActiveTotal:    11,
			ActiveLeader:   4,
			ActiveFollower: 7,
		},
	}

	encoded, err := encodeManagerConnectionResponse(managerConnectionRPCResponse{Status: "ok", Summary: want})
	if err != nil {
		t.Fatalf("encodeManagerConnectionResponse() error = %v", err)
	}
	got, err := decodeManagerConnectionResponse(encoded)
	if err != nil {
		t.Fatalf("decodeManagerConnectionResponse() error = %v", err)
	}
	if got.Summary.ControlRevision != 42 || got.Summary.NodeID != 2 {
		t.Fatalf("summary = %#v, want control revision 42 for node 2", got.Summary)
	}
	if got.Summary.PendingActivations != 3 {
		t.Fatalf("summary = %#v, want pending activations 3", got.Summary)
	}
	if got.Summary.ChannelRuntime != want.ChannelRuntime {
		t.Fatalf("channel runtime = %#v, want %#v", got.Summary.ChannelRuntime, want.ChannelRuntime)
	}
}

func TestManagerConnectionDrainModeCodecRoundTrip(t *testing.T) {
	encoded, err := encodeManagerConnectionRequest(managerConnectionRPCRequest{
		Op: managerConnectionOpSetDrainMode, NodeID: 4, Draining: true,
	})
	if err != nil {
		t.Fatalf("encode request error = %v", err)
	}
	got, err := decodeManagerConnectionRequest(encoded)
	if err != nil {
		t.Fatalf("decode request error = %v", err)
	}
	if got.Op != managerConnectionOpSetDrainMode || got.NodeID != 4 || !got.Draining {
		t.Fatalf("request = %#v, want set drain mode node 4", got)
	}
}

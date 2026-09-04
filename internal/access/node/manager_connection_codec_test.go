package node

import (
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

func TestManagerConnectionPageCodecCarriesCursorAndTotal(t *testing.T) {
	cursor := managementusecase.ConnectionListCursor{ConnectedAt: time.Unix(1713859200, 123).UTC(), SessionID: 101}
	request, err := encodeManagerConnectionRequest(managerConnectionRPCRequest{
		Op: managerConnectionOpList, NodeID: 2, Limit: 100, Cursor: cursor,
	})
	if err != nil {
		t.Fatalf("encodeManagerConnectionRequest() error = %v", err)
	}
	gotRequest, err := decodeManagerConnectionRequest(request)
	if err != nil {
		t.Fatalf("decodeManagerConnectionRequest() error = %v", err)
	}
	if gotRequest.Version != managerConnectionRPCVersion4 || gotRequest.Cursor != cursor {
		t.Fatalf("request = %#v, want v4 cursor %#v", gotRequest, cursor)
	}

	response, err := encodeManagerConnectionResponse(managerConnectionRPCResponse{
		Status: rpcStatusOK, Total: 250, HasMore: true, NextCursor: cursor,
	})
	if err != nil {
		t.Fatalf("encodeManagerConnectionResponse() error = %v", err)
	}
	gotResponse, err := decodeManagerConnectionResponse(response)
	if err != nil {
		t.Fatalf("decodeManagerConnectionResponse() error = %v", err)
	}
	if gotResponse.Version != managerConnectionRPCVersion4 || gotResponse.Total != 250 || !gotResponse.HasMore || gotResponse.NextCursor != cursor {
		t.Fatalf("response = %#v, want v4 total and cursor", gotResponse)
	}
}

func TestManagerConnectionCodecAcceptsVersion2Frames(t *testing.T) {
	request, err := encodeManagerConnectionRequest(managerConnectionRPCRequest{
		Version: managerConnectionRPCVersion2, Op: managerConnectionOpList, NodeID: 2, Limit: 100,
	})
	if err != nil {
		t.Fatalf("encodeManagerConnectionRequest(v2) error = %v", err)
	}
	gotRequest, err := decodeManagerConnectionRequest(request)
	if err != nil || gotRequest.Version != managerConnectionRPCVersion2 {
		t.Fatalf("decodeManagerConnectionRequest(v2) = %#v, %v", gotRequest, err)
	}

	response, err := encodeManagerConnectionResponse(managerConnectionRPCResponse{
		Version: managerConnectionRPCVersion2, Status: rpcStatusOK,
		Connections: []managementusecase.Connection{{SessionID: 101}},
	})
	if err != nil {
		t.Fatalf("encodeManagerConnectionResponse(v2) error = %v", err)
	}
	gotResponse, err := decodeManagerConnectionResponse(response)
	if err != nil || gotResponse.Version != managerConnectionRPCVersion2 || gotResponse.Total != 1 {
		t.Fatalf("decodeManagerConnectionResponse(v2) = %#v, %v", gotResponse, err)
	}
}

func TestManagerConnectionRuntimeSummaryCodecCarriesControlRevision(t *testing.T) {
	want := managementusecase.NodeRuntimeSummary{
		NodeID:               2,
		Version:              "v3.0.0-beta.7",
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
	if got.Summary.ControlRevision != 42 || got.Summary.NodeID != 2 || got.Summary.Version != "v3.0.0-beta.7" {
		t.Fatalf("summary = %#v, want control revision 42 and version v3.0.0-beta.7 for node 2", got.Summary)
	}
	if got.Summary.PendingActivations != 3 {
		t.Fatalf("summary = %#v, want pending activations 3", got.Summary)
	}
	if got.Summary.ChannelRuntime != want.ChannelRuntime {
		t.Fatalf("channel runtime = %#v, want %#v", got.Summary.ChannelRuntime, want.ChannelRuntime)
	}
}

func TestManagerConnectionVersion3RuntimeSummaryOmitsProgramVersion(t *testing.T) {
	encoded, err := encodeManagerConnectionResponse(managerConnectionRPCResponse{
		Version: managerConnectionRPCVersion3,
		Status:  rpcStatusOK,
		Summary: managementusecase.NodeRuntimeSummary{NodeID: 2, Version: "v3.0.0-beta.7"},
	})
	if err != nil {
		t.Fatalf("encodeManagerConnectionResponse(v3) error = %v", err)
	}
	got, err := decodeManagerConnectionResponse(encoded)
	if err != nil {
		t.Fatalf("decodeManagerConnectionResponse(v3) error = %v", err)
	}
	if got.Version != managerConnectionRPCVersion3 || got.Summary.NodeID != 2 || got.Summary.Version != "" {
		t.Fatalf("response = %#v, want v3 runtime summary without program version", got)
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

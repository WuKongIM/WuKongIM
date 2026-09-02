package node

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	opscontract "github.com/WuKongIM/WuKongIM/internal/contracts/opsmcp"
)

func TestOpsMCPRPCRejectsUnauthenticatedOrMalformedRequestsBeforeExecution(t *testing.T) {
	forward := &opsBoundaryForwardExecutor{}
	profile := &opsBoundaryProfileExecutor{}
	audits := &opsBoundaryAuditReader{}
	leases := &opsBoundaryLeaseExecutor{}
	adapter := NewOpsMCPRPCAdapterWithServices(forward, profile, audits, leases)
	validForward := &opscontract.ForwardRequest{
		Version: opscontract.RPCVersion, IngressNodeID: 2, Payload: []byte(`{"jsonrpc":"2.0"}`),
	}
	validProfile := &opscontract.ProfileRequest{
		Version: opscontract.RPCVersion, OwnerNodeID: 2, NodeID: 3, LeaseID: "lease-1",
	}
	validLease := &opscontract.ProfileLeaseRequest{
		Version: opscontract.RPCVersion, OwnerNodeID: 1, TargetNodeID: 2, LeaseID: "lease-1",
	}

	cases := []struct {
		name    string
		payload []byte
	}{
		{name: "malformed JSON", payload: []byte("{")},
		{name: "unsupported version", payload: marshalOpsMCPRequest(t, opsMCPRPCRequest{Op: opsMCPRPCOpForward})},
		{name: "unknown operation", payload: marshalOpsMCPRequest(t, opsMCPRPCRequest{Version: opscontract.RPCVersion, CallerNodeID: 2, Op: "unknown"})},
		{name: "missing forward", payload: marshalOpsMCPRequest(t, opsMCPRPCRequest{Version: opscontract.RPCVersion, CallerNodeID: 2, Op: opsMCPRPCOpForward})},
		{name: "forged forward ingress", payload: marshalOpsMCPRequest(t, opsMCPRPCRequest{Version: opscontract.RPCVersion, CallerNodeID: 1, Op: opsMCPRPCOpForward, Forward: validForward})},
		{name: "oversized forward", payload: marshalOpsMCPRequest(t, opsMCPRPCRequest{Version: opscontract.RPCVersion, CallerNodeID: 2, Op: opsMCPRPCOpForward, Forward: &opscontract.ForwardRequest{IngressNodeID: 2, Payload: make([]byte, opscontract.MaxForwardRequestBytes+1)}})},
		{name: "missing profile", payload: marshalOpsMCPRequest(t, opsMCPRPCRequest{Version: opscontract.RPCVersion, CallerNodeID: 2, Op: opsMCPRPCOpProfile})},
		{name: "forged profile owner", payload: marshalOpsMCPRequest(t, opsMCPRPCRequest{Version: opscontract.RPCVersion, CallerNodeID: 1, Op: opsMCPRPCOpProfile, Profile: validProfile})},
		{name: "missing profile lease", payload: marshalOpsMCPRequest(t, opsMCPRPCRequest{Version: opscontract.RPCVersion, CallerNodeID: 2, Op: opsMCPRPCOpProfileLease})},
		{name: "forged lease target", payload: marshalOpsMCPRequest(t, opsMCPRPCRequest{Version: opscontract.RPCVersion, CallerNodeID: 1, Op: opsMCPRPCOpProfileLease, ProfileLease: validLease})},
		{name: "missing audits", payload: marshalOpsMCPRequest(t, opsMCPRPCRequest{Version: opscontract.RPCVersion, CallerNodeID: 2, Op: opsMCPRPCOpAudits})},
		{name: "zero audit limit", payload: marshalOpsMCPRequest(t, opsMCPRPCRequest{Version: opscontract.RPCVersion, CallerNodeID: 2, Op: opsMCPRPCOpAudits, Audits: &opscontract.AuditRequest{Limit: 0}})},
		{name: "oversized audit limit", payload: marshalOpsMCPRequest(t, opsMCPRPCRequest{Version: opscontract.RPCVersion, CallerNodeID: 2, Op: opsMCPRPCOpAudits, Audits: &opscontract.AuditRequest{Limit: opscontract.MaxAuditEntries + 1}})},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := adapter.HandleRPC(context.Background(), test.payload); err == nil {
				t.Fatal("HandleRPC() error = nil, want fail-closed rejection")
			}
		})
	}
	if forward.calls != 0 || profile.calls != 0 || audits.calls != 0 || leases.calls != 0 {
		t.Fatalf("rejected requests reached services: forward=%d profile=%d audits=%d leases=%d",
			forward.calls, profile.calls, audits.calls, leases.calls)
	}
}

func TestOpsMCPRPCPropagatesCancellationWithoutConstructingSuccess(t *testing.T) {
	canceled := context.Canceled
	forward := &opsBoundaryForwardExecutor{err: canceled}
	profile := &opsBoundaryProfileExecutor{err: canceled}
	audits := &opsBoundaryAuditReader{err: canceled}
	leases := &opsBoundaryLeaseExecutor{err: canceled}
	adapter := NewOpsMCPRPCAdapterWithServices(forward, profile, audits, leases)

	requests := []opsMCPRPCRequest{
		{Version: opscontract.RPCVersion, CallerNodeID: 2, Op: opsMCPRPCOpForward, Forward: &opscontract.ForwardRequest{Version: opscontract.RPCVersion, IngressNodeID: 2}},
		{Version: opscontract.RPCVersion, CallerNodeID: 2, Op: opsMCPRPCOpProfile, Profile: &opscontract.ProfileRequest{Version: opscontract.RPCVersion, OwnerNodeID: 2, NodeID: 3}},
		{Version: opscontract.RPCVersion, CallerNodeID: 2, Op: opsMCPRPCOpProfileLease, ProfileLease: &opscontract.ProfileLeaseRequest{Version: opscontract.RPCVersion, OwnerNodeID: 1, TargetNodeID: 2, LeaseID: "lease"}},
		{Version: opscontract.RPCVersion, CallerNodeID: 2, Op: opsMCPRPCOpAudits, Audits: &opscontract.AuditRequest{Version: opscontract.RPCVersion, Limit: 10}},
	}
	for _, request := range requests {
		body, err := adapter.HandleRPC(context.Background(), marshalOpsMCPRequest(t, request))
		if !errors.Is(err, canceled) {
			t.Fatalf("HandleRPC(%s) error = %v, want context.Canceled", request.Op, err)
		}
		if body != nil {
			t.Fatalf("HandleRPC(%s) body = %q, want nil on cancellation", request.Op, body)
		}
	}
}

func TestOpsMCPRPCRejectsOversizedOwnerResponse(t *testing.T) {
	adapter := NewOpsMCPRPCAdapter(
		&opsBoundaryForwardExecutor{response: opscontract.ForwardResponse{
			Version: opscontract.RPCVersion,
			Payload: make([]byte, opscontract.MaxForwardResponseBytes+1),
		}},
		nil,
	)
	request := opsMCPRPCRequest{
		Version:      opscontract.RPCVersion,
		CallerNodeID: 2,
		Op:           opsMCPRPCOpForward,
		Forward: &opscontract.ForwardRequest{
			Version: opscontract.RPCVersion, IngressNodeID: 2,
		},
	}
	if _, err := adapter.HandleRPC(context.Background(), marshalOpsMCPRequest(t, request)); err == nil {
		t.Fatal("HandleRPC() accepted oversized forward response")
	}
}

func TestOpsMCPClientsRejectInvalidRequestsAndResponsesBeforeUse(t *testing.T) {
	clientWithoutTransport := (*Client)(nil)
	if _, err := clientWithoutTransport.ReadOpsMCPAudits(context.Background(), 2, 1); err == nil {
		t.Fatal("ReadOpsMCPAudits() with nil client error = nil")
	}

	invalidCalls := []struct {
		name string
		call func(*Client) error
	}{
		{name: "lease", call: func(client *Client) error {
			return client.VerifyOpsMCPProfileLease(context.Background(), 0, opscontract.ProfileLeaseRequest{})
		}},
		{name: "forward", call: func(client *Client) error {
			_, err := client.ForwardOpsMCP(context.Background(), 0, opscontract.ForwardRequest{})
			return err
		}},
		{name: "profile", call: func(client *Client) error {
			_, err := client.CaptureOpsMCPProfile(context.Background(), 0, opscontract.ProfileRequest{})
			return err
		}},
	}
	counting := &opsBoundaryNode{nodeID: 1}
	for _, test := range invalidCalls {
		if err := test.call(NewClient(counting)); err == nil {
			t.Fatalf("%s invalid request error = nil", test.name)
		}
	}
	if counting.calls != 0 {
		t.Fatalf("invalid client requests reached transport %d times", counting.calls)
	}

	responseCases := []struct {
		name     string
		response opsMCPRPCResponse
		call     func(*Client) error
	}{
		{
			name: "lease denied",
			response: opsMCPRPCResponse{Version: opscontract.RPCVersion, ProfileLease: &opscontract.ProfileLeaseResponse{
				Version: opscontract.RPCVersion,
			}},
			call: func(client *Client) error {
				return client.VerifyOpsMCPProfileLease(context.Background(), 1, opscontract.ProfileLeaseRequest{
					Version: opscontract.RPCVersion, OwnerNodeID: 1, TargetNodeID: 2, LeaseID: "lease",
				})
			},
		},
		{
			name:     "missing forward",
			response: opsMCPRPCResponse{Version: opscontract.RPCVersion},
			call: func(client *Client) error {
				_, err := client.ForwardOpsMCP(context.Background(), 1, opscontract.ForwardRequest{Version: opscontract.RPCVersion})
				return err
			},
		},
		{
			name:     "missing profile",
			response: opsMCPRPCResponse{Version: opscontract.RPCVersion},
			call: func(client *Client) error {
				_, err := client.CaptureOpsMCPProfile(context.Background(), 2, opscontract.ProfileRequest{Version: opscontract.RPCVersion, NodeID: 2})
				return err
			},
		},
	}
	for _, test := range responseCases {
		node := &opsBoundaryNode{nodeID: 2, response: marshalOpsMCPResponse(t, test.response)}
		if err := test.call(NewClient(node)); err == nil {
			t.Fatalf("%s invalid response error = nil", test.name)
		}
	}

	malformed := NewClient(&opsBoundaryNode{nodeID: 1, response: []byte("{")})
	if _, err := malformed.ReadOpsMCPAudits(context.Background(), 2, 1); err == nil {
		t.Fatal("ReadOpsMCPAudits() accepted malformed response")
	}
	wrongVersion := NewClient(&opsBoundaryNode{nodeID: 1, response: marshalOpsMCPResponse(t, opsMCPRPCResponse{})})
	if _, err := wrongVersion.ReadOpsMCPAudits(context.Background(), 2, 1); err == nil {
		t.Fatal("ReadOpsMCPAudits() accepted unsupported response version")
	}
}

func marshalOpsMCPRequest(t *testing.T, request opsMCPRPCRequest) []byte {
	t.Helper()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("json.Marshal(request) error = %v", err)
	}
	return body
}

func marshalOpsMCPResponse(t *testing.T, response opsMCPRPCResponse) []byte {
	t.Helper()
	body, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("json.Marshal(response) error = %v", err)
	}
	return body
}

type opsBoundaryForwardExecutor struct {
	calls    int
	response opscontract.ForwardResponse
	err      error
}

func (e *opsBoundaryForwardExecutor) ExecuteForward(context.Context, opscontract.ForwardRequest) (opscontract.ForwardResponse, error) {
	e.calls++
	return e.response, e.err
}

type opsBoundaryProfileExecutor struct {
	calls    int
	response opscontract.ProfileResponse
	err      error
}

func (e *opsBoundaryProfileExecutor) CaptureProfile(context.Context, opscontract.ProfileRequest) (opscontract.ProfileResponse, error) {
	e.calls++
	return e.response, e.err
}

type opsBoundaryLeaseExecutor struct {
	calls int
	err   error
}

func (e *opsBoundaryLeaseExecutor) AuthorizeProfileLease(context.Context, opscontract.ProfileLeaseRequest) error {
	e.calls++
	return e.err
}

type opsBoundaryAuditReader struct {
	calls   int
	entries []opscontract.AuditEntry
	err     error
}

func (r *opsBoundaryAuditReader) RecentAudits(context.Context, int) ([]opscontract.AuditEntry, error) {
	r.calls++
	return r.entries, r.err
}

type opsBoundaryNode struct {
	nodeID   uint64
	response []byte
	err      error
	calls    int
}

func (n *opsBoundaryNode) NodeID() uint64 { return n.nodeID }

func (n *opsBoundaryNode) CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error) {
	n.calls++
	return append([]byte(nil), n.response...), n.err
}

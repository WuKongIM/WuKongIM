package node

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	opscontract "github.com/WuKongIM/WuKongIM/internal/contracts/opsmcp"
)

func TestOpsMCPRPCForwardsWithoutRawToken(t *testing.T) {
	executor := &opsMCPForwardExecutorStub{}
	adapter := NewOpsMCPRPCAdapter(executor, nil)
	node := &opsMCPRPCNodeStub{handler: adapter, nodeID: 2}
	client := NewClient(node)
	request := opscontract.ForwardRequest{
		Version: opscontract.RPCVersion, RequestID: "request-1", IngressNodeID: 2,
		CredentialID: "credential", DigestSHA256: "digest", ExpectedRevision: 7,
		Payload: []byte(`{"jsonrpc":"2.0","method":"tools/list","id":1}`),
	}
	got, err := client.ForwardOpsMCP(context.Background(), 1, request)
	if err != nil {
		t.Fatalf("ForwardOpsMCP() error = %v", err)
	}
	if string(got.Payload) != `{"jsonrpc":"2.0","result":{},"id":1}` {
		t.Fatalf("response = %#v", got)
	}
	if executor.request.CredentialID != "credential" || executor.request.DigestSHA256 != "digest" {
		t.Fatalf("executor request = %#v", executor.request)
	}
	if string(node.payload) == "raw-token" {
		t.Fatal("raw token crossed RPC")
	}
}

func TestOpsMCPRPCProfilesUseTypedBoundedContract(t *testing.T) {
	profiler := &opsMCPProfileExecutorStub{}
	adapter := NewOpsMCPRPCAdapter(nil, profiler)
	client := NewClient(&opsMCPRPCNodeStub{handler: adapter, nodeID: 1})
	got, err := client.CaptureOpsMCPProfile(context.Background(), 3, opscontract.ProfileRequest{
		Version: opscontract.RPCVersion, OwnerNodeID: 1, ExpectedRevision: 8,
		NodeID: 3, Kind: "heap", Seconds: 1, LeaseID: "lease-1",
	})
	if err != nil {
		t.Fatalf("CaptureOpsMCPProfile() error = %v", err)
	}
	if string(got.Payload) != "profile" || profiler.request.OwnerNodeID != 1 {
		t.Fatalf("response=%#v request=%#v", got, profiler.request)
	}
}

func TestOpsMCPRPCVerifiesProfileLeaseAtOwner(t *testing.T) {
	leases := &opsMCPProfileLeaseExecutorStub{}
	adapter := NewOpsMCPRPCAdapterWithServices(nil, nil, nil, leases)
	client := NewClient(&opsMCPRPCNodeStub{handler: adapter, nodeID: 3})
	request := opscontract.ProfileLeaseRequest{
		Version: opscontract.RPCVersion, OwnerNodeID: 1, ExpectedRevision: 8,
		TargetNodeID: 3, LeaseID: "lease-1",
	}
	if err := client.VerifyOpsMCPProfileLease(context.Background(), 1, request); err != nil {
		t.Fatalf("VerifyOpsMCPProfileLease() error = %v", err)
	}
	if leases.request.LeaseID != "lease-1" || leases.request.TargetNodeID != 3 {
		t.Fatalf("lease request = %#v", leases.request)
	}
}

func TestOpsMCPRPCRejectsProfileWhenCallerIsNotConfiguredOwner(t *testing.T) {
	profiler := &opsMCPProfileExecutorStub{}
	adapter := NewOpsMCPRPCAdapter(nil, profiler)
	payload, err := json.Marshal(opsMCPRPCRequest{
		Version: opscontract.RPCVersion, CallerNodeID: 2, Op: opsMCPRPCOpProfile,
		Profile: &opscontract.ProfileRequest{
			Version: opscontract.RPCVersion, OwnerNodeID: 1, ExpectedRevision: 8,
			NodeID: 3, Kind: "heap",
		},
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if _, err := adapter.HandleRPC(context.Background(), payload); err == nil {
		t.Fatal("forged owner caller was accepted")
	}
	if profiler.request.NodeID != 0 {
		t.Fatalf("profile executor received forged request: %#v", profiler.request)
	}
}

type opsMCPForwardExecutorStub struct {
	request opscontract.ForwardRequest
}

func (e *opsMCPForwardExecutorStub) ExecuteForward(_ context.Context, request opscontract.ForwardRequest) (opscontract.ForwardResponse, error) {
	e.request = request
	return opscontract.ForwardResponse{
		Version: opscontract.RPCVersion, StatusCode: 200, ContentType: "application/json",
		Payload: []byte(`{"jsonrpc":"2.0","result":{},"id":1}`),
	}, nil
}

type opsMCPProfileExecutorStub struct {
	request opscontract.ProfileRequest
}

type opsMCPProfileLeaseExecutorStub struct {
	request opscontract.ProfileLeaseRequest
}

func (e *opsMCPProfileLeaseExecutorStub) AuthorizeProfileLease(_ context.Context, request opscontract.ProfileLeaseRequest) error {
	e.request = request
	return nil
}

func (e *opsMCPProfileExecutorStub) CaptureProfile(_ context.Context, request opscontract.ProfileRequest) (opscontract.ProfileResponse, error) {
	e.request = request
	return opscontract.ProfileResponse{Version: opscontract.RPCVersion, Payload: []byte("profile")}, nil
}

type opsMCPRPCNodeStub struct {
	handler *OpsMCPRPCAdapter
	payload []byte
	nodeID  uint64
}

func (n *opsMCPRPCNodeStub) NodeID() uint64 { return n.nodeID }

func (n *opsMCPRPCNodeStub) CallRPC(ctx context.Context, _ uint64, serviceID uint8, payload []byte) ([]byte, error) {
	if serviceID != OpsMCPRPCServiceID {
		return nil, errors.New("unexpected service")
	}
	n.payload = append([]byte(nil), payload...)
	return n.handler.HandleRPC(ctx, payload)
}

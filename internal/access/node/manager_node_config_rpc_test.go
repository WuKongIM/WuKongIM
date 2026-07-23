package node

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestManagerNodeConfigCodecRoundTrip(t *testing.T) {
	req := managerNodeConfigRPCRequest{NodeID: 2}
	body, err := encodeManagerNodeConfigRequest(req)
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	gotReq, err := decodeManagerNodeConfigRequest(body)
	if err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if gotReq != req {
		t.Fatalf("decoded request = %#v, want %#v", gotReq, req)
	}

	resp := managerNodeConfigRPCResponse{
		Status: rpcStatusOK,
		Snapshot: managementusecase.NodeConfigSnapshot{
			GeneratedAt:     time.Unix(10, 0).UTC(),
			NodeID:          2,
			Source:          managementusecase.NodeConfigSnapshotSourceEffectiveStartup,
			RequiresRestart: true,
			Groups: []managementusecase.NodeConfigGroup{{
				ID:    "cluster",
				Title: "Cluster",
				Items: []managementusecase.NodeConfigItem{{
					Key:    "WK_CLUSTER_HASH_SLOT_COUNT",
					Label:  "Hash slot count",
					Value:  "256",
					Source: managementusecase.NodeConfigValueSourceTOML,
				}, {
					Key:       "WK_MANAGER_JWT_SECRET",
					Label:     "Manager JWT secret",
					Value:     "******",
					Source:    managementusecase.NodeConfigValueSourceEnvironment,
					Sensitive: true,
					Redacted:  true,
				}},
			}},
		},
	}
	body, err = encodeManagerNodeConfigResponse(resp)
	if err != nil {
		t.Fatalf("encode response: %v", err)
	}
	gotResp, err := decodeManagerNodeConfigResponse(body)
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !reflect.DeepEqual(gotResp, resp) {
		t.Fatalf("decoded response = %#v, want %#v", gotResp, resp)
	}
}

func TestManagerNodeConfigRPCReadsLocalProvider(t *testing.T) {
	expected := managementusecase.NodeConfigSnapshot{
		GeneratedAt: time.Unix(10, 0).UTC(),
		NodeID:      2,
		Source:      managementusecase.NodeConfigSnapshotSourceEffectiveStartup,
		Groups: []managementusecase.NodeConfigGroup{{
			ID: "cluster",
			Items: []managementusecase.NodeConfigItem{{
				Key:    "WK_CLUSTER_HASH_SLOT_COUNT",
				Label:  "Hash slot count",
				Value:  "256",
				Source: managementusecase.NodeConfigValueSourceTOML,
			}},
		}},
	}
	reader := &fakeManagerNodeConfigReader{snapshot: expected}
	adapter := New(Options{ManagerNodeConfig: reader})

	body, err := encodeManagerNodeConfigRequest(managerNodeConfigRPCRequest{NodeID: 2})
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	respBody, err := adapter.HandleManagerNodeConfigRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleManagerNodeConfigRPC() error = %v", err)
	}
	resp, err := decodeManagerNodeConfigResponse(respBody)
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != rpcStatusOK || resp.Snapshot.NodeID != 2 {
		t.Fatalf("response = %#v, want ok node 2", resp)
	}
	if gotSource := resp.Snapshot.Groups[0].Items[0].Source; gotSource != managementusecase.NodeConfigValueSourceTOML {
		t.Fatalf("remote item source = %q, want toml", gotSource)
	}
	if reader.nodeID != 2 {
		t.Fatalf("reader nodeID = %d, want 2", reader.nodeID)
	}
}

func TestManagerNodeConfigRPCClientCallsExpectedService(t *testing.T) {
	expected := managementusecase.NodeConfigSnapshot{NodeID: 2, Source: managementusecase.NodeConfigSnapshotSourceEffectiveStartup}
	adapter := New(Options{ManagerNodeConfig: &fakeManagerNodeConfigReader{snapshot: expected}})
	node := &fakeManagerNodeConfigRPCNode{handler: adapter.HandleManagerNodeConfigRPC}

	got, err := NewClient(node).GetManagerNodeConfig(context.Background(), 2)
	if err != nil {
		t.Fatalf("GetManagerNodeConfig() error = %v", err)
	}
	if got.NodeID != 2 {
		t.Fatalf("snapshot node = %d, want 2", got.NodeID)
	}
	if node.nodeID != 2 || node.serviceID != ManagerNodeConfigRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want node 2 service %d", node.nodeID, node.serviceID, ManagerNodeConfigRPCServiceID)
	}
}

func TestManagerNodeConfigRPCMapsUnavailable(t *testing.T) {
	adapter := New(Options{ManagerNodeConfig: &fakeManagerNodeConfigReader{err: managementusecase.ErrNodeConfigUnavailable}})
	node := &fakeManagerNodeConfigRPCNode{handler: adapter.HandleManagerNodeConfigRPC}

	_, err := NewClient(node).GetManagerNodeConfig(context.Background(), 2)
	if !errors.Is(err, managementusecase.ErrNodeConfigUnavailable) {
		t.Fatalf("GetManagerNodeConfig() error = %v, want ErrNodeConfigUnavailable", err)
	}
}

func TestManagerNodeConfigRPCMapsNotFound(t *testing.T) {
	adapter := New(Options{ManagerNodeConfig: &fakeManagerNodeConfigReader{err: metadb.ErrNotFound}})
	node := &fakeManagerNodeConfigRPCNode{handler: adapter.HandleManagerNodeConfigRPC}

	_, err := NewClient(node).GetManagerNodeConfig(context.Background(), 2)
	if !errors.Is(err, metadb.ErrNotFound) {
		t.Fatalf("GetManagerNodeConfig() error = %v, want metadb.ErrNotFound", err)
	}
}

func TestManagerNodeConfigRPCStatusMapping(t *testing.T) {
	cases := []struct {
		name   string
		status string
		want   error
	}{
		{name: "invalid argument", status: rpcStatusInvalidArgument, want: metadb.ErrInvalidArgument},
		{name: "not found", status: rpcStatusNotFound, want: metadb.ErrNotFound},
		{name: "unavailable", status: rpcStatusUnavailable, want: managementusecase.ErrNodeConfigUnavailable},
		{name: "rejected", status: rpcStatusRejected, want: managementusecase.ErrNodeConfigUnavailable},
		{name: "canceled", status: rpcStatusContextCanceled, want: context.Canceled},
		{name: "deadline", status: rpcStatusContextDeadlineExceeded, want: context.DeadlineExceeded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := managerNodeConfigRPCErrorForStatus(tc.status)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestManagerNodeConfigRPCUnavailableWhenNotConfigured(t *testing.T) {
	body, err := encodeManagerNodeConfigRequest(managerNodeConfigRPCRequest{NodeID: 2})
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	respBody, err := New(Options{}).HandleManagerNodeConfigRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleManagerNodeConfigRPC() error = %v", err)
	}
	resp, err := decodeManagerNodeConfigResponse(respBody)
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != rpcStatusUnavailable {
		t.Fatalf("status = %q, want %q", resp.Status, rpcStatusUnavailable)
	}
}

type fakeManagerNodeConfigReader struct {
	nodeID   uint64
	snapshot managementusecase.NodeConfigSnapshot
	err      error
}

func (f *fakeManagerNodeConfigReader) NodeConfigSnapshot(_ context.Context, nodeID uint64) (managementusecase.NodeConfigSnapshot, error) {
	f.nodeID = nodeID
	if f.err != nil {
		return managementusecase.NodeConfigSnapshot{}, f.err
	}
	return f.snapshot, nil
}

type fakeManagerNodeConfigRPCNode struct {
	nodeID    uint64
	serviceID uint8
	handler   func(context.Context, []byte) ([]byte, error)
}

func (f *fakeManagerNodeConfigRPCNode) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.nodeID = nodeID
	f.serviceID = serviceID
	return f.handler(ctx, payload)
}

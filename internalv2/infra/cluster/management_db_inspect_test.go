package cluster

import (
	"context"
	"testing"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

func TestManagementDBInspectReaderRoutesToNodeRPC(t *testing.T) {
	adapter := accessnode.New(accessnode.Options{
		ManagerDBInspect: fakeDBInspectLocalReader{
			resp: managementusecase.DBInspectQueryResponse{
				NodeID: 2,
				Rows: []managementusecase.DBInspectRow{
					{"table": "meta.user"},
				},
				Stats: managementusecase.DBInspectStats{ReturnedRows: 1},
			},
		},
	})
	node := &fakeManagementDBInspectNode{handler: adapter.HandleManagerDBInspectRPC}
	reader := NewManagementDBInspectReader(node)

	got, err := reader.NodeDBInspectQuery(context.Background(), managementusecase.DBInspectQueryRequest{
		NodeID: 2,
		Query:  "show tables",
	})
	if err != nil {
		t.Fatalf("NodeDBInspectQuery() error = %v", err)
	}
	if got.NodeID != 2 || got.Rows[0]["table"] != "meta.user" {
		t.Fatalf("response = %#v, want table row from target node", got)
	}
	if node.calledNodeID != 2 || node.calledServiceID != accessnode.ManagerDBInspectRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want node 2 service %d", node.calledNodeID, node.calledServiceID, accessnode.ManagerDBInspectRPCServiceID)
	}
}

type fakeDBInspectLocalReader struct {
	resp managementusecase.DBInspectQueryResponse
}

func (f fakeDBInspectLocalReader) QueryDBInspect(_ context.Context, req managementusecase.DBInspectQueryRequest) (managementusecase.DBInspectQueryResponse, error) {
	resp := f.resp
	resp.NodeID = req.NodeID
	return resp, nil
}

type fakeManagementDBInspectNode struct {
	handler         func(context.Context, []byte) ([]byte, error)
	calledNodeID    uint64
	calledServiceID uint8
}

func (f *fakeManagementDBInspectNode) NodeID() uint64 {
	return 1
}

func (f *fakeManagementDBInspectNode) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.calledNodeID = nodeID
	f.calledServiceID = serviceID
	return f.handler(ctx, payload)
}

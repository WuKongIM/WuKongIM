package node

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	dbinspect "github.com/WuKongIM/WuKongIM/pkg/db/inspect"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestManagerDBInspectCodecRoundTrip(t *testing.T) {
	req := managerDBInspectRPCRequest{NodeID: 2, Query: "show tables"}
	body, err := encodeManagerDBInspectRequest(req)
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	gotReq, err := decodeManagerDBInspectRequest(body)
	if err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if !reflect.DeepEqual(gotReq, req) {
		t.Fatalf("decoded request = %#v, want %#v", gotReq, req)
	}

	resp := managerDBInspectRPCResponse{
		Status: "ok",
		Page: managementusecase.DBInspectQueryResponse{
			NodeID:      2,
			GeneratedAt: time.Unix(100, 0).UTC(),
			Rows: []managementusecase.DBInspectRow{
				{"table": "meta.user", "payload": []byte("abc")},
			},
			Stats: managementusecase.DBInspectStats{ScanMode: "message-catalog", ReturnedRows: 1},
		},
	}
	body, err = encodeManagerDBInspectResponse(resp)
	if err != nil {
		t.Fatalf("encode response: %v", err)
	}
	gotResp, err := decodeManagerDBInspectResponse(body)
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if gotResp.Status != "ok" || gotResp.Page.NodeID != 2 || gotResp.Page.Rows[0]["table"] != "meta.user" {
		t.Fatalf("decoded response = %#v, want encoded response", gotResp)
	}
}

func TestManagerDBInspectRPCServerAndClient(t *testing.T) {
	reader := &fakeManagerDBInspectReader{
		resp: managementusecase.DBInspectQueryResponse{
			NodeID: 2,
			Rows:   []managementusecase.DBInspectRow{{"table": "meta.user"}},
			Stats:  managementusecase.DBInspectStats{ReturnedRows: 1},
		},
	}
	adapter := New(Options{ManagerDBInspect: reader})
	reqBody, err := encodeManagerDBInspectRequest(managerDBInspectRPCRequest{NodeID: 2, Query: "show tables"})
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	respBody, err := adapter.HandleManagerDBInspectRPC(context.Background(), reqBody)
	if err != nil {
		t.Fatalf("HandleManagerDBInspectRPC() error = %v", err)
	}
	resp, err := decodeManagerDBInspectResponse(respBody)
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != rpcStatusOK || resp.Page.Rows[0]["table"] != "meta.user" {
		t.Fatalf("server response = %#v, want ok table row", resp)
	}

	clientNode := &fakeManagerDBInspectRPCNode{response: resp}
	got, err := NewClient(clientNode).NodeDBInspectQuery(context.Background(), managementusecase.DBInspectQueryRequest{NodeID: 2, Query: "show tables"})
	if err != nil {
		t.Fatalf("NodeDBInspectQuery() error = %v", err)
	}
	if got.NodeID != 2 || got.Rows[0]["table"] != "meta.user" {
		t.Fatalf("client response = %#v, want decoded page", got)
	}
	if clientNode.serviceID != ManagerDBInspectRPCServiceID || clientNode.nodeID != 2 {
		t.Fatalf("client call service/node = %d/%d, want db inspect service node 2", clientNode.serviceID, clientNode.nodeID)
	}
}

func TestManagerDBInspectRPCStatusMapping(t *testing.T) {
	cases := []struct {
		name   string
		status string
		want   error
	}{
		{name: "invalid request", status: rpcStatusInvalidRequest, want: metadb.ErrInvalidArgument},
		{name: "invalid cursor", status: rpcStatusInvalidCursor, want: dbinspect.ErrCursorMismatch},
		{name: "unavailable", status: rpcStatusUnavailable, want: managementusecase.ErrDBInspectUnavailable},
		{name: "canceled", status: rpcStatusContextCanceled, want: context.Canceled},
		{name: "deadline", status: rpcStatusContextDeadlineExceeded, want: context.DeadlineExceeded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := managerDBInspectRPCErrorForStatus(tc.status)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestManagerDBInspectRPCRejectedStatusIsNotUnavailable(t *testing.T) {
	err := managerDBInspectRPCErrorForStatus(rpcStatusRejected)
	if err == nil {
		t.Fatal("error = nil, want rejected error")
	}
	if errors.Is(err, managementusecase.ErrDBInspectUnavailable) {
		t.Fatalf("error = %v, must not map rejected status to DB inspect unavailable", err)
	}
}

type fakeManagerDBInspectReader struct {
	resp managementusecase.DBInspectQueryResponse
	err  error
}

func (f *fakeManagerDBInspectReader) QueryDBInspect(_ context.Context, req managementusecase.DBInspectQueryRequest) (managementusecase.DBInspectQueryResponse, error) {
	if f.err != nil {
		return managementusecase.DBInspectQueryResponse{}, f.err
	}
	resp := f.resp
	resp.NodeID = req.NodeID
	return resp, nil
}

type fakeManagerDBInspectRPCNode struct {
	response  managerDBInspectRPCResponse
	nodeID    uint64
	serviceID uint8
	payload   []byte
}

func (f *fakeManagerDBInspectRPCNode) CallRPC(_ context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.nodeID = nodeID
	f.serviceID = serviceID
	f.payload = append([]byte(nil), payload...)
	return encodeManagerDBInspectResponse(f.response)
}

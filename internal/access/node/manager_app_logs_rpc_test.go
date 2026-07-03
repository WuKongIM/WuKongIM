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

func TestManagerAppLogRPCEntriesRoundTrip(t *testing.T) {
	entryTime := time.Unix(100, 123).UTC()
	reader := &fakeManagerAppLogReader{
		entriesResp: managementusecase.ApplicationLogEntriesResponse{
			NodeID:  2,
			Source:  "app",
			Cursor:  "next",
			Rotated: true,
			Items: []managementusecase.ApplicationLogEntry{{
				Seq:       7,
				Offset:    99,
				Time:      entryTime,
				Level:     "warn",
				Module:    "gateway",
				Caller:    "server.go:10",
				Message:   "slow write",
				Fields:    map[string]any{"uid": "u1", "cost": float64(12)},
				Raw:       `{"level":"warn","msg":"slow write"}`,
				Truncated: true,
			}},
		},
	}
	adapter := New(Options{ManagerAppLogs: reader})
	body, err := encodeManagerAppLogRequest(managerAppLogRPCRequest{
		Op:      managerAppLogOpEntries,
		NodeID:  2,
		Source:  "app",
		Limit:   50,
		Cursor:  "cursor-1",
		Keyword: "slow",
		Levels:  []string{"warn", "error"},
	})
	if err != nil {
		t.Fatalf("encodeManagerAppLogRequest() error = %v", err)
	}

	respBody, err := adapter.HandleManagerAppLogRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleManagerAppLogRPC() error = %v", err)
	}
	resp, err := decodeManagerAppLogResponse(respBody)
	if err != nil {
		t.Fatalf("decodeManagerAppLogResponse() error = %v", err)
	}

	if resp.Status != rpcStatusOK {
		t.Fatalf("status = %q, want %q", resp.Status, rpcStatusOK)
	}
	wantReq := managementusecase.ApplicationLogEntriesRequest{
		NodeID:  2,
		Source:  "app",
		Limit:   50,
		Cursor:  "cursor-1",
		Keyword: "slow",
		Levels:  []string{"warn", "error"},
	}
	if !reflect.DeepEqual(reader.entriesReq, wantReq) {
		t.Fatalf("entries request = %#v, want %#v", reader.entriesReq, wantReq)
	}
	if !reflect.DeepEqual(resp.Entries.Items, reader.entriesResp.Items) {
		t.Fatalf("entries = %#v, want %#v", resp.Entries.Items, reader.entriesResp.Items)
	}
	if !resp.Entries.Rotated || resp.Entries.Cursor != "next" {
		t.Fatalf("entries page = %#v, want rotated next cursor", resp.Entries)
	}
}

func TestManagerAppLogRPCSourcesRoundTrip(t *testing.T) {
	modified := time.Unix(200, 456).UTC()
	reader := &fakeManagerAppLogReader{
		sourcesResp: managementusecase.ApplicationLogSourcesResponse{
			NodeID: 2,
			Sources: []managementusecase.ApplicationLogSource{{
				Name:       "app",
				File:       "wukongim.log",
				Available:  true,
				SizeBytes:  12345,
				ModifiedAt: modified,
			}},
		},
	}
	adapter := New(Options{ManagerAppLogs: reader})
	body, err := encodeManagerAppLogRequest(managerAppLogRPCRequest{Op: managerAppLogOpSources, NodeID: 2})
	if err != nil {
		t.Fatalf("encodeManagerAppLogRequest() error = %v", err)
	}

	respBody, err := adapter.HandleManagerAppLogRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleManagerAppLogRPC() error = %v", err)
	}
	resp, err := decodeManagerAppLogResponse(respBody)
	if err != nil {
		t.Fatalf("decodeManagerAppLogResponse() error = %v", err)
	}

	if resp.Status != rpcStatusOK || !reflect.DeepEqual(resp.Sources, reader.sourcesResp) {
		t.Fatalf("sources response = %#v, want ok encoded sources", resp)
	}
	if reader.sourcesReq != (managementusecase.ApplicationLogSourcesRequest{NodeID: 2}) {
		t.Fatalf("sources request = %#v, want node 2", reader.sourcesReq)
	}
}

func TestManagerAppLogRPCZeroTimesRoundTrip(t *testing.T) {
	resp := managerAppLogRPCResponse{
		Status: rpcStatusOK,
		Sources: managementusecase.ApplicationLogSourcesResponse{
			NodeID: 2,
			Sources: []managementusecase.ApplicationLogSource{{
				Name:      "app",
				Available: true,
			}},
		},
		Entries: managementusecase.ApplicationLogEntriesResponse{
			NodeID: 2,
			Source: "app",
			Items: []managementusecase.ApplicationLogEntry{{
				Seq:     1,
				Message: "no timestamp",
			}},
		},
	}

	body, err := encodeManagerAppLogResponse(resp)
	if err != nil {
		t.Fatalf("encodeManagerAppLogResponse() error = %v", err)
	}
	got, err := decodeManagerAppLogResponse(body)
	if err != nil {
		t.Fatalf("decodeManagerAppLogResponse() error = %v", err)
	}

	if !got.Sources.Sources[0].ModifiedAt.IsZero() {
		t.Fatalf("source ModifiedAt = %v, want zero time", got.Sources.Sources[0].ModifiedAt)
	}
	if !got.Entries.Items[0].Time.IsZero() {
		t.Fatalf("entry Time = %v, want zero time", got.Entries.Items[0].Time)
	}
}

func TestManagerAppLogRPCEncodeRejectsUnsupportedFields(t *testing.T) {
	_, err := encodeManagerAppLogResponse(managerAppLogRPCResponse{
		Status: rpcStatusOK,
		Entries: managementusecase.ApplicationLogEntriesResponse{
			NodeID: 2,
			Source: "app",
			Items: []managementusecase.ApplicationLogEntry{{
				Seq:     1,
				Message: "unsupported field",
				Fields:  map[string]any{"bad": func() {}},
			}},
		},
	})
	if err == nil {
		t.Fatal("encodeManagerAppLogResponse() error = nil, want unsupported fields error")
	}
}

func TestManagerAppLogRPCClientUsesServiceAndTargetNode(t *testing.T) {
	resp := managerAppLogRPCResponse{
		Status: rpcStatusOK,
		Entries: managementusecase.ApplicationLogEntriesResponse{
			NodeID: 2,
			Source: "app",
			Items:  []managementusecase.ApplicationLogEntry{{Seq: 1, Message: "ready"}},
		},
	}
	node := &fakeManagerAppLogRPCNode{response: resp}
	got, err := NewClient(node).GetManagerApplicationLogEntries(context.Background(), managementusecase.ApplicationLogEntriesRequest{
		NodeID: 2,
		Source: "app",
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("GetManagerApplicationLogEntries() error = %v", err)
	}

	if len(got.Items) != 1 || got.Items[0].Message != "ready" {
		t.Fatalf("entries page = %#v, want ready entry", got)
	}
	if node.nodeID != 2 || node.serviceID != ManagerAppLogRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want node 2 service %d", node.nodeID, node.serviceID, ManagerAppLogRPCServiceID)
	}
	req, err := decodeManagerAppLogRequest(node.payload)
	if err != nil {
		t.Fatalf("decode client request: %v", err)
	}
	if req.Op != managerAppLogOpEntries || req.NodeID != 2 || req.Source != "app" || req.Limit != 10 {
		t.Fatalf("client request = %#v, want entries request", req)
	}
}

func TestManagerAppLogRPCStatusMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{name: "nil", err: nil, want: rpcStatusOK},
		{name: "canceled", err: context.Canceled, want: rpcStatusContextCanceled},
		{name: "deadline", err: context.DeadlineExceeded, want: rpcStatusContextDeadlineExceeded},
		{name: "not found", err: metadb.ErrNotFound, want: rpcStatusNotFound},
		{name: "invalid argument", err: metadb.ErrInvalidArgument, want: rpcStatusRejected},
		{name: "unavailable", err: managementusecase.ErrApplicationLogReaderUnavailable, want: rpcStatusRejected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := managerAppLogRPCStatusForError(tc.err); got != tc.want {
				t.Fatalf("status = %q, want %q", got, tc.want)
			}
		})
	}

	statusCases := []struct {
		name   string
		status string
		want   error
	}{
		{name: "rejected", status: rpcStatusRejected, want: managementusecase.ErrApplicationLogReaderUnavailable},
		{name: "canceled", status: rpcStatusContextCanceled, want: context.Canceled},
		{name: "deadline", status: rpcStatusContextDeadlineExceeded, want: context.DeadlineExceeded},
		{name: "not found", status: rpcStatusNotFound, want: metadb.ErrNotFound},
	}
	for _, tc := range statusCases {
		t.Run(tc.name, func(t *testing.T) {
			err := managerAppLogRPCErrorForStatus(tc.status)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

type fakeManagerAppLogReader struct {
	sourcesReq  managementusecase.ApplicationLogSourcesRequest
	entriesReq  managementusecase.ApplicationLogEntriesRequest
	sourcesResp managementusecase.ApplicationLogSourcesResponse
	entriesResp managementusecase.ApplicationLogEntriesResponse
	err         error
}

func (f *fakeManagerAppLogReader) ApplicationLogSources(_ context.Context, req managementusecase.ApplicationLogSourcesRequest) (managementusecase.ApplicationLogSourcesResponse, error) {
	f.sourcesReq = req
	if f.err != nil {
		return managementusecase.ApplicationLogSourcesResponse{}, f.err
	}
	return f.sourcesResp, nil
}

func (f *fakeManagerAppLogReader) ApplicationLogEntries(_ context.Context, req managementusecase.ApplicationLogEntriesRequest) (managementusecase.ApplicationLogEntriesResponse, error) {
	f.entriesReq = req
	if f.err != nil {
		return managementusecase.ApplicationLogEntriesResponse{}, f.err
	}
	return f.entriesResp, nil
}

type fakeManagerAppLogRPCNode struct {
	response  managerAppLogRPCResponse
	nodeID    uint64
	serviceID uint8
	payload   []byte
}

func (f *fakeManagerAppLogRPCNode) CallRPC(_ context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.nodeID = nodeID
	f.serviceID = serviceID
	f.payload = append([]byte(nil), payload...)
	return encodeManagerAppLogResponse(f.response)
}

package cluster

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

func TestManagementAppLogReaderUsesLocalEntries(t *testing.T) {
	entryTime := time.Unix(100, 0).UTC()
	local := &fakeManagementAppLogLocal{
		entriesResp: managementusecase.ApplicationLogEntriesResponse{
			NodeID: 1,
			Source: "app",
			Items: []managementusecase.ApplicationLogEntry{{
				Seq:     7,
				Time:    entryTime,
				Level:   "info",
				Message: "local ready",
			}},
		},
	}
	node := &fakeManagementAppLogNode{nodeID: 1}
	reader := NewManagementApplicationLogReader(node, local)

	got, err := reader.ApplicationLogEntries(context.Background(), managementusecase.ApplicationLogEntriesRequest{
		NodeID: 1,
		Source: "app",
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("ApplicationLogEntries() error = %v", err)
	}

	if len(got.Items) != 1 || got.Items[0].Message != "local ready" || !got.Items[0].Time.Equal(entryTime) {
		t.Fatalf("entries = %#v, want local entry", got.Items)
	}
	if !reflect.DeepEqual(local.entriesReq, managementusecase.ApplicationLogEntriesRequest{NodeID: 1, Source: "app", Limit: 10}) {
		t.Fatalf("local entries request = %#v, want node 1 app limit 10", local.entriesReq)
	}
	if node.calledServiceID != 0 {
		t.Fatalf("called service id = %d, want no remote rpc", node.calledServiceID)
	}
}

func TestManagementAppLogReaderUsesLocalEntriesForEmptyNodeID(t *testing.T) {
	local := &fakeManagementAppLogLocal{
		entriesResp: managementusecase.ApplicationLogEntriesResponse{
			NodeID: 1,
			Source: "app",
			Items: []managementusecase.ApplicationLogEntry{{
				Seq:     8,
				Message: "local empty node",
			}},
		},
	}
	node := &fakeManagementAppLogNode{nodeID: 1}
	reader := NewManagementApplicationLogReader(node, local)

	got, err := reader.ApplicationLogEntries(context.Background(), managementusecase.ApplicationLogEntriesRequest{
		NodeID: 0,
		Source: "app",
		Limit:  5,
	})
	if err != nil {
		t.Fatalf("ApplicationLogEntries(empty node) error = %v", err)
	}

	if len(got.Items) != 1 || got.Items[0].Message != "local empty node" {
		t.Fatalf("entries = %#v, want local empty-node entry", got.Items)
	}
	if !reflect.DeepEqual(local.entriesReq, managementusecase.ApplicationLogEntriesRequest{NodeID: 0, Source: "app", Limit: 5}) {
		t.Fatalf("local entries request = %#v, want empty node app limit 5", local.entriesReq)
	}
	if node.calledServiceID != 0 {
		t.Fatalf("called service id = %d, want no remote rpc", node.calledServiceID)
	}
}

func TestManagementAppLogReaderRoutesRemoteEntries(t *testing.T) {
	remote := &fakeManagementAppLogLocal{
		entriesResp: managementusecase.ApplicationLogEntriesResponse{
			NodeID: 2,
			Source: "warn",
			Cursor: "next",
			Items: []managementusecase.ApplicationLogEntry{{
				Seq:     9,
				Level:   "warn",
				Message: "remote slow path",
				Fields:  map[string]any{"uid": "u1"},
			}},
		},
	}
	adapter := accessnode.New(accessnode.Options{ManagerAppLogs: remote})
	node := &fakeManagementAppLogNode{
		nodeID:  1,
		handler: adapter.HandleManagerAppLogRPC,
	}
	reader := NewManagementApplicationLogReader(node, &fakeManagementAppLogLocal{})

	req := managementusecase.ApplicationLogEntriesRequest{
		NodeID:  2,
		Source:  "warn",
		Limit:   20,
		Cursor:  "cursor-1",
		Keyword: "slow",
		Levels:  []string{"warn", "error"},
	}
	got, err := reader.ApplicationLogEntries(context.Background(), req)
	if err != nil {
		t.Fatalf("ApplicationLogEntries() error = %v", err)
	}

	if !reflect.DeepEqual(got, remote.entriesResp) {
		t.Fatalf("remote entries = %#v, want %#v", got, remote.entriesResp)
	}
	if !reflect.DeepEqual(remote.entriesReq, req) {
		t.Fatalf("remote entries request = %#v, want %#v", remote.entriesReq, req)
	}
	if node.calledNodeID != 2 || node.calledServiceID != accessnode.ManagerAppLogRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want node 2 service %d", node.calledNodeID, node.calledServiceID, accessnode.ManagerAppLogRPCServiceID)
	}
}

func TestManagementAppLogReaderRoutesSources(t *testing.T) {
	local := &fakeManagementAppLogLocal{
		sourcesResp: managementusecase.ApplicationLogSourcesResponse{
			NodeID: 1,
			Sources: []managementusecase.ApplicationLogSource{{
				Name:      "app",
				File:      "wukongim.log",
				Available: true,
			}},
		},
	}
	remote := &fakeManagementAppLogLocal{
		sourcesResp: managementusecase.ApplicationLogSourcesResponse{
			NodeID: 2,
			Sources: []managementusecase.ApplicationLogSource{{
				Name:      "error",
				File:      "error.log",
				Available: true,
			}},
		},
	}
	adapter := accessnode.New(accessnode.Options{ManagerAppLogs: remote})
	node := &fakeManagementAppLogNode{
		nodeID:  1,
		handler: adapter.HandleManagerAppLogRPC,
	}
	reader := NewManagementApplicationLogReader(node, local)

	localGot, err := reader.ApplicationLogSources(context.Background(), managementusecase.ApplicationLogSourcesRequest{NodeID: 1})
	if err != nil {
		t.Fatalf("ApplicationLogSources(local) error = %v", err)
	}
	remoteGot, err := reader.ApplicationLogSources(context.Background(), managementusecase.ApplicationLogSourcesRequest{NodeID: 2})
	if err != nil {
		t.Fatalf("ApplicationLogSources(remote) error = %v", err)
	}

	if !reflect.DeepEqual(localGot, local.sourcesResp) {
		t.Fatalf("local sources = %#v, want %#v", localGot, local.sourcesResp)
	}
	if !reflect.DeepEqual(remoteGot, remote.sourcesResp) {
		t.Fatalf("remote sources = %#v, want %#v", remoteGot, remote.sourcesResp)
	}
	if local.sourcesReq != (managementusecase.ApplicationLogSourcesRequest{NodeID: 1}) {
		t.Fatalf("local sources request = %#v, want node 1", local.sourcesReq)
	}
	if remote.sourcesReq != (managementusecase.ApplicationLogSourcesRequest{NodeID: 2}) {
		t.Fatalf("remote sources request = %#v, want node 2", remote.sourcesReq)
	}
}

func TestManagementAppLogReaderMissingLocalReader(t *testing.T) {
	reader := NewManagementApplicationLogReader(&fakeManagementAppLogNode{nodeID: 1}, nil)

	_, err := reader.ApplicationLogEntries(context.Background(), managementusecase.ApplicationLogEntriesRequest{NodeID: 1})
	if !errors.Is(err, managementusecase.ErrApplicationLogReaderUnavailable) {
		t.Fatalf("ApplicationLogEntries() error = %v, want ErrApplicationLogReaderUnavailable", err)
	}
}

type fakeManagementAppLogLocal struct {
	sourcesReq  managementusecase.ApplicationLogSourcesRequest
	entriesReq  managementusecase.ApplicationLogEntriesRequest
	sourcesResp managementusecase.ApplicationLogSourcesResponse
	entriesResp managementusecase.ApplicationLogEntriesResponse
}

func (f *fakeManagementAppLogLocal) ApplicationLogSources(_ context.Context, req managementusecase.ApplicationLogSourcesRequest) (managementusecase.ApplicationLogSourcesResponse, error) {
	f.sourcesReq = req
	return f.sourcesResp, nil
}

func (f *fakeManagementAppLogLocal) ApplicationLogEntries(_ context.Context, req managementusecase.ApplicationLogEntriesRequest) (managementusecase.ApplicationLogEntriesResponse, error) {
	f.entriesReq = req
	return f.entriesResp, nil
}

type fakeManagementAppLogNode struct {
	nodeID          uint64
	calledNodeID    uint64
	calledServiceID uint8
	handler         func(context.Context, []byte) ([]byte, error)
}

func (f *fakeManagementAppLogNode) NodeID() uint64 { return f.nodeID }

func (f *fakeManagementAppLogNode) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.calledNodeID = nodeID
	f.calledServiceID = serviceID
	return f.handler(ctx, payload)
}

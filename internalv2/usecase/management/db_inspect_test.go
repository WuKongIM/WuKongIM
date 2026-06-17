package management

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	dbinspect "github.com/WuKongIM/WuKongIM/pkg/db/inspect"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestDBInspectUsesLocalReaderForEmptyOrLocalNode(t *testing.T) {
	local := &fakeDBInspectReader{
		resp: DBInspectQueryResponse{
			NodeID:      1,
			GeneratedAt: time.Unix(10, 0),
			Rows:        []DBInspectRow{{"table": "meta.user"}},
			Stats:       DBInspectStats{ReturnedRows: 1},
		},
	}
	app := New(Options{
		Cluster:   fakeDBInspectCluster{nodeID: 1, nodes: []uint64{1, 2}},
		DBInspect: local,
		Now:       func() time.Time { return time.Unix(10, 0) },
	})

	got, err := app.QueryDBInspect(context.Background(), DBInspectQueryRequest{Query: "show tables"})
	if err != nil {
		t.Fatalf("QueryDBInspect(local empty node) error = %v", err)
	}
	if got.NodeID != 1 || len(got.Rows) != 1 || got.Rows[0]["table"] != "meta.user" {
		t.Fatalf("local empty node response = %#v, want local node result", got)
	}
	if len(local.calls) != 1 || local.calls[0].NodeID != 1 || local.calls[0].Query != "show tables" {
		t.Fatalf("local calls = %#v, want one normalized local call", local.calls)
	}

	got, err = app.QueryDBInspect(context.Background(), DBInspectQueryRequest{NodeID: 1, Query: "describe meta.user"})
	if err != nil {
		t.Fatalf("QueryDBInspect(local node) error = %v", err)
	}
	if got.NodeID != 1 {
		t.Fatalf("local node response node = %d, want 1", got.NodeID)
	}
	if len(local.calls) != 2 || local.calls[1].Query != "describe meta.user" {
		t.Fatalf("local calls after explicit local = %#v", local.calls)
	}
}

func TestDBInspectRoutesRemoteNode(t *testing.T) {
	local := &fakeDBInspectReader{}
	remote := &fakeRemoteDBInspectReader{
		resp: DBInspectQueryResponse{
			NodeID:      2,
			GeneratedAt: time.Unix(20, 0),
			Rows:        []DBInspectRow{{"node": float64(2)}},
			Stats:       DBInspectStats{ReturnedRows: 1},
		},
	}
	app := New(Options{
		Cluster:         fakeDBInspectCluster{nodeID: 1, nodes: []uint64{1, 2}},
		DBInspect:       local,
		RemoteDBInspect: remote,
	})

	got, err := app.QueryDBInspect(context.Background(), DBInspectQueryRequest{NodeID: 2, Query: "show tables"})
	if err != nil {
		t.Fatalf("QueryDBInspect(remote) error = %v", err)
	}
	if got.NodeID != 2 || len(remote.calls) != 1 || remote.calls[0].NodeID != 2 {
		t.Fatalf("remote response/calls = %#v %#v, want node 2", got, remote.calls)
	}
	if len(local.calls) != 0 {
		t.Fatalf("local calls = %#v, want none for remote query", local.calls)
	}
}

func TestDBInspectRejectsInvalidRequests(t *testing.T) {
	app := New(Options{
		Cluster:   fakeDBInspectCluster{nodeID: 1, nodes: []uint64{1}},
		DBInspect: &fakeDBInspectReader{},
	})

	if _, err := app.QueryDBInspect(context.Background(), DBInspectQueryRequest{Query: "   "}); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("empty query err = %v, want invalid argument", err)
	}
	if _, err := app.DescribeDBInspectTable(context.Background(), 0, "../meta", "user"); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("bad domain err = %v, want invalid argument", err)
	}
	if _, err := app.DescribeDBInspectTable(context.Background(), 0, "meta", "user/x"); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("bad table err = %v, want invalid argument", err)
	}
	if _, err := app.QueryDBInspect(context.Background(), DBInspectQueryRequest{NodeID: 9, Query: "show tables"}); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("unknown node err = %v, want invalid argument", err)
	}
}

func TestDBInspectUnavailableErrors(t *testing.T) {
	app := New(Options{Cluster: fakeDBInspectCluster{nodeID: 1, nodes: []uint64{1, 2}}})
	if _, err := app.QueryDBInspect(context.Background(), DBInspectQueryRequest{Query: "show tables"}); !errors.Is(err, ErrDBInspectUnavailable) {
		t.Fatalf("missing local reader err = %v, want ErrDBInspectUnavailable", err)
	}

	app = New(Options{
		Cluster:   fakeDBInspectCluster{nodeID: 1, nodes: []uint64{1, 2}},
		DBInspect: &fakeDBInspectReader{},
	})
	if _, err := app.QueryDBInspect(context.Background(), DBInspectQueryRequest{NodeID: 2, Query: "show tables"}); !errors.Is(err, ErrDBInspectUnavailable) {
		t.Fatalf("missing remote reader err = %v, want ErrDBInspectUnavailable", err)
	}
}

func TestDBInspectPreservesRemoteQueryErrors(t *testing.T) {
	remote := &fakeRemoteDBInspectReader{err: dbinspect.ErrInvalidQuery}
	app := New(Options{
		Cluster:         fakeDBInspectCluster{nodeID: 1, nodes: []uint64{1, 2}},
		DBInspect:       &fakeDBInspectReader{},
		RemoteDBInspect: remote,
	})

	_, err := app.QueryDBInspect(context.Background(), DBInspectQueryRequest{NodeID: 2, Query: "select * from meta.user order by uid"})
	if !errors.Is(err, dbinspect.ErrInvalidQuery) {
		t.Fatalf("remote invalid query err = %v, want ErrInvalidQuery", err)
	}
	if len(remote.calls) != 1 || remote.calls[0].NodeID != 2 {
		t.Fatalf("remote calls = %#v, want one node 2 call", remote.calls)
	}
}

func TestDBInspectConvenienceQueries(t *testing.T) {
	local := &fakeDBInspectReader{
		resp: DBInspectQueryResponse{NodeID: 1, Rows: []DBInspectRow{{"column": "uid"}}, Stats: DBInspectStats{ReturnedRows: 1}},
	}
	app := New(Options{
		Cluster:   fakeDBInspectCluster{nodeID: 1, nodes: []uint64{1}},
		DBInspect: local,
	})

	if _, err := app.ListDBInspectTables(context.Background(), 0); err != nil {
		t.Fatalf("ListDBInspectTables() error = %v", err)
	}
	if _, err := app.DescribeDBInspectTable(context.Background(), 0, "meta", "user"); err != nil {
		t.Fatalf("DescribeDBInspectTable() error = %v", err)
	}
	want := []string{"show tables", "describe meta.user"}
	var got []string
	for _, call := range local.calls {
		got = append(got, call.Query)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("queries = %#v, want %#v", got, want)
	}
}

type fakeDBInspectReader struct {
	resp  DBInspectQueryResponse
	err   error
	calls []DBInspectQueryRequest
}

func (f *fakeDBInspectReader) QueryDBInspect(_ context.Context, req DBInspectQueryRequest) (DBInspectQueryResponse, error) {
	f.calls = append(f.calls, req)
	if f.err != nil {
		return DBInspectQueryResponse{}, f.err
	}
	resp := f.resp
	resp.NodeID = req.NodeID
	return resp, nil
}

type fakeRemoteDBInspectReader struct {
	resp  DBInspectQueryResponse
	err   error
	calls []DBInspectQueryRequest
}

func (f *fakeRemoteDBInspectReader) NodeDBInspectQuery(_ context.Context, req DBInspectQueryRequest) (DBInspectQueryResponse, error) {
	f.calls = append(f.calls, req)
	if f.err != nil {
		return DBInspectQueryResponse{}, f.err
	}
	resp := f.resp
	resp.NodeID = req.NodeID
	return resp, nil
}

type fakeDBInspectCluster struct {
	nodeID uint64
	nodes  []uint64
	err    error
}

func (f fakeDBInspectCluster) NodeID() uint64 { return f.nodeID }

func (f fakeDBInspectCluster) LocalControlSnapshot(context.Context) (control.Snapshot, error) {
	if f.err != nil {
		return control.Snapshot{}, f.err
	}
	nodes := make([]control.Node, 0, len(f.nodes))
	for _, id := range f.nodes {
		nodes = append(nodes, control.Node{NodeID: id})
	}
	return control.Snapshot{Nodes: nodes}, nil
}

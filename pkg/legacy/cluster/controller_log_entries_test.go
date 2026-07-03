package cluster

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	raftstorage "github.com/WuKongIM/WuKongIM/pkg/raftlog"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"go.etcd.io/raft/v3/raftpb"
)

func TestControllerLogEntriesOnNodeReadsLocalControllerLogDescending(t *testing.T) {
	db, err := raftstorage.Open(filepath.Join(t.TempDir(), "controller-raft"), raftstorage.Options{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store := db.ForController()
	hs := raftpb.HardState{Term: 2, Commit: 4}
	if err := store.Save(context.Background(), multiraft.PersistentState{
		HardState: &hs,
		Entries: []raftpb.Entry{
			{Index: 1, Term: 1, Type: raftpb.EntryNormal},
			{Index: 2, Term: 1, Type: raftpb.EntryNormal, Data: []byte("corrupt")},
			{Index: 3, Term: 2, Type: raftpb.EntryConfChange, Data: mustMarshalControllerLogConfChange(t)},
			{Index: 4, Term: 2, Type: raftpb.EntryNormal},
		},
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.MarkApplied(context.Background(), 3); err != nil {
		t.Fatalf("MarkApplied() error = %v", err)
	}

	cluster := &Cluster{
		cfg:                 Config{NodeID: 1},
		router:              NewRouter(NewHashSlotTable(4, 1), 1, nil),
		controllerResources: controllerResources{controllerHost: &controllerHost{raftDB: db}},
	}

	got, err := cluster.ControllerLogEntriesOnNode(context.Background(), 1, ControllerLogEntriesOptions{
		Limit:  2,
		Cursor: 5,
	})
	if err != nil {
		t.Fatalf("ControllerLogEntriesOnNode() error = %v", err)
	}

	want := ControllerLogEntries{
		NodeID:       1,
		FirstIndex:   1,
		LastIndex:    4,
		CommitIndex:  4,
		AppliedIndex: 3,
		NextCursor:   3,
		Items: []ControllerLogEntry{{
			Index:        4,
			Term:         2,
			Type:         "normal",
			DecodeStatus: "empty",
			DecodedType:  "noop",
			Decoded:      map[string]any{"command": "noop"},
		}, {
			Index:    3,
			Term:     2,
			Type:     "conf_change",
			DataSize: len(mustMarshalControllerLogConfChange(t)),
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ControllerLogEntriesOnNode() = %+v, want %+v", got, want)
	}
}

func mustMarshalControllerLogConfChange(t *testing.T) []byte {
	t.Helper()
	cc := raftpb.ConfChange{Type: raftpb.ConfChangeAddNode, NodeID: 1}
	data, err := cc.Marshal()
	if err != nil {
		t.Fatalf("ConfChange.Marshal() error = %v", err)
	}
	return data
}

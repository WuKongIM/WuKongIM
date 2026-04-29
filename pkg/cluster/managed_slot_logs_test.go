package cluster

import (
	"context"
	"errors"
	"reflect"
	"testing"

	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"go.etcd.io/raft/v3/raftpb"
)

func TestSlotLogEntriesOnNodeReadsLatestEntriesDescending(t *testing.T) {
	userCommand := metafsm.EncodeUpsertUserCommand(metadb.User{
		UID:         "u1",
		Token:       "secret-token",
		DeviceFlag:  3,
		DeviceLevel: 7,
	})
	userProposal := encodeProposalPayload(0, userCommand)
	storage := &slotLogEntriesStorage{
		entries: []raftpb.Entry{
			{Index: 1, Term: 1, Type: raftpb.EntryNormal, Data: []byte("one")},
			{Index: 2, Term: 1, Type: raftpb.EntryNormal, Data: []byte("two")},
			{Index: 3, Term: 2, Type: raftpb.EntryConfChange, Data: []byte("config")},
			{Index: 4, Term: 2, Type: raftpb.EntryNormal, Data: userProposal},
		},
	}
	cluster := &Cluster{
		cfg: Config{
			NodeID: 1,
			NewStorage: func(slotID multiraft.SlotID) (multiraft.Storage, error) {
				if slotID != 9 {
					t.Fatalf("NewStorage slotID = %d, want 9", slotID)
				}
				return storage, nil
			},
		},
		router: NewRouter(NewHashSlotTable(4, 1), 1, nil),
	}
	restore := cluster.setManagedSlotStatusTestHook(func(_ *Cluster, nodeID multiraft.NodeID, slotID multiraft.SlotID) (managedSlotStatus, error, bool) {
		if nodeID != 1 || slotID != 9 {
			t.Fatalf("status hook node/slot = %d/%d, want 1/9", nodeID, slotID)
		}
		return managedSlotStatus{LeaderID: 1, CommitIndex: 4, AppliedIndex: 3}, nil, true
	})
	defer restore()

	got, err := cluster.SlotLogEntriesOnNode(context.Background(), 1, 9, SlotLogEntriesOptions{
		Limit:  2,
		Cursor: 5,
	})
	if err != nil {
		t.Fatalf("SlotLogEntriesOnNode() error = %v", err)
	}

	want := SlotLogEntries{
		NodeID:       1,
		SlotID:       9,
		FirstIndex:   1,
		LastIndex:    4,
		CommitIndex:  4,
		AppliedIndex: 3,
		NextCursor:   3,
		Items: []SlotLogEntry{{
			Index:        4,
			Term:         2,
			Type:         "normal",
			DataSize:     len(userProposal),
			DecodeStatus: "ok",
			DecodedType:  "upsert_user",
			Decoded: map[string]any{
				"command":      "upsert_user",
				"uid":          "u1",
				"token":        "***",
				"device_flag":  int64(3),
				"device_level": int64(7),
			},
		}, {
			Index:    3,
			Term:     2,
			Type:     "conf_change",
			DataSize: 6,
		}},
	}
	if !equalSlotLogEntries(got, want) {
		t.Fatalf("SlotLogEntriesOnNode() = %+v, want %+v", got, want)
	}
}

type slotLogEntriesStorage struct {
	entries []raftpb.Entry
}

func (s *slotLogEntriesStorage) InitialState(context.Context) (multiraft.BootstrapState, error) {
	return multiraft.BootstrapState{}, nil
}

func (s *slotLogEntriesStorage) Entries(_ context.Context, lo, hi, _ uint64) ([]raftpb.Entry, error) {
	if hi < lo {
		return nil, errors.New("invalid range")
	}
	out := make([]raftpb.Entry, 0, hi-lo)
	for _, entry := range s.entries {
		if entry.Index >= lo && entry.Index < hi {
			out = append(out, entry)
		}
	}
	return out, nil
}

func (s *slotLogEntriesStorage) Term(context.Context, uint64) (uint64, error) {
	return 0, nil
}

func (s *slotLogEntriesStorage) FirstIndex(context.Context) (uint64, error) {
	if len(s.entries) == 0 {
		return 1, nil
	}
	return s.entries[0].Index, nil
}

func (s *slotLogEntriesStorage) LastIndex(context.Context) (uint64, error) {
	if len(s.entries) == 0 {
		return 0, nil
	}
	return s.entries[len(s.entries)-1].Index, nil
}

func (s *slotLogEntriesStorage) Snapshot(context.Context) (raftpb.Snapshot, error) {
	return raftpb.Snapshot{}, nil
}

func (s *slotLogEntriesStorage) Save(context.Context, multiraft.PersistentState) error {
	return nil
}

func (s *slotLogEntriesStorage) MarkApplied(context.Context, uint64) error {
	return nil
}

func equalSlotLogEntries(left, right SlotLogEntries) bool {
	if left.NodeID != right.NodeID ||
		left.SlotID != right.SlotID ||
		left.FirstIndex != right.FirstIndex ||
		left.LastIndex != right.LastIndex ||
		left.CommitIndex != right.CommitIndex ||
		left.AppliedIndex != right.AppliedIndex ||
		left.NextCursor != right.NextCursor ||
		len(left.Items) != len(right.Items) {
		return false
	}
	for i := range left.Items {
		if !reflect.DeepEqual(left.Items[i], right.Items[i]) {
			return false
		}
	}
	return true
}

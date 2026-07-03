package cluster

import (
	"context"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cmdsync"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestCMDSyncStoreListsCMDKindOnly(t *testing.T) {
	node := &cmdSyncNodeFake{
		rows: []metadb.ConversationState{
			{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "normal", ChannelType: 2, ActiveAt: 300},
			{UID: "u1", Kind: metadb.ConversationKindCMD, ChannelID: "cmd____cmd", ChannelType: 2, ActiveAt: 200},
		},
	}
	store := NewCMDSyncStore(node)

	rows, err := store.ListConversationActiveView(context.Background(), "u1", 10)
	if err != nil {
		t.Fatalf("ListConversationActiveView(): %v", err)
	}
	if got, want := node.activeCalls, []cmdSyncActiveCallFake{{kind: metadb.ConversationKindCMD, uid: "u1", limit: 10}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("active calls = %#v, want %#v", got, want)
	}
	if len(rows) != 1 || rows[0].Kind != metadb.ConversationKindCMD || rows[0].ChannelID != "cmd____cmd" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestCMDSyncStoreUpsertsCMDKindRows(t *testing.T) {
	node := &cmdSyncNodeFake{}
	store := NewCMDSyncStore(node)
	states := []metadb.ConversationState{{
		UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ReadSeq: 7,
	}}

	if err := store.UpsertConversationStates(context.Background(), states); err != nil {
		t.Fatalf("UpsertConversationStates(): %v", err)
	}
	states[0].ReadSeq = 1

	if got, want := node.upserts, []metadb.ConversationState{{
		UID: "u1", Kind: metadb.ConversationKindCMD, ChannelID: "g1", ChannelType: 2, ReadSeq: 7,
	}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("upserts = %#v, want %#v", got, want)
	}
}

func TestCMDMessageReaderReadsCommittedCommandMessages(t *testing.T) {
	node := &cmdSyncNodeFake{
		readResult: channelstore.ReadCommittedResult{Messages: []channelruntime.Message{
			{MessageID: 10, MessageSeq: 3, ChannelID: "g1", ChannelType: 2, FromUID: "u2", ClientMsgNo: "normal", ServerTimestampMS: 90, Payload: []byte("normal")},
			{MessageID: 11, MessageSeq: 4, ChannelID: "g1", ChannelType: 2, FromUID: "u2", ClientMsgNo: "c1", ServerTimestampMS: 99, SyncOnce: true, Payload: []byte("x")},
		}, NextSeq: 5},
	}
	store := NewCMDSyncStore(node)

	msgs, err := store.LoadCommandMessages(context.Background(), cmdsync.CommandChannelKey{ChannelID: "g1", ChannelType: 2}, 3, 1)
	if err != nil {
		t.Fatalf("LoadCommandMessages(): %v", err)
	}
	if node.lastReadID != (channelruntime.ChannelID{ID: "g1", Type: 2}) {
		t.Fatalf("read channel id = %#v, want source channel", node.lastReadID)
	}
	if node.lastReadReq.FromSeq != 3 || node.lastReadReq.Limit != cmdSyncReadPageLimit || node.lastReadReq.Reverse || node.lastReadReq.MaxBytes != maxInt() {
		t.Fatalf("read request = %#v, want forward from seq 3 page limit", node.lastReadReq)
	}
	if len(msgs) != 1 || msgs[0].MessageSeq != 4 || msgs[0].MessageID != 11 || msgs[0].ServerTimestampMS != 99 || !msgs[0].SyncOnce || string(msgs[0].Payload) != "x" {
		t.Fatalf("msgs = %+v", msgs)
	}
	msgs[0].Payload[0] = 'X'
	again, err := store.LoadCommandMessages(context.Background(), cmdsync.CommandChannelKey{ChannelID: "g1", ChannelType: 2}, 3, 1)
	if err != nil {
		t.Fatalf("LoadCommandMessages(again): %v", err)
	}
	if string(again[0].Payload) != "x" {
		t.Fatalf("message payload aliases node storage: %q", again[0].Payload)
	}
}

func TestCMDMessageReaderScansPastOrdinaryMessages(t *testing.T) {
	node := &cmdSyncNodeFake{
		readPages: map[uint64]channelstore.ReadCommittedResult{
			1: {Messages: []channelruntime.Message{
				{MessageID: 10, MessageSeq: 1, ChannelID: "g1", ChannelType: 2, ClientMsgNo: "normal-1"},
				{MessageID: 11, MessageSeq: 2, ChannelID: "g1", ChannelType: 2, ClientMsgNo: "normal-2"},
			}, NextSeq: 3},
			3: {Messages: []channelruntime.Message{
				{MessageID: 12, MessageSeq: 3, ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmd-1", SyncOnce: true},
				{MessageID: 13, MessageSeq: 4, ChannelID: "g1", ChannelType: 2, ClientMsgNo: "normal-3"},
			}, NextSeq: 5},
		},
	}
	store := NewCMDSyncStore(node)

	msgs, err := store.LoadCommandMessages(context.Background(), cmdsync.CommandChannelKey{ChannelID: "g1", ChannelType: 2}, 1, 1)
	if err != nil {
		t.Fatalf("LoadCommandMessages(): %v", err)
	}
	if got, want := node.readFromSeqs, []uint64{1, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("read from seqs = %#v, want %#v", got, want)
	}
	if len(msgs) != 1 || msgs[0].ClientMsgNo != "cmd-1" || msgs[0].MessageSeq != 3 {
		t.Fatalf("msgs = %+v, want only command message", msgs)
	}
}

type cmdSyncActiveCallFake struct {
	kind  metadb.ConversationKind
	uid   string
	limit int
}

type cmdSyncNodeFake struct {
	rows         []metadb.ConversationState
	activeCalls  []cmdSyncActiveCallFake
	upserts      []metadb.ConversationState
	lastReadID   channelruntime.ChannelID
	lastReadReq  channelstore.ReadCommittedRequest
	readResult   channelstore.ReadCommittedResult
	readPages    map[uint64]channelstore.ReadCommittedResult
	readFromSeqs []uint64
}

func (n *cmdSyncNodeFake) ListConversationActivePage(_ context.Context, kind metadb.ConversationKind, uid string, _ metadb.ConversationActiveCursor, limit int) ([]metadb.ConversationState, metadb.ConversationActiveCursor, bool, error) {
	n.activeCalls = append(n.activeCalls, cmdSyncActiveCallFake{kind: kind, uid: uid, limit: limit})
	rows := make([]metadb.ConversationState, 0, len(n.rows))
	for _, row := range n.rows {
		if row.UID == uid && row.Kind == kind {
			rows = append(rows, row)
		}
	}
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, metadb.ConversationActiveCursor{}, true, nil
}

func (n *cmdSyncNodeFake) UpsertConversationStatesBatch(_ context.Context, states []metadb.ConversationState) error {
	n.upserts = append(n.upserts, states...)
	return nil
}

func (n *cmdSyncNodeFake) ReadChannelCommitted(_ context.Context, id channelruntime.ChannelID, req channelstore.ReadCommittedRequest) (channelstore.ReadCommittedResult, error) {
	n.lastReadID = id
	n.lastReadReq = req
	n.readFromSeqs = append(n.readFromSeqs, req.FromSeq)
	if n.readPages != nil {
		return n.readPages[req.FromSeq], nil
	}
	return n.readResult, nil
}

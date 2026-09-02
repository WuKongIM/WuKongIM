package cluster

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cmdsync"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	clusterchannels "github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestCMDSyncStoreListsCMDMemberships(t *testing.T) {
	node := &cmdSyncNodeFake{
		rows: []metadb.UserCMDChannelMembership{
			{UID: "u1", CommandChannelID: "cmd____cmd", ChannelType: 2, StartSeq: 3},
		},
	}
	store := NewCMDSyncStore(node)

	rows, _, done, err := store.ListUserCMDChannelMembershipPage(context.Background(), "u1", metadb.UserCMDChannelMembershipCursor{}, 10)
	if err != nil {
		t.Fatalf("ListUserCMDChannelMembershipPage(): %v", err)
	}
	if !done || node.listUID != "u1" || node.listLimit != 10 {
		t.Fatalf("list call uid=%q limit=%d done=%v", node.listUID, node.listLimit, done)
	}
	if len(rows) != 1 || rows[0].CommandChannelID != "cmd____cmd" || rows[0].StartSeq != 3 {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestCMDSyncStoreAdvancesCMDMembershipAcks(t *testing.T) {
	node := &cmdSyncNodeFake{}
	store := NewCMDSyncStore(node)
	memberships := []metadb.UserCMDChannelMembership{{
		UID: "u1", CommandChannelID: "g1____cmd", ChannelType: 2, AckSeq: 7,
	}}

	if err := store.AdvanceUserCMDChannelMembershipAcks(context.Background(), memberships); err != nil {
		t.Fatalf("AdvanceUserCMDChannelMembershipAcks(): %v", err)
	}
	memberships[0].AckSeq = 1

	if got, want := node.acks, []metadb.UserCMDChannelMembership{{
		UID: "u1", CommandChannelID: "g1____cmd", ChannelType: 2, AckSeq: 7,
	}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("acks = %#v, want %#v", got, want)
	}
}

func TestCMDSyncStoreClonesDirectoryMutationsAndPreservesTailIdentity(t *testing.T) {
	t.Parallel()

	node := &cmdSyncNodeFake{mutateMembershipInputs: true, committedTail: 81}
	store := NewCMDSyncStore(node)
	upserts := []metadb.UserCMDChannelMembership{{UID: "u1", CommandChannelID: "g1____cmd", ChannelType: 2, StartSeq: 7}}
	tombstones := []metadb.UserCMDChannelMembership{{UID: "u1", CommandChannelID: "g2____cmd", ChannelType: 3, StartSeq: 9}}

	if err := store.UpsertUserCMDChannelMemberships(context.Background(), upserts); err != nil {
		t.Fatalf("UpsertUserCMDChannelMemberships() error = %v", err)
	}
	if err := store.TombstoneUserCMDChannelMemberships(context.Background(), tombstones); err != nil {
		t.Fatalf("TombstoneUserCMDChannelMemberships() error = %v", err)
	}
	if upserts[0].UID != "u1" || tombstones[0].UID != "u1" {
		t.Fatalf("caller-owned membership slices mutated: upserts=%#v tombstones=%#v", upserts, tombstones)
	}
	if len(node.upserts) != 1 || node.upserts[0].CommandChannelID != "g1____cmd" || len(node.tombstones) != 1 || node.tombstones[0].CommandChannelID != "g2____cmd" {
		t.Fatalf("forwarded mutations upserts=%#v tombstones=%#v", node.upserts, node.tombstones)
	}

	tail, err := store.CommandChannelTail(context.Background(), cmdsync.CommandChannelKey{ChannelID: "g1____cmd", ChannelType: 2})
	if err != nil || tail != 81 || node.tailChannelID != "g1____cmd" || node.tailChannelType != 2 {
		t.Fatalf("CommandChannelTail() = %d args=%q/%d err=%v", tail, node.tailChannelID, node.tailChannelType, err)
	}

	upsertCalls, tombstoneCalls := node.upsertCalls, node.tombstoneCalls
	if err := store.UpsertUserCMDChannelMemberships(context.Background(), nil); err != nil {
		t.Fatalf("UpsertUserCMDChannelMemberships(empty) error = %v", err)
	}
	if err := store.TombstoneUserCMDChannelMemberships(context.Background(), nil); err != nil {
		t.Fatalf("TombstoneUserCMDChannelMemberships(empty) error = %v", err)
	}
	if node.upsertCalls != upsertCalls || node.tombstoneCalls != tombstoneCalls {
		t.Fatalf("empty mutations reached node: upsert=%d tombstone=%d", node.upsertCalls, node.tombstoneCalls)
	}
}

func TestCMDMessageReaderReadsCommittedCommandMessages(t *testing.T) {
	node := &cmdSyncNodeFake{
		readResult: channelstore.ReadCommittedResult{Messages: []channelruntime.Message{
			{MessageID: 11, MessageSeq: 4, ChannelID: "g1____cmd", ChannelType: 2, FromUID: "u2", ClientMsgNo: "c1", ServerTimestampMS: 99, Payload: []byte("x")},
		}, NextSeq: 5},
	}
	store := NewCMDSyncStore(node)

	msgs, err := store.LoadCommandMessages(context.Background(), cmdsync.CommandChannelKey{ChannelID: "g1____cmd", ChannelType: 2}, 3, 1)
	if err != nil {
		t.Fatalf("LoadCommandMessages(): %v", err)
	}
	if node.lastReadID != (channelruntime.ChannelID{ID: "g1____cmd", Type: 2}) {
		t.Fatalf("read channel id = %#v, want command channel", node.lastReadID)
	}
	if node.lastReadReq.FromSeq != 3 || node.lastReadReq.Limit != cmdSyncReadPageLimit || node.lastReadReq.Reverse || node.lastReadReq.MaxBytes != maxInt() {
		t.Fatalf("read request = %#v, want forward from seq 3 page limit", node.lastReadReq)
	}
	if node.batchReadCalls != 1 {
		t.Fatalf("batch read calls = %d, want routed cluster batch read", node.batchReadCalls)
	}
	if len(msgs) != 1 || msgs[0].MessageSeq != 4 || msgs[0].MessageID != 11 || msgs[0].ServerTimestampMS != 99 || !msgs[0].SyncOnce || string(msgs[0].Payload) != "x" {
		t.Fatalf("msgs = %+v", msgs)
	}
	msgs[0].Payload[0] = 'X'
	again, err := store.LoadCommandMessages(context.Background(), cmdsync.CommandChannelKey{ChannelID: "g1____cmd", ChannelType: 2}, 3, 1)
	if err != nil {
		t.Fatalf("LoadCommandMessages(again): %v", err)
	}
	if string(again[0].Payload) != "x" {
		t.Fatalf("message payload aliases node storage: %q", again[0].Payload)
	}
}

func TestCMDMessageReaderDoesNotFilterIsolatedCommandLog(t *testing.T) {
	node := &cmdSyncNodeFake{
		readResult: channelstore.ReadCommittedResult{Messages: []channelruntime.Message{
			{MessageID: 10, MessageSeq: 1, ChannelID: "g1____cmd", ChannelType: 2, ClientMsgNo: "cmd-1"},
			{MessageID: 11, MessageSeq: 2, ChannelID: "g1____cmd", ChannelType: 2, ClientMsgNo: "cmd-2"},
		}, NextSeq: 3},
	}
	store := NewCMDSyncStore(node)

	msgs, err := store.LoadCommandMessages(context.Background(), cmdsync.CommandChannelKey{ChannelID: "g1____cmd", ChannelType: 2}, 1, 2)
	if err != nil {
		t.Fatalf("LoadCommandMessages(): %v", err)
	}
	if got, want := node.readFromSeqs, []uint64{1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("read from seqs = %#v, want %#v", got, want)
	}
	if len(msgs) != 2 || msgs[0].ClientMsgNo != "cmd-1" || msgs[1].ClientMsgNo != "cmd-2" || !msgs[0].SyncOnce || !msgs[1].SyncOnce {
		t.Fatalf("msgs = %+v, want isolated command messages", msgs)
	}
}

func TestCMDMessageReaderRejectsDisbandedSourceChannel(t *testing.T) {
	node := &cmdSyncNodeFake{channel: metadb.Channel{ChannelID: "g1", ChannelType: 2, Disband: 1}}
	store := NewCMDSyncStore(node)

	_, err := store.LoadCommandMessages(context.Background(), cmdsync.CommandChannelKey{ChannelID: "g1____cmd", ChannelType: 2}, 1, 10)
	if !errors.Is(err, cmdsync.ErrChannelDisbanded) || len(node.readFromSeqs) != 0 {
		t.Fatalf("LoadCommandMessages() error=%v reads=%+v", err, node.readFromSeqs)
	}
}

type cmdSyncNodeFake struct {
	rows                   []metadb.UserCMDChannelMembership
	listUID                string
	listLimit              int
	acks                   []metadb.UserCMDChannelMembership
	upserts                []metadb.UserCMDChannelMembership
	tombstones             []metadb.UserCMDChannelMembership
	lastReadID             channelruntime.ChannelID
	lastReadReq            channelstore.ReadCommittedRequest
	readResult             channelstore.ReadCommittedResult
	readPages              map[uint64]channelstore.ReadCommittedResult
	readFromSeqs           []uint64
	batchReadCalls         int
	channel                metadb.Channel
	channelErr             error
	mutateMembershipInputs bool
	upsertCalls            int
	tombstoneCalls         int
	committedTail          uint64
	tailChannelID          string
	tailChannelType        int64
}

func (n *cmdSyncNodeFake) UpsertUserCMDChannelMemberships(_ context.Context, memberships []metadb.UserCMDChannelMembership) error {
	n.upsertCalls++
	n.upserts = append(n.upserts, memberships...)
	if n.mutateMembershipInputs && len(memberships) > 0 {
		memberships[0].UID = "node-mutated"
	}
	return nil
}

func (n *cmdSyncNodeFake) ListUserCMDChannelMembershipPage(_ context.Context, uid string, _ metadb.UserCMDChannelMembershipCursor, limit int) ([]metadb.UserCMDChannelMembership, metadb.UserCMDChannelMembershipCursor, bool, error) {
	n.listUID, n.listLimit = uid, limit
	rows := make([]metadb.UserCMDChannelMembership, 0, len(n.rows))
	for _, row := range n.rows {
		if row.UID == uid {
			rows = append(rows, row)
		}
	}
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, metadb.UserCMDChannelMembershipCursor{}, true, nil
}

func (n *cmdSyncNodeFake) AdvanceUserCMDChannelMembershipAcks(_ context.Context, memberships []metadb.UserCMDChannelMembership) error {
	n.acks = append(n.acks, memberships...)
	return nil
}

func (n *cmdSyncNodeFake) TombstoneUserCMDChannelMemberships(_ context.Context, memberships []metadb.UserCMDChannelMembership) error {
	n.tombstoneCalls++
	n.tombstones = append(n.tombstones, memberships...)
	if n.mutateMembershipInputs && len(memberships) > 0 {
		memberships[0].UID = "node-mutated"
	}
	return nil
}

func (n *cmdSyncNodeFake) CommittedChannelTail(_ context.Context, channelID string, channelType int64) (uint64, error) {
	n.tailChannelID, n.tailChannelType = channelID, channelType
	return n.committedTail, nil
}

func (n *cmdSyncNodeFake) GetChannelMetadataAuthoritative(context.Context, string, int64) (metadb.Channel, error) {
	if n.channelErr != nil {
		return metadb.Channel{}, n.channelErr
	}
	if n.channel.ChannelID == "" {
		return metadb.Channel{}, metadb.ErrNotFound
	}
	return n.channel, nil
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

func (n *cmdSyncNodeFake) ReadChannelCommittedBatch(ctx context.Context, reads []clusterchannels.CommittedRead) ([]clusterchannels.CommittedReadResult, error) {
	n.batchReadCalls++
	results := make([]clusterchannels.CommittedReadResult, len(reads))
	for index, read := range reads {
		result, err := n.ReadChannelCommitted(ctx, read.ChannelID, read.Request)
		results[index] = clusterchannels.CommittedReadResult{Read: result, Err: err}
	}
	return results, nil
}

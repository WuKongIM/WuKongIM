package cluster

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	clusterchannels "github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestMessageMembershipStorePreservesExactPullAuthorizationIdentity(t *testing.T) {
	t.Parallel()

	node := &recordingMembershipNode{membership: metadb.UserChannelMembership{
		UID: "u1", ChannelID: "g1", ChannelType: 2,
	}, found: true}
	store := NewMessageMembershipStore(node)
	membership, found, err := store.GetUserChannelMembership(context.Background(), "u1", "g1", 2)
	if err != nil || !found || membership.UID != "u1" || node.uid != "u1" || node.channelID != "g1" || node.channelType != 2 {
		t.Fatalf("GetUserChannelMembership() = %#v found=%v args=%q/%q/%d err=%v", membership, found, node.uid, node.channelID, node.channelType, err)
	}

	var nilStore *MessageMembershipStore
	if _, _, err := nilStore.GetUserChannelMembership(context.Background(), "u1", "g1", 2); !errors.Is(err, message.ErrSyncMembershipRequired) {
		t.Fatalf("nil store error = %v, want %v", err, message.ErrSyncMembershipRequired)
	}
}

func TestCommittedMessageReaderPreservesScanAndRecordOwnership(t *testing.T) {
	node := &recordingReadNode{batchResults: []clusterchannels.CommittedReadResult{{
		Read: channelstore.ReadCommittedResult{Messages: []channelruntime.Message{
			{MessageID: 15, MessageSeq: 5, ChannelID: "g1", ChannelType: 2, SyncOnce: true, ServerTimestampMS: 1700000000123, Payload: []byte("command")},
			{MessageID: 14, MessageSeq: 4, ChannelID: "g1", ChannelType: 2, Setting: 2, FromUID: "u1", ClientMsgNo: "client-4", RedDot: true, Expire: 3600, Payload: []byte("ordinary")},
		}},
	}}}
	reader := NewCommittedMessageReader(node)
	results, err := reader.ReadCommittedMessages(context.Background(), []message.CommittedMessageQuery{{
		ChannelID: message.ChannelID{ID: "g1", Type: 2}, FromSeq: 5, MinSeq: 3, MaxSeq: 9, Limit: 2, MaxBytes: 71, Reverse: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if node.batchCalls != 1 || len(node.batchReads) != 1 || node.lastID != (channelruntime.ChannelID{}) {
		t.Fatalf("reads=%+v, local read=%v, want one routed read", node.batchReads, node.lastID)
	}
	want := channelstore.ReadCommittedRequest{FromSeq: 5, MinSeq: 3, MaxSeq: 9, Limit: 2, MaxBytes: 71, Reverse: true}
	if got := node.batchReads[0]; got.ChannelID != (channelruntime.ChannelID{ID: "g1", Type: 2}) || got.Request != want {
		t.Fatalf("read=%+v, want unchanged scan %+v", got, want)
	}
	messages := results[0].Messages
	if len(messages) != 2 || messages[0].MessageSeq != 5 || !messages[0].Flags.SyncOnce || messages[0].Timestamp != 1700000000 || messages[1].MessageSeq != 4 || messages[1].ClientMsgNo != "client-4" || messages[1].Setting != 2 || !messages[1].Flags.RedDot || messages[0].Flags.RedDot || messages[1].Expire != 3600 {
		t.Fatalf("messages=%+v, want unchanged scan order, command flag and mapped fields", messages)
	}
	messages[1].Payload[0] = 'X'
	if string(node.batchResults[0].Read.Messages[1].Payload) != "ordinary" {
		t.Fatal("mapped payload aliases cluster storage")
	}
}

func TestCommittedMessageReaderPreservesAlignedErrorsAndTransportCauses(t *testing.T) {
	queries := []message.CommittedMessageQuery{{ChannelID: message.ChannelID{ID: "a", Type: 2}}, {ChannelID: message.ChannelID{ID: "b", Type: 2}}}
	node := &recordingReadNode{batchResults: []clusterchannels.CommittedReadResult{{}, {Err: channelruntime.ErrNotReady}}}
	reader := NewCommittedMessageReader(node)
	results, err := reader.ReadCommittedMessages(context.Background(), queries)
	if err != nil || len(results) != 2 || results[0].Err != nil || !errors.Is(results[1].Err, channelruntime.ErrNotReady) {
		t.Fatalf("results=%+v err=%v", results, err)
	}
	node.batchResults = nil
	if _, err := reader.ReadCommittedMessages(context.Background(), queries); !errors.Is(err, message.ErrSyncBatchResultMismatch) {
		t.Fatalf("cardinality error=%v", err)
	}
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, metadb.ErrNotFound} {
		node.err = cause
		if _, err := reader.ReadCommittedMessages(context.Background(), queries); !errors.Is(err, cause) {
			t.Fatalf("error=%v, want cause %v", err, cause)
		}
	}
}

func TestCommittedMessageReaderRequiresRoutedCapability(t *testing.T) {
	var missing *CommittedMessageReader
	if _, err := missing.ReadCommittedMessages(context.Background(), nil); !errors.Is(err, message.ErrMessageReaderRequired) {
		t.Fatalf("nil reader error=%v", err)
	}
	reader := NewCommittedMessageReader(&recordingManagementMessageNode{})
	if _, err := reader.ReadCommittedMessages(context.Background(), nil); !errors.Is(err, message.ErrSyncBatchReaderRequired) {
		t.Fatalf("local-only reader error=%v", err)
	}
}

type recordingReadNode struct {
	lastID       channelruntime.ChannelID
	lastReq      channelstore.ReadCommittedRequest
	result       channelstore.ReadCommittedResult
	err          error
	batchCalls   int
	batchReads   []clusterchannels.CommittedRead
	batchResults []clusterchannels.CommittedReadResult
}

type recordingMembershipNode struct {
	membership  metadb.UserChannelMembership
	found       bool
	err         error
	uid         string
	channelID   string
	channelType int64
}

func (n *recordingMembershipNode) GetUserChannelMembership(_ context.Context, uid, channelID string, channelType int64) (metadb.UserChannelMembership, bool, error) {
	n.uid, n.channelID, n.channelType = uid, channelID, channelType
	return n.membership, n.found, n.err
}

func (n *recordingReadNode) ReadChannelCommittedBatch(_ context.Context, reads []clusterchannels.CommittedRead) ([]clusterchannels.CommittedReadResult, error) {
	n.batchCalls++
	n.batchReads = append([]clusterchannels.CommittedRead(nil), reads...)
	return n.batchResults, n.err
}

func (n *recordingReadNode) ReadChannelCommitted(_ context.Context, id channelruntime.ChannelID, req channelstore.ReadCommittedRequest) (channelstore.ReadCommittedResult, error) {
	n.lastID = id
	n.lastReq = req
	return n.result, n.err
}
